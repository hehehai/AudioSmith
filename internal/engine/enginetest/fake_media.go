package enginetest

// The fake's M1 methods. What each does, so tests can set up inputs:
//
//   - Inputs: a missing path fails with file_not_found. WAV files are read
//     for real. Other audio/video extensions (FakeMediaExts) stand for
//     FakeMediaSeconds of a 220 Hz stereo tone (and, for .mov/.mp4/.m4v, a
//     1080p video track); any other extension, or a broken WAV, fails with
//     unsupported_media.
//   - stt.transcribe: the transcript is the content of "<audio>.txt" when
//     that file exists, else FakeTranscript. Sentences become segments,
//     spread over the audio's duration. Words come only from models whose
//     repo contains "parakeet" (like the catalogue's wordTimes); "translate"
//     is unsupported unless the repo contains "whisper". The language is
//     the one asked for, else "zh" for CJK text, else "en".
//   - stt.align: the text's words (CJK: characters) spread evenly.
//   - vad.detect: an energy detector over 30 ms frames (speech above
//     -45 dBFS), with the request's minSpeech/minSilence/pad.
//   - enhance.run: the input converted to 48 kHz mono, unchanged otherwise.
//   - tts.encodeVoice: writes a small JSON file (format "fake/1"); models
//     whose repo contains kokoro, customvoice or voicedesign are
//     unsupported. tts.synthesize accepts that file as
//     voicePrompt for the same model, else falls back to refAudio.
//   - media.probe / media.decode: as described for inputs; decode cuts,
//     resamples and remixes for real.
//   - capture.*: FakeDevices; capture.start records a -12 dBFS tone in
//     real time, sends capture.level every level-ms (default 50 ms), and
//     ends on capture.stop, maxSeconds, or (FakeFlakyDevice) after 0.2 s
//     with reason deviceLost. Option deny-mic fails it with permission_denied.
//     FakeSilentMic sends silence until 0.15 s, then, unless the request
//     asked for aec, turns voice processing on by itself (levels and the
//     result with aecAuto, reason noSound) and records the tone;
//     FakeDeadMic fails with no_input after 0.15 s. The result's aec says
//     whether voice processing was on.

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// FakeTranscript is what the fake hears in audio without a ".txt" sidecar.
const FakeTranscript = "This is a fake transcript. It has two sentences."

// FakeMediaSeconds is the duration of non-WAV inputs.
const FakeMediaSeconds = 10.0

// FakeMediaExts are the non-WAV extensions the fake "decodes".
var FakeMediaExts = []string{".aif", ".aiff", ".aifc", ".caf", ".mp3", ".m4a", ".m4b", ".aac", ".flac", ".mov", ".mp4", ".m4v"}

func isVideoExt(ext string) bool { return ext == ".mov" || ext == ".mp4" || ext == ".m4v" }

