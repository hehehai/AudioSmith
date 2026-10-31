package services

// Importing a finished render (0.5 F6b): a bundled sample story or
// audiobook comes with its audio, kept as the project's render so the
// page opens ready to play, export and check, as if it had been rendered
// here. The files are the ones a render writes (chapters/NNN.wav,
// book.wav, cues.txt, report.json), with the loudness measured.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// ImportedAudio is decoded audio: interleaved samples at Rate with
// Channels.
type ImportedAudio struct {
	Samples  []float32
	Rate     int
	Channels int
}

// ImportRender records audio as a finished render of spec (its project,
// kind, title and chapters; one audio per chapter, all at one rate and
// channel count) and publishes it.
func (s *Longform) ImportRender(ctx context.Context, spec LongformSpec, chapters []ImportedAudio) (core.Render, error) {
	if spec.Kind != core.ProjectStory && spec.Kind != core.ProjectAudiobook {
		return core.Render{}, fmt.Errorf("import render: kind %q: %w", spec.Kind, core.ErrInvalid)
	}
	if len(chapters) == 0 || len(chapters) != len(spec.Chapters) {
		return core.Render{}, fmt.Errorf("import render: %d audios for %d chapters: %w", len(chapters), len(spec.Chapters), core.ErrInvalid)
	}
	rate, channels := chapters[0].Rate, chapters[0].Channels
	for _, c := range chapters {
		if c.Rate != rate || c.Channels != channels || rate <= 0 || channels <= 0 {
			return core.Render{}, fmt.Errorf("import render: chapters of other formats: %w", core.ErrInvalid)
		}
	}
	if spec.Title == "" {
		spec.Title = "Untitled"
	}
	now := s.now()
	id := core.NewID(now)
	dir := filepath.Join(s.dataDir, "renders", id)
	if err := os.MkdirAll(filepath.Join(dir, "chapters"), 0o755); err != nil {
		return core.Render{}, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	format := wav.Format{Rate: rate, Channels: channels, Encoding: wav.Float32}
	res := AssembleResult{Book: filepath.Join(dir, "book.wav"), Cues: filepath.Join(dir, "cues.txt"), Report: filepath.Join(dir, "report.json")}
	body := RenderBody{Spec: spec, Summary: summarize(&spec), Dir: dir, Runs: 1, Started: now, Finished: now}
	book := newVerifier(rate, channels)
	var all []float32
	var rendered []doc.RenderedChapter
	start := 0.0
	for i, c := range chapters {
		file := filepath.Join(dir, "chapters", fmt.Sprintf("%03d.wav", i+1))
		if err := wav.WriteFile(file, dsp.Buffer{Samples: c.Samples, Rate: rate, Channels: channels}, wav.Float32, wav.Options{}); err != nil {
			return core.Render{}, err
		}
		v := newVerifier(rate, channels)
		v.Write(c.Samples)
		book.Write(c.Samples)
		all = append(all, c.Samples...)
		secs := float64(len(c.Samples)/channels) / float64(rate)
		title := spec.Chapters[i].Title
		body.Chapters = append(body.Chapters, RenderChapter{Title: title, Chars: chapterChars(spec.Chapters[i]), Lines: len(spec.Chapters[i].Lines),
			State: ChapterDone, File: file, AudioSeconds: secs})
		res.Chapters = append(res.Chapters, BookChapter{Title: title, File: file, Start: start, Seconds: secs, Report: v.Report(nil)})
		rendered = append(rendered, doc.RenderedChapter{Title: title, Duration: time.Duration(secs * float64(time.Second)), OK: true})
		start += secs
	}
	w, err := wav.Create(res.Book, format, wav.Options{Large: true})
	if err != nil {
		return core.Render{}, err
	}
	if err := w.Write(all); err != nil {
		w.Abort()
		return core.Render{}, err
	}
	if err := w.Close(); err != nil {
		return core.Render{}, err
	}
	res.Seconds = start
	if fi, err := os.Stat(res.Book); err == nil {
		res.Bytes = fi.Size()
	}
	res.Loudness = book.Report(loudnessTarget(spec.Book.Loudness))
	if err := writeFileAtomic(res.Cues, []byte(doc.CueSheet(doc.MarksFromDurations(rendered), Untitled))); err != nil {
		return core.Render{}, err
	}
	rep, _ := json.MarshalIndent(BookReport{Title: spec.Title, Target: spec.Book.Loudness, Book: res.Loudness, Chapters: res.Chapters, Made: now}, "", "  ")
	if err := writeFileAtomic(res.Report, rep); err != nil {
		return core.Render{}, err
	}
	body.Output = &res
	b, err := json.Marshal(body)
	if err != nil {
		return core.Render{}, err
	}
	rec := core.Render{ID: id, ProjectID: spec.Project, Kind: spec.Kind, Title: spec.Title, State: core.RenderDone,
		AudioSeconds: res.Seconds, Body: b, CreatedAt: now, UpdatedAt: now}
	if err := s.renders.Put(ctx, rec); err != nil {
		return core.Render{}, err
	}
	ok = true
	s.bus.Publish(core.RenderChanged{Render: rec})
	return rec, nil
}
