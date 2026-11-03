package services

import (
	"context"
	"io"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/edit"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// exportStreamed is ExportAudio without a loudness target: trimmed (a
// first pass finds the silence), resampled and written a chunk at a time,
// so a long take is never held whole (thread #150). It writes what
// ProcessAudio would.
func exportStreamed(ctx context.Context, src, dst string, o AudioExport, enc AudioEncoder) (ExportedAudio, error) {
	var res ExportedAudio
	f, err := wav.Open(src)
	if err != nil {
		return res, err
	}
	defer f.Close()
	fm := f.Format()
	total := f.Frames()
	if total < 0 {
		// Not a regular file: read it whole.
		f.Close()
		in, _, err := wav.ReadFile(src)
		if err != nil {
			return res, err
		}
		out, offset, _ := ProcessAudio(in, o)
		res.Seconds, res.Offset = out.Seconds(), offset
		res.Bytes, err = writeExport(ctx, dst, o, enc, func(path string, e wav.Encoding) error {
			return wav.WriteFile(path, out, e, wav.Options{Dither: e == wav.PCM16})
		})
		return res, err
	}
	start, end := int64(0), total
	fadeIn, fadeOut := 0, 0
	if o.Trim && total > 0 {
		sc := edit.NewTrimScanner(fm.Rate, fm.Channels, exportTrim)
		r, err := f.Stream(0, -1)
		if err != nil {
			return res, err
		}
		buf := make([]float32, 16384*fm.Channels)
		for {
			k, err := r.Read(buf)
			sc.Write(buf[:k])
			if err == io.EOF {
				break
			}
			if err != nil {
				return res, err
			}
		}
		s, e := sc.Range()
		if e > s && (s > 0 || int64(e) < total) {
			start, end = int64(s), int64(e)
			res.Offset = dsp.SecondsAt(s, fm.Rate)
			if s > 0 {
				fadeIn = sc.FadeFrames()
			}
			if int64(e) < total {
				fadeOut = sc.FadeFrames()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	rate, ch := fm.Rate, fm.Channels
	if o.SampleRate > 0 {
		rate = o.SampleRate
	}
	if o.Channels > 0 {
		ch = o.Channels
	}
	var frames int64
	res.Bytes, err = writeExport(ctx, dst, o, enc, func(path string, e wav.Encoding) error {
		r, err := f.Stream(start, end)
		if err != nil {
			return err
		}
		src := &fadeSource{r: r, n: end - start, in: fadeIn, out: fadeOut}
		w, err := wav.Create(path, wav.Format{Rate: rate, Channels: ch, Encoding: e}, wav.Options{Dither: e == wav.PCM16})
		if err != nil {
			return err
		}
		frames, err = resample.Stream(src, rate, ch, resample.Good, func(s []float32) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return w.Write(s)
		})
		if err != nil {
			w.Abort()
			return err
		}
		return w.Close()
	})
	res.Seconds = dsp.SecondsAt(int(frames), rate)
	return res, err
}

// fadeSource reads n frames from r with Trim's fades: in frames up from
// silence at the start, out frames down to silence at the end
// (dsp.FadeIn and dsp.FadeOut, equal power).
type fadeSource struct {
	r       *wav.Reader
	n       int64
	in, out int
	pos     int64
}

func (s *fadeSource) Format() wav.Format { return s.r.Format() }

func (s *fadeSource) Read(dst []float32) (int, error) {
	k, err := s.r.Read(dst)
	ch := s.r.Format().Channels
	in, out := int64(min(int64(s.in), s.n)), int64(min(int64(s.out), s.n))
	for i := range k / ch {
		p := s.pos + int64(i)
		if p < in {
			g := float32(dsp.EqualPower.FadeGain((float64(p) + 0.5) / float64(in)))
			for c := range ch {
				dst[i*ch+c] *= g
			}
		}
		if q := s.n - 1 - p; q < out {
			g := float32(dsp.EqualPower.FadeGain((float64(q) + 0.5) / float64(out)))
			for c := range ch {
				dst[i*ch+c] *= g
			}
		}
	}
	s.pos += int64(k / ch)
	return k, err
}
