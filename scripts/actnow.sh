# shellcheck shell=bash disable=SC2034 # constants for the scripts that source it
# Sourced by the release scripts (release-config.sh, release-publish.sh):
# where AudioSmith's releases go on the family website, actnow.dev
# (threads #69, #71). Not a program of its own.
#
# Every file goes through the site's release API (scripts/actnow-publish.sh,
# the site's own script, vendored), which checks it, stores it in its R2
# bucket and publishes it; CI holds only ACTNOW_INGEST_TOKEN. The site
# serves the update files publicly, and the disk images only through its
# sign-in download page (https://actnow.dev/download/audiosmith):
#
#   https://actnow.dev/updates/audiosmith/<file>       releases (channel stable)
#   https://actnow.dev/updates/audiosmith/beta/<file>  betas (channel beta); its
#                                                      manifest names the newer of both
#
# Every build, beta or not, has updates.url = ACTNOW_UPDATES_URL and
# updates.prereleases: MyGo points a beta's archive and deltas into beta/,
# and the app reads beta/'s manifest as well only with Receive beta updates
# on (mygo.Updater.SetPrereleases).

ACTNOW_APP=audiosmith
# ACTNOW_URL, as actnow-publish.sh reads it, is for tests (a fake site).
ACTNOW_URL="${ACTNOW_URL:-https://actnow.dev}"
ACTNOW_UPDATES_URL="$ACTNOW_URL/updates/audiosmith"
ACTNOW_DOWNLOAD_URL="https://actnow.dev/download/audiosmith"
ACTNOW_CHANGELOG_URL="https://actnow.dev/audiosmith/changelog"

# actnow_is_prerelease VERSION: a version with a prerelease part, such as
# 0.4.0-beta.5, is a beta (semantic versioning, as MyGo compares them; the
# site's script decides the channel the same way).
actnow_is_prerelease() {
  local v="${1%%+*}"
  [[ "$v" == *-* ]]
}

# actnow_channel VERSION: beta or stable, as the site names them.
actnow_channel() {
  if actnow_is_prerelease "$1"; then echo beta; else echo stable; fi
}

# changelog_section FILE VERSION: the body of VERSION's "## " section of a
# Keep a Changelog file, without its heading and the blank lines around
# it, as MyGo's update.ReleaseNotes reads it for the update window: the
# heading's first word, without [ ] and a leading v, is the version,
# exactly ("## 0.4.0-beta.1" is not 0.4.0's section).
changelog_section() {
  awk -v v="$2" '
    /^## / {
      if (on) exit
      h = $2; sub(/^\[/, "", h); sub(/\].*$/, "", h); sub(/^v/, "", h)
      on = (h == v)
      next
    }
    on {
      line[++n] = $0
      if ($0 ~ /[^[:space:]]/) { if (!first) first = n; last = n }
    }
    END { for (i = first; first && i <= last; i++) print line[i] }
  ' "$1"
}
