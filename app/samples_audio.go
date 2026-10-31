package main

// A sample's finished audio (0.5 F6b): bundled as Ogg Vorbis (small, and
// the players read it), decoded when the sample is copied and kept as
// what the user would have made: a story's or a chapter's render, the
// narration's take in Text to speech. The page opens ready to play.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	myaudio "github.com/egoist/mygo/audio"
	_ "github.com/egoist/mygo/audio/vorbis" // the samples' audio is Ogg Vorbis
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/services"
)

// decodeSampleAudio decodes file name of fsys (Ogg Vorbis or WAV).
func decodeSampleAudio(fsys fs.FS, name string) (services.ImportedAudio, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return services.ImportedAudio{}, err
	}
	d, _, err := myaudio.NewDecoder(bytes.NewReader(b))
	if err != nil {
		return services.ImportedAudio{}, fmt.Errorf("%s: %w", name, err)
	}
	ch := d.Channels()
	out := services.ImportedAudio{Rate: d.SampleRate(), Channels: ch}
	buf := make([]float32, 4096*ch)
	for {
		n, err := d.Read(buf)
		out.Samples = append(out.Samples, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, fmt.Errorf("%s: %w", name, err)
		}
		if n == 0 {
			break
		}
	}
	if len(out.Samples) == 0 {
		return out, fmt.Errorf("%s: no audio", name)
	}
	return out, nil
}

// sampleRender keeps a story's or a chapter's bundled audio as project
// id's render (the document's Render): its ID.
func (a *app) sampleRender(ctx context.Context, fsys fs.FS, m sampleManifest, id string, doc services.LongformDoc) (string, error) {
	audio, err := decodeSampleAudio(fsys, path.Join(m.ID, m.file(roleAudio)))
	if err != nil {
		return "", err
	}
	// The samples name built-in speakers only: no saved voice is needed.
	noFill := func(*core.Take, *core.Voice, catalog.Entry) {}
	spec, _, _ := services.CompileBook(&doc, lfResolverOf(&doc, nil, noFill, func(core.Voice, string) string { return "" }))
	spec.Project, spec.Kind, spec.Title = id, doc.Kind, doc.Title
	if len(spec.Chapters) != 1 { // the audio is the whole: one chapter
		one := services.ChapterSpec{}
		for i, ch := range spec.Chapters {
			if i == 0 {
				one.Title = ch.Title
			}
			one.Lines = append(one.Lines, ch.Lines...)
		}
		spec.Chapters = []services.ChapterSpec{one}
	}
	rec, err := a.lib.svc.Longform.ImportRender(ctx, spec, []services.ImportedAudio{audio})
	if err != nil {
		return "", err
	}
	_, _, err = a.lib.svc.Library.UpdateDoc(ctx, id, func(_ *core.Project, d *services.LongformDoc) error {
		d.Render = rec.ID
		return nil
	})
	return rec.ID, err
}

// sampleTake is a narration's recipe (its project file): what the take
// was spoken with.
type sampleTake struct {
	Model    string  `json:"model"` // a catalogue repo
	Voice    string  `json:"voice,omitempty"`
	Language string  `json:"language,omitempty"`
	Seed     int64   `json:"seed,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
}

// sampleNarrationTake keeps a narration's bundled audio as a take of Text
// to speech (its latest): its ID.
func (a *app) sampleNarrationTake(ctx context.Context, fsys fs.FS, m sampleManifest) (string, error) {
	audio, err := decodeSampleAudio(fsys, path.Join(m.ID, m.file(roleAudio)))
	if err != nil {
		return "", err
	}
	script, err := fs.ReadFile(fsys, path.Join(m.ID, m.file(roleScript)))
	if err != nil {
		return "", err
	}
	var rc sampleTake
	if f := m.file(roleProject); f != "" {
		b, err := fs.ReadFile(fsys, path.Join(m.ID, f))
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(b, &rc); err != nil {
			return "", fmt.Errorf("%s: %w", f, err)
		}
	}
	svc := a.lib.svc.Takes
	now := time.Now()
	id := core.NewID(now)
	file := id + ".wav"
	buf := dsp.Buffer{Samples: audio.Samples, Rate: audio.Rate, Channels: audio.Channels}
	if err := wav.WriteFile(filepath.Join(svc.AudioDir(), file), buf, wav.PCM16, wav.Options{}); err != nil {
		return "", err
	}
	t := core.Take{ID: id, CreatedAt: now, File: file, Text: strings.TrimSpace(string(script)), Model: rc.Model, Voice: rc.Voice,
		Language: rc.Language, Seed: rc.Seed, Speed: rc.Speed, SampleRate: audio.Rate, AudioSeconds: float64(buf.Frames()) / float64(audio.Rate)}
	t, err = svc.Add(ctx, t)
	return t.ID, err
}

// sampleTranscriptAudio is a transcript's audio as the transcripts keep
// it: a WAV as it is, other audio (Ogg Vorbis) decoded to a 16-bit WAV
// (re-transcribing, clips and the dub hand-off read WAV); its reader and
// extension.
func sampleTranscriptAudio(fsys fs.FS, name string) (io.ReadCloser, string, error) {
	if strings.EqualFold(path.Ext(name), ".wav") {
		f, err := fsys.Open(name)
		return f, ".wav", err
	}
	audio, err := decodeSampleAudio(fsys, name)
	if err != nil {
		return nil, "", err
	}
	var b bytes.Buffer
	if err := wav.Encode(&b, dsp.Buffer{Samples: audio.Samples, Rate: audio.Rate, Channels: audio.Channels}, wav.PCM16); err != nil {
		return nil, "", err
	}
	return io.NopCloser(&b), ".wav", nil
}
