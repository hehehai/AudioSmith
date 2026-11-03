package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
)

func touchOld(t *testing.T, path string, age time.Duration) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	os.Chtimes(path, at, at)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Code review 1102 M2: Go's atomic writes (.tmp, .tmp.wav) and what
// renders left in cache/tmp were never cleaned.
func TestCleanLeftovers(t *testing.T) {
	data, cache := t.TempDir(), t.TempDir()
	oldTmp := filepath.Join(data, "dubs", "01A", "renders", "es", "s1-abc.wav.tmp")
	oldWav := filepath.Join(data, "dubs", "01A", "line.tmp.wav")
	young := filepath.Join(data, "transcripts", "x.wav.tmp")
	joined := filepath.Join(cache, "tmp", "01J-joined.wav")
	dir := filepath.Join(cache, "tmp", "01J-k1", "a.wav")
	fresh := filepath.Join(cache, "tmp", "01K-joined.wav")
	keep := filepath.Join(data, "dubs", "01A", "audio.wav")
	for _, p := range []string{oldTmp, oldWav, joined, dir, keep} {
		touchOld(t, p, 2*time.Hour)
	}
	os.Chtimes(filepath.Dir(dir), time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))
	touchOld(t, young, time.Minute)
	touchOld(t, fresh, time.Minute)
	cleanLeftovers(data, cache)
	for p, want := range map[string]bool{oldTmp: false, oldWav: false, young: true, joined: false, dir: false, fresh: true, keep: true} {
		if exists(p) != want {
			t.Errorf("%s: there %v, want %v", p, exists(p), want)
		}
	}
}

// Code review 1102 M3: the clone flow's clips nothing names go after a
// week; a clip a take was cloned from stays, and a young one.
func TestSweepUnusedClips(t *testing.T) {
	a, _, _ := newTestApp(t)
	refs := a.referencesDir()
	used := filepath.Join(refs, "ref-01A.wav")
	unused := filepath.Join(refs, "rec-01B.wav")
	young := filepath.Join(refs, "clip-01C.wav")
	for _, p := range []string{used, unused} {
		touchOld(t, p, 8*24*time.Hour)
	}
	touchOld(t, young, time.Hour)
	draft, _ := json.Marshal(map[string]string{"refAudio": used})
	if err := a.lib.store.Documents().Put(context.Background(), "test.draft", draft); err != nil {
		t.Fatal(err)
	}
	a.sweepUnused(context.Background(), refs, unusedClipAge)
	for p, want := range map[string]bool{used: true, unused: false, young: true} {
		if exists(p) != want {
			t.Errorf("%s: there %v, want %v", filepath.Base(p), exists(p), want)
		}
	}
	// The clip a new one replaces goes at once; a kept part does not.
	clip, ref := filepath.Join(refs, "clip-01D.wav"), filepath.Join(refs, "ref-01E.wav")
	touchOld(t, clip, 0)
	touchOld(t, ref, 0)
	a.dropCloneWork(clip, ref, "")
	if exists(clip) || !exists(ref) {
		t.Errorf("clip there %v, ref there %v", exists(clip), exists(ref))
	}
}

// The sweep: agent files a month old go; Clear empties every cache part
// and measures what it clears.
func TestSweepAgentFilesAndClearCaches(t *testing.T) {
	a, _, _ := newTestApp(t)
	ctx := context.Background()
	mcp := filepath.Join(a.dataDir(), "mcp")
	old, recent := filepath.Join(mcp, "take-old.wav"), filepath.Join(mcp, "take-new.wav")
	touchOld(t, old, 31*24*time.Hour)
	touchOld(t, recent, time.Hour)
	a.sweepDisk(ctx)
	if exists(old) || !exists(recent) {
		t.Errorf("old there %v, recent there %v", exists(old), exists(recent))
	}
	cache := a.cacheDir()
	for _, p := range []string{"tmp/x.wav", "previews/p.wav", "calls/c.wav", "peaks/a.peaks"} {
		touchOld(t, filepath.Join(cache, p), 0)
	}
	if n := cacheBytes(ctx, cache); n < 20 {
		t.Fatalf("caches measure %d", n)
	}
	a.clearCaches()
	if n := cacheBytes(ctx, cache); n != 0 {
		t.Errorf("%d bytes left after Clear", n)
	}
	a.clearAgentFiles()
	if exists(recent) {
		t.Error("Clear left an agent file")
	}
}

