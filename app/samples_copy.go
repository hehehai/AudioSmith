package main

// Opening a sample (samples.go): a bundled one is copied into the
// library as a normal project — editable, deletable — and opened; the
// copy is remembered (settings' Setup.Samples), so opening the sample
// again asks whether to open that copy or make a new one. The built-in
// fallbacks (no bundled sample yet) make their project the way the
// Stories and Audiobook pages' 载入示例 does.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// samplesState is the samples' view state.
type samplesState struct {
	sampleAsk     string // the sample whose copy exists: open it, or a new one?
	sampleAskOpen bool
	sampleBusy    bool // a sample is being copied
}

// openSample opens the sample of a card: its copy, after asking, when
// one was made before and is still there; a new one otherwise.
func (a *app) openSample(key string) {
	s, ok := a.sampleCardOf(key)
	if !ok || s.soon {
		a.toast(i18n.T("The sample projects come with the next update."))
		return
	}
	if a.sampleBusy {
		return
	}
	ref := a.prefs.Setup.Samples[key]
	if ref == "" {
		a.copySample(s)
		return
	}
	a.background(func() {
		ok := a.sampleCopyExists(ref)
		a.post(func() {
			if ok {
				a.sampleAsk, a.sampleAskOpen = key, true
				return
			}
			a.copySample(s)
		})
	})
}

// rememberSample keeps the copy a sample made ("<kind>:<id>").
func (a *app) rememberSample(key, ref string) {
	a.updateSettings(func(st *settings.Settings) {
		m := make(map[string]string, len(st.Setup.Samples)+1)
		for k, v := range st.Setup.Samples {
			m[k] = v
		}
		m[key] = ref
		st.Setup.Samples = m
	})
}

// sampleCopyExists reports whether the copy ref is still in the library.
func (a *app) sampleCopyExists(ref string) bool {
	kind, id, _ := strings.Cut(ref, ":")
	ctx := context.Background()
	var err error
	switch kind {
	case sampleStory, sampleAudiobook:
		_, err = a.lib.svc.Projects.Get(ctx, id)
	case sampleDub:
		if a.dubSvc == nil {
			return false
		}
		_, err = a.dubSvc.Get(ctx, id)
	case sampleTranscriptKind:
		if a.trSvc == nil {
			return false
		}
		_, err = a.trSvc.Get(ctx, id)
	case "export":
		_, err = a.lib.svc.Library.Export(ctx, id)
	default:
		return false
	}
	return err == nil
}

// openSampleCopy opens the copy ref of sample key.
func (a *app) openSampleCopy(key, ref string) {
	kind, id, _ := strings.Cut(ref, ":")
	oplog.Info("sample.open", "sample", key, "copy", ref)
	a.skipIntroOf(map[string]string{sampleStory: "/stories", sampleAudiobook: "/audiobook", sampleDub: "/dub",
		sampleTranscriptKind: "/transcripts"}[kind]) // the user chose to try the sample: no feature intro first
	switch kind {
	case sampleStory:
		a.openProj(projItem{kind: projStory, id: id})
	case sampleAudiobook:
		a.openProj(projItem{kind: projBook, id: id})
	case sampleDub:
		a.openDub(id)
	case sampleTranscriptKind:
		a.navigate("/transcripts")
		a.selectTranscript(id)
	case "export": // a narration: its script and take in Text to speech, its audio in Assets
		if s, ok := a.sampleCardOf(key); ok && s.bundled != nil {
			a.sampleNarrate(*s.bundled)
		}
		lib := a.lib.svc.Library
		a.background(func() {
			e, err := lib.Export(context.Background(), id)
			a.post(func() {
				if err == nil && e.SourceID != "" && a.findTake(e.SourceID) != nil {
					a.lastClipID = e.SourceID
				}
			})
		})
	}
}

// copySample makes a new copy of a sample and opens it.
func (a *app) copySample(s sampleCard) {
	oplog.Info("sample.copy", "sample", s.key, "bundled", s.bundled != nil)
	if s.bundled == nil {
		a.useBuiltinSample(s.key)
		return
	}
	m, fsys := *s.bundled, samplesFS
	in := sampleInputs{data: a.dataDir(), voice: a.lfStories.doc.Voice}
	if m.Kind == sampleAudiobook {
		in.voice = a.lfBook.doc.Voice
	}
	in.model = a.preset().Repo
	in.voices = a.lfVoices(entryFor(in.model))
	a.sampleBusy = true
	a.background(func() {
		ref, err := a.copyBundledSample(context.Background(), fsys, m, in)
		a.post(func() {
			a.sampleBusy = false
			if err != nil {
				oplog.Err("sample.copy", err)
				a.toastWarn(i18n.T("Could not open the sample: %v", err))
				return
			}
			if ref == "" { // a narration without audio: nothing to keep
				a.sampleNarrate(m)
				return
			}
			a.rememberSample(m.ID, ref)
			a.projects.dirty = true
			a.openSampleCopy(m.ID, ref)
		})
	})
}

