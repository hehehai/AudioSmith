package services

// Exporting audio with settings (the app's Export audio dialog): a WAV
// (a take, a render's file) is read, trimmed of its leading and trailing
// silence, resampled and remixed, brought to a loudness target with a
// true-peak ceiling, then written as a WAV of a bit depth here, or handed
// to an encoder for the other formats (the engine's media.encode for M4A,
// ffmpeg for MP3, FLAC and Opus; the caller passes it). All the DSP is
// internal/dsp's.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/edit"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// Audio export formats (and FormatWAV, longform_doc.go).
const (
	FormatM4A  = "m4a"
	FormatMP3  = "mp3"
	FormatFLAC = "flac"
	FormatOpus = "opus"
)

// AudioExport is how to export audio.
type AudioExport struct {
	Format     string // FormatWAV…
	Bits       int    // WAV: 16 (dithered), 24, 32 (float); FLAC: 16 or 24
	Bitrate    int    // lossy formats, bits per second (0: the encoder's choice)
	SampleRate int    // 0 keeps the source's
	Channels   int    // 0 keeps the source's
	Loudness   string // "" off, or a key of ExportLoudness
	Trim       bool   // drop leading and trailing silence
	// Metadata are the file's tags ("title", "artist"); WAV has none.
	Metadata map[string]string
}

// ExportLoudness are the loudness targets of an export, by key: integrated
// loudness (LUFS) and true-peak ceiling (dBTP).
var ExportLoudness = map[string]loudness.Target{
	"podcast":   loudness.Podcast, // −16 LUFS, −1.5 dBTP
	"speech":    {ID: "speech", Integrated: -18, MinIntegrated: -19, MaxIntegrated: -17, TruePeak: -1},
	"broadcast": loudness.Broadcast, // −23 LUFS, −1 dBTP (EBU R128)
}

// exportTrim finds the silence an export trims: a little quieter than
// edit's default, so breaths and soft word ends stay.
var exportTrim = edit.TrimOptions{ThresholdDB: -55, Keep: 0.08}

// AudioEncoder writes the WAV in as dst in o.Format (not WAV) with
// o.Bitrate and o.Metadata; the WAV is already at the export's rate and
// channels.
type AudioEncoder func(ctx context.Context, in, dst string, o AudioExport) error

// ExportedAudio is what an export wrote.
type ExportedAudio struct {
	Seconds float64 // the output's length
	// Offset is how much of the source's start was trimmed, seconds:
	// times in the source (chunks, markers) move back by it.
	Offset   float64
	Bytes    int64
	Loudness float64 // integrated, LUFS, when a target was applied
}

// ErrNoEncoder is returned for a format other than WAV without an encoder.
var ErrNoEncoder = errors.New("export: nothing can write this format")

// ProcessAudio applies o's processing to b: trim, then sample rate and
// channels, then loudness. It returns the result, the seconds trimmed
// from the start and, with a target, the loudness reached (LUFS). b is
// not modified.
func ProcessAudio(b dsp.Buffer, o AudioExport) (dsp.Buffer, float64, float64) {
	offset := 0.0
	if o.Trim && b.Frames() > 0 {
		start, end := edit.TrimRange(b, exportTrim)
		if end > start && (start > 0 || end < b.Frames()) {
			b = edit.Trim(b, exportTrim)
			offset = dsp.SecondsAt(start, b.Rate)
		}
	}
	rate, ch := b.Rate, b.Channels
	if o.SampleRate > 0 {
		rate = o.SampleRate
	}
	if o.Channels > 0 {
		ch = o.Channels
	}
	if rate != b.Rate || ch != b.Channels {
		b = resample.Convert(b, rate, ch, resample.Good)
	}
	lufs := 0.0
	if t, ok := ExportLoudness[o.Loudness]; ok {
		var r loudness.Report
		b, r = loudness.Normalize(b, t, loudness.NormalizeOptions{})
		lufs = r.After.Integrated
	}
	return b, offset, lufs
}

