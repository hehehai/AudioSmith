package main

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// b1Run is one appearance of TestSnapshotsB1: a window size, a text
// scale, a language and light or dark.
type b1Run struct {
	size  string
	w, h  int
	scale float64
	lang  string
	dark  bool
}

func (r b1Run) suffix() string {
	s := r.size + "-light"
	if r.dark {
		s = r.size + "-dark"
	}
	if r.lang != "en" {
		s += "-" + r.lang
	}
	return s
}

// b1Runs are the appearances of the phase-B review: the default window
// light and dark in both languages, the minimum window, and the minimum
// at 125 % text.
var b1Runs = []b1Run{
	{"1100", 1100, 720, 1, "en", false}, {"1100", 1100, 720, 1, "en", true},
	{"1100", 1100, 720, 1, "zh-Hans", false}, {"1100", 1100, 720, 1, "zh-Hans", true},
	{"min", 820, 520, 1, "en", false}, {"min", 820, 520, 1, "zh-Hans", false},
	{"min125", 820, 520, 1.25, "en", false}, {"min125", 820, 520, 1.25, "zh-Hans", false},
	{"min125", 820, 520, 1.25, "en", true},
}

// b1Shooter saves a tester's frames as <name>-<suffix>.png in out, for
// the names AUDIOSMITH_B1_ONLY lets through (comma-separated prefixes).
type b1Shooter struct {
	t    *testing.T
	out  string
	run  b1Run
	only []string
}

func (s *b1Shooter) want(group string) bool {
	if len(s.only) == 0 {
		return true
	}
	for _, o := range s.only {
		if strings.HasPrefix(group, o) || strings.HasPrefix(o, group) {
			return true
		}
	}
	return false
}

func (s *b1Shooter) save(tt *ui.Tester, name string) {
	if !s.want(name) {
		return
	}
	tt.Frame()
	tt.Frame()
	f, err := os.Create(filepath.Join(s.out, fmt.Sprintf("%s-%s.png", name, s.run.suffix())))
	if err != nil {
		s.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		s.t.Fatal(err)
	}
}

// setup gives a test app the run's appearance.
func (s *b1Shooter) setup(a *app, tt *ui.Tester) {
	r := s.run
	a.updateSettings(func(st *settings.Settings) {
		if r.lang != "en" {
			st.General.Language = r.lang
		}
		st.Appearance.Scale = r.scale
	})
	tt.SetSize(r.w, r.h)
	tt.SetDark(r.dark)
	tt.SetScale(2)
}

// TestSnapshotsB1 renders the phase-B1 pages of the 0.3 redesign —
// Stories, Audiobook, Dub (every step), Batch, Transcribe and Projects —
// at the review's sizes (b1Runs):
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/b1 go test -run TestSnapshotsB1 .
//	AUDIOSMITH_B1_ONLY=stories,dub-generate AUDIOSMITH_B1_SIZES=1100,min125 …
func TestSnapshotsB1(t *testing.T) { runSnapshots(t, snapshotsB1) }

func snapshotsB1(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	var only, sizes []string
	if v := os.Getenv("AUDIOSMITH_B1_ONLY"); v != "" {
		only = strings.Split(v, ",")
	}
	if v := os.Getenv("AUDIOSMITH_B1_SIZES"); v != "" {
		sizes = strings.Split(v, ",")
	}
	for _, run := range b1Runs {
		if len(sizes) > 0 && !strings.Contains(","+strings.Join(sizes, ",")+",", ","+run.size+",") {
			continue
		}
		s := &b1Shooter{t: t, out: out, run: run, only: only}
		if s.want("stories") || s.want("audiobook") || s.want("projects") {
			b1Longform(t, s)
		}
		if s.want("dub") || s.want("batch") {
			b1Dub(t, s)
		}
		if s.want("transcribe") {
			b1Transcribe(t, s)
		}
	}
}

func b1Longform(t *testing.T, s *b1Shooter) {
	i18n.SetLocale("en")
	a, eng, tt := newTestApp(t)
	useLfSynth(a)
	s.setup(a, tt)
	save := func(name string) { s.save(tt, name) }
	ctx := context.Background()
	at := time.Date(2026, 10, 13, 21, 50, 0, 0, time.Local)
	addTake(t, a, core.Take{ID: "T1", CreatedAt: at, Text: i18n.T("The last ferry left the harbour at eleven. [pause 500ms] Fog had settled on the water like wool."),
		Model: kokoro, Voice: "af_heart", SampleRate: 24000, AudioSeconds: 4}, 1)
	a.lib.svc.Voices.Save(ctx, core.Voice{Name: "Mara", Kind: core.VoiceCloned, Language: "en"})

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
	a.toasts = nil
	save("stories-rendering")
	playLongform(t, a, eng)
	a.toasts = nil
	save("stories-ready")

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
	a.toasts = nil
	save("audiobook-rendering")
	playLongform(t, a, eng)
	b.open["book"], b.open["cast"], b.open["setup"] = false, false, false
	b.open["loudness"] = true
	a.toasts = nil
	save("audiobook-ready")

	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	a.toasts = nil
	save("projects")
	a.projects.cards = 1
	save("projects-cards")
	a.projects.cards = 0
}

