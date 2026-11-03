package dub

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// countingAudio counts the frames read through it.
type countingAudio struct {
	Audio
	frames int
}

func (c *countingAudio) ReadSeconds(from, to float64) (dsp.Buffer, error) {
	b, err := c.Audio.ReadSeconds(from, to)
	c.frames += b.Frames()
	return b, err
}

// Code review 1103 F9: PickReferences reads only its candidate windows of
// the speech file (it read the whole 16 kHz track into memory), and picks
// what it picked from the whole buffer.
func TestPickReferencesReadsWindows(t *testing.T) {
	const rate, seconds = 16000, 120.0
	audio := tone(rate, 1, seconds, 0.1)
	for i := range audio.Samples { // a pause every 2 s, so it sounds like speech
		if math.Mod(float64(i)/rate, 2) > 1.6 {
			audio.Samples[i] = 0
		}
	}
	path := filepath.Join(t.TempDir(), "speech.wav")
	if err := wav.WriteFile(path, audio, wav.Float32, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	p := &Project{}
	for i, start := range []float64{1, 7, 30, 33.5, 60, 100} {
		s := p.NewSegment(start, start+[]float64{5, 12, 3, 9, 20, 4}[i], "", "words")
		s.Speaker = []string{"A", "A", "B", "B", "A", "C"}[i]
		p.Segments = append(p.Segments, s)
	}
	want, err := PickReferences(p.Segments, BufferAudio{audio})
	if err != nil {
		t.Fatal(err)
	}
	f, err := wav.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := &countingAudio{Audio: f}
	got, err := PickReferences(p.Segments, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("got %v, want %v", got, want)
	}
	for sp, w := range want {
		if g := got[sp]; g != w {
			t.Errorf("%s: %+v, want %+v", sp, g, w)
		}
	}
	if whole := audio.Frames(); c.frames >= whole/2 {
		t.Errorf("read %d frames of %d", c.frames, whole)
	}
	if empty, err := PickReferences(p.Segments, BufferAudio{dsp.Buffer{Rate: rate, Channels: 1}}); err != nil || len(empty) != 0 {
		t.Errorf("empty audio: %v, %v", empty, err)
	}
}