// ExportAudio writes the WAV src to dst as o says; enc writes the formats
// other than WAV. dst is written whole or not at all. Without a loudness
// target it streams (export_stream.go); a target needs the whole take.
func ExportAudio(ctx context.Context, src, dst string, o AudioExport, enc AudioEncoder) (ExportedAudio, error) {
	if err := checkExportFree(filepath.Dir(dst), exportNeed(src, o)); err != nil {
		return ExportedAudio{}, err
	}
	if _, ok := ExportLoudness[o.Loudness]; !ok {
		return exportStreamed(ctx, src, dst, o, enc)
	}
	var res ExportedAudio
	in, _, err := wav.ReadFile(src)
	if err != nil {
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	out, offset, lufs := ProcessAudio(in, o)
	in = dsp.Buffer{} // let it go before writing
	res.Seconds, res.Offset, res.Loudness = out.Seconds(), offset, lufs
	if err := ctx.Err(); err != nil {
		return res, err
	}
	res.Bytes, err = writeExport(ctx, dst, o, enc, func(path string, e wav.Encoding) error {
		return wav.WriteFile(path, out, e, wav.Options{Dither: e == wav.PCM16})
	})
	return res, err
}

// writeExport writes dst through write, which writes a WAV of the
// encoding given at the path given: dst itself for WAV, else a temporary
// WAV that enc encodes. It returns dst's size.
func writeExport(ctx context.Context, dst string, o AudioExport, enc AudioEncoder, write func(path string, e wav.Encoding) error) (int64, error) {
	format := strings.ToLower(o.Format)
	if format == "" || format == FormatWAV {
		if err := write(dst, encodingOf(o.Bits)); err != nil {
			return 0, err
		}
	} else {
		if enc == nil {
			return 0, fmt.Errorf("%w: %s", ErrNoEncoder, strings.ToUpper(format))
		}
		// The encoder reads a WAV: FLAC keeps the bit depth asked for,
		// the lossy formats get floats (no rounding before encoding).
		e := wav.Float32
		if format == FormatFLAC {
			e = wav.PCM16
			if o.Bits == 24 {
				e = wav.PCM24
			}
		}
		tmp, err := os.CreateTemp("", "audiosmith-export-*.wav")
		if err != nil {
			return 0, err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := write(tmp.Name(), e); err != nil {
			return 0, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, err
		}
		if err := enc(ctx, tmp.Name(), dst, o); err != nil {
			return 0, err
		}
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return 0, fmt.Errorf("export: %s was not written: %w", filepath.Base(dst), err)
	}
	return fi.Size(), nil
}

// AudioInfo describes a WAV for the Export dialog: its length, the
// length once trimmed, and its loudness.
type AudioInfo struct {
	Rate, Channels int
	Seconds        float64
	Trimmed        float64 // seconds left after Trim
	Integrated     float64 // LUFS (loudness.Floor when silent)
	TruePeak       float64 // dBTP
}

// AnalyzeAudio reads a WAV's AudioInfo, streaming: a long take is never
// held whole (thread #150).
func AnalyzeAudio(path string) (AudioInfo, error) {
	f, err := wav.Open(path)
	if err != nil {
		return AudioInfo{}, err
	}
	defer f.Close()
	r, err := f.Stream(0, -1)
	if err != nil {
		return AudioInfo{}, err
	}
	fm := f.Format()
	m := loudness.NewMeter(fm.Rate, fm.Channels, loudness.Options{})
	sc := edit.NewTrimScanner(fm.Rate, fm.Channels, exportTrim)
	buf := make([]float32, 16384*fm.Channels)
	frames := 0
	for {
		k, err := r.Read(buf)
		m.Write(buf[:k])
		sc.Write(buf[:k])
		frames += k / fm.Channels
		if err == io.EOF {
			break
		}
		if err != nil {
			return AudioInfo{}, err
		}
	}
	st := m.Stats()
	secs := dsp.SecondsAt(frames, fm.Rate)
	info := AudioInfo{Rate: fm.Rate, Channels: fm.Channels, Seconds: secs, Trimmed: secs, Integrated: st.Integrated, TruePeak: st.TruePeak}
	if start, end := sc.Range(); end > start {
		info.Trimmed = dsp.SecondsAt(end-start, fm.Rate)
	}
	return info, nil
}
