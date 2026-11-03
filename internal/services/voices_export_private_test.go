package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery/persona"
)

// TestVoiceExportNoTake (code review 11-02, N4): exporting a voice never
// packs its takes as the preview: the newest is whatever the user last
// said with it. Without a sample or reference there is no preview; with
// a sample, the sample and its text go.
func TestVoiceExportNoTake(t *testing.T) {
	e := newEnv(t, nil)
	vs := e.svc.Voices
	dir := t.TempDir()
	d, err := vs.Save(ctx, core.Voice{Name: "Mom", Kind: core.VoiceDesigned, Design: "a mother"})
	if err != nil {
		t.Fatal(err)
	}
	tk := core.Take{ID: "t1", Text: "My bank PIN is 4711.", Model: "m", VoiceID: d.ID, File: "t1.wav", CreatedAt: time.Now()}
	if err := e.st.Takes().Put(ctx, tk); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(e.st.Takes().AudioDir(), 0o755)
	writeFile(t, e.st.Takes().AudioPath(tk), testWAV(100))
	if u, _ := vs.Usage(ctx, d.ID); u.LatestTake != "t1" {
		t.Fatalf("usage %+v", u)
	}
	if err := vs.Export(ctx, d.ID, filepath.Join(dir, "a.asvoice"), core.VoiceExport{}); !errors.Is(err, ErrNoPreview) {
		t.Fatalf("export with only a take: %v", err)
	}

	sample := writeFile(t, filepath.Join(dir, "s.wav"), testWAV(2400))
	d.SampleText = "Hello, this is how I sound."
	if d, err = vs.SaveFiles(ctx, d, core.VoiceFiles{Sample: sample}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "b.asvoice")
	if err := vs.Export(ctx, d.ID, out, core.VoiceExport{}); err != nil {
		t.Fatal(err)
	}
	b, err := persona.ReadFile(out, persona.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Manifest.Files.Preview.Text; got != "Hello, this is how I sound." {
		t.Errorf("preview text %q", got)
	}
	take, _ := os.ReadFile(e.st.Takes().AudioPath(tk))
	if string(b.Preview) == string(take) {
		t.Error("the take is the preview")
	}
}
