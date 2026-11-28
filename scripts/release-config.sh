#!/usr/bin/env bash
# Writes a release's settings into mygo.json, in place, for `mygo build`:
# the repository keeps none of them, so builds made without them (go run,
# mygo dev, scripts/mac.sh, CI's dry runs) are signed ad hoc and never
# update themselves (mygo.Updater.Enabled() is false). The release
# workflow (.github/workflows/release.yml) and scripts/release-local.sh
# run it on a copy they throw away or restore; never commit its output.
#
# Updates come from actnow.dev (threads #69, #71, scripts/actnow.sh): the
# app reads https://actnow.dev/updates/audiosmith/update-darwin-arm64.json,
# and with Receive beta updates on the beta/ one beside it too
# (updates.prereleases: MyGo links both feeds into the app and
# mygo.Updater.SetPrereleases chooses). Betas are built with that same url,
# not …/audiosmith/beta as the site suggests: with updates.prereleases
# MyGo points a beta's archive and deltas into beta/ (which the site
# checks) and makes deltas from the newest versions of both channels, and a
# beta with Receive beta updates off must read the releases' feed, not
# beta/. No updates.s3 or updates.github:
# the files go to the site through scripts/actnow-publish.sh. Up to
# 0.4.0-beta.4 the app read GitHub releases.
#
# From the environment, each optional:
#   MYGO_UPDATER_PUBLIC_KEY  contents of mygo-update.pub (mygo keygen): adds
#                            "updates": {publicKey, url, prereleases}
#   MACOS_SIGNING_IDENTITY   "Developer ID Application: Name (TEAMID)" or
#                            its SHA-1: macos.signingIdentity
#   NOTARY_PROFILE           a notarytool keychain profile: macos.notarize
#   NOTARY_KEYCHAIN          the keychain holding it (CI's temporary one)
#   WINDOWS_SIGN_COMMAND     Authenticode sign command with %1 placeholder
#                            (e.g. Azure Trusted Signing): windows.signCommand
#
# GITHUB_FEED (release.yml's transition toggle) changes nothing here: the
# app always reads the website; release-publish.sh puts a copy of the
# update on GitHub for the copies that still read it.
#
#   scripts/release-config.sh            # edit mygo.json
#   scripts/release-config.sh --print    # print the result, change nothing
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG="$ROOT/mygo.json"
# shellcheck source=SCRIPTDIR/actnow.sh
source "$ROOT/scripts/actnow.sh"
command -v jq > /dev/null || { echo "release-config.sh needs jq" >&2; exit 1; }

key="$(printf '%s' "${MYGO_UPDATER_PUBLIC_KEY:-}" | tr -d '[:space:]')"
if [[ -n "$key" ]]; then
  # An Ed25519 public key: 32 bytes in base64, as mygo keygen writes it.
  len="$(printf '%s' "$key" | base64 -d 2> /dev/null | wc -c | tr -d ' ')" || len=0
  if [[ "$len" != 32 ]]; then
    echo "MYGO_UPDATER_PUBLIC_KEY is not the contents of mygo-update.pub (an Ed25519 public key in base64)" >&2
    exit 1
  fi
fi

result="$(jq \
  --arg key "$key" \
  --arg url "$ACTNOW_UPDATES_URL" \
  --arg identity "${MACOS_SIGNING_IDENTITY:-}" \
  --arg profile "${NOTARY_PROFILE:-}" \
  --arg keychain "${NOTARY_KEYCHAIN:-}" \
  --arg winSign "${WINDOWS_SIGN_COMMAND:-}" '
  (if $key == "" then .
    else .updates = ((.updates // {}) | del(.github, .tagPrefix, .s3)) + {publicKey: $key, url: $url, prereleases: true}
    end)
  | (if $identity != "" then .macos.signingIdentity = $identity else . end)
  | (if $profile != "" then .macos.notarize = ({keychainProfile: $profile}
      + (if $keychain != "" then {keychain: $keychain} else {} end)) else . end)
  | (if $winSign != "" then .windows = ((.windows // {}) + {signCommand: $winSign}) else . end)
' "$CONFIG")"

if [[ "${1:-}" == "--print" ]]; then
  printf '%s\n' "$result"
  exit 0
fi
printf '%s\n' "$result" > "$CONFIG"
echo "mygo.json: updates $([[ -n "$key" ]] && echo "on ($ACTNOW_UPDATES_URL)" || echo off)," \
  "signing ${MACOS_SIGNING_IDENTITY:-ad hoc}," \
  "notarization $([[ -n "${NOTARY_PROFILE:-}" ]] && echo on || echo off)"
