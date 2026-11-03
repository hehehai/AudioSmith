package services

// Voice packages: importing a .asvoice or .ovsvoice file into a new
// voice, and exporting a voice as a .asvoice file with its preview,
// reference, portrait and consent; split from voices.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery/persona"
	"github.com/hehehai/AudioSmith/internal/i18n"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// Import reads a .asvoice or .ovsvoice file into a new profile: the
// reference, preview (as the sample), portrait and consent recording are
// copied into its directory. A consent record in the file is the
// exporter's claim: the voice is ConsentIncluded until the user records
// the statement here.
func (s *Voices) Import(ctx context.Context, path string) (core.VoiceImport, error) {
	out, err := s.importFile(ctx, path)
	logOp(ctx, "voice.import", out.Voice.ID, out.Voice.Name, err, "format", out.Format, "warnings", len(out.Warnings))
	return out, err
}

func (s *Voices) importFile(ctx context.Context, path string) (core.VoiceImport, error) {
	var out core.VoiceImport
	var b *persona.Bundle
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case persona.Ext:
		out.Format = "asvoice"
		b, err = persona.ReadFile(path, persona.DefaultLimits)
	case persona.OVSExt:
		out.Format = "ovsvoice"
		var data []byte
		if data, err = readLimited(path, persona.DefaultLimits.MaxFile); err == nil {
			b, out.Warnings, err = persona.ReadOVS(bytes.NewReader(data), int64(len(data)), persona.DefaultLimits)
		}
	default:
		return out, fmt.Errorf("import %s: not a voice file: %w", filepath.Base(path), core.ErrInvalid)
	}
	if errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	if err != nil {
		return out, fmt.Errorf("import %s: %w", filepath.Base(path), errors.Join(core.ErrInvalid, err))
	}
	m := b.Manifest
	v := core.Voice{
		Name: m.Name, Language: m.Language, Tags: slices.Clone(m.Tags),
		Style: strings.TrimSpace(m.Engine.Voice), CreatedWith: m.Engine.Repo,
		Origin: out.Format + ":" + m.ID,
	}
	switch m.Kind {
	case persona.KindDesign:
		v.Kind, v.Style = core.VoiceDesigned, ""
		if m.Recipe != nil {
			v.Design, v.Seed = m.Recipe.Description, m.Recipe.Seed
			v.DesignAttrs, _ = json.Marshal(m.Recipe.Attrs)
		}
	default:
		v.Kind = core.VoiceCloned
		if b.Reference == nil {
			out.Warnings = append(out.Warnings, i18n.T("The file has no reference clip: the voice is cloned from its preview."))
		}
	}
	if m.Engine.Seed != nil && v.Seed == 0 {
		v.Seed = *m.Engine.Seed
	}
	if v.Seed < 0 || v.Seed > vd.MaxSeed {
		v.Seed = vd.FoldSeed(v.Seed)
	}
	if m.Files.Reference != nil {
		v.RefText = m.Files.Reference.Text
	}
	if err := validVoice(&v); err != nil {
		return out, err
	}

	s.mu.Lock()
	now := s.now()
	v.ID, v.CreatedAt, v.UpdatedAt = core.NewID(now), now, now
	err = func() error {
		if err := os.MkdirAll(s.dir(v.ID), 0o755); err != nil {
			return err
		}
		write := func(name string, data []byte) error {
			return writeFileAtomic(s.FilePath(v, name), data)
		}
		if b.Reference != nil {
			v.RefAudio = "reference." + b.ReferenceExt
			if err := write(v.RefAudio, b.Reference); err != nil {
				return err
			}
		} else if v.Kind == core.VoiceCloned {
			v.RefAudio, v.RefText = "reference.wav", m.Files.Preview.Text
			if err := write(v.RefAudio, b.Preview); err != nil {
				return err
			}
		}
		v.Sample, v.SampleText = "sample.wav", m.Files.Preview.Text
		if err := write(v.Sample, b.Preview); err != nil {
			return err
		}
		if m.Engine.Locked {
			v.Locked = true
		}
		if b.Portrait != nil {
			v.Portrait = "portrait." + b.PortraitExt
			if err := write(v.Portrait, b.Portrait); err != nil {
				return err
			}
		}
		if b.ConsentComplete() {
			v.Consent = core.VoiceConsent{State: core.ConsentIncluded, Recording: "consent.wav",
				Statement: m.Consent.Statement, At: m.Consent.AttestedAt}
			if err := write(v.Consent.Recording, b.ConsentAudio); err != nil {
				return err
			}
		}
		return s.store.Put(ctx, v)
	}()
	if err != nil {
		os.RemoveAll(s.dir(v.ID))
		s.mu.Unlock()
		return out, err
	}
	s.mu.Unlock()
	out.Voice = v
	s.bus.Publish(core.VoiceChanged{Voice: v})
	return out, nil
}

