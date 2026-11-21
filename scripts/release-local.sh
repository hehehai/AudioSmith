#!/usr/bin/env bash
# Builds a release on this Mac the way .github/workflows/release.yml does on
# GitHub's: the engine, then `go tool mygo build` signed with your Developer
# ID, notarized, in a disk image, with the signed update files, then
# scripts/verify-release.sh. The keys come from this Mac (docs/release.md).
#
#   scripts/release-local.sh                 # signed + notarized + updates, in build/darwin-arm64
#   scripts/release-local.sh --upload        # and publish it: actnow.dev + a draft GitHub release
#                                            # (scripts/release-publish.sh: needs ACTNOW_INGEST_TOKEN
#                                            # and gh; GITHUB_FEED=true for the transition release;
#                                            # add --no-publish to stop at "verified" on the site)
#   scripts/release-local.sh --skip-notarize # signed, not notarized (quicker, for checking the signing)
#   scripts/release-local.sh --dry-run       # ad hoc, no notarization, no updates: what CI's dry run makes
#   add --skip-engine to reuse resources/darwin-arm64/engine
#
# From the environment, each with a default:
#   MACOS_SIGNING_IDENTITY   default: the one "Developer ID Application" identity
#                            of the keychains (of team APPLE_TEAM_ID when set;
#                            several without a choice is an error)
#   NOTARY_PROFILE           default "notary" (xcrun notarytool store-credentials notary)
#   MYGO_UPDATER_PRIVATE_KEY default: the contents of mygo-update.key in KEYS_DIR
#   MYGO_UPDATER_PUBLIC_KEY  default: the contents of mygo-update.pub in KEYS_DIR
#   KEYS_DIR                 default: where mygo keygen writes them,
#                            ~/Library/Application Support/mygo/update-keys
#
# mygo.json is edited for the build (scripts/release-config.sh) and put back
# afterwards, whatever happens.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
dry=false upload=false notarize=true engine=true publish_args=()
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry=true ;;
    --upload) upload=true ;;
    --no-publish) publish_args+=(--no-publish) ;;
    --skip-notarize) notarize=false ;;
    --skip-engine) engine=false ;;
    *) echo "unknown option $arg (see the header of $0)" >&2; exit 2 ;;
  esac
done
fail() { echo "release-local: $*" >&2; exit 1; }

[[ "$(uname -s)/$(uname -m)" == Darwin/arm64 ]] || fail "builds on a Mac with Apple silicon only"
export PATH="/opt/homebrew/bin:$PATH" GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
command -v jq > /dev/null || fail "needs jq"
scripts/release-check.sh > /dev/null || exit 1 # the version and its CHANGELOG.md section
if ! git diff --quiet HEAD 2> /dev/null; then
  echo "release-local: note: the working tree has changes; the build includes them" >&2
fi

if $dry; then
  unset MACOS_SIGNING_IDENTITY NOTARY_PROFILE MYGO_UPDATER_PRIVATE_KEY MYGO_UPDATER_PUBLIC_KEY
  $upload && fail "--dry-run does not upload"
else
  if [[ -z "${MACOS_SIGNING_IDENTITY:-}" ]]; then
    ids="$(security find-identity -v -p codesigning | sed -n 's/.*"\(Developer ID Application: .*\)"/\1/p' | sort -u)"
    if [[ -n "${APPLE_TEAM_ID:-}" ]]; then
      ids="$(grep -F "($APPLE_TEAM_ID)" <<<"$ids" || true)"
    fi
    [[ -n "$ids" ]] || fail "no Developer ID Application identity${APPLE_TEAM_ID:+ of team $APPLE_TEAM_ID} in the keychains (docs/release.md, step 1)"
    [[ "$(wc -l <<<"$ids")" -eq 1 ]] || fail "several Developer ID Application identities; set APPLE_TEAM_ID or MACOS_SIGNING_IDENTITY:
$ids"
    MACOS_SIGNING_IDENTITY="$ids"
  fi
  export MACOS_SIGNING_IDENTITY
  if $notarize; then
    export NOTARY_PROFILE="${NOTARY_PROFILE:-notary}"
  else
    unset NOTARY_PROFILE
  fi
  keys="${KEYS_DIR:-$HOME/Library/Application Support/mygo/update-keys}"
  if [[ -z "${MYGO_UPDATER_PRIVATE_KEY:-}" && -f "$keys/mygo-update.key" ]]; then
    MYGO_UPDATER_PRIVATE_KEY="$(cat "$keys/mygo-update.key")"
  fi
  if [[ -z "${MYGO_UPDATER_PUBLIC_KEY:-}" && -f "$keys/mygo-update.pub" ]]; then
    MYGO_UPDATER_PUBLIC_KEY="$(cat "$keys/mygo-update.pub")"
  fi
  [[ -n "${MYGO_UPDATER_PRIVATE_KEY:-}" && -n "${MYGO_UPDATER_PUBLIC_KEY:-}" ]] ||
    fail "no update keys: run go tool mygo keygen (docs/release.md, step 4), or set MYGO_UPDATER_PRIVATE_KEY and MYGO_UPDATER_PUBLIC_KEY"
  export MYGO_UPDATER_PRIVATE_KEY MYGO_UPDATER_PUBLIC_KEY
  if $upload; then
    # Before the long build: publishing needs the website's token and gh.
    [[ -n "${ACTNOW_INGEST_TOKEN:-}" ]] || fail "--upload needs ACTNOW_INGEST_TOKEN (docs/release.md, step 5)"
    command -v gh > /dev/null || fail "--upload needs the GitHub CLI (gh) for the draft GitHub release"
  fi
fi

if $engine || [[ ! -x resources/darwin-arm64/engine/audiosmith-engine ]]; then
  scripts/build-engine.sh Release
fi

# The family's cue sounds (never in this repository): fetched from the
# private library; without access the build plays system sounds.
scripts/sync-sounds.sh --optional || true

backup="$(mktemp)"
cp mygo.json "$backup"
trap 'cp "$backup" mygo.json; rm -f "$backup"' EXIT
scripts/release-config.sh

flags=()
$notarize || flags+=(-skip-notarize)
rm -rf build/darwin-arm64
go tool mygo build -platform darwin/arm64 ${flags[@]+"${flags[@]}"}
scripts/dmg-name.sh

if $dry || ! $notarize; then
  scripts/verify-release.sh
else
  scripts/verify-release.sh --release
fi
if $upload; then
  # The website (scripts/actnow-publish.sh), then the draft GitHub release.
  scripts/release-publish.sh ${publish_args[@]+"${publish_args[@]}"}
fi
exit 0
