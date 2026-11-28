// Package scripts holds no code, only tests of the release scripts
// (threads #69, #71): release-config.sh, actnow.sh and release-publish.sh
// with the vendored actnow-publish.sh, run with bash against fakes (the
// site's release API and gh), so they never touch the website or GitHub.
package scripts

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// needTools skips the test without bash, jq or curl.
func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "jq", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
}

// fakeRoot makes a checkout of its own: the scripts named, copied from
// this directory, and files (path → contents).
func fakeRoot(t *testing.T, scripts []string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "scripts"), 0o755)
	for _, s := range scripts {
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(root, "scripts", s), data, 0o755)
	}
	for path, content := range files {
		p := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// run runs a command with env added to a clean environment (PATH and HOME
// only, so that a secret of the caller never leaks in), and returns its
// stdout, stderr and error.
func run(t *testing.T, dir string, env []string, name string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func publicKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

const testConfig = `{"name": "AudioSmith", "version": "0.4.0-beta.5", "macos": {"minimumSystemVersion": "15.0"}}`

func TestReleaseConfig(t *testing.T) {
	needTools(t)
	root := fakeRoot(t, []string{"release-config.sh", "actnow.sh"}, map[string]string{"mygo.json": testConfig})
	key := publicKey()
	config := func(env ...string) (map[string]any, error) {
		t.Helper()
		out, stderr, err := run(t, root, env, "bash", "scripts/release-config.sh", "--print")
		if err != nil {
			return nil, &exec.ExitError{Stderr: []byte(stderr)}
		}
		var c map[string]any
		if err := json.Unmarshal([]byte(out), &c); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return c, nil
	}
	updates := func(c map[string]any) string {
		b, _ := json.Marshal(c["updates"])
		return string(b)
	}

	// The website's feed, for betas too (updates.prereleases puts a beta's
	// files in beta/); no s3 or github, even when mygo.json had them.
	website := `{"prereleases":true,"publicKey":"` + key + `","url":"https://actnow.dev/updates/audiosmith"}`
	c, err := config("MYGO_UPDATER_PUBLIC_KEY=" + key)
	if err != nil || updates(c) != website {
		t.Errorf("website: %s, %v\nwant %s", updates(c), err, website)
	}
	// The GitHub transition toggle leaves the app on the website.
	c, err = config("MYGO_UPDATER_PUBLIC_KEY="+key, "GITHUB_FEED=true")
	if err != nil || updates(c) != website {
		t.Errorf("website with GITHUB_FEED=true: %s, %v", updates(c), err)
	}
	old := fakeRoot(t, []string{"release-config.sh", "actnow.sh"}, map[string]string{"mygo.json": `{"version": "0.4.0", "updates": {"github": "a/b", "tagPrefix": "v", "s3": {"bucket": "x"}, "url": "https://example.com"}}`})
	out, stderr, err := run(t, old, []string{"MYGO_UPDATER_PUBLIC_KEY=" + key}, "bash", "scripts/release-config.sh", "--print")
	var oc map[string]any
	if err != nil || json.Unmarshal([]byte(out), &oc) != nil || updates(oc) != website {
		t.Errorf("old settings: %v, %s\n%s%s", err, updates(oc), out, stderr)
	}
	// Without a key, no updates; signing and notarization as before.
	c, err = config("MACOS_SIGNING_IDENTITY=Developer ID Application: X (T)", "NOTARY_PROFILE=notary", "NOTARY_KEYCHAIN=/tmp/k")
	macos, _ := json.Marshal(c["macos"])
	if err != nil || c["updates"] != nil || string(macos) != `{"minimumSystemVersion":"15.0","notarize":{"keychain":"/tmp/k","keychainProfile":"notary"},"signingIdentity":"Developer ID Application: X (T)"}` {
		t.Errorf("no key: %v, %s, %v", c["updates"], macos, err)
	}
	if _, err := config("MYGO_UPDATER_PUBLIC_KEY=bm90IGEga2V5"); err == nil {
		t.Errorf("not a key: no error")
	}
	// Windows signing command is injected into windows.signCommand.
	c, err = config("WINDOWS_SIGN_COMMAND=signtool sign /fd SHA256 %1")
	win, _ := json.Marshal(c["windows"])
	if err != nil || string(win) != `{"signCommand":"signtool sign /fd SHA256 %1"}` {
		t.Errorf("windows signCommand: %s, %v", win, err)
	}
	// --print changes nothing.
	if b, _ := os.ReadFile(filepath.Join(root, "mygo.json")); string(b) != testConfig {
		t.Errorf("mygo.json changed: %s", b)
	}
}

const testChangelog = `# Changelog

## 0.4.0-beta.10

- Ten.

## 0.4.0-beta.1 — 2026-10-16


### Updates
- One, with **Markdown** and "quotes" and a \backslash.

- Two.

## [0.3.0](https://example.com) - 2026-10-14
- Three.
## v0.2.0
- Two point oh.
`

// shell runs bash lines with scripts/actnow.sh sourced.
func shell(t *testing.T, root, lines string) string {
	t.Helper()
	out, stderr, err := run(t, root, nil, "bash", "-c", "set -euo pipefail; source scripts/actnow.sh; "+lines)
	if err != nil {
		t.Fatalf("%s: %v\n%s", lines, err, stderr)
	}
	return out
}

func TestActnowHelpers(t *testing.T) {
	needTools(t)
	root := fakeRoot(t, []string{"actnow.sh"}, map[string]string{"CHANGELOG.md": testChangelog})
	for version, want := range map[string]string{
		"0.4.0-beta.1":  "### Updates\n- One, with **Markdown** and \"quotes\" and a \\backslash.\n\n- Two.\n",
		"0.4.0-beta.10": "- Ten.\n",
		"0.3.0":         "- Three.\n",
		"0.2.0":         "- Two point oh.\n",
		"0.4.0":         "",
		"0.4.0-beta":    "",
	} {
		if got := shell(t, root, "changelog_section CHANGELOG.md "+version); got != want {
			t.Errorf("changelog_section %s = %q, want %q", version, got, want)
		}
	}
	for version, want := range map[string]string{
		"0.4.0-beta.5": "beta", "0.4.0": "stable", "1.0.0+build.5": "stable", "1.0.0-rc.1+build.5": "beta",
	} {
		if got := shell(t, root, "actnow_channel "+version); got != want+"\n" {
			t.Errorf("%s: %q, want %q", version, got, want)
		}
	}
}