// readInput decodes path as the fake can; false when it answered id with
// an error.
func (f *fakeEngine) readInput(id, path string) (Audio, bool) {
	if path == "" {
		f.fail(id, proto.CodeBadRequest, "Missing input")
		return Audio{}, false
	}
	if _, err := os.Stat(path); err != nil {
		f.failData(id, proto.CodeFileNotFound, map[string]string{"path": path}, "No such file: %s", path)
		return Audio{}, false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".wav" || ext == ".wave" {
		a, err := ReadWAV(path)
		if err == nil && a.Frames() > 0 {
			return a, true
		}
	} else if slices.Contains(FakeMediaExts, ext) {
		rate := 44100
		if isVideoExt(ext) {
			rate = 48000
		}
		return Tone(rate, 2, FakeMediaSeconds, 220, 0.25), true
	}
	f.failData(id, proto.CodeUnsupportedMedia, map[string]string{"path": path}, "Cannot read %s as audio", path)
	return Audio{}, false
}

// inputOK checks path like readInput.
func (f *fakeEngine) inputOK(id, path string) bool {
	_, ok := f.readInput(id, path)
	return ok
}

// probeOf describes path (readable, see readInput).
func probeOf(path string, a Audio) proto.ProbeResult {
	ext := strings.ToLower(filepath.Ext(path))
	p := proto.ProbeResult{Format: strings.TrimPrefix(ext, "."), Duration: a.Seconds(), HasAudio: true, AudioTracks: 1,
		SampleRate: a.Rate, Channels: a.Channels}
	switch ext {
	case ".wav", ".wave":
		p.Format, p.Codec, p.Bits = "wav", "lpcm", a.Bits
		p.BitRate = a.Rate * a.Channels * a.Bits
	case ".aif", ".aiff", ".aifc":
		p.Format, p.Codec, p.Bits = "aiff", "lpcm", 16
	case ".caf":
		p.Codec, p.Bits = "lpcm", 16
	case ".mp3":
		p.Codec, p.BitRate = "mp3", 192000
	case ".flac":
		p.Codec = "flac"
	default:
		p.Codec, p.BitRate = "aac", 256000
	}
	if isVideoExt(ext) {
		p.HasVideo = true
		p.Video = &proto.VideoInfo{Codec: "h264", Width: 1920, Height: 1080, FPS: 30}
	}
	return p
}

func (f *fakeEngine) probe(m proto.Message) {
	var p proto.ProbeParams
	m.DecodeParams(&p)
	if a, ok := f.readInput(m.ID, p.Input); ok {
		f.result(m.ID, probeOf(p.Input, a))
	}
}

func (f *fakeEngine) decode(m proto.Message) {
	var p proto.DecodeParams
	if err := m.DecodeParams(&p); err != nil || p.Output.Path == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing input or output")
		return
	}
	src, ok := f.readInput(m.ID, p.Input)
	if !ok || !f.work(m.ID, p.Input, "decoding", nil) {
		return
	}
	out := Convert(Cut(src, p.Start, p.Duration), p.SampleRate, p.Channels)
	if !f.write(m.ID, p.Output, out) {
		return
	}
	f.result(m.ID, proto.DecodeResult{AudioOut: audioOut(p.Output.Path, out), Source: probeOf(p.Input, src), Seconds: 0.01})
}

// encode "encodes" to M4B/M4A: the output holds "FAKE-M4B" and then the
// params as JSON, so tests can read back what was asked.
func (f *fakeEngine) encode(m proto.Message) {
	var p proto.EncodeParams
	if err := m.DecodeParams(&p); err != nil || p.Output.Path == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing input or output")
		return
	}
	switch p.Output.Format {
	case "m4b", "m4a":
	case "mp3":
		f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "mp3"}, "This engine cannot encode MP3")
		return
	default:
		f.fail(m.ID, proto.CodeBadRequest, "Unknown output format %q", p.Output.Format)
		return
	}
	if p.Codec != "" && p.Codec != "aac" {
		f.fail(m.ID, proto.CodeBadRequest, "Unknown codec %q", p.Codec)
		return
	}
	src, ok := f.readInput(m.ID, p.Input)
	if !ok || !f.work(m.ID, p.Input, "encoding", nil) {
		return
	}
	if p.Cover != "" {
		if _, err := os.Stat(p.Cover); err != nil {
			f.failData(m.ID, proto.CodeFileNotFound, map[string]string{"path": p.Cover}, "No such file: %s", p.Cover)
			return
		}
	}
	body, _ := json.Marshal(p)
	data := append([]byte("FAKE-M4B\n"), body...)
	if err := os.WriteFile(p.Output.Path, data, 0o644); err != nil {
		f.fail(m.ID, proto.CodeInternal, "Could not write %s: %v", p.Output.Path, err)
		return
	}
	rate := p.Bitrate
	if rate == 0 {
		rate = 64000
		if src.Channels > 1 {
			rate = 96000
		}
	}
	f.result(m.ID, proto.EncodeResult{Output: p.Output.Path, Format: p.Output.Format, Bytes: int64(len(data)), Duration: round3(src.Seconds()), Bitrate: rate})
}

func audioOut(path string, a Audio) proto.AudioOut {
	return proto.AudioOut{Output: path, SampleRate: a.Rate, Channels: a.Channels, Frames: int64(a.Frames()), AudioSeconds: a.Seconds()}
}

// write writes a to out; false when it answered id with an error.
func (f *fakeEngine) write(id string, out proto.Output, a Audio) bool {
	if err := WriteWAV(out.Path, a, out.Bits); err != nil {
		f.fail(id, proto.CodeInternal, "Could not write %s: %v", out.Path, err)
		return false
	}
	return true
}

// modelJob decodes a model job's params and loads its model; false when
// it answered.
func (f *fakeEngine) modelJob(m proto.Message, p any, model func() string) bool {
	if err := m.DecodeParams(p); err != nil || model() == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing model")
		return false
	}
	return f.ensure(m.ID, model())
}

