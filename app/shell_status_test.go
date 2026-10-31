package main

// Tests of the shell's overlays and its sidebar's foot: notifications,
// logs, the ⌘E quick switch and the activity cards (shell_status.go,
// shell_activity.go, shell_logs.go).

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

func TestNotifications(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	a.notify(core.SeverityWarning, "Disk almost full", "2 GB left", "/settings/models")
	a.notify(core.SeverityInfo, "Hello", "", "")
	tt.Frame()
	if len(a.notices) != 2 || a.notices[0].Title != "Hello" {
		t.Fatalf("notices %+v", a.notices)
	}
	if n := a.unreadNotices(); n != 2 {
		t.Errorf("%d unread", n)
	}
	tt.Click("Tasks and notifications") // the top bar's bell
	if !a.noticesOpen || !tt.HasText("Disk almost full") || !tt.HasText("2 GB left") {
		t.Fatalf("centre: %q", tt.Texts())
	}
	tt.Click("Open")
	if a.shownPath() != "/settings/models" || a.noticesOpen {
		t.Errorf("open link: %s", a.shownPath())
	}
	a.settingsOpen = false
	a.noticesOpen = true
	tt.Frame()
	tt.Click("Mark all read")
	list, _ := a.notes.List(context.Background())
	if a.notices[0].Read != true || !list[1].Read {
		t.Errorf("read: %+v", list)
	}
	tt.Click("Clear")
	if list, _ := a.notes.List(context.Background()); len(list) != 0 || len(a.notices) != 0 {
		t.Error("not cleared")
	}
}

func TestLogs(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.logs.Append(core.LogEntry{Source: "app", Message: "app started"})
	a.logs.Append(core.LogEntry{Source: "hub", Level: core.SeverityError, Message: "download failed"})
	a.logs.Append(core.LogEntry{Source: "engine:gpu", Message: "weights loaded"})
	a.navigate("/logs")
	tt.Frame()
	for _, s := range []string{"app started", "download failed", "weights loaded", "3 lines"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	a.logsSource = "engine:"
	tt.Frame()
	if tt.HasText("app started") || !tt.HasText("weights loaded") {
		t.Errorf("engine filter: %q", tt.Texts())
	}
	a.logsSource, a.logsQuery = "", "FAILED"
	tt.Frame()
	if !tt.HasText("download failed") || tt.HasText("weights loaded") {
		t.Errorf("text filter: %q", tt.Texts())
	}
	// The panel, under another page.
	a.logsQuery = ""
	a.navigate("/studio")
	tt.Frame() // the Settings dialog (Settings → Logs) goes
	tt.Key(ui.Cmd|ui.Shift, ui.KeyL)
	if !a.logsPanel || !tt.HasText("weights loaded") || !tt.HasText("Generate") {
		t.Errorf("panel: %v %q", a.logsPanel, tt.Texts())
	}
}

func TestQuickSwitch(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyE)
	if !a.switchOpen || !tt.HasText("Speech engine") || !tt.HasText("Standard voices") {
		t.Fatalf("⌘E: %q", tt.Texts())
	}
	if err := tt.Click("Standard voices"); err != nil {
		t.Fatal(err)
	}
	if a.preset().ID != "qwen3-tts-0.6b-customvoice-8bit" || a.voice != "Serena" {
		t.Errorf("chosen %s voice %q", a.preset().ID, a.voice)
	}
	for _, e := range catalog.Builtin().Entries {
		if e.Hidden && (tt.HasText(e.Label) || tt.HasText(e.DisplayName())) {
			t.Errorf("hidden %s offered", e.Label)
		}
	}
}

// No status bar: a stopped engine, downloads and jobs are cards, counted
// by the top bar's bell and listed in its popover (0.5 F1).
func TestActivityCards(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.Frame()
	if tt.HasText("Engine ready") || len(a.actCards) != 0 {
		t.Errorf("a card while all is well: %q %+v", tt.Texts(), a.actCards)
	}
	m := a.model(entry(t, "chatterbox-turbo-8bit"))
	m.downloading, m.progress = true, hub.Progress{Bytes: 50, TotalBytes: 200}
	tt.Frame()
	if !tt.HasText("1 in progress") {
		t.Errorf("bell: %q", tt.Texts())
	}
	a.noticesOpen = true
	tt.Frame()
	if !tt.HasText("Downloading “English clone”") || !tt.HasText("25%") {
		t.Errorf("download card: %q", tt.Texts())
	}
	tt.Click("Downloading “English clone” · 25%")
	if a.shownPath() != "/settings/models" {
		t.Errorf("click: %s", a.shownPath())
	}
	a.settingsOpen = false
	m.downloading = false
	a.engState = engineFailed
	a.noticesOpen = true
	tt.Frame()
	if !tt.HasText("Engine stopped") || a.actCards[0].key != "engine" {
		t.Errorf("engine card: %q", tt.Texts())
	}
	if err := tt.Click("Restart engine"); err != nil { // its button shows without the pointer
		t.Error(err)
	}
	a.engState = engineReady
}

// Every unfinished job is on a card, those of a kind on one, and on the
// Tasks page; each is cancelled by its id.
func TestJobsInCards(t *testing.T) {
	a, eng, tt := newTestApp(t)
	tt.Frame()
	tt.Click("Generate")
	take := eng.last(t).ID
	other, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: "x.batch", Lane: jobs.LaneCPU, Title: "Batch export", Owner: "project:p1"})
	eng.progress(take, "generating", 0.5, 1)
	eng.update(other, func(j *jobs.Job) { j.State = jobs.StateQueued })
	a.noticesOpen = true // the bell's popover
	tt.Frame()
	if len(a.actCards) != 2 || !tt.HasText(i18n.T("Generate take")) || !tt.HasText("2 in progress") {
		t.Fatalf("cards %+v: %q", a.actCards, tt.Texts())
	}
	a.navigate("/tasks")
	tt.Frame()
	if !tt.HasText("Batch export") || !tt.HasText("Running and waiting · 2") {
		t.Fatalf("tasks page: %q", tt.Texts())
	}
	// Running first; the Studio's take is cancelled through the Studio.
	active := a.activeJobs()
	if active[0].ID != take || active[1].ID != other {
		t.Fatalf("order %+v", active)
	}
	a.cancelJobByID(active[1])
	if r := eng.last(t); r.Cmd != "cancel" || r.ID != other {
		t.Errorf("cancelled %+v, want %s", r, other)
	}
	a.cancelJobByID(active[0])
	if r := eng.last(t); r.Cmd != "cancel" || r.ID != take || !a.job.cancelling {
		t.Errorf("cancelled %+v, want %s", r, take)
	}
	eng.cancelled(other)
	eng.cancelled(take)
	tt.Frame()
	if len(a.activeJobs()) != 0 || len(a.actCards) != 0 || tt.HasText("2 in progress") {
		t.Errorf("after: %+v %+v", a.activeJobs(), a.actCards)
	}
}

// Notifications other than errors come back after a restart.
func TestNotificationsPersist(t *testing.T) {
	dir := t.TempDir()
	a, _, _ := newTestAppIn(t, dir)
	a.notify(core.SeverityWarning, "Disk almost full", "", "/settings/models")
	a.notify(core.SeverityError, "Download failed", "", "")
	a.notes.Close()
	a.lib.close()
	b, _, _ := newTestAppIn(t, dir)
	if len(b.notices) != 1 || b.notices[0].Title != "Disk almost full" {
		t.Errorf("after restart %+v", b.notices)
	}
}
