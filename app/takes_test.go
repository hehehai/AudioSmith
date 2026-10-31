package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store/historyjson"
)

func TestHistoryReuseAndDelete(t *testing.T) {
	a, _, tt := newTestApp(t)
	for i, text := range []string{"First take", "Second take"} {
		at := time.Now().Add(time.Duration(i) * time.Second)
		addTake(t, a, core.Take{
			ID: historyjson.NewTakeID(at), CreatedAt: at,
			Text: text, Model: "mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit", Voice: "Vivian, cheerful",
			Language: "English", Temperature: 0.4, TopP: 0.8, MaxTokens: 900, AudioSeconds: 1,
		}, 0.3)
	}
	a.showPage("history")
	tt.Frame()
	if !tt.HasText("2 items") || !tt.HasText("First take") || !tt.HasText("Second take") {
		t.Fatalf("history: %q", tt.Texts())
	}

	second := &a.takes[0]
	a.reuse(second)
	if a.currentPage() != "studio" || a.text != "Second take" || a.modelLabel != "Qwen3-TTS 0.6B CustomVoice" ||
		a.voice != "Vivian, cheerful" || a.temperature != 0.4 || a.topP != 0.8 {
		t.Errorf("reuse: %+v", a)
	}

	a.showPage("history")
	tt.Frame()
	path := a.takeStore.AudioPath(*second)
	a.deleteClip(second)
	tt.Frame()
	if len(a.takes) != 1 || a.takes[0].Text != "First take" {
		t.Fatalf("after delete: %v", a.takes)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("audio of a deleted take remains: %v", err)
	}
}

// TestSnapshotFollowsEvents: takes created, starred or deleted by anyone
// (the Local API later) reach the views through the bus, not by re-reading.
func TestSnapshotFollowsEvents(t *testing.T) {
	a, _, tt := newTestApp(t)
	ctx := context.Background()
	takes := a.lib.svc.Takes
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	addTake(t, a, core.Take{ID: "A", CreatedAt: at, Text: "Older", Model: "m"}, 0.2)
	addTake(t, a, core.Take{ID: "B", CreatedAt: at.Add(time.Minute), Text: "Newer", Model: "m"}, 0.2)
	addTake(t, a, core.Take{ID: "C", CreatedAt: at.Add(-time.Minute), Text: "Oldest, added last", Model: "m"}, 0.2)
	if len(a.takes) != 3 || a.takes[0].ID != "B" || a.takes[2].ID != "C" {
		t.Fatalf("snapshot %v", a.takes)
	}
	a.showPage("history")
	a.requestPeaks(a.findTake("A")) // the Studio's history draws them (Assets lists rows)
	tt.Frame()
	if !tt.HasText("3 items") || len(a.peaks["A"]) != peakCount {
		t.Fatalf("history %q, peaks %d", tt.Texts(), len(a.peaks["A"]))
	}
	cache := filepath.Join(a.lib.peaks.Dir, "A.f32")
	if _, err := os.Stat(cache); err != nil {
		t.Fatal(err)
	}

	if err := takes.SetStarred(ctx, "A", true); err != nil {
		t.Fatal(err)
	}
	if !a.findTake("A").Starred {
		t.Error("star not applied")
	}

	a.play(a.findTake("A"))
	if err := takes.Delete(ctx, "A"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.findTake("A") != nil || a.pl.Current() == "A" || a.peaks["A"] != nil || !tt.HasText("2 items") {
		t.Errorf("after delete: %v, playing %q", a.takes, a.pl.Current())
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("cached peaks of a deleted take remain")
	}

	// A failed delete is reported, and the take stays.
	a.deleteClip(&core.Take{ID: "nope"})
	tt.Frame()
	if len(a.takes) != 2 || !strings.Contains(strings.Join(tt.Texts(), "|"), "Could not delete") {
		t.Errorf("failed delete: %q", tt.Texts())
	}
}

func TestExportStreams(t *testing.T) {
	a, _, _ := newTestApp(t)
	addTake(t, a, core.Take{ID: "E", CreatedAt: time.Now(), Text: "Export me", Model: "m"}, 0.5)
	dst := filepath.Join(t.TempDir(), "out.wav")
	if err := a.lib.svc.Takes.Export(context.Background(), "E", dst); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(a.takeStore.AudioPath(*a.findTake("E")))
	if got, _ := os.ReadFile(dst); len(got) != len(src) || len(got) < 1000 {
		t.Errorf("exported %d bytes of %d", len(got), len(src))
	}
}

func TestEmptyHistory(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.showPage("history")
	tt.Frame()
	if !tt.HasText("Nothing here yet") {
		t.Errorf("texts %q", tt.Texts())
	}
}

func TestExportNames(t *testing.T) {
	for text, want := range map[string]string{
		"What: a/test of the export name, really long": "What- a-test of the export name.wav",
		"Hello there. And more.":                       "Hello there.wav",
		"你好，世界":                                        "你好，世界.wav",
		"最后一班渡轮在十一点离港。[pause 500ms] 雾像羊毛一样铺在水面上。": "最后一班渡轮在十一点离港.wav",
		"[pause 1s]":         "AudioSmith.wav",
		"Version 1.5 is out": "Version 1.5 is out.wav",
	} {
		if got := exportName(&core.Take{Text: text}); got != want {
			t.Errorf("export name of %q: %q, want %q", text, got, want)
		}
	}
}
