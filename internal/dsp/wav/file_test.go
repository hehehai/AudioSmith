package wav

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// testTone is frames of a deterministic stereo signal.
func testTone(frames int) dsp.Buffer {
	b := dsp.Buffer{Rate: 16000, Channels: 2, Samples: make([]float32, frames*2)}
	for i := range b.Samples {
		b.Samples[i] = float32((i*7919)%2001-1000) / 1001
	}
	return b
}

// TestReadRange reads windows the same as a whole read sliced, in a file
// with an unknown chunk before the data (thread #150).
func TestReadRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	if err := WriteFile(path, testTone(5000), Float32, Options{}); err != nil {
		t.Fatal(err)
	}
	whole, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Frames() != 5000 || f.Seconds() != 5000.0/16000 {
		t.Fatalf("frames %d seconds %v", f.Frames(), f.Seconds())
	}
	for _, r := range [][2]int64{{0, 10}, {100, 4100}, {4990, 6000}, {-5, 3}, {6000, 7000}, {0, -1}, {3000, -1}} {
		got, err := f.ReadFrames(r[0], r[1])
		if err != nil {
			t.Fatal(err)
		}
		to := int(r[1])
		if to < 0 {
			to = 5000
		}
		want := whole.Slice(int(r[0]), to)
		if !slices.Equal(got.Samples, want.Samples) || got.Rate != 16000 || got.Channels != 2 {
			t.Errorf("ReadFrames%v: %d samples, want %d", r, len(got.Samples), len(want.Samples))
		}
	}
	got, _, err := ReadSeconds(path, 0, 0.1)
	if err != nil || got.Frames() != 1600 {
		t.Fatalf("ReadSeconds: %d frames, %v", got.Frames(), err)
	}
}

// TestReadFileStreamedHeader: a header written as it streamed (data size
// 0) gets its length from the file, and a header claiming more than the
// file holds allocates only what the file holds.
func TestReadFileStreamedHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.wav")
	if err := WriteFile(path, testTone(3000), PCM16, Options{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := ReadFile(path)
	for _, size := range []uint32{0, 0xffffffff, 0x7fffffff} {
		b := slices.Clone(raw)
		binary.LittleEndian.PutUint32(b[40:], size)
		p := filepath.Join(dir, "s.wav")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		got, _, err := ReadFile(p)
		if err != nil || !slices.Equal(got.Samples, want.Samples) {
			t.Fatalf("size %#x: %d samples (want %d), %v", size, len(got.Samples), len(want.Samples), err)
		}
		if cap(got.Samples) != len(want.Samples) {
			t.Errorf("size %#x: cap %d, want %d", size, cap(got.Samples), len(want.Samples))
		}
		f, err := Open(p)
		if err != nil || f.Frames() != 3000 {
			t.Fatalf("size %#x: Open frames %d, %v", size, f.Frames(), err)
		}
		f.Close()
	}
}

// TestReadFileAllocatesOnce: reading a long WAV allocates its samples
// once, not doubling buffers (thread #150: 10 min allocated 2 GB).
func TestReadFileAllocatesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.wav")
	frames := 16000 * 600 // past the old 16 M-sample trust limit
	if err := WriteFile(path, dsp.Buffer{Rate: 16000, Channels: 2, Samples: make([]float32, frames*2)}, PCM16, Options{}); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	b, _, err := ReadFile(path)
	runtime.ReadMemStats(&after)
	if err != nil || b.Frames() != frames {
		t.Fatal(b.Frames(), err)
	}
	samples := uint64(frames * 2 * 4)
	if got := after.TotalAlloc - before.TotalAlloc; got > samples+samples/8 {
		t.Errorf("allocated %d MB for %d MB of samples", got>>20, samples>>20)
	}
}
