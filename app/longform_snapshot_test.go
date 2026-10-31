package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestSnapshotsLongform renders the M3 pages (Stories, Audiobook,
// Projects) in their main states, light and dark, English and Chinese,
// like TestSnapshots:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsLongform .
func TestSnapshotsLongform(t *testing.T) { runSnapshots(t, snapshotsLongform) }

func snapshotsLongform(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		i18n.SetLocale("en")
		a, eng, tt := newTestApp(t)
		useLfSynth(a)
		if run.lang != "en" {
			a.updateSettings(func(s *settings.Settings) { s.General.Language = run.lang })
		}
		tt.SetDark(run.dark)
		tt.SetScale(2)
		suffix := "light"
		if run.dark {
			suffix = "dark"
		}
		if run.lang != "en" {
			suffix += "-" + run.lang
		}
		save := func(name string) {
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		ctx := context.Background()
		at := time.Date(2026, 10, 13, 21, 50, 0, 0, time.Local)
		addTake(t, a, core.Take{ID: "T1", CreatedAt: at, Text: i18n.T("The last ferry left the harbour at eleven. [pause 500ms] Fog had settled on the water like wool."),
			Model: kokoro, Voice: "af_heart", SampleRate: 24000, AudioSeconds: 4}, 1)
		a.lib.svc.Voices.Save(ctx, core.Voice{Name: "Mara", Kind: core.VoiceCloned, Language: "en"})

		// Stories: empty, the sample with a line's options, rendering, ready.
		a.navigate("/stories")
		save("stories-empty")
		p := a.lfStories
		a.loadSampleStory(p)
		p.open["production"] = true
		p.sel = 2
		save("stories")
		p.sel = -1
		p.saveName = i18n.T("The Night Ferry")
		a.saveLf(p, false)
		a.startLf(p, "")
		jl := lfJobs(eng)
		eng.run(jl[0].ID, a.lib.svc.Longform.ChapterExecutor())
		eng.progress(jl[1].ID, "generating", 0.4, 6)
		p.open["production"], p.open["cast"], p.open["setup"] = false, false, false
		p.open["project"] = true
		save("stories-rendering")
		playLongform(t, a, eng)
		save("stories-ready")

		// Audiobook: the sample with its warnings, rendering, ready with
		// the read-along.
		a.navigate("/audiobook")
		save("audiobook-empty")
		b := a.lfBook
		a.loadSample(b)
		a.setManuscript(b, b.doc.Script+"\n\n# "+i18n.T("Epilogue")+"\n\n[voice:Nobody] [boop]")
		b.doc.Book.Loudness = services.LoudnessACX
		b.open["plan"] = true
		b.open["lexicon"] = true
		b.doc.Lexicon = []services.LexiconEntry{{Word: "Harrow", Say: "Harro"}}
		save("audiobook")
		a.setManuscript(b, sampleManuscript())
		b.open["plan"], b.open["lexicon"], b.open["cast"] = false, false, true
		b.open["book"] = true
		tt.Frame()
		a.startLf(b, "")
		jl = lfJobs(eng)
		eng.run(jl[0].ID, a.lib.svc.Longform.ChapterExecutor())
		eng.progress(jl[1].ID, "generating", 0.7, 9)
		save("audiobook-rendering")
		playLongform(t, a, eng)
		b.open["book"], b.open["cast"], b.open["setup"] = false, false, false
		b.open["loudness"] = true
		tt.Frame()
		tt.Frame()
		save("audiobook-ready")
		tt.SetSize(1100, 1500) // the whole panel, with the read-along
		save("audiobook-ready-tall")
		tt.SetSize(1100, 720)

		// Projects: the list, cards, a delete.
		a.navigate("/projects")
		tt.Frame()
		tt.Frame()
		save("projects")
		a.projects.cards = 1
		save("projects-cards")
		a.projects.cards = 0
		if items := a.projItems("", ""); len(items) > 0 {
			a.projects.confirm, a.projects.confirmOpen = items[0], true
		}
		save("projects-delete")
		a.projects.confirmOpen = false
		a.toasts = nil
	}
}