// Code review 1102 L1: a sample's copy is deleted with its record, so
// Assets offers Delete, not "Remove (the file stays)".
func TestSampleCopyDeletesItsFile(t *testing.T) {
	a, _, _ := newTestApp(t)
	in := filepath.Join(a.dataDir(), "samples", "01S", "story.ogg")
	if !a.sampleCopy(in) || a.sampleCopy(filepath.Join(t.TempDir(), "story.ogg")) || a.sampleCopy(filepath.Join(a.dataDir(), "samples", "x.ogg")) {
		t.Error("sampleCopy")
	}
	touchOld(t, in, 0)
	e, err := a.lib.svc.Library.RecordExport(context.Background(), core.Export{Kind: "story", Path: in, Format: "ogg"})
	if err != nil {
		t.Fatal(err)
	}
	a.deleteExportRecord(e.ID)
	if exists(filepath.Dir(in)) {
		t.Error("the sample's copy stayed")
	}
}

// Code review 1102 L4: opening a freed dub says its working files were
// freed, and notes the opening (they stay a week from now).
func TestFreedDubOpens(t *testing.T) {
	a, _, _ := newTestApp(t)
	p, err := a.dubSvc.Create(context.Background(), &dub.Project{Title: "Interview", Freed: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	a.dubShow(p, nil)
	if a.dubNotice == "" {
		t.Error("no notice for a freed dub")
	}
	if !exists(filepath.Join(a.dubSvc.Dir(p.ID), ".opened")) {
		t.Error("the opening was not noted")
	}
}

// Code review 1103 M-1: the sweep and Clear deleted every file in
// <data>/mcp, also the exports agents made there (recorded in the
// library) and the source of a dub an agent put there. What the library
// names stays, in subfolders too; the rest goes.
func TestAgentFilesKeepWhatTheLibraryNames(t *testing.T) {
	a, _, _ := newTestApp(t)
	ctx := context.Background()
	mcp := filepath.Join(a.dataDir(), "mcp")
	book := filepath.Join(mcp, "book.m4b")
	source := filepath.Join(mcp, "inbox", "talk.mp4")
	stray := filepath.Join(mcp, "inbox", "old.wav")
	take := filepath.Join(mcp, "take-old.wav")
	loose := filepath.Join(mcp, "x", "y.wav")
	for _, p := range []string{book, source, stray, take, loose} {
		touchOld(t, p, 31*24*time.Hour)
	}
	if _, err := a.lib.svc.Library.RecordExport(ctx, core.Export{Kind: "book", Path: book, Format: "m4b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.dubSvc.Create(ctx, &dub.Project{Title: "Talk", Source: dub.Source{Path: source}}); err != nil {
		t.Fatal(err)
	}
	a.sweepDisk(ctx)
	for p, want := range map[string]bool{book: true, source: true, stray: false, take: false, loose: false} {
		if exists(p) != want {
			t.Errorf("sweep: %s there %v, want %v", filepath.Base(p), exists(p), want)
		}
	}
	young := filepath.Join(mcp, "inbox", "new.wav")
	touchOld(t, young, time.Hour)
	a.toasts = nil
	a.clearAgentFiles()
	if !exists(book) || !exists(source) || exists(young) {
		t.Errorf("Clear: book %v, source %v, young %v", exists(book), exists(source), exists(young))
	}
	if len(a.toasts) != 1 || !strings.Contains(a.toasts[0], "kept 2 files") {
		t.Errorf("toasts %q", a.toasts)
	}
}

// Code review 1103 L-9: a clip used once in the Studio is named by no
// take until one is generated: a week on, the sweep deleted it under
// the Studio. The clips the Studio and the clone flow hold stay.
func TestSweepKeepsClipsInUse(t *testing.T) {
	a, _, _ := newTestApp(t)
	refs := a.referencesDir()
	studio, work, original, unused := filepath.Join(refs, "ref-01A.wav"), filepath.Join(refs, "clean-01B.wav"),
		filepath.Join(refs, "clip-01C.wav"), filepath.Join(refs, "rec-01D.wav")
	for _, p := range []string{studio, work, original, unused} {
		touchOld(t, p, 8*24*time.Hour)
	}
	a.studioClip = &clipRef{Path: studio, Text: "Words.", Name: "clip"}
	a.clone.work, a.clone.original = work, original
	a.sweepDisk(context.Background())
	for p, want := range map[string]bool{studio: true, work: true, original: true, unused: false} {
		if exists(p) != want {
			t.Errorf("%s: there %v, want %v", filepath.Base(p), exists(p), want)
		}
	}
}
