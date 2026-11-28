package onnx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

const (
	kokoroSampleRate   = 24000
	kokoroStyleDim     = 256
	kokoroMaxTokens    = 510
	kokoroDefaultVoice = "af_heart"
)

// KokoroTTS runs Kokoro 82M ONNX text-to-speech over an ONNX Runtime Session.
type KokoroTTS struct {
	session    Session
	modelDir   string
	vocab      map[rune]int64
	tokensName string
	styleName  string
	speedName  string
	audioName  string
	voices     map[string][]float32 // cached voiceName -> flattened [N * 256] float32
}

// NewKokoroTTS wraps an open Kokoro 82M ONNX Session and inspects its tensor
// names and optional config.json / voice files in modelDir.
func NewKokoroTTS(sess Session, modelDir string) *KokoroTTS {
	k := &KokoroTTS{
		session:    sess,
		modelDir:   modelDir,
		vocab:      loadKokoroVocab(modelDir),
		tokensName: "input_ids",
		styleName:  "style",
		speedName:  "speed",
		audioName:  "waveform",
		voices:     map[string][]float32{},
	}
	for _, name := range sess.InputNames() {
		low := strings.ToLower(name)
		switch {
		case low == "tokens" || low == "input_ids":
			k.tokensName = name
		case low == "style":
			k.styleName = name
		case low == "speed":
			k.speedName = name
		}
	}
	if outs := sess.OutputNames(); len(outs) > 0 {
		k.audioName = outs[0]
	}
	return k
}