func b1Dub(t *testing.T, s *b1Shooter) {
	i18n.SetLocale("en")
	d := newDubTest(t, 6)
	a, tt := d.a, d.tt
	s.setup(a, tt)
	save := func(name string) {
		a.toasts = nil
		s.save(tt, name)
	}
	a.navigate("/dub")
	save("dub-empty")

	a.dubSourceFile(d.src)
	a.dubSet(func(p *dub.Project) { p.Targets = []string{"es", "fr"} })
	save("dub-source")

	a.dubStart()
	a.dubStep = dubStepTranscribe
	save("dub-transcript")

	a.dubTranslate(false)
	a.dubLang = "es"
	a.dubStep = dubStepTranslate
	a.dubSet(func(p *dub.Project) {
		l := p.Segments[1].Lines["es"]
		l.Fit = &translate.Fit{Predicted: 3.9, Slot: 2.2, Ratio: 1.77, Over: 1.7, Verdict: translate.FitOverflow}
		l.Shorter = "¿Qué tal hoy?"
		p.Segments[1].Lines["es"] = l
		p.Segments[3].Lines["es"] = dub.Line{Err: translate.CodeLineFailed}
	})
	save("dub-translate")

	a.dubStep = dubStepCast
	save("dub-cast")

	a.dubSet(func(p *dub.Project) { p.Segments[3].Lines["es"] = dub.Line{Text: "Me alegro de oírlo."} })
	a.dubGenerate()
	a.dubStep = dubStepGenerate
	a.dubOpen = a.dub.Segments[0].ID
	a.dubSeek(4)
	save("dub-generate")
	a.dubOpen = ""

	a.dubStep = dubStepExport
	save("dub-export")

	a.dubPaste.open = true
	a.dubPaste.text = "1\n00:00:00,500 --> 00:00:02,000\nHola.\n\n2\n00:00:02,800 --> 00:00:05,000\n¿Cómo estás hoy?\n"
	save("dub-paste")
	a.dubPaste = dubPasteState{}
	a.dubGloss.open = true
	a.dubSet(func(p *dub.Project) {
		p.Glossary = translate.Glossary{{Source: "AudioSmith", Target: "AudioSmith", Note: "brand"}, {Source: "dubbing", Target: "doblaje", Auto: true}}
	})
	save("dub-glossary")
	a.dubGloss.open = false

	a.navigate("/batch")
	save("batch-empty")
	a.batchDropped([]string{d.src})
	a.batchSetSettings(func(st *services.BatchSettings) { st.Targets = []string{"es", "de"} })
	a.batchEnqueue()
	a.batchDropped([]string{filepath.Join(filepath.Dir(d.src), "Episode 2 of the long-running documentary series.mov")})
	a.loadBatch()
	a.batch.tab = 1
	save("batch")
}

func b1Transcribe(t *testing.T, s *b1Shooter) {
	i18n.SetLocale("en")
	m := newM2Test(t)
	a, tt, eng := m.a, m.tt, m.eng
	s.setup(a, tt)
	save := func(name string) {
		a.toasts = nil
		s.save(tt, name)
	}
	a.navigate("/transcripts")
	save("transcribe-empty")

	now := time.Now()
	d := core.Transcript{ID: "d1", CreatedAt: now.Add(-3 * time.Minute), UpdatedAt: now, Source: core.SourceDictation, App: "Slack",
		Mode: "fast", Language: "zh", Duration: 3.2, Segments: []core.TranscriptSegment{{Start: 0, End: 3.2, Text: "明天下午三点开会"}}, Clean: "明天下午三点开会。"}
	putTranscript(t, a, d)
	long := sampleTranscript("t1", now.Add(-26*time.Hour))
	long.Duration = 74
	long.Segments = append(long.Segments,
		core.TranscriptSegment{Start: 4.1, End: 9.8, Text: "You are a bold one. I have been looking forward to this.", Edited: true},
		core.TranscriptSegment{Start: 10.5, End: 14, Text: "So uncivilized."})
	putTranscript(t, a, long)
	a.selectTranscript("t1")
	a.trDetail.Clean = "Hello there. General Kenobi. You're a bold one — I've been looking forward to this. So uncivilized."
	a.trDetail.CleanBy = "Ollama · qwen3:4b"
	a.trOrig = true
	save("transcribe-detail")
	a.trOrig = false

	a.selectTranscript("")
	src := filepath.Join(testDir(t), "keynote.mov")
	os.WriteFile(src, []byte("mov"), 0o644)
	a.transcribeFile(src)
	r, _ := transcribeReq(t, eng)
	eng.progress(r.ID, "aligning", 0.62, 0)
	save("transcribe-running")
	eng.cancelled(r.ID)
}
