package services

// Guards around speech recognition (thread #46, from thread #44's
// evaluation, research/model-eval-stt-llm.md §2, §8):
//
//   - Qwen3-ASR, given a vocabulary prompt, copied the whole list into the
//     end of the text of audio longer than 30 s (5 of 46 runs; never on
//     3–8 s dictation): ASRPrompt drops the prompt for longer audio.
//   - On audio longer than 30 s whose last 30 s chunk is short or nearly
//     silent, it ends the text with a hallucinated " The." (and with
//     " 嗯。" when the language was given): CleanTranscription drops it.
//   - The forced aligner's word times stop at about 4.5 minutes (273–278 s
//     of a 378 s file): alignWords aligns longer audio in windows of whole
//     transcript segments, at most AlignWindowSeconds each.

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

const (
	// ASRPromptMaxSeconds is the longest audio Qwen3-ASR gets a prompt
	// (vocabulary) with: one of its 30 s chunks.
	ASRPromptMaxSeconds = 30
	// AlignWindowSeconds bounds a window of the forced aligner on long
	// audio; audio up to AlignSingleMaxSeconds is aligned in one call.
	AlignWindowSeconds    = 240
	AlignSingleMaxSeconds = 270
)

// isQwenASR reports whether model (a repo or id) is Qwen3-ASR.
func isQwenASR(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "qwen3-asr") || strings.Contains(m, "qwen3_asr")
}

// ASRPrompt is the prompt to send model with audio of seconds: none to
// Qwen3-ASR for audio longer than ASRPromptMaxSeconds (or of unknown
// length), prompt otherwise.
func ASRPrompt(model, prompt string, seconds float64) string {
	if isQwenASR(model) && (seconds <= 0 || seconds > ASRPromptMaxSeconds) {
		return ""
	}
	return prompt
}

// asrTails are what Qwen3-ASR invents after the last sentence of a long
// file: a lone English "The." (also after Chinese text) or a Chinese
// "嗯。" (with the language given).
var asrTails = []string{"The.", "The", "the.", "嗯。", "嗯"}

// cutASRTail removes a hallucinated tail from text: one of asrTails after
// a space that follows the end of a sentence. ok says it did.
func cutASRTail(text string) (string, bool) {
	t := strings.TrimRightFunc(text, unicode.IsSpace)
	for _, tail := range asrTails {
		rest, found := strings.CutSuffix(t, tail)
		if !found || rest == "" || !unicode.IsSpace(rune(rest[len(rest)-1])) {
			continue
		}
		rest = strings.TrimRightFunc(rest, unicode.IsSpace)
		if r := []rune(rest); len(r) > 0 && strings.ContainsRune(".!?…。！？」”\"'", r[len(r)-1]) {
			return rest, true
		}
	}
	return text, false
}

// isASRTail reports whether text is only a hallucinated tail.
func isASRTail(text string) bool {
	t := strings.TrimSpace(text)
	for _, tail := range asrTails {
		if t == tail {
			return true
		}
	}
	return false
}

// CleanTranscription drops the ending Qwen3-ASR (model) hallucinated on
// audio longer than ASRPromptMaxSeconds: a last segment that is only
// "The." or "嗯。", or that tail after the last sentence of the text and
// of its last segment. Other models' results, and shorter audio, are left
// alone. It reports whether it changed tr.
func CleanTranscription(model string, tr *proto.TranscribeResult) bool {
	if !isQwenASR(model) || tr.AudioSeconds <= ASRPromptMaxSeconds {
		return false
	}
	changed := false
	if n := len(tr.Segments); n > 1 && isASRTail(tr.Segments[n-1].Text) {
		tr.Segments = tr.Segments[:n-1]
		changed = true
	} else if n > 0 {
		if t, ok := cutASRTail(tr.Segments[n-1].Text); ok {
			tr.Segments[n-1].Text = t
			changed = true
		}
	}
	if t, ok := cutASRTail(tr.Text); ok {
		tr.Text = t
		changed = true
	}
	return changed
}

// alignWindow is a stretch of the audio and the transcript text in it.
type alignWindow struct {
	start, end float64
	text       string
}

// alignWindows cuts a transcript of seconds into windows of whole
// segments, each at most AlignWindowSeconds long (a longer segment is a
// window of its own), cut halfway between two segments. Nil when the
// audio is short enough for one call, or the segments don't split it.
func alignWindows(segs []proto.Segment, seconds float64) []alignWindow {
	if seconds <= AlignSingleMaxSeconds {
		return nil
	}
	var spoken []proto.Segment
	for _, s := range segs {
		if strings.TrimSpace(s.Text) != "" && s.End > s.Start {
			spoken = append(spoken, s)
		}
	}
	var out []alignWindow
	var texts []string
	first := 0.0 // the window's first segment's start
	for i, s := range spoken {
		if len(texts) > 0 && s.End-first > AlignWindowSeconds {
			prev := spoken[i-1]
			cut := (prev.End + s.Start) / 2
			out = append(out, alignWindow{end: cut, text: strings.Join(texts, " ")})
			texts = nil
		}
		if len(texts) == 0 {
			first = s.Start
		}
		texts = append(texts, strings.TrimSpace(s.Text))
	}
	if len(texts) > 0 {
		out = append(out, alignWindow{end: seconds, text: strings.Join(texts, " ")})
	}
	if len(out) < 2 {
		return nil
	}
	for i := 1; i < len(out); i++ {
		out[i].start = out[i-1].end
	}
	return out
}

// alignWords times the words of base.Text in base.Audio (a WAV) with the
// forced aligner, through call: in one call, or, for audio longer than
// AlignSingleMaxSeconds, window by window (alignWindows over the
// transcript's segments), each window cut into a WAV of its own next to
// the audio and its words shifted to the window's start.
func alignWords(ctx context.Context, call func(p *proto.AlignParams, out *proto.AlignResult) error, base proto.AlignParams, segs []proto.Segment, seconds float64) ([]proto.Word, error) {
	wins := alignWindows(segs, seconds)
	var audio *wav.File // read window by window, never whole (thread #150)
	if len(wins) > 0 {
		var err error
		if audio, err = wav.Open(base.Audio); err != nil {
			wins = nil // not a WAV Go reads: in one call
		} else {
			defer audio.Close()
		}
	}
	if len(wins) == 0 {
		p := base
		var res proto.AlignResult
		if err := call(&p, &res); err != nil {
			return nil, err
		}
		return res.Words, nil
	}
	var words []proto.Word
	for i, w := range wins {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rate := float64(audio.Format().Rate)
		from, to := int(math.Round(w.start*rate)), int(math.Round(w.end*rate))
		piece, err := readSpan(audio, from, to)
		if err != nil {
			return nil, err
		}
		path := fmt.Sprintf("%s.align%d.wav", strings.TrimSuffix(base.Audio, ".wav"), i)
		if err := wav.WriteFile(path, piece, wav.PCM16, wav.Options{}); err != nil {
			return nil, err
		}
		p := base
		p.Audio, p.Text = path, w.text
		var res proto.AlignResult
		err = call(&p, &res)
		os.Remove(path)
		if err != nil {
			return nil, err
		}
		for _, wd := range res.Words {
			wd.Start = min(wd.Start+w.start, w.end)
			wd.End = min(wd.End+w.start, w.end)
			words = append(words, wd)
		}
	}
	return words, nil
}
