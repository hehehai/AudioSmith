package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const testToken = "test-ingest-token-5f2a"

// site is a fake of actnow.dev's release API (the session flow that
// scripts/actnow-publish.sh speaks: docs/release-pipeline.md of
// actnow-www): it keeps the session request and the uploaded files, and
// logs each call in the same log as the fake gh, so the order of the two
// shows.
type site struct {
	srv     *httptest.Server
	log     string
	mu      sync.Mutex
	auth    []string
	session map[string]any
	files   map[string]string
	refuse  int // the session's answer, when not 0
	calls   int
}

func newSite(t *testing.T, log string) *site {
	s := &site{log: log, files: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.calls++
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		appendLog(log, "site "+r.Method+" "+r.URL.Path)
		const prefix = "/api/releases/r1"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/releases/sessions":
			if s.refuse != 0 {
				w.WriteHeader(s.refuse)
				io.WriteString(w, `{"error":"version_exists","message":"0.4.0-beta.5 is already published"}`)
				return
			}
			if err := json.Unmarshal(data, &s.session); err != nil {
				t.Errorf("session: %v: %s", err, data)
			}
			var artifacts []map[string]any
			for _, a := range s.session["artifacts"].([]any) {
				name := a.(map[string]any)["filename"].(string)
				artifacts = append(artifacts, map[string]any{"filename": name, "upload": map[string]any{"method": "PUT", "url": s.srv.URL + prefix + "/artifacts/" + name}})
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"release": map[string]any{"id": "r1", "status": "uploading"}, "artifacts": artifacts})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, prefix+"/artifacts/"):
			s.files[strings.TrimPrefix(r.URL.Path, prefix+"/artifacts/")] = string(data)
			json.NewEncoder(w).Encode(map[string]any{"size": len(data), "verified": true})
		case r.Method == http.MethodPost && r.URL.Path == prefix+"/complete":
			json.NewEncoder(w).Encode(map[string]any{
				"release":   map[string]any{"id": "r1", "app": "audiosmith", "version": "0.4.0-beta.5", "channel": "beta", "status": "published"},
				"published": true, "warnings": []string{},
				"feeds": []map[string]any{{"channel": "beta", "key": "audiosmith/beta/update-darwin-arm64.json", "version": "0.4.0-beta.5", "changed": true}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"not_found"}`)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func appendLog(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	io.WriteString(f, line+"\n")
}

func (s *site) reset() {
	s.mu.Lock()
	s.auth, s.session, s.files, s.refuse, s.calls = nil, nil, map[string]string{}, 0, 0
	s.mu.Unlock()
}

// publishRoot is a checkout with a verified build of 0.4.0-beta.5 in
// build/darwin-arm64 (the manifest's URLs as MyGo makes them with
// updates.url https://actnow.dev/updates/audiosmith and updates.prereleases), the
// vendored actnow-publish.sh, and a fake gh that logs its calls in log
// (fails "release upload" when $GH_FAIL is set).
func publishRoot(t *testing.T, siteURL, manifestURL string) (root, log string) {
	t.Helper()
	base := siteURL + "/updates/audiosmith/beta/"
	if manifestURL == "" {
		manifestURL = base + "audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz"
	}
	manifest, _ := json.Marshal(map[string]any{
		"version": "0.4.0-beta.5", "notes": "- Five.", "url": manifestURL, "size": 7, "signature": "sig",
		"deltas": []map[string]any{{"from": "0.4.0-beta.4", "url": base + "audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta", "size": 5, "signature": "dsig"}},
	})
	config := `{"name": "AudioSmith", "version": "0.4.0-beta.5", "macos": {"minimumSystemVersion": "15.0"},
		"updates": {"publicKey": "k", "url": "` + siteURL + `/updates/audiosmith"}}`
	root = fakeRoot(t, []string{"actnow.sh", "actnow-publish.sh", "release-publish.sh"}, map[string]string{
		"mygo.json":            config,
		"CHANGELOG.md":         "# Changelog\n\n## 0.4.0-beta.5\n\n- Five.\n\n## 0.4.0-beta.4\n\n- Four.\n",
		"CHANGELOG.zh-Hans.md": "# 更新日志\n\n## 0.4.0-beta.5\n\n- 五。\n\n## 0.4.0-beta.4\n\n- 四。\n",
		"build/darwin-arm64/AudioSmith-0.4.0-beta.5-arm64.dmg":                          "disk image",
		"build/darwin-arm64/audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz":                "archive",
		"build/darwin-arm64/audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta": "delta",
		"build/darwin-arm64/update-darwin-arm64.json":                                   string(manifest),
		"build/darwin-arm64/AudioSmith.app/Contents/Info.plist":                         "plist",
		"bin/gh": `#!/bin/bash
echo "gh $*" >> "$LOG"
# A release exists once created.
if [ "$1 $2" = "release view" ]; then [ -f "$LOG.created" ]; exit; fi
if [ "$1 $2" = "release create" ]; then touch "$LOG.created"; fi
if [ "$1 $2" = "release upload" ] && [ -n "$GH_FAIL" ]; then echo "HTTP 502" >&2; exit 1; fi
exit 0
`,
	})
	return root, filepath.Join(root, "log")
}

func readLog(path string) []string {
	b, _ := os.ReadFile(path)
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestReleasePublish(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	s := newSite(t, filepath.Join(dir, "log"))
	root, _ := publishRoot(t, s.srv.URL, "")
	log := s.log
	publish := func(root, log string, env []string, args ...string) (string, string, error) {
		env = append([]string{
			"PATH=" + filepath.Join(root, "bin") + ":" + os.Getenv("PATH"), "LOG=" + log, "ACTNOW_URL=" + s.srv.URL,
		}, env...)
		// The site's handler reads and writes under its lock: taking it
		// before and after a run orders the test's reads and writes with
		// the handler's (the race detector sees no edge through curl).
		s.mu.Lock()
		s.mu.Unlock() //nolint:staticcheck // an empty critical section on purpose
		stdout, stderr, err := run(t, root, env, "bash", append([]string{"scripts/release-publish.sh"}, args...)...)
		s.mu.Lock()
		s.mu.Unlock() //nolint:staticcheck // an empty critical section on purpose
		return stdout, stderr, err
	}
	token := []string{"ACTNOW_INGEST_TOKEN=" + testToken}
	ghManifest := func(root string) map[string]any {
		t.Helper()
		var m map[string]any
		b, err := os.ReadFile(filepath.Join(root, "build/github/update-darwin-arm64.json"))
		if err != nil || json.Unmarshal(b, &m) != nil {
			t.Fatalf("build/github/update-darwin-arm64.json: %v, %s", err, b)
		}
		return m
	}

	// A dry run of the transition: actnow-publish.sh --dry-run checks the
	// build and prints the session request; the GitHub files are made;
	// nothing is sent, and no token is needed.
	out, stderr, err := publish(root, log, []string{"GITHUB_FEED=true"}, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s%s", err, out, stderr)
	}
	jsonStart := strings.Index(out, "{")
	var request map[string]any
	if err := json.NewDecoder(strings.NewReader(out[jsonStart:])).Decode(&request); err != nil {
		t.Fatalf("dry run printed no request: %v\n%s", err, out)
	}
	var names []string
	for _, a := range request["artifacts"].([]any) {
		names = append(names, a.(map[string]any)["filename"].(string))
	}
	if want := []string{"AudioSmith-0.4.0-beta.5-arm64.dmg", "audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta", "audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz", "update-darwin-arm64.json"}; !slices.Equal(names, want) {
		t.Errorf("dry run: files %v", names)
	}
	if request["app"] != "audiosmith" || request["version"] != "0.4.0-beta.5" || request["channel"] != "beta" || request["minMacOS"] != "15.0" || request["notes"] != "- Five." || request["publish"] != nil {
		t.Errorf("dry run: request %v", request)
	}
	// Both changelogs go to the site (it checks them; the app reads each language).
	if cl, _ := request["changelogs"].(map[string]any); cl == nil || cl["en"] == nil || cl["zh-Hans"] == nil {
		t.Errorf("dry run: changelogs %v", request["changelogs"])
	}
	if !strings.Contains(stderr, "dry run: nothing sent") {
		t.Errorf("dry run says:\n%s", stderr)
	}
	gh := "https://github.com/hehehai/AudioSmith/releases/download/v0.4.0-beta.5/"
	m := ghManifest(root)
	delta := m["deltas"].([]any)[0].(map[string]any)
	if m["url"] != gh+"audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz" || delta["url"] != gh+"audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta" ||
		m["signature"] != "sig" || delta["signature"] != "dsig" || m["version"] != "0.4.0-beta.5" {
		t.Errorf("the transition manifest: %v", m)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "build/github"))
	names = nil
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"AudioSmith-0.4.0-beta.5-arm64.dmg", "audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta", "audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz", "update-darwin-arm64.json"}; !slices.Equal(names, want) {
		t.Errorf("build/github: %v", names)
	}
	notes, _ := os.ReadFile(filepath.Join(root, "build/github-notes.md"))
	if !strings.HasPrefix(string(notes), "- Five.\n\n---\n") || !strings.Contains(string(notes), "AudioSmith-0.4.0-beta.5-arm64.dmg below") || strings.Contains(string(notes), "Four") {
		t.Errorf("notes:\n%s", notes)
	}
	if calls := readLog(log); calls != nil || s.calls != 0 {
		t.Errorf("a dry run called %v", calls)
	}

	// The transition for real: the website (session, the four files, complete),
	// then the GitHub draft prerelease with the archive and delta, then its
	// manifest.
	out, stderr, err = publish(root, log, append([]string{"GITHUB_FEED=true"}, token...))
	if err != nil {
		t.Fatalf("publish: %v\n%s%s", err, out, stderr)
	}
	calls := readLog(log)
	want := []string{
		"site POST /api/releases/sessions",
		"site PUT /api/releases/r1/artifacts/AudioSmith-0.4.0-beta.5-arm64.dmg",
		"site PUT /api/releases/r1/artifacts/audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta",
		"site PUT /api/releases/r1/artifacts/audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz",
		"site PUT /api/releases/r1/artifacts/update-darwin-arm64.json",
		"site POST /api/releases/r1/complete",
		"gh release view v0.4.0-beta.5 --repo hehehai/AudioSmith",
		"gh release create v0.4.0-beta.5 --repo hehehai/AudioSmith --draft --title v0.4.0-beta.5 --notes-file build/github-notes.md --prerelease",
		"gh release edit v0.4.0-beta.5 --repo hehehai/AudioSmith --prerelease --latest=false",
		"gh release upload v0.4.0-beta.5 --repo hehehai/AudioSmith --clobber build/github/AudioSmith-0.4.0-beta.5-arm64.dmg build/github/audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz build/github/audiosmith-0.4.0-beta.4-to-0.4.0-beta.5-darwin-arm64.delta",
		"gh release upload v0.4.0-beta.5 --repo hehehai/AudioSmith --clobber build/github/update-darwin-arm64.json",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("calls\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	for _, a := range s.auth {
		if a != "Bearer "+testToken {
			t.Errorf("Authorization %q", a)
		}
	}
	manifestBytes, _ := os.ReadFile(filepath.Join(root, "build/darwin-arm64/update-darwin-arm64.json"))
	if s.files["AudioSmith-0.4.0-beta.5-arm64.dmg"] != "disk image" || s.files["audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz"] != "archive" || s.files["update-darwin-arm64.json"] != string(manifestBytes) {
		t.Errorf("uploaded %v", s.files)
	}
	for _, a := range s.session["artifacts"].([]any) {
		a := a.(map[string]any)
		if name := a["filename"].(string); a["sha256"] != sum(s.files[name]) {
			t.Errorf("%s: sha256 %v", name, a["sha256"])
		}
	}
	if strings.Contains(out+stderr, testToken) {
		t.Error("printed the token")
	}
	if !strings.Contains(out, `"status": "published"`) {
		t.Errorf("publish says:\n%s", out)
	}

	// After the transition, stopping at "verified": the GitHub release
	// (already there) gets the website's manifest only, pointing at
	// actnow.dev.
	os.Remove(log)
	s.reset()
	if out, stderr, err := publish(root, log, append([]string{"GITHUB_FEED=false"}, token...), "--no-publish"); err != nil {
		t.Fatalf("publish: %v\n%s%s", err, out, stderr)
	}
	calls = readLog(log)
	if want := []string{
		"gh release view v0.4.0-beta.5 --repo hehehai/AudioSmith",
		"gh release edit v0.4.0-beta.5 --repo hehehai/AudioSmith --prerelease --latest=false",
		"gh release upload v0.4.0-beta.5 --repo hehehai/AudioSmith --clobber build/github/AudioSmith-0.4.0-beta.5-arm64.dmg",
		"gh release upload v0.4.0-beta.5 --repo hehehai/AudioSmith --clobber build/github/update-darwin-arm64.json",
	}; len(calls) != 6+len(want) || !slices.Equal(calls[6:], want) {
		t.Errorf("after the transition, calls\n%s", strings.Join(calls, "\n"))
	}
	if s.session["publish"] != false {
		t.Errorf("--no-publish: session %v", s.session)
	}
	if m := ghManifest(root); m["url"] != s.srv.URL+"/updates/audiosmith/beta/audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz" {
		t.Errorf("after the transition, the GitHub manifest: %v", m)
	}

	// --skip-github: the website only; --skip-website: GitHub only, no token.
	os.Remove(log)
	s.reset()
	if _, _, err := publish(root, log, token, "--skip-github"); err != nil || len(readLog(log)) != 6 || strings.HasPrefix(readLog(log)[5], "gh") {
		t.Errorf("--skip-github: %v, %v", err, readLog(log))
	}
	os.Remove(log)
	s.reset()
	// (from the run's artifact, which has no disk image)
	dmg := filepath.Join(root, "build/darwin-arm64/AudioSmith-0.4.0-beta.5-arm64.dmg")
	os.Rename(dmg, dmg+".away")
	if _, stderr, err := publish(root, log, []string{"GITHUB_FEED=true"}, "--skip-website"); err != nil || s.calls != 0 || len(readLog(log)) != 4 {
		t.Errorf("--skip-website: %v, %v\n%s", err, readLog(log), stderr)
	}
	os.Rename(dmg+".away", dmg)

	// The site refuses: no GitHub release, the site's reason shown.
	os.Remove(log)
	s.reset()
	s.mu.Lock()
	s.refuse = http.StatusConflict
	s.mu.Unlock()
	_, stderr, err = publish(root, log, token)
	if calls := readLog(log); err == nil || !strings.Contains(stderr, "0.4.0-beta.5 is already published") || len(calls) != 1 {
		t.Errorf("refused: %v, %s, calls %v", err, stderr, calls)
	}
	// GitHub fails after the website took the version: says how to finish.
	os.Remove(log)
	s.reset()
	_, stderr, err = publish(root, log, append([]string{"GH_FAIL=1"}, token...))
	if err == nil || !strings.Contains(stderr, "after actnow.dev took 0.4.0-beta.5") || !strings.Contains(stderr, "--skip-website") {
		t.Errorf("GitHub failed: %v, %s", err, stderr)
	}

	// Refused before anything is sent.
	for _, c := range []struct {
		name, url string
		env       []string
		says      string
	}{
		{"no token", "", nil, "ACTNOW_INGEST_TOKEN is not set"},
		// A beta built with the stable feed's files, or with the site's
		// suggested updates.url …/audiosmith/beta (beta/beta/).
		{"stable dir", s.srv.URL + "/updates/audiosmith/audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz", token, "/updates/audiosmith/beta/: build with"},
		{"site's beta url", s.srv.URL + "/updates/audiosmith/beta/beta/audiosmith-0.4.0-beta.5-darwin-arm64.tar.gz", token, "/updates/audiosmith/beta/: build with"},
		{"a GitHub build", "https://github.com/hehehai/AudioSmith/releases/download/v0.4.0-beta.5/a.tar.gz", token, "/updates/audiosmith/beta/: build with"},
		{"bad toggle", "", append([]string{"GITHUB_FEED=yes"}, token...), "not true or false"},
	} {
		root, _ := publishRoot(t, s.srv.URL, c.url)
		os.Remove(log)
		s.reset()
		_, stderr, err := publish(root, log, c.env)
		if err == nil || !strings.Contains(stderr, c.says) || readLog(log) != nil || s.calls != 0 {
			t.Errorf("%s: %v, %s, calls %v", c.name, err, stderr, readLog(log))
		}
	}
}

// TestReleasePublishWindows verifies Windows installer and manifest discovery
// (--include-windows and --windows-dir) without breaking the macOS flow or
// touching the vendored actnow-publish.sh.
func TestReleasePublishWindows(t *testing.T) {
	needTools(t)
	log := filepath.Join(t.TempDir(), "calls.log")
	s := newSite(t, log)
	root, _ := publishRoot(t, s.srv.URL, "")
	base := s.srv.URL + "/updates/audiosmith/beta/"
	publish := func(root, log string, env []string, args ...string) (string, string, error) {
		env = append([]string{
			"PATH=" + filepath.Join(root, "bin") + ":" + os.Getenv("PATH"), "LOG=" + log, "ACTNOW_URL=" + s.srv.URL,
		}, env...)
		return run(t, root, env, "bash", append([]string{"scripts/release-publish.sh"}, args...)...)
	}

	winDir := filepath.Join(root, "build/windows-amd64")
	if err := os.MkdirAll(winDir, 0o755); err != nil {
		t.Fatal(err)
	}
	winManifest, _ := json.Marshal(map[string]any{
		"version": "0.4.0-beta.5", "notes": "- Five.",
		"url": base + "audiosmith-0.4.0-beta.5-windows-amd64.tar.gz", "size": 9, "signature": "wsig",
	})
	for name, content := range map[string]string{
		"audiosmith-setup-0.4.0-beta.5-amd64.exe":      "nsis installer",
		"audiosmith-0.4.0-beta.5-windows-amd64.tar.gz": "win archive",
		"update-windows-amd64.json":                    string(winManifest),
	} {
		if err := os.WriteFile(filepath.Join(winDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Dry run with --include-windows stages both macOS and Windows artifacts
	// and mentions Windows in github-notes.md.
	out, stderr, err := publish(root, log, nil, "--dry-run", "--include-windows")
	if err != nil {
		t.Fatalf("dry-run --include-windows: %v\n%s%s", err, out, stderr)
	}
	if !strings.Contains(out, "audiosmith-setup-0.4.0-beta.5-amd64.exe") || !strings.Contains(out, "update-windows-amd64.json") {
		t.Errorf("dry-run missing Windows artifacts:\n%s", out)
	}
	notes, err := os.ReadFile(filepath.Join(root, "build/github-notes.md"))
	if err != nil || !strings.Contains(string(notes), "audiosmith-setup-0.4.0-beta.5-amd64.exe") {
		t.Errorf("github-notes.md missing Windows installer: %v\n%s", err, notes)
	}

	// Windows-only GitHub upload (--skip-website --windows-dir) works without
	// build/darwin-arm64 and uploads manifests last.
	winOnlyRoot := fakeRoot(t, []string{"actnow.sh", "actnow-publish.sh", "release-publish.sh"}, map[string]string{
		"mygo.json":    `{"name": "AudioSmith", "version": "0.4.0-beta.5", "updates": {"publicKey": "k", "url": "` + s.srv.URL + `/updates/audiosmith"}}`,
		"CHANGELOG.md": "# Changelog\n\n## 0.4.0-beta.5\n\n- Five.\n",
		"build/windows-amd64/audiosmith-setup-0.4.0-beta.5-amd64.exe":      "nsis installer",
		"build/windows-amd64/audiosmith-0.4.0-beta.5-windows-amd64.tar.gz": "win archive",
		"build/windows-amd64/update-windows-amd64.json":                    string(winManifest),
		"bin/gh": "#!/usr/bin/env bash\n" +
			"set -euo pipefail\n" +
			"echo \"gh $*\" >> \"$LOG\"\n" +
			"if [[ \"$1 $2\" == \"release view\" ]]; then exit 0; fi\n",
	})
	os.Remove(log)
	s.reset()
	if out, stderr, err := publish(winOnlyRoot, log, nil, "--skip-website", "--windows-dir", "build/windows-amd64"); err != nil {
		t.Fatalf("windows-only publish: %v\n%s%s", err, out, stderr)
	}
	if s.calls != 0 {
		t.Errorf("windows-only touched website: %d calls", s.calls)
	}
	if want := []string{
		"gh release view v0.4.0-beta.5 --repo hehehai/AudioSmith",
		"gh release edit v0.4.0-beta.5 --repo hehehai/AudioSmith --prerelease --latest=false",
		"gh release upload v0.4.0-beta.5 --repo hehehai/AudioSmith --clobber build/github/audiosmith-setup-0.4.0-beta.5-amd64.exe",
		"gh release upload v0.4.0-beta.5 --repo hehehai/AudioSmith --clobber build/github/update-windows-amd64.json",
	}; !slices.Equal(readLog(log), want) {
		t.Errorf("windows-only calls:\n%s\nwant:\n%s", strings.Join(readLog(log), "\n"), strings.Join(want, "\n"))
	}

	// Bad Windows manifest URL is rejected before anything is sent.
	badManifest, _ := json.Marshal(map[string]any{
		"version": "0.4.0-beta.5",
		"url":     s.srv.URL + "/updates/audiosmith/audiosmith-0.4.0-beta.5-windows-amd64.tar.gz",
	})
	if err := os.WriteFile(filepath.Join(winDir, "update-windows-amd64.json"), badManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(log)
	_, stderr, err = publish(root, log, nil, "--skip-website", "--windows-dir", "build/windows-amd64")
	if err == nil || !strings.Contains(stderr, "/updates/audiosmith/beta/: build with") || readLog(log) != nil {
		t.Errorf("bad windows manifest: err=%v stderr=%s calls=%v", err, stderr, readLog(log))
	}
}

// TestActnowPublishVendored checks that scripts/actnow-publish.sh is the
// site's script, untouched (refresh it with scripts/vendor-actnow-publish.sh,
// never by hand): the sha256 below changes only with such a refresh.
func TestActnowPublishVendored(t *testing.T) {
	data, err := os.ReadFile("actnow-publish.sh")
	if err != nil {
		t.Fatal(err)
	}
	const want = "d8e9c9a9a229c47697e9626568b859e7ad4aa04799ce3c8e57a4f0fdf6e3a2c3"
	if got := sum(string(data)); got != want {
		t.Errorf("scripts/actnow-publish.sh has sha256 %s, not %s: if scripts/vendor-actnow-publish.sh refreshed it, review the diff and update this test", got, want)
	}
}
