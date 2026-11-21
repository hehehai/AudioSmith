# Releasing AudioSmith

中文版：[release.zh-CN.md](release.zh-CN.md)

A release is a tag `v<version>`. GitHub Actions ([`.github/workflows/release.yml`](../.github/workflows/release.yml)) then, on an Apple-silicon macOS runner:

1. builds the Swift engine (`scripts/build-engine.sh`, Xcode 26 and its Metal toolchain);
2. builds the app with `go tool mygo build`: signed with your **Developer ID** with the hardened runtime, the app's entitlements ([`entitlements.plist`](../entitlements.plist): microphone) and the engine's ([`engine/Support/engine.entitlements`](../engine/Support/engine.entitlements)), in a disk image that Apple **notarizes**, with the ticket stapled;
3. writes the **update files**, signed with your update key: `audiosmith-<version>-darwin-arm64.tar.gz`, deltas from up to three earlier versions, and `update-darwin-arm64.json` (the manifest, with this version's section of `CHANGELOG.md` as the release notes);
4. checks the build (`scripts/verify-release.sh`), then publishes it (`scripts/release-publish.sh`) on **actnow.dev**, the family website, which serves AudioSmith's downloads, updates and changelog:
   - **actnow.dev first.** [`scripts/actnow-publish.sh`](../scripts/actnow-publish.sh), the website's own script, sends the disk image, the update archive, the deltas and the manifest through the site's release API, with the one secret `ACTNOW_INGEST_TOKEN`. The site checks them all (sizes and sha256, the manifest's URLs and update signatures, this version's `CHANGELOG.md` section), stores them, lists the version on its [download page](https://actnow.dev/download/audiosmith) (a free account) and [changelog](https://actnow.dev/audiosmith/changelog), and publishes it: it writes the public manifest last. Installed copies read `https://actnow.dev/updates/audiosmith/update-darwin-arm64.json` once a day (and from AudioSmith → Check for Updates…): **they see the version as soon as the site publishes it**, as "Update available" at the foot of the sidebar, with the notes in the app's language from the site's changelog API (updates.go: actnowkit's `updates` over MyGo's updater API, no MyGo update window). CI holds no storage keys; the site does the writing.
   - **Then a draft GitHub release** with the notes, the signed and notarized disk image (`AudioSmith-<version>-arm64.dmg`, so people coming from GitHub, awesome lists or Homebrew can download without an account) and a link to the product page (the source stays on GitHub, AGPL), which you review and publish.

A tag with a prerelease part, such as `v0.4.0-beta.5`, makes a **beta** instead: on the website's beta channel, `/updates/audiosmith/beta/`, which only copies with **Receive beta updates** on read, and on GitHub a prerelease (see [Betas and releases](#betas-and-releases)).

**Moving from GitHub to actnow.dev (0.4.0-beta.5).** Up to 0.4.0-beta.4, copies read their updates from GitHub releases. While `GITHUB_FEED` is `"true"` at the top of `release.yml`, a release also puts its update archive on its GitHub release, with a manifest pointing at it there, so that those copies update to it; from that version on, copies read actnow.dev. Set `GITHUB_FEED: "false"` once 0.4.0-beta.5 is published. Later GitHub releases still carry the website's manifest (pointing at actnow.dev), so a copy that missed the transition finds the newest version anyway.

This follows MyGo's guides: [auto-updates](https://github.com/egoist/mygo/blob/main/docs/updates.md), [the updater plugin](https://github.com/egoist/mygo/blob/main/docs/plugins/updater.md), [distribution](https://github.com/egoist/mygo/blob/main/docs/distribution.md) and [GitHub Actions](https://github.com/egoist/mygo/blob/main/docs/github-actions.md).

The website's script is vendored, not downloaded at release time: `scripts/actnow-publish.sh` is the site's `https://actnow.dev/actnow-publish.sh` byte for byte, so a release runs the copy that was reviewed, and tests run it against a fake site. `scripts/vendor-actnow-publish.sh` refreshes it (review the diff, and update the sha256 in `scripts/release_publish_test.go`, which fails on any other change); `--check` compares it with the site's, which every workflow run does, warning only.

Nothing about releases is in the repository: `mygo.json` has no `updates` section and no signing identity. The workflow adds them for its build (`scripts/release-config.sh`). So every other build — `go run ./app`, `mygo dev`, `scripts/mac.sh go tool mygo build`, CI — is signed ad hoc and never updates itself (`mygo.Updater.Enabled()` is false; Settings → Diagnostics and about says so).

## What you set up once

You need: the Apple Developer Program (you have it), the GitHub CLI (`gh auth login`), this repository on your Mac, and the website's token (step 5).

### 1. A Developer ID Application certificate

Only the account holder can create one.

- Xcode → Settings → Accounts → your team → Manage Certificates… → **+** → **Developer ID Application**; or
- [developer.apple.com → Certificates](https://developer.apple.com/account/resources/certificates/list) → **+** → Developer ID → **Developer ID Application** (G2 Sub-CA), with a certificate signing request from Keychain Access (Certificate Assistant → Request a Certificate from a Certificate Authority → Saved to disk), then download it and double-click it.

Check: `security find-identity -v -p codesigning` lists `Developer ID Application: <Name> (<TEAMID>)`. The 10 characters in brackets are your **team ID** (also on developer.apple.com → Membership details).

If the keychain also holds a Developer ID certificate of another team (an older account), export the new team's one in step 2 (check the team ID in brackets), and run `scripts/release-local.sh` with `APPLE_TEAM_ID=<new team ID>` so it picks that one (with several and no choice it stops rather than guess).

### 2. Export it as .p12 and add the signing secrets

Keychain Access → login → My Certificates → the Developer ID Application certificate (with its private key under it) → right-click → **Export…** → `.p12`, with a strong password. Then:

```sh
base64 -i DeveloperID.p12 | gh secret set MACOS_CERTIFICATE
gh secret set MACOS_CERTIFICATE_PASSWORD     # the .p12's password
gh secret set APPLE_TEAM_ID                  # e.g. AB12CD34EF
rm DeveloperID.p12                           # keep a copy in your password manager instead
```

### 3. Notarization credentials (one of the two)

**A. Apple ID and an app-specific password** (what MyGo's guide uses). Create the password at [account.apple.com](https://account.apple.com) → Sign-In and Security → App-Specific Passwords.

```sh
gh secret set APPLE_ID             # the Apple ID of your developer account
gh secret set APPLE_APP_PASSWORD   # the app-specific password (xxxx-xxxx-xxxx-xxxx)
```

**B. An App Store Connect API key** (not tied to your Apple ID's password; revocable on its own). [App Store Connect → Users and Access → Integrations → App Store Connect API](https://appstoreconnect.apple.com/access/integrations/api) → Team Keys → **+**, access **Developer** → download `AuthKey_<KEYID>.p8` (only once) and note the **Key ID** and the **Issuer ID**.

```sh
gh secret set APPLE_API_KEY < AuthKey_ABC123DEFG.p8
gh secret set APPLE_API_KEY_ID     # ABC123DEFG
gh secret set APPLE_API_ISSUER     # the Issuer ID, a UUID
```

With `APPLE_API_KEY` set the workflow uses B, else A.

For releases from your Mac (`scripts/release-local.sh`), store the same credentials once in your keychain under the profile `notary`:

```sh
xcrun notarytool store-credentials notary --apple-id you@example.com --team-id AB12CD34EF --password xxxx-xxxx-xxxx-xxxx
# or: xcrun notarytool store-credentials notary --key AuthKey_ABC123DEFG.p8 --key-id ABC123DEFG --issuer <issuer-uuid>
```

### 4. The update key

```sh
go tool mygo keygen
```

writes `mygo-update.key` (secret) and `mygo-update.pub` (public) to `~/Library/Application Support/mygo/update-keys/`. Installed copies accept only updates signed with this key, for good: **back the .key up in your password manager** (losing it strands every installed copy: they would need a manual reinstall), never commit it, and never replace it (`-force`) once a release is out. Then:

```sh
keys="$HOME/Library/Application Support/mygo/update-keys"
gh secret set MYGO_UPDATER_PRIVATE_KEY < "$keys/mygo-update.key"
gh variable set MYGO_UPDATER_PUBLIC_KEY < "$keys/mygo-update.pub"
```

The public key is a repository **variable**, not a secret: `mygo build` links it into the app. It stays out of `mygo.json` so that copies built elsewhere don't update themselves; the workflow checks the secret key matches it.

### 5. The website's token

One secret: the site's release-ingest token for audiosmith (the ActNow project keeps it in `~/.config/actnow/ingest-tokens.env`, the line for audiosmith). It is set already; to set it again:

```sh
gh secret set ACTNOW_INGEST_TOKEN -R hehehai/AudioSmith
```

No R2 or S3 keys: the site writes its own storage. A tag's run stops before the build when the token is missing; dry runs don't need it. The site also holds AudioSmith's update public key (panel → Release settings) and refuses archives not signed with the private one.

### 6. A dry run

GitHub → Actions → **Release** → Run workflow, with **sign** ticked. It builds, signs and notarizes with your secrets, verifies (`scripts/verify-release.sh`), shows what a release would publish (`scripts/release-publish.sh --dry-run`: `actnow-publish.sh --dry-run` checks the build directory and prints the session request it would send, files, sizes and sha256s; then the GitHub files), and attaches the disk image, the update files and the GitHub files to the run as an artifact, without touching the website or any release. Download the disk image and check on your Mac:

```sh
spctl --assess --type open --context context:primary-signature -vv AudioSmith-0.4.0-beta.5.dmg   # accepted, source=Notarized Developer ID
```

Without **sign** it builds ad hoc and needs no secrets: that checks that the engine and the app build on GitHub's runner.

## Every release

The same steps make a beta (`0.4.0-beta.2`) or a release (`0.4.0`); the version decides which.

1. In `CHANGELOG.md`, rename `## Unreleased` to the version and date, e.g. `## 0.4.0 — 2026-10-20` or `## 0.4.0-beta.2 — 2026-10-18` (the update's manifest and the website's changelog carry this section; the build fails without it, and so does the site), and do the same in `CHANGELOG.zh-Hans.md`, the same section in Chinese. The two files have the same versions (`scripts/release-check.sh` checks it before the build, and the site refuses a release whose files disagree: `changelog_mismatch`); the app's update panel and changelog show the Chinese notes in a Chinese window, and actnow.dev's changelog page in Chinese. The heading's first word must be the version exactly: `## 0.4.0-beta.1` is not 0.4.0's section, so a release after its betas gets a section of its own (it may repeat the betas' notes). Start a new `## Unreleased` above it in both files when you like.
2. Set `"version"` in `mygo.json` to it. It is the only version: the app's About and Settings, the Local API's `/health`, MCP and the updater all read it (`mygo.App.Version()`).
3. Check that `THIRD-PARTY-NOTICES.txt`, the third-party licences the app ships, is current: `scripts/third-party-notices.sh --check`. If it is not (a dependency changed since it was written; CI's `go test` fails too), run `scripts/third-party-notices.sh` (with `--fetch` after a Swift pin moved) and commit the file with the release.
4. Commit, tag and push:

   ```sh
   git commit -am "AudioSmith 0.4.0"
   git tag v0.4.0
   git push origin main v0.4.0
   ```

5. Actions → Release builds (about 30–60 min the first time, less with the engine cache), checks the build, and publishes it on actnow.dev (step "Publish to actnow.dev"): **installed copies see it at once** (betas: those with beta updates on), and the site's download and changelog pages list it. The run's summary shows the site's answer and the feed it moved. Check: `curl -s https://actnow.dev/updates/audiosmith/beta/update-darwin-arm64.json | jq '{version, url, deltas: [.deltas[]?.from]}'` (a release: without `beta/`).
6. Review the draft GitHub release `v0.4.0` (step "Draft the GitHub release") — the notes come from `CHANGELOG.md`, with the download link — and **Publish release** (or `gh release edit v0.4.0 --draft=false`).
   - A **beta**'s draft is marked **pre-release**. Leave "Set as a pre-release" ticked when you publish (and don't tick "Set as the latest release").
   - While `GITHUB_FEED` is `"true"`, copies that still read GitHub update once it is published.

A tag that is not `v` + the version in `mygo.json` (`scripts/release-check.sh`) fails at once and builds nothing.

**The site's rules.** A version must be newer than every version the site has published on its channel (a beta also newer than the newest release); a version once published or yanked can never be used again, so a broken release is fixed with a new version. The `CHANGELOG.md` section must be there and match the notes MyGo built into the manifest. Each upload needs the disk image, the archive and the manifest.

**Publishing later.** Set `WEBSITE_PUBLISH: "false"` at the top of `release.yml` (or `scripts/release-local.sh --upload --no-publish`): the site checks and stores everything and stops at **verified** (`actnow-publish.sh --no-publish`); publish it in the site's panel, [actnow.dev/panel/releases](https://actnow.dev/panel/releases).

**When a step fails.** If the website refuses the upload, the run stops before GitHub and says why (the site deletes what it took): fix it and push a new tag, or run the workflow again for the same tag (a version that never got published can be sent again). If the GitHub step fails after the site published, running the whole workflow again would not help (the site refuses the version now): download the run's `AudioSmith-<version>-update` artifact into `build/darwin-arm64` and run `scripts/release-publish.sh --skip-website` (with `GITHUB_FEED` as in the workflow, and `gh` logged in).

**Taking a version back.** In [the panel](https://actnow.dev/panel/releases): **Unpublish** (yank) a version, or **Roll back to this** on an earlier one; the site rewrites the public manifests. Neither downgrades copies that already installed the newer version: they only stop it from reaching more copies. Ship the fix as a newer version.

## Betas and releases

Settings → Diagnostics and about → Software updates → **Receive beta updates** chooses the channel (MyGo patches `0003-update-prereleases.patch` and `0008-update-url-prereleases.patch`, `mygo.Updater.SetPrereleases`):

- **Off: releases only.** The copy reads `https://actnow.dev/updates/audiosmith/update-darwin-arm64.json`, the stable channel.
- **On: betas and releases.** The copy reads that and `https://actnow.dev/updates/audiosmith/beta/update-darwin-arm64.json`, the beta channel, and offers the newer (`0.4.0-beta.1 < 0.4.0-beta.2 < 0.4.0 < 0.4.1-beta.1`). The site keeps the beta manifest at the newer of the newest beta and the newest release, so either way a release is never hidden. With no beta manifest (404), the check reads the releases' alone.
- **One feed URL for both kinds of build.** Every build, beta or release, has `updates.url` = `https://actnow.dev/updates/audiosmith` (`scripts/release-config.sh`), and MyGo puts a beta's archive and deltas in `beta/` (`…/updates/audiosmith/beta/<file>`), which is what the site checks. The site's guide suggests building betas with `…/audiosmith/beta` instead; AudioSmith doesn't, because then a beta copy with Receive beta updates **off** would still read the beta channel (and the client would look for `beta/beta/`). `scripts/release-publish.sh` refuses a manifest whose files are not exactly in the channel's directory.
- **Default:** on in a beta copy (whoever installed a beta wants the next one), off in a release, until the user chooses (`settings.Updates.Beta`).
- **No downgrades.** Turning it off in a beta keeps the beta until a release newer than it is out: 0.4.0-beta.5 with betas off updates to 0.4.0, never to 0.3.x.
- **Deltas** follow both channels: `mygo build` makes them from the newest versions of both manifests together (up to three), so 0.4.0-beta.6 has a delta from 0.4.0-beta.5, and 0.4.0 from the betas and the release before. 0.4.0-beta.5, the first on the website, has none (the website had no manifest before it).
- **The version on the Mac.** MyGo writes the whole version (`0.4.0-beta.1`) into `CFBundleShortVersionString` and `CFBundleVersion`, and `mygo.App.Version()` reads it back: the updater compares with it, so it must keep the prerelease part. Apple only requires numeric versions for the App Store; Developer ID notarization and Gatekeeper don't check them (VS Code Insiders ships `1.x.0-insider` the same way). If notarization ever refused it, the build log's `notarytool log` would say so.
- About shows a **Beta** badge next to a beta's version.

**Copies of 0.4.0-beta.1 … beta.4** still read GitHub: with betas off `releases/latest/download/update-darwin-arm64.json`, with betas on the newest release or prerelease through the GitHub API (patch 0003). Each GitHub release made by `scripts/release-publish.sh` carries an `update-darwin-arm64.json` for them: during the transition (`GITHUB_FEED=true`) one pointing at the archive on the GitHub release, after it the website's own. Only the archives are signed (the manifest carries their signatures), so either manifest installs the same signed archive. Check: `curl -sL https://github.com/hehehai/AudioSmith/releases/download/v0.4.0-beta.5/update-darwin-arm64.json | jq '{version, url}'`.

## Checking an update end to end

Updates need two signed releases: one installed, one newer.

1. Install the older release from its disk image into `/Applications` and open it (on a Mac where you are an administrator, so it can replace itself).
2. Publish the newer one. Check the feed: `curl -s https://actnow.dev/updates/audiosmith/update-darwin-arm64.json | jq '{version, size, deltas: [.deltas[]?.from]}'` (betas: `…/updates/audiosmith/beta/update-darwin-arm64.json`; while only betas are published, the releases' manifest is a 404).
3. In the older copy: AudioSmith → **Check for Updates…** → the window shows the new version and its notes → **Install Update** → it downloads (the delta if there is one) → **Relaunch**.
4. Settings → Diagnostics and about shows the new version under About, and "Last checked …" under Software updates. The microphone and Accessibility permissions stay granted: both versions carry your Developer ID.

The daily check starts 10 seconds after launch when a day has passed since the last one. Settings → Diagnostics and about turns it off, or on "Install without asking".

## From your Mac instead

`scripts/release-local.sh` does what the workflow does, with your keychain's Developer ID, the `notary` profile and the update keys of step 4:

```sh
scripts/release-local.sh                  # signed, notarized, with updates, in build/darwin-arm64
scripts/release-local.sh --upload         # and publishes it: actnow.dev, then the draft GitHub release
scripts/release-local.sh --skip-notarize  # signed only: quick check of the signing
scripts/release-local.sh --dry-run        # ad hoc, like the workflow's dry run
```

`--upload` runs `scripts/release-publish.sh` and needs `ACTNOW_INGEST_TOKEN` in the environment and `gh` (`GITHUB_FEED=true` for the transition release); add `--no-publish` to stop at "verified" on the site. It puts `mygo.json` back when it is done.

The scripts can also run alone: `scripts/release-publish.sh [--dry-run] [--no-publish] [--skip-github | --skip-website]` publishes what is in `build/darwin-arm64`, and `scripts/actnow-publish.sh --app audiosmith --dir build/darwin-arm64 --changelog CHANGELOG.md --changelog CHANGELOG.zh-Hans.md --min-macos 15.0 [--dry-run]` is the website part alone (`--help` lists its options).

## Secrets and variables

| Name | Kind | What | Needed |
|---|---|---|---|
| `MACOS_CERTIFICATE` | secret | the Developer ID Application `.p12`, base64 | signing |
| `MACOS_CERTIFICATE_PASSWORD` | secret | its password | signing |
| `APPLE_TEAM_ID` | secret | your team ID (10 characters) | signing, notarization |
| `APPLE_ID` | secret | Apple ID of the developer account | notarization (A) |
| `APPLE_APP_PASSWORD` | secret | an app-specific password of it | notarization (A) |
| `APPLE_API_KEY` | secret | contents of `AuthKey_<KEYID>.p8` | notarization (B) |
| `APPLE_API_KEY_ID` | secret | its Key ID | notarization (B) |
| `APPLE_API_ISSUER` | secret | the Issuer ID | notarization (B) |
| `MYGO_UPDATER_PRIVATE_KEY` | secret | contents of `mygo-update.key` | updates |
| `MYGO_UPDATER_PUBLIC_KEY` | **variable** | contents of `mygo-update.pub` | updates |
| `ACTNOW_INGEST_TOKEN` | secret | the site's release-ingest token for audiosmith | releases (not dry runs) |

`GITHUB_TOKEN` is the workflow's own; it creates the draft release and uploads to it.

## Notes

- **Runner.** `macos-26` (Apple silicon, Xcode 26). The engine needs Xcode 26 (`swift-tools-version: 6.2`); the workflow picks the newest `Xcode_26*.app` and downloads the Metal toolchain when it is missing. If `macos-26` is unavailable, `macos-15` with Xcode 26 installed works the same.
- **Entitlements.** The app: `com.apple.security.device.audio-input` (it asks for the microphone). The engine: the same (its listen worker records). Nothing else: AudioSmith isn't sandboxed, so it needs no network entitlements; dictation pastes with `CGEventPost`, which the Accessibility permission allows; MLX's Metal kernels are compiled ahead of time, so no JIT. `scripts/verify-release.sh` checks both binaries' signatures, hardened runtime, timestamps and entitlements.
- **Permissions after switching to Developer ID.** Copies signed ad hoc (local builds) and the Developer ID release are different apps to macOS's privacy settings: after installing the first release, grant the microphone and Accessibility again (System Settings → Privacy & Security; remove the old AudioSmith entry first). Later releases keep them.
- **Licence.** From the version after 0.4.0-beta.1, AudioSmith is under the GNU AGPL-3.0 (earlier ones: Apache-2.0). The app ships `LICENSE`, `NOTICE` and `THIRD-PARTY-NOTICES.txt` (the full licence and notice texts of every third-party component, generated by `scripts/third-party-notices.sh`) in `Contents/Resources` (mygo.json `resources`), its Finder copyright says so (`copyright` → `NSHumanReadableCopyright`), and Settings → Diagnostics and about links to the source and opens the third-party licences. A release is built from a tag of the public repository, whose source archive GitHub attaches to the release: keep the tag, and don't publish a build whose source isn't pushed.
- **Delta updates** need the earlier versions' archives, which `mygo build` downloads from the website's published manifests, releases and `beta/` (and checks against the key). **The first version on the website (0.4.0-beta.5) has none** (the site had no manifest before it); each later one has deltas from up to three published versions.
- **DMGs are not public.** The site keeps the disk image (sent as `AudioSmith-<version>-arm64.dmg`: `actnow-publish.sh` turns MyGo's spaces into dashes) and serves it only through its sign-in download page; `/updates/audiosmith/` serves the manifests, archives and deltas (`update-*.json` uncached, the rest cached for long).
- **Notarization failed?** `xcrun notarytool log <submission-id> --keychain-profile notary` (the build prints the ID) lists what Apple rejected.
- **"the update signing key does not match updates.publicKey":** the secret and the variable are of different pairs; set both from the same `mygo keygen` output.