// Synthesize converts p.Text into a 24 kHz WAV file at p.Output.Path.
func (k *KokoroTTS) Synthesize(ctx context.Context, jobID string, p proto.SynthesizeParams, n server.Notifier) (proto.SynthesizeResult, error) {
	if strings.TrimSpace(p.Text) == "" {
		return proto.SynthesizeResult{}, proto.Errorf(proto.CodeBadRequest, "tts.synthesize: text is required")
	}
	if p.Output.Path == "" {
		return proto.SynthesizeResult{}, proto.Errorf(proto.CodeBadRequest, "tts.synthesize: output.path is required")
	}

	speed := float32(1.0)
	if p.Speed != nil && *p.Speed > 0.25 && *p.Speed <= 4.0 {
		speed = float32(*p.Speed)
	}
	voice := strings.TrimSpace(p.Voice)
	if voice == "" {
		voice = kokoroDefaultVoice
	}
	styleTable := k.loadVoice(voice)

	chunks := splitSentences(p.Text, 300)
	if len(chunks) == 0 {
		chunks = []string{p.Text}
	}

	start := time.Now()
	var allAudio []float32
	seq := 0

	for i, textChunk := range chunks {
		if err := ctx.Err(); err != nil {
			return proto.SynthesizeResult{}, err
		}
		if n != nil {
			frac := float64(i) / float64(len(chunks))
			_ = n.Notify(proto.MethodJobProgress, proto.ProgressParams{
				Job:      jobID,
				Stage:    "generating",
				Fraction: &frac,
			})
		}

		phonemes := Phonemize(textChunk)
		ids := TokenizeKokoro(phonemes, k.vocab)
		if len(ids) > kokoroMaxTokens+2 {
			ids = append(ids[:kokoroMaxTokens+1], 0)
		}
		if len(ids) <= 2 {
			continue
		}

		styleVec := pickStyleVector(styleTable, len(ids)-2)
		outs, err := k.session.Run([]Tensor{
			Int64Tensor(k.tokensName, []int64{1, int64(len(ids))}, ids),
			Float32Tensor(k.styleName, []int64{1, kokoroStyleDim}, styleVec),
			Float32Tensor(k.speedName, []int64{1}, []float32{speed}),
		}, []string{k.audioName})
		if err != nil {
			return proto.SynthesizeResult{}, fmt.Errorf("kokoro onnx chunk %d: %w", i, err)
		}
		if len(outs) == 0 || len(outs[0].Float32) == 0 {
			continue
		}
		pcm := outs[0].Float32
		allAudio = append(allAudio, pcm...)

		if p.Stream && n != nil {
			_ = n.Notify(proto.MethodTTSChunk, proto.ChunkParams{
				Job:        jobID,
				Seq:        seq,
				SampleRate: kokoroSampleRate,
				Channels:   1,
				Encoding:   "pcm16le",
				Data:       float32ToPCM16LE(pcm),
				Final:      i == len(chunks)-1,
			})
			seq++
		}
	}

	if p.Stream && n != nil && seq == 0 {
		_ = n.Notify(proto.MethodTTSChunk, proto.ChunkParams{
			Job:        jobID,
			Seq:        0,
			SampleRate: kokoroSampleRate,
			Channels:   1,
			Encoding:   "pcm16le",
			Data:       nil,
			Final:      true,
		})
	}

	enc := wav.PCM16
	switch p.Output.Bits {
	case 24:
		enc = wav.PCM24
	case 32:
		enc = wav.Float32
	}
	w, err := wav.Create(p.Output.Path, wav.Format{Rate: kokoroSampleRate, Channels: 1, Encoding: enc}, wav.Options{})
	if err != nil {
		return proto.SynthesizeResult{}, fmt.Errorf("kokoro wav create: %w", err)
	}
	if len(allAudio) > 0 {
		if err := w.Write(allAudio); err != nil {
			_ = w.Close()
			return proto.SynthesizeResult{}, fmt.Errorf("kokoro wav write: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return proto.SynthesizeResult{}, fmt.Errorf("kokoro wav close: %w", err)
	}

	secs := time.Since(start).Seconds()
	audioSecs := float64(len(allAudio)) / float64(kokoroSampleRate)
	rtf := 0.0
	if secs > 0 && audioSecs > 0 {
		rtf = audioSecs / secs
	}
	return proto.SynthesizeResult{
		Output:       p.Output.Path,
		Model:        p.Model,
		SampleRate:   kokoroSampleRate,
		Channels:     1,
		Frames:       int64(len(allAudio)),
		AudioSeconds: audioSecs,
		Seconds:      secs,
		RTF:          rtf,
	}, nil
}

func (k *KokoroTTS) loadVoice(voice string) []float32 {
	if v, ok := k.voices[voice]; ok {
		return v
	}
	if k.modelDir != "" {
		candidates := []string{
			filepath.Join(k.modelDir, "voices", voice+".bin"),
			filepath.Join(k.modelDir, voice+".bin"),
			filepath.Join(k.modelDir, "voices", voice+".npy"),
			filepath.Join(k.modelDir, voice+".npy"),
		}
		for _, p := range candidates {
			if data, err := os.ReadFile(p); err == nil && len(data) >= kokoroStyleDim*4 {
				vec := decodeFloat32Voice(data)
				if len(vec) >= kokoroStyleDim {
					k.voices[voice] = vec
					return vec
				}
			}
		}
		for _, archive := range []string{
			filepath.Join(k.modelDir, "voices-v1.0.bin"),
			filepath.Join(k.modelDir, "voices.npz"),
			filepath.Join(k.modelDir, "voices", "voices.npz"),
		} {
			if vec := loadVoiceFromNPZ(archive, voice); len(vec) >= kokoroStyleDim {
				k.voices[voice] = vec
				return vec
			}
		}
	}
	// Neutral fallback if voice pack is not present on disk.
	neutral := make([]float32, kokoroStyleDim)
	k.voices[voice] = neutral
	return neutral
}

func pickStyleVector(table []float32, phonemeLen int) []float32 {
	if len(table) < kokoroStyleDim {
		return make([]float32, kokoroStyleDim)
	}
	rows := len(table) / kokoroStyleDim
	idx := phonemeLen
	if idx < 0 {
		idx = 0
	}
	if idx >= rows {
		idx = rows - 1
	}
	off := idx * kokoroStyleDim
	out := make([]float32, kokoroStyleDim)
	copy(out, table[off:off+kokoroStyleDim])
	return out
}

func loadVoiceFromNPZ(archivePath, voice string) []float32 {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil
	}
	defer zr.Close()
	want := voice + ".npy"
	var fallback *zip.File
	for _, f := range zr.File {
		if fallback == nil && strings.HasSuffix(f.Name, ".npy") {
			fallback = f
		}
		if f.Name == want || f.Name == voice {
			return readZipFloat32Voice(f)
		}
	}
	if fallback != nil {
		return readZipFloat32Voice(fallback)
	}
	return nil
}

func readZipFloat32Voice(f *zip.File) []float32 {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
	if err != nil {
		return nil
	}
	return decodeFloat32Voice(data)
}

// decodeFloat32Voice decodes either a NumPy .npy float32 array or raw little-endian
// float32 bytes into a []float32 slice.
func decodeFloat32Voice(data []byte) []float32 {
	raw := data
	if len(data) >= 10 && bytes.Equal(data[:6], []byte{0x93, 'N', 'U', 'M', 'P', 'Y'}) {
		major := data[6]
		var hdrLen int
		var off int
		if major == 1 {
			hdrLen = int(binary.LittleEndian.Uint16(data[8:10]))
			off = 10 + hdrLen
		} else if major >= 2 && len(data) >= 12 {
			hdrLen = int(binary.LittleEndian.Uint32(data[8:12]))
			off = 12 + hdrLen
		}
		if off > 0 && off <= len(data) {
			raw = data[off:]
		}
	}
	n := len(raw) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return out
}

func loadKokoroVocab(modelDir string) map[rune]int64 {
	if modelDir == "" {
		return defaultKokoroVocab
	}
	data, err := os.ReadFile(filepath.Join(modelDir, "config.json"))
	if err != nil {
		return defaultKokoroVocab
	}
	var cfg struct {
		Vocab map[string]int64 `json:"vocab"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || len(cfg.Vocab) == 0 {
		return defaultKokoroVocab
	}
	out := make(map[rune]int64, len(cfg.Vocab))
	for k, v := range cfg.Vocab {
		runes := []rune(k)
		if len(runes) == 1 {
			out[runes[0]] = v
		}
	}
	if len(out) == 0 {
		return defaultKokoroVocab
	}
	return out
}

func splitSentences(text string, maxChars int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= maxChars {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	for _, r := range text {
		cur.WriteRune(r)
		if (r == '.' || r == '!' || r == '?' || r == '\n') && cur.Len() >= maxChars/2 {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		} else if cur.Len() >= maxChars && r == ' ' {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func float32ToPCM16LE(samples []float32) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		v := int16(s * 32767)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}
