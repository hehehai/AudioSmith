package media

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

func isWAVPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".wav" || ext == ".wave" || ext == ".rf64"
}

func probeWAV(path string) (proto.ProbeResult, error) {
	wf, err := wav.Open(path)
	if err != nil {
		return proto.ProbeResult{}, err
	}
	defer wf.Close()
	f := wf.Format()
	return proto.ProbeResult{
		Format:      "wav",
		Duration:    wf.Seconds(),
		HasAudio:    true,
		HasVideo:    false,
		AudioTracks: 1,
		Codec:       "lpcm",
		SampleRate:  f.Rate,
		Channels:    f.Channels,
		Bits:        f.Encoding.Bits(),
		BitRate:     f.BytesPerSecond() * 8,
	}, nil
}

// decodeWAVNative decodes, trims, mixes channels, and resamples a WAV file
// completely in pure Go using internal/dsp/wav and internal/dsp/resample.
func decodeWAVNative(p proto.DecodeParams) (proto.AudioOut, proto.ProbeResult, error) {
	srcProbe, err := probeWAV(p.Input)
	if err != nil {
		return proto.AudioOut{}, proto.ProbeResult{}, err
	}
	wf, err := wav.Open(p.Input)
	if err != nil {
		return proto.AudioOut{}, proto.ProbeResult{}, err
	}
	defer wf.Close()

	srcFmt := wf.Format()
	fromFrame := int64(0)
	if p.Start > 0 {
		fromFrame = int64(dsp.FramesAt(p.Start, srcFmt.Rate))
	}
	toFrame := int64(-1)
	if p.Duration > 0 {
		toFrame = fromFrame + int64(dsp.FramesAt(p.Duration, srcFmt.Rate))
	}

	buf, err := wf.ReadFrames(fromFrame, toFrame)
	if err != nil {
		return proto.AudioOut{}, proto.ProbeResult{}, err
	}

	// Channel conversion if requested.
	targetCh := p.Channels
	if targetCh <= 0 {
		targetCh = buf.Channels
	}
	if targetCh != buf.Channels {
		buf = convertChannels(buf, targetCh)
	}

	// Resample if requested.
	targetRate := p.SampleRate
	if targetRate <= 0 {
		targetRate = buf.Rate
	}
	if targetRate != buf.Rate && len(buf.Samples) > 0 {
		buf = resample.Buffer(buf, targetRate, resample.Good)
	}

	enc := wavEncoding(p.Output.Bits)
	outFmt := wav.Format{Rate: targetRate, Channels: buf.Channels, Encoding: enc}
	if err := os.MkdirAll(filepath.Dir(p.Output.Path), 0o755); err != nil {
		return proto.AudioOut{}, proto.ProbeResult{}, err
	}
	w, err := wav.Create(p.Output.Path, outFmt, wav.Options{})
	if err != nil {
		return proto.AudioOut{}, proto.ProbeResult{}, err
	}
	if len(buf.Samples) > 0 {
		if err := w.Write(buf.Samples); err != nil {
			w.Abort()
			return proto.AudioOut{}, proto.ProbeResult{}, err
		}
	}
	if err := w.Close(); err != nil {
		return proto.AudioOut{}, proto.ProbeResult{}, err
	}

	frames := int64(len(buf.Samples)) / int64(max(buf.Channels, 1))
	audioSecs := float64(frames) / float64(max(targetRate, 1))
	return proto.AudioOut{
		Output:       p.Output.Path,
		SampleRate:   targetRate,
		Channels:     buf.Channels,
		Frames:       frames,
		AudioSeconds: audioSecs,
	}, srcProbe, nil
}

func wavEncoding(bits int) wav.Encoding {
	switch bits {
	case 24:
		return wav.PCM24
	case 32:
		return wav.Float32
	default:
		return wav.PCM16
	}
}

func convertChannels(in dsp.Buffer, target int) dsp.Buffer {
	frames := len(in.Samples) / max(in.Channels, 1)
	out := dsp.Buffer{
		Rate:     in.Rate,
		Channels: target,
		Samples:  make([]float32, frames*target),
	}
	if target == 1 {
		scale := float32(1.0 / float64(in.Channels))
		for i := range frames {
			var sum float32
			base := i * in.Channels
			for c := range in.Channels {
				sum += in.Samples[base+c]
			}
			out.Samples[i] = clampSample(sum * scale)
		}
		return out
	}
	for i := range frames {
		inBase := i * in.Channels
		outBase := i * target
		for c := range target {
			srcCh := c
			if srcCh >= in.Channels {
				srcCh = in.Channels - 1
			}
			out.Samples[outBase+c] = in.Samples[inBase+srcCh]
		}
	}
	return out
}

func clampSample(v float32) float32 {
	if math.IsNaN(float64(v)) {
		return 0
	}
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}
