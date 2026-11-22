#!/usr/bin/env bash
# Publishes the release that `go tool mygo build` made in build/darwin-arm64
# and scripts/verify-release.sh checked (threads #69, #71):
# .github/workflows/release.yml runs it after both, and
# scripts/release-local.sh --upload.
#
#   1. actnow.dev: scripts/actnow-publish.sh, the site's own script
#      (vendored; scripts/vendor-actnow-publish.sh), sends the disk image,
#      the update archive and deltas and the manifest through the site's
#      release API with ACTNOW_INGEST_TOKEN. The site checks them (sizes,
#      sha256, the manifest's URLs and signatures, the CHANGELOG section),
#      stores them, lists the version on its download and changelog pages
#      and publishes it: a beta (a version with a "-") on the beta channel,
#      /updates/audiosmith/beta/, a release on /updates/audiosmith/. With
#      --no-publish it stops at "verified": publish it in the site's panel
#      (https://actnow.dev/panel/releases), where yanks and rollbacks are too.
#   2. GitHub: a draft release v<version> (a prerelease for a beta) with
#      the notes, the disk image (the signed and notarized DMG, as
#      AudioSmith-<version>-arm64.dmg, for people who come from GitHub,
#      awesome lists and Homebrew without an account), plus update-darwin-arm64.json
#      for copies that still read GitHub (0.4.0-beta.1 … beta.4):
#      - GITHUB_FEED=true (the transition): the update archive and deltas
#        too, and the manifest points at them on GitHub;
#      - otherwise the website's manifest as it is, pointing at actnow.dev,
#        so a copy that missed the transition still finds the newest version.
#      Publish the draft on GitHub after checking it.
#
# The website first: once it has published the version, the site refuses
# it again (409 version_exists), so when step 2 fails, run step 2 alone
# (--skip-website) with the same build, such as the run's artifact.
#
#   scripts/release-publish.sh                # both
#   scripts/release-publish.sh --dry-run      # checks the build with actnow-publish.sh --dry-run
#                                             # and makes the GitHub files in build/github;
#                                             # sends nothing anywhere
#   scripts/release-publish.sh --no-publish   # the website stops at "verified"
#   scripts/release-publish.sh --skip-github  # 1 only
#   scripts/release-publish.sh --skip-website # 2 only
#
# Environment:
#   ACTNOW_INGEST_TOKEN  the site's release-ingest token for audiosmith (not for --dry-run)
#   ACTNOW_URL           default https://actnow.dev (tests point it at a fake)
#   GITHUB_FEED          true during the transition (release.yml)
#   GITHUB_REPOSITORY    default hehehai/AudioSmith; gh needs GH_TOKEN or gh auth
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
# shellcheck source=SCRIPTDIR/actnow.sh
source "$ROOT/scripts/actnow.sh"
fail() { echo "release-publish: $*" >&2; exit 1; }
command -v jq > /dev/null || fail "needs jq"

dry=false website=true github=true website_args=()
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry=true; website_args+=(--dry-run) ;;
    --no-publish) website_args+=(--no-publish) ;;
    --skip-github) github=false ;;
    --skip-website) website=false ;;
    *) fail "unknown option $arg (see the header of $0)" ;;
  esac
done
github_feed="${GITHUB_FEED:-false}"
[[ "$github_feed" == true || "$github_feed" == false ]] || fail "GITHUB_FEED is \"$github_feed\", not true or false"
repo="${GITHUB_REPOSITORY:-hehehai/AudioSmith}"

OUT="build/darwin-arm64"
version="$(jq -r .version mygo.json)"
min_macos="$(jq -r '.macos.minimumSystemVersion // empty' mygo.json)"
tag="v$version"
manifest="$OUT/update-darwin-arm64.json"
# The disk image as people download it (scripts/dmg-name.sh renames MyGo's
# "audiosmith-<version>-arm64.dmg" after the build).
dmg="$OUT/AudioSmith-$version-arm64.dmg"

# Everything that can be checked before anything is sent.
# The disk image goes to the website and to the GitHub release.
if $website && [[ ! -f "$dmg" ]]; then
  fail "no disk image $dmg (build first: go tool mygo build -platform darwin/arm64)"
