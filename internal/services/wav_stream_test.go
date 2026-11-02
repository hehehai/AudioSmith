package services

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/edit"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// allocDuring returns the bytes fn allocates.
func allocDuring(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestTranscribeWAVStreams: decoding a long WAV for transcription streams
// it (code review 11-02, H2: 10 min of 48 kHz stereo allocated 2064 MB);
// it allocates less than 3× the file and writes what the whole-buffer
// conversion wrote.
func TestTranscribeWAVStreams(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.wav")
	frames := 48000 * 120
	src := dsp.Buffer{Rate: 48000, Channels: 2, Samples: make([]float32, frames*2)}
	for i := range src.Samples {
		src.Samples[i] = float32((i*7919)%2001-1000) / 4000
	}
	if err := wav.WriteFile(in, src, wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	src = dsp.Buffer{}
	fi, _ := os.Stat(in)
	out := filepath.Join(dir, "out.wav")
	var secs float64
	var err error
	alloc := allocDuring(func() {
		secs, err = (&Transcripts{}).decode(ctx, jobs.Job{}, in, out, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input %d MB, allocated %d KB", fi.Size()>>20, alloc>>10)
	if alloc > 3*uint64(fi.Size()) {
		t.Errorf("allocated %d MB for a %d MB file", alloc>>20, fi.Size()>>20)
	}
	if secs != 120 {
		t.Errorf("seconds %v", secs)
	}
	whole, _, _ := wav.ReadFile(in)
	want := resample.Convert(whole, TranscriptRate, 1, resample.Good)
	wantPath := filepath.Join(dir, "want.wav")
	if err := wav.WriteFile(wantPath, want, wav.PCM16, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(out)
	b, _ := os.ReadFile(wantPath)
	if !slices.Equal(a, b) {
		t.Error("streamed decode differs from the whole-buffer one")
	}
}

// TestExportStreamedSame: an export without a loudness target streams and
// writes exactly what ProcessAudio does; AnalyzeAudio streams and reports
// what the whole buffer gives.
func TestExportStreamedSame(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "take.wav")
	b := dsp.Buffer{Rate: 24000, Channels: 1, Samples: make([]float32, 24000*3)}
	for i := 12000; i < 60000; i++ {
		b.Samples[i] = float32((i*7919)%2001-1000) / 3000
	}
	if err := wav.WriteFile(src, b, wav.Float32, wav.Options{}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []AudioExport{
		{Format: FormatWAV, Bits: 32},
		{Format: FormatWAV, Bits: 32, Trim: true},
		{Format: FormatWAV, Bits: 24, Trim: true, SampleRate: 48000, Channels: 2},
		{Format: FormatWAV, Bits: 16, Trim: true, SampleRate: 16000},
	} {
		dst := filepath.Join(dir, "out.wav")
		res, err := ExportAudio(ctx, src, dst, o, nil)
		if err != nil {
			t.Fatal(err)
		}
		want, offset, _ := ProcessAudio(b, o)
		wantPath := filepath.Join(dir, "want.wav")
		e := encodingOf(o.Bits)
		if err := wav.WriteFile(wantPath, want, e, wav.Options{Dither: e == wav.PCM16}); err != nil {
			t.Fatal(err)
		}
		x, _ := os.ReadFile(dst)
		y, _ := os.ReadFile(wantPath)
		if !slices.Equal(x, y) || res.Offset != offset || res.Seconds != want.Seconds() || res.Bytes != int64(len(y)) {
			t.Errorf("%+v: %d bytes offset %v %v s, want %d %v %v s", o, len(x), res.Offset, res.Seconds, len(y), offset, want.Seconds())
		}
	}
	info, err := AnalyzeAudio(src)
	if err != nil {
		t.Fatal(err)
	}
	st := loudness.Measure(b)
	s, e := edit.TrimRange(b, exportTrim)
	if info.Seconds != 3 || info.Trimmed != dsp.SecondsAt(e-s, b.Rate) || info.Integrated != st.Integrated || info.TruePeak != st.TruePeak {
		t.Errorf("AnalyzeAudio %+v, want trimmed %v, %v LUFS, %v dBTP", info, dsp.SecondsAt(e-s, b.Rate), st.Integrated, st.TruePeak)
	}
}