// sampleInputs is what a copy needs from the UI thread.
type sampleInputs struct {
	data, model, voice string
	voices             []lfVoice
}

// copyBundledSample copies sample m of fsys into the library: the copy's
// ref ("" for a narration without audio).
func (a *app) copyBundledSample(ctx context.Context, fsys fs.FS, m sampleManifest, in sampleInputs) (string, error) {
	read := func(name string) ([]byte, error) { return fs.ReadFile(fsys, path.Join(m.ID, name)) }
	title := localized(m.Title)
	switch m.Kind {
	case sampleStory, sampleAudiobook:
		kind := core.ProjectStory
		if m.Kind == sampleAudiobook {
			kind = core.ProjectAudiobook
		}
		doc := services.NewLongformDoc(kind, in.model)
		doc.Voice = in.voice
		if f := m.file(roleProject); f != "" {
			b, err := read(f)
			if err != nil {
				return "", err
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				return "", fmt.Errorf("%s: %w", f, err)
			}
		} else {
			b, err := read(m.file(roleScript))
			if err != nil {
				return "", err
			}
			if kind == core.ProjectStory {
				if t, _ := storyFromMarkup(&doc, string(b), in.voices); t != "" {
					title = t
				}
			} else {
				doc.Script = strings.TrimSpace(string(b))
			}
		}
		if doc.Title == "" {
			doc.Title = title
		}
		pr, err := a.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: kind, Title: doc.Title}, doc)
		if err != nil {
			return "", err
		}
		exp := "book"
		if kind == core.ProjectStory {
			exp = "story"
		}
		render := ""
		if m.file(roleAudio) != "" { // the page opens with it, ready to play
			if render, err = a.sampleRender(ctx, fsys, m, pr.ID, doc); err != nil {
				return "", err
			}
		}
		if _, err := a.sampleExport(ctx, fsys, m, in.data, pr.ID, exp, doc.Title, render); err != nil {
			return "", err
		}
		return m.Kind + ":" + pr.ID, nil
	case sampleDub:
		return a.copySampleDub(ctx, fsys, m, title)
	case sampleTranscriptKind:
		return a.copySampleTranscript(ctx, fsys, m, title)
	default: // a narration: its audio, when it has one, goes to Assets
		if m.file(roleAudio) == "" {
			return "", nil
		}
		take, err := a.sampleNarrationTake(ctx, fsys, m) // Text to speech's latest take
		if err != nil {
			return "", err
		}
		id := core.NewID(time.Now())
		ex, err := a.sampleExport(ctx, fsys, m, in.data, id, "take", title, take)
		if err != nil || ex == "" {
			return "", err
		}
		return "export:" + ex, nil
	}
}

// sampleExport copies a sample's audio to <data>/samples/<id>/ and
// records it (an export record: Assets lists it) as made from source (the
// render or take the copy keeps); its record's ID, "" when the sample has
// no audio.
func (a *app) sampleExport(ctx context.Context, fsys fs.FS, m sampleManifest, data, id, kind, title, source string) (string, error) {
	f := m.file(roleAudio)
	if f == "" {
		return "", nil
	}
	dst := filepath.Join(data, "samples", id, path.Base(f))
	if err := copyFromFS(fsys, path.Join(m.ID, f), dst); err != nil {
		return "", err
	}
	e := core.Export{Kind: kind, Title: title, Path: dst, Format: strings.TrimPrefix(path.Ext(f), "."), SourceID: source}
	if kind != "take" {
		e.ProjectID = id
	}
	e, err := a.lib.svc.Library.RecordExport(ctx, e)
	return e.ID, err
}

