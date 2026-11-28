package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStudioWithoutModules: a backend without the app's modules answers
// their routes 501 unsupported; deletes check the confirmation first;
// a queue job long-polls with ?wait to its files. The modules themselves
// are tested in the app (api_studio_test.go).
func TestStudioWithoutModules(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	for _, r := range [][2]string{{"GET", "/api/projects"}, {"GET", "/api/workflows"}, {"GET", "/api/batch"}, {"GET", "/api/gallery"},
		{"GET", "/api/longform/x"}, {"GET", "/api/dubs/x"}, {"GET", "/api/agent-skill?agent=codex"}, {"POST", "/api/takes/x/export"}, {"GET", "/api/jobs/dub:x"}} {
		var e apiErr
		resp := do(t, ts, r[0], r[1], nil, &e)
		if resp.StatusCode != 501 || e.Error.Code != CodeUnsupported {
			t.Errorf("%s %s: %d %+v", r[0], r[1], resp.StatusCode, e)
		}
	}
	var e apiErr
	if resp := do(t, ts, "DELETE", "/api/longform/x", nil, &e); resp.StatusCode != 400 || e.Error.Code != CodeConfirmRequired {
		t.Errorf("delete without confirm: %d %+v", resp.StatusCode, e)
	}
	if resp := do(t, ts, "DELETE", "/api/dubs/x?confirm=true", nil, &e); resp.StatusCode != 501 {
		t.Errorf("delete with confirm: %d", resp.StatusCode)
	}
	// The older deletes keep working without it.
	if resp := do(t, ts, "DELETE", "/api/takes/x", nil, nil); resp.StatusCode == 400 {
		t.Error("DELETE /api/takes needs no confirm")
	}
	// Bad input paths and folders.
	if resp := do(t, ts, "POST", "/api/audiobooks", map[string]any{"path": "book.epub"}, &e); resp.StatusCode != 501 {
		t.Errorf("audiobooks: %d", resp.StatusCode) // no modules: refused before the path is read
	}

	// A queue job: ?wait answers when it ends, with its file.
	gate := make(chan struct{})
	f.update(func(f *fakeBackend) { f.renderGate = gate })
	var acc struct {
		JobID string `json:"job_id"`
	}
	do(t, ts, "POST", "/api/generate", map[string]any{"text": "Wait for me.", "wait": false}, &acc)
	var j JobJSON
	do(t, ts, "GET", "/api/jobs/"+acc.JobID, nil, &j)
	if j.Done || j.ID != acc.JobID {
		t.Fatalf("at once: %+v", j)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(gate)
	}()
	start := time.Now()
	do(t, ts, "GET", "/api/jobs/"+acc.JobID+"?wait=10", nil, &j)
	if !j.Done || j.State != "succeeded" || len(j.Files) != 1 || !strings.HasSuffix(j.Files[0], ".wav") || time.Since(start) > 5*time.Second {
		t.Fatalf("waited %v: %+v", time.Since(start), j)
	}
}

func TestPathsAndNames(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.wav")
	os.WriteFile(file, []byte("x"), 0o644)
	wantFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := InputFile("path", file); err != nil || p != wantFile {
		t.Fatal(p, err)
	}
	for _, bad := range []string{"", "a.wav", dir, filepath.Join(dir, "none.wav")} {
		if _, err := InputFile("path", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := InputFile("path", filepath.Join(dir, "none.wav")); ErrorOf(err).Code != CodeNotFound {
		t.Errorf("missing: %v", err)
	}
	for in, want := range map[string]string{"": "fallback.wav", "../../etc/passwd": "passwd.wav", "My Book": "My Book.wav",
		".hidden": "hidden.wav", "a/b:c": "b-c.wav", "x.WAV": "x.WAV", "book.m4b": "book.m4b.wav"} {
		if got := SafeName(in, "fallback", "wav"); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	f := newFake(t)
	s, _ := f.server()
	out, err := s.OutputFile("", "../x", "f", "srt")
	if err != nil || out != filepath.Join(f.dir, "mcp", "x.srt") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := s.OutputFolder("relative"); err == nil {
		t.Error("relative folder accepted")
	}
	made := filepath.Join(dir, "new", "folder")
	if got, err := s.OutputFolder(made); err != nil || got != made {
		t.Fatal(got, err)
	}
	if fi, err := os.Stat(made); err != nil || !fi.IsDir() {
		t.Fatal("not made")
	}
}