var sentenceEnd = regexp.MustCompile(`[^.!?。！？]+[.!?。！？]*`)

func hasCJK(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
	}) >= 0
}

// words splits text into words (CJK: characters), without punctuation.
func words(text string) []string {
	var out []string
	for _, w := range strings.Fields(text) {
		if hasCJK(w) {
			for _, r := range w {
				if !unicode.IsPunct(r) && !unicode.IsSpace(r) {
					out = append(out, string(r))
				}
			}
			continue
		}
		if w = strings.TrimFunc(w, unicode.IsPunct); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// spread times words evenly over [start, end].
func spread(ws []string, start, end float64) []proto.Word {
	out := make([]proto.Word, len(ws))
	step := (end - start) / float64(max(len(ws), 1))
	for i, w := range ws {
		out[i] = proto.Word{Text: w, Start: round3(start + float64(i)*step), End: round3(start + float64(i+1)*step)}
	}
	return out
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

func (f *fakeEngine) transcribe(m proto.Message) {
	var p proto.TranscribeParams
	if !f.modelJob(m, &p, func() string { return p.Model }) {
		return
	}
	lower := strings.ToLower(p.Model)
	if p.Task == "translate" && !strings.Contains(lower, "whisper") {
		f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "translate"}, "%s cannot translate", p.Model)
		return
	}
	a, ok := f.readInput(m.ID, p.Audio)
	if !ok || !f.work(m.ID, p.Audio, "transcribing", nil) {
		return
	}
	text := FakeTranscript
	if b, err := os.ReadFile(p.Audio + ".txt"); err == nil {
		text = strings.TrimSpace(string(b))
	}
	lang := p.Language
	if lang == "" || lang == "auto" {
		lang = "en"
		if hasCJK(text) {
			lang = "zh"
		}
	}
	dur := a.Seconds()
	res := proto.TranscribeResult{Model: p.Model, Text: text, Language: lang, Segments: []proto.Segment{},
		AudioSeconds: dur, Seconds: 0.01, RTF: dur / 0.01}
	sentences := sentenceEnd.FindAllString(text, -1)
	total := 0
	for _, s := range sentences {
		total += len([]rune(strings.TrimSpace(s)))
	}
	at := 0.0
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		end := at + dur*float64(len([]rune(s)))/float64(max(total, 1))
		seg := proto.Segment{Start: round3(at), End: round3(end), Text: s}
		if p.Words && strings.Contains(lower, "parakeet") {
			seg.Words = spread(words(s), at, end)
		}
		res.Segments = append(res.Segments, seg)
		at = end
	}
	f.touch(p.Model)
	f.result(m.ID, res)
}

func (f *fakeEngine) align(m proto.Message) {
	var p proto.AlignParams
	if !f.modelJob(m, &p, func() string { return p.Model }) {
		return
	}
	a, ok := f.readInput(m.ID, p.Audio)
	if !ok || !f.work(m.ID, p.Audio, "aligning", nil) {
		return
	}
	f.touch(p.Model)
	f.result(m.ID, proto.AlignResult{Model: p.Model, Words: spread(words(p.Text), 0, a.Seconds()), AudioSeconds: a.Seconds(), Seconds: 0.01})
}

func orDefault(v *float64, d float64) float64 {
	if v == nil {
		return d
	}
	return *v
}