fi
feed="$ACTNOW_UPDATES_URL"
actnow_is_prerelease "$version" && feed="$feed/beta"
if [[ -f "$manifest" ]]; then
  jq -e --arg v "$version" '.version == $v' "$manifest" > /dev/null || fail "$manifest is not of $version: build again"
  # The site takes the archive and deltas at exactly $feed/<file> (a beta's
  # in beta/, as MyGo names them from updates.url with updates.prereleases).
  for url in $(jq -r '.url, .deltas[]?.url' "$manifest"); do
    [[ "$url" == "$feed/"* && "${url#"$feed/"}" != */* ]] ||
      fail "$manifest points at $url, not into $feed/: build with scripts/release-config.sh (updates.url $ACTNOW_UPDATES_URL)"
  done
elif ! $dry; then
  fail "no $manifest: a release needs the update keys (MYGO_UPDATER_PRIVATE_KEY, MYGO_UPDATER_PUBLIC_KEY)"
fi
if ! $dry; then
  if $website && [[ -z "${ACTNOW_INGEST_TOKEN:-}" ]]; then
    fail "ACTNOW_INGEST_TOKEN is not set (the site's release-ingest token for audiosmith)"
  fi
  if $github; then
    command -v gh > /dev/null || fail "needs the GitHub CLI (gh) for the GitHub release; or --skip-github"
  fi
fi

# 2's files, made first so that a dry run shows them: build/github.
notes_file="build/github-notes.md"
gh_dir="build/github"
rm -rf "$gh_dir"
gh_files=()
if $github; then
  mkdir -p "$gh_dir"
  {
    changelog_section CHANGELOG.md "$version"
    echo
    echo "---"
    echo
    if [[ -f "$dmg" ]]; then
      echo "**Download:** AudioSmith-$version-arm64.dmg below (signed and notarized; macOS ${min_macos:-15.0}+, Apple silicon), or [actnow.dev/audiosmith](${ACTNOW_DOWNLOAD_URL%/download/*}/audiosmith) · [Changelog]($ACTNOW_CHANGELOG_URL) · or build it from this tag's source (AGPL-3.0)."
    else
      echo "**Download:** [actnow.dev/download/audiosmith]($ACTNOW_DOWNLOAD_URL) · [Changelog]($ACTNOW_CHANGELOG_URL) · or build it from this tag's source (AGPL-3.0)."
    fi
    if [[ -f "$manifest" ]]; then
      echo
      echo "Installed copies update themselves: AudioSmith → Check for Updates…"
    fi
  } > "$notes_file"
  # The disk image first, so the release offers it as soon as it is published.
  if [[ -f "$dmg" ]]; then
    cp "$dmg" "$gh_dir/AudioSmith-$version-arm64.dmg"
    gh_files+=("$gh_dir/AudioSmith-$version-arm64.dmg")
  else
    echo "release-publish: no $dmg, so the GitHub release gets no disk image (upload it by hand: gh release upload $tag)" >&2
  fi
  if [[ -f "$manifest" ]]; then
    if [[ "$github_feed" == true ]]; then
      # The transition: copies that read GitHub get the archive (and the
      # deltas) from this release. Only the archives are signed, not the
      # manifest, so a second manifest of the same archive is as good.
      base="https://github.com/$repo/releases/download/$tag/"
      for f in "$(jq -r .url "$manifest")" $(jq -r '.deltas[]?.url' "$manifest"); do
        name="${f##*/}"
        [[ -f "$OUT/$name" ]] || fail "no $OUT/$name, which $manifest names"
        cp "$OUT/$name" "$gh_dir/$name"
        gh_files+=("$gh_dir/$name")
      done
      jq --arg base "$base" '.url = $base + (.url | split("/") | last)
        | if .deltas then .deltas |= map(.url = $base + (.url | split("/") | last)) else . end' \
        "$manifest" > "$gh_dir/update-darwin-arm64.json"
    else
      cp "$manifest" "$gh_dir/update-darwin-arm64.json"
    fi
  fi
fi

# 1. The website.
if $website; then
  # Both changelogs: the site keeps the English and the Chinese notes and
  # checks that their versions agree (the app shows them in its language).
  zh=()
  if [[ -f CHANGELOG.zh-Hans.md ]]; then zh=(--changelog CHANGELOG.zh-Hans.md); fi
  bash scripts/actnow-publish.sh --app "$ACTNOW_APP" --dir "$OUT" --changelog CHANGELOG.md ${zh[@]+"${zh[@]}"} \
    --version "$version" ${min_macos:+--min-macos "$min_macos"} ${website_args[@]+"${website_args[@]}"}
fi

if $dry; then
  if $github; then
    echo "release-publish: dry run; would create the draft GitHub release $tag of $repo (GITHUB_FEED=$github_feed) with:"
    for f in "$gh_dir"/*; do
      if [[ -e "$f" ]]; then echo "  ${f##*/}"; fi
    done
    if [[ -f "$gh_dir/update-darwin-arm64.json" ]]; then
      jq '{version, url, deltas: [.deltas[]?.url]}' "$gh_dir/update-darwin-arm64.json"
    else
      echo "  (no update manifest: this build has no update keys)"
    fi
  fi
  exit 0
fi

# 2. GitHub.
if $github; then
  gh_failed() {
    echo "release-publish: the GitHub release failed$($website && echo ", after actnow.dev took $version"): run scripts/release-publish.sh --skip-website with this build to finish it" >&2
  }
  trap gh_failed ERR
  beta=()
  actnow_is_prerelease "$version" && beta=(--prerelease)
  if ! gh release view "$tag" --repo "$repo" > /dev/null 2>&1; then
    gh release create "$tag" --repo "$repo" --draft --title "$tag" --notes-file "$notes_file" ${beta[@]+"${beta[@]}"}
  fi
  if [[ ${#beta[@]} -gt 0 ]]; then
    # A beta stays a prerelease and never becomes GitHub's latest release.
    gh release edit "$tag" --repo "$repo" --prerelease --latest=false > /dev/null
  fi
  if [[ ${#gh_files[@]} -gt 0 ]]; then
    gh release upload "$tag" --repo "$repo" --clobber "${gh_files[@]}"
  fi
  # The manifest last, so it never names a file that is not there.
  if [[ -f "$gh_dir/update-darwin-arm64.json" ]]; then
    gh release upload "$tag" --repo "$repo" --clobber "$gh_dir/update-darwin-arm64.json"
  fi
  trap - ERR
  echo "release-publish: the draft GitHub release $tag of $repo is ready: check it and publish it (GITHUB_FEED=$github_feed)"
fi
if $website; then
  echo "release-publish: AudioSmith $version went to actnow.dev ($(actnow_channel "$version"); its status is above)"
fi
