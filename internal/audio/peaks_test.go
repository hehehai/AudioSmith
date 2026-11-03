package audio

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPeaksOf(t *testing.T) {
	p := PeaksOf([]float32{0, 0.5, 0.25, 1, 0, 0}, 3)
	if len(p) != 3 || p[0] != 0.5 || p[1] != 1 || p[2] != 0 {
		t.Errorf("peaks %v", p)
	}
	if PeaksOf(nil, 3) != nil || PeaksOf([]float32{1}, 0) != nil {
		t.Error("empty input or n=0 must give nil")
	}
}

// writeWAV writes a mono 16-bit swelling sine.
func writeWAV(t *testing.T, path string, rate, frames int) {
	t.Helper()
	data := make([]byte, 44+2*frames)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*frames))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*frames))
	for i := range frames {
		v := math.Sin(2*math.Pi*220*float64(i)/float64(rate)) * float64(i) / float64(frames)
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(v*20000)))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPeaksStreamsLikePeaksOf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	writeWAV(t, path, 24000, 28800)
	got, err := Peaks(path, 160)
	if err != nil || len(got) != 160 {
		t.Fatal(len(got), err)
	}
	// The swell rises: the last peak is the loudest, the first near silent.
	if got[159] != 1 || got[0] > 0.05 || !slices.IsSortedFunc(got[10:], func(a, b float32) int {
		if a > b+0.02 {
			return 1
		}
		return 0
	}) {
		t.Errorf("shape %v", got)
	}
	// Fewer frames than peaks takes the in-memory path.
	short := filepath.Join(t.TempDir(), "b.wav")
	writeWAV(t, short, 24000, 50)
	if p, err := Peaks(short, 160); err != nil || len(p) != 160 {
		t.Errorf("short %d %v", len(p), err)
	}
	if _, err := Peaks(filepath.Join(t.TempDir(), "missing.wav"), 10); err == nil {
		t.Error("missing file")
	}
}

func TestPeakCache(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "take.wav")
	writeWAV(t, audio, 24000, 24000)
	c := PeakCache{Dir: filepath.Join(dir, "cache", "peaks"), N: 160}
	p, err := c.Get("T1", audio)
	if err != nil || len(p) != 160 {
		t.Fatal(len(p), err)
	}
	cached := filepath.Join(c.Dir, "T1.f32")
	if fi, err := os.Stat(cached); err != nil || fi.Size() != 640 {
		t.Fatalf("cache file %v %v", fi, err)
	}
	// Served from the cache: the audio is no longer needed.
	os.Remove(audio)
	again, err := c.Get("T1", audio)
	if err != nil || !slices.Equal(p, again) {
		t.Fatalf("cached peaks differ: %v", err)
	}
	// A truncated cache file is recomputed (and fails here: no audio).
	os.WriteFile(cached, []byte{1, 2, 3}, 0o644)
	if _, err := c.Get("T1", audio); err == nil {
		t.Error("corrupt cache used")
	}
	if err := c.Remove("T1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("T1"); err != nil {
		t.Errorf("removing twice: %v", err)
	}
	if _, err := c.Get("../x", audio); err == nil {
		t.Error("id escapes the cache directory")
	}
	// Empty audio caches as no peaks.
	empty := filepath.Join(dir, "empty.wav")
	writeWAV(t, empty, 24000, 0)
	if p, err := c.Get("E", empty); err != nil || p == nil || len(p) != 0 {
		t.Errorf("empty %v %v", p, err)
	}
	if p, err := c.Get("E", empty); err != nil || p == nil || len(p) != 0 {
		t.Errorf("empty, cached %v %v", p, err)
	}
}

func TestPlayerIdle(t *testing.T) {
	var pl Player
	if pl.Active() || pl.Playing("x") || pl.Progress("x") != 0 || pl.Current() != "" || pl.Left() != 0 {
		t.Error("a new player plays nothing")
	}
	pl.Seek("x", 0.5)
	pl.Stop()
	if err := pl.Toggle("x", "/does/not/exist.wav"); err == nil {
		t.Error("missing file played")
	}
	bad := filepath.Join(t.TempDir(), "bad.wav")
	os.WriteFile(bad, []byte("not audio"), 0o644)
	if err := pl.Toggle("x", bad); err == nil || pl.Current() != "" {
		t.Error("garbage played")
	}
}

// TestPlayerStreams plays a file (unheard where no output opens).
func TestPlayerStreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	writeWAV(t, path, 24000, 24000*3)
	var pl Player
	if err := pl.Toggle("a", path); err != nil {
		t.Fatal(err)
	}
	defer pl.Stop()
	if !pl.Playing("a") || pl.Current() != "a" || pl.Playing("b") {
		t.Fatal("not playing")
	}
	if l := pl.Left(); l != leftMax {
		t.Errorf("3 s left: look again after %v, want %v", l, leftMax)
	}
	pl.Seek("a", 0.5)
	if p := pl.Progress("a"); p < 0.45 || p > 0.6 {
		t.Errorf("progress after seek %v", p)
	}
	pl.Seek("a", 0.9)
	if l := pl.Left(); l < 200*time.Millisecond || l > 400*time.Millisecond {
		t.Errorf("0.3 s left: look again after %v", l)
	}
	pl.Toggle("a", path) // pause
	if pl.Active() || pl.Left() != 0 {
		t.Error("still playing after pause")
	}
	// Another take replaces it.
	other := filepath.Join(t.TempDir(), "b.wav")
	writeWAV(t, other, 24000, 2400)
	pl.Toggle("b", other)
	time.Sleep(10 * time.Millisecond)
	if pl.Current() != "b" || pl.Progress("a") != 0 {
		t.Error("not switched")
	}
	pl.Stop()
	if pl.Current() != "" || pl.Active() {
		t.Error("not stopped")
	}
}
