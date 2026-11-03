package resample

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// TestConvertFile streams the same samples Convert makes from the whole
// file, for each way the channels go (thread #150).
func TestConvertFile(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ inCh, rate, outCh, outRate int }{
		{2, 48000, 1, 16000}, {1, 24000, 2, 44100}, {2, 16000, 2, 16000}, {3, 22050, 2, 16000},
	} {
		in := dsp.Buffer{Rate: c.rate, Channels: c.inCh, Samples: make([]float32, 50000*c.inCh)}
		for i := range in.Samples {
			in.Samples[i] = float32((i*7919)%2001-1000) / 2002
		}
		src := filepath.Join(dir, "in.wav")
		if err := wav.WriteFile(src, in, wav.Float32, wav.Options{}); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "out.wav")
		secs, err := ConvertFile(src, dst, c.outRate, c.outCh, Good, wav.Float32, wav.Options{})
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := wav.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		want := Convert(in, c.outRate, c.outCh, Good)
		if !slices.Equal(got.Samples, want.Samples) || got.Rate != c.outRate || got.Channels != c.outCh || secs != want.Seconds() {
			t.Errorf("%+v: %d samples %v s, want %d %v s", c, len(got.Samples), secs, len(want.Samples), want.Seconds())
		}
	}
}