// Regions finds speech in a like the fake's vad.detect: 30 ms frames above
// -45 dBFS, pauses shorter than minSilence bridged, regions shorter than
// minSpeech dropped, pad added on both sides.
func Regions(a Audio, minSpeech, minSilence, pad float64) []proto.Region {
	mono := a.Mono()
	frame := max(a.Rate*30/1000, 1)
	var out []proto.Region
	var cur *proto.Region
	for i := 0; i < len(mono); i += frame {
		rms, _ := levels(mono[i:min(i+frame, len(mono))])
		t0 := float64(i) / float64(a.Rate)
		t1 := float64(min(i+frame, len(mono))) / float64(a.Rate)
		if rms > -45 {
			if cur != nil && t0-cur.End < minSilence {
				cur.End = t1
			} else {
				out = append(out, proto.Region{Start: t0, End: t1, Prob: 0.9})
				cur = &out[len(out)-1]
			}
		}
	}
	dur := a.Seconds()
	kept := []proto.Region{}
	for _, r := range out {
		if r.End-r.Start < minSpeech {
			continue
		}
		r.Start, r.End = round3(max(0, r.Start-pad)), round3(min(dur, r.End+pad))
		if n := len(kept); n > 0 && r.Start <= kept[n-1].End {
			kept[n-1].End = r.End
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

func (f *fakeEngine) vad(m proto.Message) {
	var p proto.VADParams
	if !f.modelJob(m, &p, func() string { return p.Model }) {
		return
	}
	a, ok := f.readInput(m.ID, p.Audio)
	if !ok || !f.work(m.ID, p.Audio, "detecting", nil) {
		return
	}
	regions := Regions(a, orDefault(p.MinSpeech, 0.25), orDefault(p.MinSilence, 0.1), orDefault(p.Pad, 0.03))
	f.touch(p.Model)
	f.result(m.ID, proto.VADResult{Model: p.Model, Regions: regions, AudioSeconds: a.Seconds(), Seconds: 0.01})
}

func (f *fakeEngine) enhance(m proto.Message) {
	var p proto.EnhanceParams
	if !f.modelJob(m, &p, func() string { return p.Model }) {
		return
	}
	if p.Output.Path == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing output")
		return
	}
	a, ok := f.readInput(m.ID, p.Audio)
	if !ok || !f.work(m.ID, p.Audio, "enhancing", nil) {
		return
	}
	out := Convert(a, 48000, 1)
	if !f.write(m.ID, p.Output, out) {
		return
	}
	f.touch(p.Model)
	f.result(m.ID, proto.EnhanceResult{AudioOut: audioOut(p.Output.Path, out), Model: p.Model, Seconds: 0.01})
}

// fakePrompt is what the fake's tts.encodeVoice writes.
type fakePrompt struct {
	Format   string `json:"format"`
	Model    string `json:"model"`
	RefAudio string `json:"refAudio"`
	RefText  string `json:"refText,omitempty"`
}

// FakePromptFormat is the format of the fake's voice prompts.
const FakePromptFormat = "fake/1"

func noVoicePrompt(model string) bool {
	l := strings.ToLower(model)
	for _, s := range []string{"kokoro", "customvoice", "voicedesign"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func (f *fakeEngine) encodeVoice(m proto.Message) {
	var p proto.EncodeVoiceParams
	if err := m.DecodeParams(&p); err != nil || p.Model == "" || p.PromptPath == "" {
		f.fail(m.ID, proto.CodeBadRequest, "Missing model or promptPath")
		return
	}
	if noVoicePrompt(p.Model) {
		f.failData(m.ID, proto.CodeUnsupported, map[string]string{"capability": "voicePrompt"}, "%s cannot encode voices", p.Model)
		return
	}
	if !f.ensure(m.ID, p.Model) {
		return
	}
	a, ok := f.readInput(m.ID, p.RefAudio)
	if !ok || !f.work(m.ID, p.RefAudio, "encoding", nil) {
		return
	}
	data, _ := json.Marshal(fakePrompt{Format: FakePromptFormat, Model: p.Model, RefAudio: p.RefAudio, RefText: p.RefText})
	if err := os.WriteFile(p.PromptPath, data, 0o644); err != nil {
		f.fail(m.ID, proto.CodeInternal, "Could not write %s: %v", p.PromptPath, err)
		return
	}
	f.touch(p.Model)
	f.result(m.ID, proto.EncodeVoiceResult{VoicePrompt: p.PromptPath, Model: p.Model, Format: FakePromptFormat,
		Bytes: int64(len(data)), AudioSeconds: a.Seconds(), Seconds: 0.01})
}

// useVoicePrompt checks a synthesize's voicePrompt; false when it answered.
func (f *fakeEngine) useVoicePrompt(id string, p proto.SynthesizeParams) bool {
	if p.VoicePrompt == "" {
		return true
	}
	var fp fakePrompt
	data, err := os.ReadFile(p.VoicePrompt)
	if err == nil {
		err = json.Unmarshal(data, &fp)
	}
	if err == nil && (fp.Format != FakePromptFormat || fp.Model != p.Model) {
		err = errors.New("made for " + fp.Model + " (" + fp.Format + ")")
	}
	if err == nil {
		return true
	}
	if p.RefAudio != "" {
		f.notify(proto.MethodLog, proto.LogParams{Level: "warning", Job: id, Message: "Voice prompt not usable, encoding the reference again: " + err.Error()})
		return true
	}
	f.fail(id, proto.CodeBadRequest, "Voice prompt %s is not usable: %v", p.VoicePrompt, err)
	return false
}
