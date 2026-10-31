package main

import (
	"context"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/assets"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/services"
)

// The five bundled samples (0.5 F6b) open as real projects: the story and
// the chapter with their built-in speakers (never a saved voice of the
// user's), the dub with every file its document names, the meeting with
// its speakers and audio, the narration's take in Assets.
func TestBundledSamplesOpen(t *testing.T) {
	useSamples(t, mustSub(assets.Samples, "samples"))
	a, _, tt := newTestApp(t)
	ctx := context.Background()
	for _, key := range []string{"story", "chapter", "narration", "dub", "meeting"} {
		if s, ok := a.sampleCardOf(key); !ok || s.bundled == nil || s.soon {
			t.Fatalf("%s: not bundled", key)
		}
	}

	a.openSample("story")
	tt.Frame()
	st := a.lfStories.doc
	if st.Title != "雨夜修表铺" || len(st.Cast) != 2 || len(st.Lines) != 8 || !st.Production.Dialogue {
		t.Fatalf("story %q cast %+v lines %d dialogue %v", st.Title, st.Cast, len(st.Lines), st.Production.Dialogue)
	}
	for _, m := range st.Cast {
		if !strings.HasPrefix(m.Voice, services.SpeakerRef) {
			t.Errorf("story: %s's voice %q is not a built-in speaker", m.Name, m.Voice)
		}
	}
	if !strings.HasPrefix(st.Voice, services.SpeakerRef) {
		t.Errorf("story narrator %q", st.Voice)
	}
	readyToPlay(t, a, a.lfStories, 55)
	if slices.Contains(a.prefs.Setup.Intros, "stories") == false || a.introOpen {
		t.Errorf("story: intro %v open %v", a.prefs.Setup.Intros, a.introOpen)
	}

	a.openSample("chapter")
	tt.Frame()
	bk := a.lfBook.doc
	if !strings.HasPrefix(bk.Script, "# 第一章") || len(bk.VoiceCast) != 2 || !strings.HasPrefix(bk.Voice, services.SpeakerRef) {
		t.Fatalf("chapter %q cast %v voice %q", bk.Script[:min(40, len(bk.Script))], bk.VoiceCast, bk.Voice)
	}
	for name, v := range bk.VoiceCast {
		if !strings.HasPrefix(v, services.SpeakerRef) {
			t.Errorf("chapter: %s's voice %q", name, v)
		}
	}
	readyToPlay(t, a, a.lfBook, 65)

	a.openSample("dub")
	tt.Frame()
	_, id, _ := strings.Cut(a.prefs.Setup.Samples["dub"], ":")
	dp, err := a.dubSvc.Get(ctx, id)
	if err != nil || len(dp.Segments) != 6 || len(dp.Tracks) != 1 {
		t.Fatalf("dub %+v %v", dp, err)
	}
	if _, err := os.Stat(dp.Source.Path); err != nil {
		t.Fatal(err)
	}
	names := []string{dp.Media.Audio, dp.Media.Background, dp.Media.Poster, dp.Media.Peaks}
	for _, r := range dp.References {
		names = append(names, r.File)
	}
	for _, tr := range dp.Tracks {
		names = append(names, tr.File)
		for _, l := range tr.Lines {
			names = append(names, l.File)
		}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		if _, err := os.Stat(a.dubSvc.Path(dp.ID, n)); err != nil {
			t.Errorf("dub: %v", err)
		}
	}

	a.openSample("meeting")
	tt.Frame()
	_, tid, _ := strings.Cut(a.prefs.Setup.Samples["meeting"], ":")
	tr, err := a.trSvc.Get(ctx, tid)
	if err != nil || len(tr.Speakers) != 2 || len(tr.Segments) < 7 {
		t.Fatalf("meeting %+v %v", tr, err)
	}
	if b, _, err := wav.ReadFile(a.trSvc.AudioPath(tr)); err != nil || b.Frames() < 60*b.Rate {
		t.Fatalf("meeting audio %d frames %v", b.Frames(), err)
	}

	a.openSample("narration")
	tt.Frame()
	if !strings.HasPrefix(a.prefs.Setup.Samples["narration"], "export:") {
		t.Fatalf("narration %q", a.prefs.Setup.Samples["narration"])
	}
	tk := a.lastClip()
	if tk == nil || tk.Voice != "Serena" || !strings.HasPrefix(tk.Text, "清晨六点") || tk.AudioSeconds < 25 || a.router.Path() != "/studio" {
		t.Fatalf("narration take %+v on %s", tk, a.router.Path())
	}
	if _, err := os.Stat(a.lib.svc.Takes.AudioPath(*tk)); err != nil {
		t.Fatal(err)
	}
}

// readyToPlay checks that page p opened its project with a finished
// render of about seconds, whose book is there.
func readyToPlay(t *testing.T, a *app, p *lfPage, seconds float64) {
	t.Helper()
	r := a.lfRenders[p.render]
	if p.render == "" || p.doc.Render != p.render || r == nil || r.rec.State != core.RenderDone || r.body.Output == nil {
		t.Fatalf("%s: render %q (doc %q) %+v", p.kind, p.render, p.doc.Render, r)
	}
	out := r.body.Output
	if out.Seconds < seconds || out.Loudness.Integrated == 0 || len(out.Chapters) != 1 {
		t.Errorf("%s: output %+v", p.kind, out)
	}
	if _, _, err := wav.ReadFile(out.Book); err != nil {
		t.Errorf("%s: book %v", p.kind, err)
	}
}

// The samples' texts are written, not colloquial: no filler words.
func TestBundledSamplesNoFillers(t *testing.T) {
	root := mustSub(assets.Samples, "samples")
	fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (path.Ext(p) != ".json" && path.Ext(p) != ".txt") {
			return err
		}
		b, _ := fs.ReadFile(root, p)
		for _, w := range []string{"嗯", "啊", "呃", "那个", "就是说", "然后呢", "um,", "uh,"} {
			if strings.Contains(string(b), w) {
				t.Errorf("%s: filler %q", p, w)
			}
		}
		return nil
	})
}