// Export writes the voice as a .asvoice file at dst (atomically).
func (s *Voices) Export(ctx context.Context, id, dst string, o core.VoiceExport) error {
	err := s.export(ctx, id, dst, o)
	logOp(ctx, "voice.export", id, "", err, "file", filepath.Base(dst), "reference", o.IncludeReference)
	return err
}

func (s *Voices) export(ctx context.Context, id, dst string, o core.VoiceExport) error {
	v, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	pid := v.ID
	if p, ok := strings.CutPrefix(v.Origin, "asvoice:"); ok && p != "" {
		pid = p // keep the persona's identity across re-exports
	}
	app := o.App
	if app == "" {
		app = "AudioSmith"
	}
	m := persona.Manifest{
		ID: pid, Name: v.Name, Language: v.Language, Tags: slices.Clone(v.Tags),
		Engine:    persona.Engine{Repo: firstNonEmpty(v.CreatedWith, v.Model), Voice: v.Style, Locked: v.Locked},
		Licence:   persona.Licence{SPDX: o.Licence},
		CreatedBy: persona.CreatedBy{App: app, Version: o.AppVersion},
	}
	if v.Seed != 0 || v.Locked || v.Kind == core.VoiceDesigned {
		seed := v.Seed
		m.Engine.Seed = &seed
	}
	switch v.Kind {
	case core.VoiceDesigned:
		m.Kind = persona.KindDesign
		r := vd.Recipe{Description: v.Design, Seed: v.Seed}
		if len(v.DesignAttrs) > 0 {
			json.Unmarshal(v.DesignAttrs, &r.Attrs)
		}
		r = r.Normalize()
		m.Recipe = &r
		m.Description = v.Design
	default:
		m.Kind = persona.KindClone
		m.Description = v.Style
	}
	b := &persona.Bundle{Manifest: m}

	// The preview: what the caller chose, else the voice's sample or its
	// reference, whichever is a WAV small enough. Never the voice's takes:
	// the newest one is whatever the user last said with it, text and all
	// (code review 11-02, N4).
	var candidates []previewSource
	if o.Preview != "" {
		candidates = append(candidates, previewSource{o.Preview, ""})
	}
	if v.Sample != "" {
		candidates = append(candidates, previewSource{s.FilePath(v, v.Sample), v.SampleText})
	}
	if v.RefAudio != "" {
		candidates = append(candidates, previewSource{s.FilePath(v, v.RefAudio), v.RefText})
	}
	for _, c := range candidates {
		data, err := readLimited(c.path, persona.DefaultLimits.MaxPreview)
		if err == nil && isWAV(data) {
			b.Preview, b.Manifest.Files.Preview.Text = data, c.text
			break
		}
	}
	if b.Preview == nil {
		return fmt.Errorf("export %s: %w", v.Name, ErrNoPreview)
	}
	if o.IncludeReference && v.RefAudio != "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(v.RefAudio)), ".")
		data, err := readLimited(s.FilePath(v, v.RefAudio), persona.DefaultLimits.MaxReference)
		if err != nil {
			return fmt.Errorf("export %s: reference: %w", v.Name, err)
		}
		b.Reference, b.ReferenceExt = data, ext
		b.Manifest.Files.Reference = &persona.FileRef{Text: v.RefText}
	}
	if v.Portrait != "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(v.Portrait)), ".")
		if ext == "jpeg" {
			ext = "jpg"
		}
		if data, err := readLimited(s.FilePath(v, v.Portrait), persona.DefaultLimits.MaxPortrait); err == nil {
			b.Portrait, b.PortraitExt = data, ext
		}
	}
	if v.Verified() {
		if data, err := readLimited(s.FilePath(v, v.Consent.Recording), persona.DefaultLimits.MaxConsent); err == nil && isWAV(data) {
			b.ConsentAudio = data
			b.Manifest.Consent = &persona.Consent{Attested: true, Method: "recording", Statement: v.Consent.Statement, AttestedAt: v.Consent.At}
		}
	}
	return persona.WriteFile(dst, b, persona.DefaultLimits)
}

// ErrNoPreview: a voice has no WAV to ship as an export's preview (no
// sample, no WAV reference; its takes are not used): lock a take as its
// identity (its sample) first.
var ErrNoPreview = errors.New("the voice has no audio to build a preview from")

type previewSource struct{ path, text string }

func isWAV(b []byte) bool {
	return len(b) >= 12 && (string(b[:4]) == "RIFF" || string(b[:4]) == "RF64") && string(b[8:12]) == "WAVE"
}

// readLimited reads a file of at most max bytes.
func readLimited(path string, max int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), persona.ErrTooLarge)
	}
	return os.ReadFile(path)
}
