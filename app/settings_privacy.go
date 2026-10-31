package main

// Settings → Privacy and Usage (M7a). Privacy says what may leave this
// Mac (nothing without the user's action; no analytics at all) and holds
// the switches that decide it, with the history's retention. Usage shows
// statistics computed from the library on this Mac, never sent.

import (
	"context"
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// privacySettings is Settings → Privacy.
func (a *app) privacySettings(c *ui.Context) {
	t := c.Theme()
	muted := func(s string) { ui.Text(c, s).FontSize(px(t, 12)).TextColor(t.TextMuted) }
	settingGroup(c, i18n.T("Analytics"), func() {
		settingRow(c, "priv-analytics", i18n.T("No analytics"), i18n.T("AudioSmith collects no usage data, crash reports or identifiers, and has no setting to turn them on. Settings → Advanced shows statistics computed on this Mac only."), func() {})

	})
	settingGroup(c, i18n.T("What leaves this Mac"), func() {
		settingBlock(c, "priv-leaves", func() {
			muted(i18n.T("Speech is made and recognised on this Mac. AudioSmith connects to the network only for these:"))
			a.privacyList(c)
		})
		local := a.prefs.Privacy.LocalOnlyLLM
		settingRow(c, "priv-local-llm", i18n.T("Local language models only"), i18n.T("Writing help and translation use only local models (Ollama, LM Studio, the engine), never a cloud provider."), func() {
			if inkSwitch(c.Key("priv-local-llm"), &local).Label(i18n.T("Keep text on this Mac")).Changed() {
				on := local
				a.updateSettings(func(s *settings.Settings) { s.Privacy.LocalOnlyLLM = on })
			}
		})
		search := a.prefs.Privacy.AllowNetworkSearch
		settingRow(c, "priv-search", i18n.T("Web lookups"), i18n.T("Lookups you did not ask for one by one, such as finding portraits for voices. Off by default."), func() {
			if inkSwitch(c.Key("priv-search"), &search).Label(i18n.T("Allow web lookups")).Changed() {
				on := search
				a.updateSettings(func(s *settings.Settings) { s.Privacy.AllowNetworkSearch = on })
			}
		})

	})
	settingGroup(c, i18n.T("Watermark"), func() {
		a.watermarkSettings(c) // watermark_app.go (thread #14)

	})
	settingGroup(c, i18n.T("History"), func() {
		a.historySettings(c) // takes_view.go: the history's cap
		settingFoot(c, "priv-history-note", func() { muted(i18n.T("Starred takes and takes in a project are always kept.")) })
	})
}

// loadUsageStats computes the statistics in the background.
func (a *app) loadUsageStats() {
	if a.usageLoading {
		return
	}
	a.usageLoading = true
	st := a.lib.store
	a.background(func() {
		u, err := st.Usage(context.Background())
		a.post(func() {
			a.usageLoading = false
			if err != nil {
				a.usageErr = err.Error()
				return
			}
			a.usage, a.usageErr = &u, ""
		})
	})
}

// audioLength shows seconds of audio: "2 h 05 min", "3 min 20 s".
func audioLength(sec float64) string {
	s := int(math.Round(sec))
	switch {
	case s >= 3600:
		return i18n.T("%d h %02d min", s/3600, s%3600/60)
	case s >= 60:
		return i18n.T("%d min %02d s", s/60, s%60)
	}
	return i18n.T("%d s", s)
}

// usageSettings is Settings → Usage.
func (a *app) usageSettings(c *ui.Context) {
	a.revealAnchor(c, "usage")
	if a.usage == nil && !a.usageLoading && a.usageErr == "" {
		a.loadUsageStats()
	}
	settingGroup(c, i18n.T("Usage"), func() {
		settingNote(c, "usage-note", i18n.T("Computed from your library on this Mac. AudioSmith never sends it anywhere."))
		settingBlock(c, "usage-body", func() { a.usageBody(c) })
	})
}

// usageBody is the statistics: counts, then takes by model and language.
func (a *app) usageBody(c *ui.Context) {
	t := c.Theme()
	if a.usageErr != "" {
		ui.Text(c, a.usageErr).TextColor(t.Danger)
		return
	}
	u := a.usage
	if u == nil {
		ui.Spinner(c)
		return
	}
	stat := func(n, label string) {
		ui.Column(c).Gap(px(t, 2)).Padding(px(t, 10)).Radius(px(t, radiusControl)).Background(tok(t).Subtle).Children(func() {
			ui.Text(c, n).FontSize(px(t, 20)).Bold()
			ui.Text(c, label).FontSize(px(t, 12)).TextColor(t.TextMuted)
		})
	}
	ui.Grid(c.Key("usage-grid")).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1)).GapX(px(t, 10)).GapY(px(t, 10)).Children(func() {
		stat(fmt.Sprint(u.Takes), i18n.T("Takes generated"))
		stat(audioLength(u.AudioSeconds), i18n.T("Audio generated"))
		stat(fmt.Sprint(u.Days), i18n.T("Days with a take"))
		stat(fmt.Sprint(u.Voices), i18n.T("Saved voices (%d cloned, %d designed)", u.Cloned, u.Designed))
		stat(fmt.Sprint(u.Starred), i18n.T("Starred takes"))
		stat(fmt.Sprint(u.Renders), i18n.T("Stories and audiobooks (%s)", audioLength(u.RenderSeconds)))
		stat(fmt.Sprint(u.Dubs), i18n.T("Dubs (%s of source)", audioLength(u.DubSeconds)))
		stat(fmt.Sprint(u.Transcripts), i18n.T("Transcripts (%s)", audioLength(u.TranscriptSeconds)))
		stat(fmt.Sprint(u.Workflows), i18n.T("Workflows"))
	})
	if u.Takes > 0 && u.AudioSeconds > 0 && u.GenSeconds > 0 {
		ui.Text(c, i18n.T("On average a take was made %.1f× faster than real time.", u.AudioSeconds/u.GenSeconds)).FontSize(px(t, 12)).TextColor(t.TextMuted)
	}
	if !u.First.IsZero() {
		ui.Text(c, i18n.T("First take: %s", i18n.Date(u.First))).FontSize(px(t, 12)).TextColor(t.TextMuted)
	}
	table := func(key, title string, rows []struct {
		name string
		n    int
		sec  float64
	}) {
		if len(rows) == 0 {
			return
		}
		sectionTitle(c, title)
		ui.Grid(c.Key(key)).ColumnTracks(ui.Fr(1), ui.Fixed(px(t, 70)), ui.Fixed(px(t, 110))).GapX(px(t, 12)).GapY(px(t, 4)).Children(func() {
			for _, r := range rows {
				ui.Text(c, r.name)
				ui.Text(c, fmt.Sprint(r.n)).TextColor(t.TextMuted).TextAlign(ui.End)
				ui.Text(c, audioLength(r.sec)).TextColor(t.TextMuted).TextAlign(ui.End)
			}
		})
	}
	var byModel, byLang []struct {
		name string
		n    int
		sec  float64
	}
	for _, m := range u.ByModel {
		byModel = append(byModel, struct {
			name string
			n    int
			sec  float64
		}{catalog.Builtin().DisplayName(m.Key), m.N, m.Seconds})
	}
	for _, l := range u.ByLanguage {
		name := i18n.T("Not set")
		if l.Key != "" {
			name = langName(l.Key) // transcribe_models.go: flag and name in the UI's language
		}
		byLang = append(byLang, struct {
			name string
			n    int
			sec  float64
		}{name, l.N, l.Seconds})
	}
	table("usage-models", i18n.T("Takes by model"), byModel)
	table("usage-langs", i18n.T("Takes by language"), byLang)
	if secondaryButton(c.Key("usage-refresh"), i18n.T("Refresh")).Clicked() {
		a.usage = nil
		a.loadUsageStats()
	}
}

// privacyList is what connects to the network, a line each.
func (a *app) privacyList(c *ui.Context) {
	t := c.Theme()
	ui.Column(c.Key("priv-list")).Gap(px(t, 4)).Padding(0, 0, 0, px(t, 8)).Children(func() {
		for _, s := range []string{
			i18n.T("Model downloads from Hugging Face or its mirror (Settings → Models & network)."),
			i18n.T("ffmpeg and yt-dlp, when you download them (Settings → Audio tools, Dubbing)."),
			i18n.T("Videos from links you paste on the Dub page."),
			i18n.T("Writing help and translation, only with a cloud provider you set up (Settings → Writing help)."),
			i18n.T("The Local API, which listens on this Mac only unless you share it on your network."),
			i18n.T("Checking for updates: once a day AudioSmith reads the newest version's file on actnow.dev; nothing about you is sent. Turn it off in Settings → About."),
		} {
			ui.Text(c, "• "+s).FontSize(px(t, 12))
		}
	})
}
