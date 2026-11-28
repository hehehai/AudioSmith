package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// WhisperRunner drives whisper.cpp (whisper-cli or whisper-server) for
// stt.transcribe on everyday Windows PCs.
type WhisperRunner struct {
	hw        hw.Info
	provider  string
	serverURL string
	cliBin    string
	client    *http.Client
}

// NewWhisperRunner discovers whisper-cli / AUDIOSMITH_WHISPER_URL automatically.
func NewWhisperRunner(hwInfo hw.Info) *WhisperRunner {
	return &WhisperRunner{
		hw:        hwInfo,
		provider:  SelectProvider(hwInfo),
		serverURL: strings.TrimRight(os.Getenv("AUDIOSMITH_WHISPER_URL"), "/"),
		cliBin:    DiscoverWhisperCLI(),
		client:    &http.Client{},
	}
}

// NewWhisperRunnerWithConfig constructs a WhisperRunner with explicit paths/URL.
func NewWhisperRunnerWithConfig(hwInfo hw.Info, serverURL, cliBin string) *WhisperRunner {
	return &WhisperRunner{
		hw:        hwInfo,
		provider:  SelectProvider(hwInfo),
		serverURL: strings.TrimRight(serverURL, "/"),
		cliBin:    cliBin,
		client:    &http.Client{},
	}
}

func (w *WhisperRunner) Provider() string { return w.provider }
func (w *WhisperRunner) Available() bool  { return w.serverURL != "" || w.cliBin != "" }