// copySampleDub copies a dub: its media into the new dub's folder, then
// the project.
func (a *app) copySampleDub(ctx context.Context, fsys fs.FS, m sampleManifest, title string) (string, error) {
	if a.dubSvc == nil {
		return "", core.ErrUnsupported
	}
	b, err := fs.ReadFile(fsys, path.Join(m.ID, m.file(roleProject)))
	if err != nil {
		return "", err
	}
	var p dub.Project
	if err := json.Unmarshal(b, &p); err != nil {
		return "", fmt.Errorf("%s: %w", m.file(roleProject), err)
	}
	p.ID, p.Batch, p.CreatedAt = core.NewID(time.Now()), "", time.Time{}
	dir := a.dubSvc.Dir(p.ID)
	for _, f := range m.Files {
		if f.Role == roleMedia {
			if err := copyFromFS(fsys, path.Join(m.ID, f.Path), filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
				return "", err
			}
		}
	}
	if p.Source.Path != "" && !filepath.IsAbs(p.Source.Path) {
		p.Source.Path = filepath.Join(dir, filepath.FromSlash(p.Source.Path))
	}
	if p.Title == "" {
		p.Title = title
	}
	made, err := a.dubSvc.Create(ctx, &p)
	if err != nil {
		return "", err
	}
	return sampleDub + ":" + made.ID, nil
}

// copySampleTranscript copies a transcript and its audio.
func (a *app) copySampleTranscript(ctx context.Context, fsys fs.FS, m sampleManifest, title string) (string, error) {
	if a.trSvc == nil {
		return "", core.ErrUnsupported
	}
	b, err := fs.ReadFile(fsys, path.Join(m.ID, m.file(roleProject)))
	if err != nil {
		return "", err
	}
	var t core.Transcript
	if err := json.Unmarshal(b, &t); err != nil {
		return "", fmt.Errorf("%s: %w", m.file(roleProject), err)
	}
	if t.Title == "" {
		t.Title = title
	}
	t.SourcePath = ""
	var made core.Transcript
	if t.Audio != "" {
		f, ext, err := sampleTranscriptAudio(fsys, path.Join(m.ID, t.Audio))
		if err != nil {
			return "", err
		}
		defer f.Close()
		made, err = a.trSvc.Import(ctx, t, ext, f)
		if err != nil {
			return "", err
		}
	} else if made, err = a.trSvc.Import(ctx, t, "", nil); err != nil {
		return "", err
	}
	return sampleTranscriptKind + ":" + made.ID, nil
}

// copyFromFS writes file name of fsys to dst (its folder made).
func copyFromFS(fsys fs.FS, name, dst string) error {
	f, err := fsys.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.part")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, f)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// sampleNarrate opens Text to speech with a narration sample's script.
func (a *app) sampleNarrate(m sampleManifest) {
	b, err := fs.ReadFile(samplesFS, path.Join(m.ID, m.file(roleScript)))
	if err != nil {
		a.toastWarn(i18n.T("Could not open the sample: %v", err))
		return
	}
	a.homeToStudio(strings.TrimSpace(string(b)))
}

// useBuiltinSample is a card's fallback while its sample is not bundled:
// a story or a chapter made from the built-in script, the product
// narration in Text to speech.
func (a *app) useBuiltinSample(key string) {
	switch key {
	case "story", "chapter":
		p, kind := a.lfStories, sampleStory
		if key == "chapter" {
			p, kind = a.lfBook, sampleAudiobook
		}
		p.onSaved = func(id string) { a.rememberSample(key, kind+":"+id) }
		if !a.lfCreate(p, func() { a.lfFillSample(p) }) {
			p.onSaved = nil
		}
	case "narration":
		for _, ex := range studioExamples() {
			if ex.key == "product" {
				a.navigate("/studio")
				a.fillExample(ex)
			}
		}
	default:
		a.toast(i18n.T("The sample projects come with the next update."))
	}
}

// sampleAskDialog asks what to do with a sample opened before.
func (a *app) sampleAskDialog(c *ui.Context) {
	if !a.sampleAskOpen {
		return
	}
	key := a.sampleAsk
	s, _ := a.sampleCardOf(key)
	dialogFrame(c, "sample-ask", &a.sampleAskOpen, i18n.T("You opened this sample before"), s.title, 440, func() {
		ui.Text(c, i18n.T("Open the copy you made then, or make a new copy of the sample?")).Wrap()
	}, func() {
		ui.Spacer(c)
		if ghostButton(c.Key("sample-new"), i18n.T("Make a new copy")).Clicked() {
			a.sampleAskOpen = false
			a.copySample(s)
		}
		if primaryButton(c.Key("sample-open"), i18n.T("Open my copy")).Clicked() {
			a.sampleAskOpen = false
			a.openSampleCopy(key, a.prefs.Setup.Samples[key])
		}
	})
}
