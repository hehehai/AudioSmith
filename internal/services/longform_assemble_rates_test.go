package services

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// TestMatchChapterFormats: a 24 kHz chapter beside a 48 kHz one (VoxCPM2,
// thread #165) is converted to 48 kHz instead of failing the book.
func TestMatchChapterFormats(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, rate int) ChapterResult {
		p := filepath.Join(dir, name)
		w, err := wav.Create(p, wav.Format{Rate: rate, Channels: 1, Encoding: wav.Float32}, wav.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Write(make([]float32, rate)); err != nil { // 1 s
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return ChapterResult{Output: p, SampleRate: rate, Channels: 1, Frames: int64(rate)}
	}
	chs := []ChapterResult{write("001.wav", 48000), write("002.wav", 24000)}
	if err := matchChapterFormats(context.Background(), chs); err != nil {
		t.Fatal(err)
	}
	if chs[0].Output != filepath.Join(dir, "001.wav") {
		t.Errorf("the 48 kHz chapter was converted: %+v", chs[0])
	}
	c := chs[1]
	if c.SampleRate != 48000 || c.Output != filepath.Join(dir, "002-48000x1.wav") || c.Frames < 47900 || c.Frames > 48100 {
		t.Fatalf("%+v", c)
	}
	f, err := wav.Open(c.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Format().Rate != 48000 {
		t.Errorf("file at %d Hz", f.Format().Rate)
	}
}
