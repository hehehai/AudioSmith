package resample

import (
	"io"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// streamChunk is the frames read per step: memory stays a few hundred KB
// whatever the length.
const streamChunk = 16384

// Source is audio read in chunks, like a wav.Reader.
type Source interface {
	Format() wav.Format
	Read(dst []float32) (int, error) // whole frames; io.EOF at the end
}

// Stream reads r to its end, converts it to rate and channels like
// Convert, and passes the result to emit chunk by chunk (interleaved,
// whole frames; emit must not keep the slice). It returns the frames
// emitted.
func Stream(r Source, rate, channels int, q Quality, emit func([]float32) error) (int64, error) {
	f := r.Format()
	if channels <= 0 {
		channels = f.Channels
	}
	// Remix first when it lowers the channel count (less to resample), as
	// Convert does.
	rsCh := f.Channels
	if channels < f.Channels {
		rsCh = channels
	}
	rs := New(f.Rate, rate, rsCh, q)
	in := make([]float32, streamChunk*f.Channels)
	var mixed, out, final []float32
	var frames int64
	send := func(s []float32) error {
		final = remixInto(final[:0], s, rsCh, channels)
		if len(final) == 0 {
			return nil
		}
		frames += int64(len(final) / channels)
		return emit(final)
	}
	for {
		k, err := r.Read(in)
		if k > 0 {
			mixed = remixInto(mixed[:0], in[:k], f.Channels, rsCh)
			out = rs.Process(out[:0], mixed)
			if err := send(out); err != nil {
				return frames, err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return frames, err
		}
	}
	out = rs.Flush(out[:0])
	return frames, send(out)
}

// ConvertFile writes the WAV file in as a WAV file at out (atomically) at
// rate and channels in encoding e, streaming: it never holds the audio
// whole. It returns the output's length in seconds.
func ConvertFile(in, out string, rate, channels int, q Quality, e wav.Encoding, opt wav.Options) (float64, error) {
	src, err := wav.Open(in)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	r, err := src.Stream(0, -1)
	if err != nil {
		return 0, err
	}
	if channels <= 0 {
		channels = src.Format().Channels
	}
	w, err := wav.Create(out, wav.Format{Rate: rate, Channels: channels, Encoding: e}, opt)
	if err != nil {
		return 0, err
	}
	frames, err := Stream(r, rate, channels, q, w.Write)
	if err != nil {
		w.Abort()
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return float64(frames) / float64(rate), nil
}

// remixInto appends to dst the interleaved src of from channels remixed
// to to channels the way dsp.Remix does, reusing dst's memory.
func remixInto(dst, src []float32, from, to int) []float32 {
	if from == to {
		return append(dst, src...)
	}
	n := len(src) / from
	switch {
	case from == 1:
		for _, v := range src[:n] {
			for range to {
				dst = append(dst, v)
			}
		}
	case to == 1:
		inv := 1 / float32(from)
		for i := range n {
			var s float32
			for _, v := range src[i*from : (i+1)*from] {
				s += v
			}
			dst = append(dst, s*inv)
		}
	default:
		k := min(to, from)
		for i := range n {
			dst = append(dst, src[i*from:i*from+k]...)
			for range to - k {
				dst = append(dst, 0)
			}
		}
	}
	return dst
}