// Transcribe runs whisper-server or whisper-cli on p.Audio and returns a
// protocol-v2 TranscribeResult.
func (w *WhisperRunner) Transcribe(ctx context.Context, p proto.TranscribeParams) (proto.TranscribeResult, error) {
	if !w.Available() {
		return proto.TranscribeResult{}, fmt.Errorf("whisper.cpp runtime not found (install whisper-cli.exe or set AUDIOSMITH_WHISPER_URL)")
	}
	if p.Audio == "" {
		return proto.TranscribeResult{}, proto.Errorf(proto.CodeBadRequest, "stt.transcribe: audio path is required")
	}

	wavPath, audioSecs, cleanup, err := prepareWhisperWAV(p.Audio)
	if err != nil {
		return proto.TranscribeResult{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	if w.serverURL != "" {
		return w.transcribeHTTP(ctx, wavPath, audioSecs, p)
	}
	return w.transcribeCLI(ctx, wavPath, audioSecs, p)
}

func (w *WhisperRunner) transcribeCLI(ctx context.Context, wavPath string, audioSecs float64, p proto.TranscribeParams) (proto.TranscribeResult, error) {
	modelFile := ResolveModelFile(p.ModelPath, ".bin", ".gguf")
	if modelFile == "" {
		return proto.TranscribeResult{}, proto.Errorf(proto.CodeBadRequest, "stt.transcribe: modelPath (.bin/.gguf) is required for whisper-cli")
	}

	tmpDir, err := os.MkdirTemp("", "as-whisper-*")
	if err != nil {
		return proto.TranscribeResult{}, err
	}
	defer os.RemoveAll(tmpDir)
	outPrefix := filepath.Join(tmpDir, "out")

	args := []string{
		"-m", modelFile,
		"-f", wavPath,
		"-ojf",
		"-of", outPrefix,
		"-t", strconv.Itoa(RecommendedThreads(w.hw)),
	}
	if w.provider == "cpu" {
		args = append(args, "--no-gpu")
	}
	if lang := strings.TrimSpace(p.Language); lang != "" && lang != "auto" {
		args = append(args, "-l", lang)
	} else {
		args = append(args, "-l", "auto")
	}
	if p.Task == "translate" {
		args = append(args, "-tr")
	}
	if p.Prompt != "" {
		args = append(args, "--prompt", p.Prompt)
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, w.cliBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return proto.TranscribeResult{}, fmt.Errorf("whisper-cli failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	secs := time.Since(start).Seconds()

	jsonPath := outPrefix + ".json"
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return proto.TranscribeResult{}, fmt.Errorf("whisper-cli output missing: %w", err)
	}
	return parseWhisperCLIJSON(data, p.Model, p.Language, audioSecs, secs)
}

func (w *WhisperRunner) transcribeHTTP(ctx context.Context, wavPath string, audioSecs float64, p proto.TranscribeParams) (proto.TranscribeResult, error) {
	f, err := os.Open(wavPath)
	if err != nil {
		return proto.TranscribeResult{}, err
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(wavPath))
	if err != nil {
		return proto.TranscribeResult{}, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return proto.TranscribeResult{}, err
	}
	_ = mw.WriteField("response_format", "verbose_json")
	if p.Language != "" && p.Language != "auto" {
		_ = mw.WriteField("language", p.Language)
	}
	if p.Task == "translate" {
		_ = mw.WriteField("translate", "true")
	}
	if p.Prompt != "" {
		_ = mw.WriteField("prompt", p.Prompt)
	}
	if err := mw.Close(); err != nil {
		return proto.TranscribeResult{}, err
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.serverURL+"/inference", &body)
	if err != nil {
		return proto.TranscribeResult{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := w.client.Do(req)
	if err != nil {
		return proto.TranscribeResult{}, fmt.Errorf("whisper-server request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return proto.TranscribeResult{}, fmt.Errorf("whisper-server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	secs := time.Since(start).Seconds()

	var parsed struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return proto.TranscribeResult{}, err
	}
	if audioSecs <= 0 && parsed.Duration > 0 {
		audioSecs = parsed.Duration
	}
	segs := make([]proto.Segment, 0, len(parsed.Segments))
	for _, s := range parsed.Segments {
		segs = append(segs, proto.Segment{Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text)})
	}
	lang := parsed.Language
	if lang == "" {
		lang = p.Language
	}
	rtf := 0.0
	if secs > 0 && audioSecs > 0 {
		rtf = audioSecs / secs
	}
	return proto.TranscribeResult{
		Model:        p.Model,
		Text:         strings.TrimSpace(parsed.Text),
		Language:     lang,
		Segments:     segs,
		AudioSeconds: audioSecs,
		Seconds:      secs,
		RTF:          rtf,
	}, nil
}

func parseWhisperCLIJSON(data []byte, model, reqLang string, audioSecs, secs float64) (proto.TranscribeResult, error) {
	var parsed struct {
		Result struct {
			Language string `json:"language"`
		} `json:"result"`
		Transcription []struct {
			Offsets struct {
				From int64 `json:"from"`
				To   int64 `json:"to"`
			} `json:"offsets"`
			Text string `json:"text"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return proto.TranscribeResult{}, fmt.Errorf("parse whisper-cli json: %w", err)
	}
	segs := make([]proto.Segment, 0, len(parsed.Transcription))
	var parts []string
	for _, seg := range parsed.Transcription {
		txt := strings.TrimSpace(seg.Text)
		if txt == "" {
			continue
		}
		segs = append(segs, proto.Segment{
			Start: float64(seg.Offsets.From) / 1000.0,
			End:   float64(seg.Offsets.To) / 1000.0,
			Text:  txt,
		})
		parts = append(parts, txt)
	}
	lang := parsed.Result.Language
	if lang == "" {
		lang = reqLang
	}
	rtf := 0.0
	if secs > 0 && audioSecs > 0 {
		rtf = audioSecs / secs
	}
	return proto.TranscribeResult{
		Model:        model,
		Text:         strings.Join(parts, " "),
		Language:     lang,
		Segments:     segs,
		AudioSeconds: audioSecs,
		Seconds:      secs,
		RTF:          rtf,
	}, nil
}

// prepareWhisperWAV ensures audioPath is a 16 kHz mono PCM16 WAV for whisper.cpp.
func prepareWhisperWAV(audioPath string) (wavPath string, audioSecs float64, cleanup func(), err error) {
	wf, openErr := wav.Open(audioPath)
	if openErr != nil {
		// Non-WAV input (e.g. MP3/FLAC): pass through directly if whisper-cli supports it.
		if _, statErr := os.Stat(audioPath); statErr != nil {
			return "", 0, nil, proto.Errorf(proto.CodeFileNotFound, "stt.transcribe: audio not found: %v", statErr)
		}
		return audioPath, 0, nil, nil
	}
	defer wf.Close()

	f := wf.Format()
	audioSecs = wf.Seconds()
	if f.Rate == 16000 && f.Channels == 1 && f.Encoding == wav.PCM16 {
		return audioPath, audioSecs, nil, nil
	}

	buf, err := wf.ReadFrames(0, -1)
	if err != nil {
		return "", 0, nil, err
	}
	if buf.Channels > 1 {
		frames := len(buf.Samples) / buf.Channels
		mono := make([]float32, frames)
		inv := float32(1.0 / float64(buf.Channels))
		for i := 0; i < frames; i++ {
			var sum float32
			for ch := 0; ch < buf.Channels; ch++ {
				sum += buf.Samples[i*buf.Channels+ch]
			}
			mono[i] = sum * inv
		}
		buf = dsp.Buffer{Samples: mono, Rate: buf.Rate, Channels: 1}
	}
	if buf.Rate != 16000 && len(buf.Samples) > 0 {
		buf = resample.Buffer(buf, 16000, resample.Fast)
	}

	tmpDir, err := os.MkdirTemp("", "as-whisper-wav-*")
	if err != nil {
		return "", 0, nil, err
	}
	tmpWAV := filepath.Join(tmpDir, "input16k.wav")
	outW, err := wav.Create(tmpWAV, wav.Format{Rate: 16000, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", 0, nil, err
	}
	if len(buf.Samples) > 0 {
		if err := outW.Write(buf.Samples); err != nil {
			_ = outW.Close()
			_ = os.RemoveAll(tmpDir)
			return "", 0, nil, err
		}
	}
	if err := outW.Close(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", 0, nil, err
	}
	return tmpWAV, audioSecs, func() { _ = os.RemoveAll(tmpDir) }, nil
}
