package services

// Chapters at different sample rates or channel counts, matched before
// the book is assembled (thread #165: VoxCPM2 speaks at 48 kHz, the other
// models at 24 kHz, and a chapter takes the rate of its first chunk).

import (
	"context"
	"fmt"
	"strings"

	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
)

// matchChapterFormats converts the chapters that differ from the highest
// rate and channel count among them, beside their file
// ("003.wav" → "003-48000x1.wav"), and points chs at the converted files.
func matchChapterFormats(ctx context.Context, chs []ChapterResult) error {
	rate, channels := 0, 0
	for _, c := range chs {
		rate, channels = max(rate, c.SampleRate), max(channels, c.Channels)
	}
	for i := range chs {
		c := &chs[i]
		if c.SampleRate == rate && c.Channels == channels {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		out := strings.TrimSuffix(c.Output, ".wav") + fmt.Sprintf("-%dx%d.wav", rate, channels)
		secs, err := resample.ConvertFile(c.Output, out, rate, channels, resample.Good, wav.Float32, wav.Options{Large: true})
		if err != nil {
			return fmt.Errorf("chapter %d (%d Hz × %d) to %d Hz × %d: %w", i+1, c.SampleRate, c.Channels, rate, channels, err)
		}
		c.Output, c.SampleRate, c.Channels = out, rate, channels
		c.Frames = int64(secs*float64(rate) + 0.5)
	}
	return nil
}
