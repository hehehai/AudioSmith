package sqlitestore

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

func TestUsageAndRewritePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "AudioSmith")
	s := openTest(t, dir)
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	for i, tk := range []core.Take{
		{ID: core.NewID(day), CreatedAt: day, Text: "a", Model: "kokoro", Language: "en", AudioSeconds: 2, GenSeconds: 1, Starred: true},
		{ID: core.NewID(day.Add(time.Hour)), CreatedAt: day.Add(time.Hour), Text: "b", Model: "kokoro", Language: "en", AudioSeconds: 3},
		{ID: core.NewID(day.AddDate(0, 0, 2)), CreatedAt: day.AddDate(0, 0, 2), Text: "c", Model: "qwen", AudioSeconds: 5},
	} {
		tk.File = tk.ID + ".wav"
		if err := s.Takes().Put(ctx, tk); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.Voices().Put(ctx, core.Voice{ID: "v1", Name: "Ann", Kind: core.VoiceCloned, CreatedAt: day, UpdatedAt: day}); err != nil {
		t.Fatal(err)
	}
	exp := core.Export{ID: core.NewID(day), CreatedAt: day, Kind: "book", Title: "Book", Path: filepath.Join(dir, "renders", "r1", "book.wav")}
	if err := s.Exports().Put(ctx, exp); err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(map[string]string{"ytdlp": filepath.Join(dir, "tools", "yt-dlp"), "other": "/elsewhere/x"})
	if err := s.Documents().Put(ctx, "settings:app", doc); err != nil {
		t.Fatal(err)
	}

	u, err := s.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.Takes != 3 || u.Starred != 1 || u.AudioSeconds != 10 || u.Days != 2 || u.Voices != 1 || u.Cloned != 1 || u.Exports != 1 {
		t.Errorf("usage %+v", u)
	}
	if len(u.ByModel) != 2 || u.ByModel[0] != (Count{Key: "kokoro", N: 2, Seconds: 5}) {
		t.Errorf("by model %+v", u.ByModel)
	}
	if len(u.ByLanguage) != 2 || u.ByLanguage[0].Key != "en" || u.ByLanguage[1].Key != "" {
		t.Errorf("by language %+v", u.ByLanguage)
	}
	if !u.First.Equal(day.Truncate(time.Millisecond)) {
		t.Errorf("first %v", u.First)
	}

	// A copy, its paths moved to another folder, checked and opened.
	newDir := filepath.Join(t.TempDir(), "Moved & Co", "AudioSmith")
	copyDir := t.TempDir()
	copyPath := filepath.Join(copyDir, FileName)
	s.Close() // the app moves the database while it is closed
	if err := copyFile(filepath.Join(dir, FileName), copyPath); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, dir)
	n, err := RewritePaths(ctx, copyPath, dir, newDir)
	if err != nil || n < 2 {
		t.Fatalf("rewrite %d %v", n, err)
	}
	if v, err := CheckFile(ctx, copyPath); err != nil || v != Version() {
		t.Fatalf("check %d %v", v, err)
	}
	m := openTest(t, copyDir)
	got, _, _ := m.Documents().Get(ctx, "settings:app")
	var settings map[string]string
	json.Unmarshal(got, &settings)
	if settings["ytdlp"] != filepath.Join(newDir, "tools", "yt-dlp") || settings["other"] != "/elsewhere/x" {
		t.Errorf("settings in the copy: %s", got)
	}
	exps, err := m.Exports().List(ctx, "", 10)
	if err != nil || len(exps) != 1 || exps[0].Path != filepath.Join(newDir, "renders", "r1", "book.wav") {
		t.Errorf("exports in the copy: %+v %v", exps, err)
	}
	if u2, err := m.Usage(ctx); err != nil || u2.Takes != 3 {
		t.Errorf("usage of the copy %+v %v", u2, err)
	}
	// The original is untouched.
	got, _, _ = s.Documents().Get(ctx, "settings:app")
	var orig map[string]string
	json.Unmarshal(got, &orig)
	if orig["ytdlp"] != filepath.Join(dir, "tools", "yt-dlp") || orig["other"] != "/elsewhere/x" {
		t.Errorf("the original changed: %s", got)
	}
	if _, err := RewritePaths(ctx, copyPath, dir, dir); err == nil {
		t.Error("rewrite to the same folder")
	}
}
