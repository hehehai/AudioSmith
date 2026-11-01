package main

// Tests of the Models page, its downloads and the licence gate
// (shell_models*.go). The fake hub is in shell_test.go.

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/AudioSmith/internal/settings"
)

func TestModelsPage(t *testing.T) {
	a, _, tt := newTestApp(t)
	f := useFakeHub(t, a)
	var progress []hub.Progress
	core.On(a.bus, func(ev hub.DownloadProgress) { progress = append(progress, ev.Progress) })
	openModels(a)
	tt.Frame()
	if !tt.HasText("Fast English voices") || !tt.HasText("Multilingual all-rounder") || !tt.HasText("Not downloaded") {
		t.Fatalf("models: %q", tt.Texts())
	}
	// The first card is Kokoro: it downloads with its G2P repos.
	if err := tt.Click("Download"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	kokoro := entry(t, "kokoro-82m-bf16")
	m := a.models[kokoro.ID]
	if m.downloading || m.local.State != hub.Downloaded {
		t.Fatalf("after download: %+v", m)
	}
	for _, repo := range []string{kokoro.Repo, kokoro.Aux[0].Repo, kokoro.Aux[1].Repo} {
		if f.served[repo] != 2 {
			t.Errorf("%s: %d files", repo, f.served[repo])
		}
	}
	if len(progress) == 0 || progress[len(progress)-1].Bytes != progress[len(progress)-1].TotalBytes {
		t.Errorf("progress events %+v", progress)
	}
	if len(a.notices) == 0 || a.notices[0].Title != "“Fast English voices” is downloaded" {
		t.Errorf("notices %+v", a.notices)
	}
	if !tt.HasText("More actions for “Fast English voices”") || !strings.Contains(strings.Join(tt.Texts(), "|"), "Downloaded · ") {
		t.Errorf("downloaded card: %q", tt.Texts())
	}
	tt.Click("More actions for “Fast English voices”")
	if err := tt.ChooseMenuItem("Verify"); err != nil {
		t.Fatalf("verify: %v (menu %q)", err, tt.Menu())
	}
	tt.Frame()
	if !strings.Contains(strings.Join(a.toasts, "|")+strings.Join(tt.Texts(), "|"), "checks out") && len(a.notices) != 1 {
		t.Errorf("verify: toasts %q notices %+v", a.toasts, a.notices)
	}

	// Delete asks first; Enter confirms.
	tt.Click("More actions for “Fast English voices”")
	tt.ChooseMenuItem("Delete")
	tt.Frame()
	if !a.deleteAsk || !tt.HasText("Delete “Fast English voices”?") {
		t.Fatalf("delete alert: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.models[kokoro.ID].local.State != hub.Missing {
		t.Errorf("after delete: %+v", a.models[kokoro.ID].local)
	}
	// A loaded model cannot be deleted.
	a.loadedModel = kokoro.Repo
	a.models[kokoro.ID].local.State = hub.Downloaded
	tt.Frame()
	if ui := a.engineSummary(); len(ui.Loaded) != 1 {
		t.Fatal("not loaded")
	}
}

func TestLicenceGate(t *testing.T) {
	a, _, tt := newTestApp(t)
	f := useFakeHub(t, a)
	omni := entry(t, "omnivoice-bf16")
	openModels(a)
	tt.Frame()
	if !tt.HasText("non-commercial use only") || !tt.HasText("Review licence") {
		t.Fatalf("licence line: %q", tt.Texts())
	}
	a.download(omni)
	tt.Frame()
	if !a.licenceOpen || !tt.HasText("Licence of “Multilingual all-rounder”") || a.models[omni.ID].downloading {
		t.Fatalf("dialog: %v %q", a.licenceOpen, tt.Texts())
	}
	if f.served[omni.Repo] != 0 {
		t.Error("downloaded before accepting")
	}
	// Accept is disabled until the box is ticked.
	tt.Click("Accept")
	if !a.licenceOpen {
		t.Fatal("accepted without the box")
	}
	tt.Click("I have read the licence and will use this model only as it allows.")
	tt.Click("Accept")
	tt.Frame()
	if a.licenceOpen || a.models[omni.ID].local.State != hub.Downloaded || f.served[omni.Repo] != 2 {
		t.Fatalf("after accepting: open %v %+v", a.licenceOpen, a.models[omni.ID])
	}
	if err := a.licences.Check(context.Background(), omni.Repo); err != nil {
		t.Errorf("gate after accepting: %v", err)
	}
	if !licence.Covers(a.accepted, omni) || !tt.HasText("· accepted") {
		t.Errorf("accepted %+v", a.accepted)
	}
	// Settings lists it, and withdrawing gates the model again.
	openModels(a)
	a.settingsReveal = "licences" // under the list of models
	tt.Frame()
	if !tt.HasText("CC BY-NC 4.0") {
		t.Fatalf("settings: %q", tt.Texts())
	}
	tt.Click("Withdraw")
	tt.Frame()
	if len(a.accepted) != 0 || a.licences.Check(context.Background(), omni.Repo) == nil {
		t.Errorf("after withdrawing: %+v", a.accepted)
	}
	// Cancelling the dialog downloads nothing.
	a.models[omni.ID].local.State = hub.Missing
	a.download(omni)
	tt.Frame()
	tt.Click("Cancel")
	if a.licenceOpen || a.licenceThen != nil || f.served[omni.Repo] != 2 {
		t.Errorf("cancel: %v", a.licenceOpen)
	}
}

func TestDownloadErrors(t *testing.T) {
	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	a.updateSettings(func(s *settings.Settings) { s.Models.Endpoints = []string{"http://127.0.0.1:1"} })
	a.resetHub()
	openModels(a)
	tt.Frame()
	a.download(entry(t, "chatterbox-turbo-8bit"))
	tt.Frame()
	m := a.models["chatterbox-turbo-8bit"]
	if m.downloading || m.err == "" || len(a.notices) == 0 || a.notices[0].Link != "/settings/models" {
		t.Fatalf("offline: %+v %+v", m, a.notices)
	}
	if !tt.HasText(m.err) {
		t.Errorf("error not shown: %q", tt.Texts())
	}
	if lines := a.logs.Tail(10, "hub"); len(lines) < 2 || lines[len(lines)-1].Level != core.SeverityError {
		t.Errorf("hub log %+v", lines)
	}
}

// TestModelsPageKinds: tabs split the catalogue into TTS, ASR, LLM and
// the rest, with counts; All lists everything.
func TestModelsPageKinds(t *testing.T) {
	a, _, tt := newTestApp(t)
	useFakeHub(t, a)
	openModels(a)
	tt.Frame()
	n := len(catalog.Builtin().Entries)
	if !tt.HasText("All  " + itoa(n)) {
		t.Fatalf("no All tab with %d: %q", n, tt.Texts())
	}
	for _, c := range []struct {
		tab       string
		has, lack string
	}{
		{"Speech", "Fast English voices", "Multilingual transcription"},
		{"Transcription", "Multilingual transcription", "Fast English voices"},
		{"Writing", "Writing assistant · Standard", "Fast English voices"},
		{"Other", "Fast voice separation", "Writing assistant · Standard"},
	} {
		label := ""
		for _, s := range tt.Texts() {
			if strings.HasPrefix(s, c.tab+"  ") {
				label = s
			}
		}
		if label == "" {
			t.Fatalf("no %s tab: %q", c.tab, tt.Texts())
		}
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !tt.HasText(c.has) || tt.HasText(c.lack) {
			t.Errorf("%s: want %q and not %q", c.tab, c.has, c.lack)
		}
	}
}
