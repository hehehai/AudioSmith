package api

// OpenAI's audio API (/v1), as the official SDKs call it: speech,
// transcriptions, translations, models; plus /v1/audio/voices, which
// OpenAI does not have, listing what "voice" may name.
//
// Files: openai.go (speech), openai_transcribe.go (transcriptions and
// translations), openai_models.go (models and voices).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// MaxOpenAIInput is OpenAI's limit on /v1/audio/speech's input.
const MaxOpenAIInput = 4096

// speechBody is /v1/audio/speech's request, with AudioSmith's extensions.
type speechBody struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          voiceRef `json:"voice"`
	Instructions   string   `json:"instructions"`
	ResponseFormat string   `json:"response_format"`
	Speed          *float64 `json:"speed"`
	StreamFormat   string   `json:"stream_format"`

	// Extensions.
	Language      string   `json:"language"`
	Seed          *int64   `json:"seed"`
	Duration      *float64 `json:"duration"`
	Steps         *int     `json:"steps"`
	NumStep       *int     `json:"num_step"` // VoiceStudio's name
	Temperature   *float64 `json:"temperature"`
	TopP          *float64 `json:"top_p"`
	Guidance      *float64 `json:"guidance_scale"`
	Exaggeration  *float64 `json:"exaggeration"`
	Normalize     *bool    `json:"normalize"`
	Instruct      string   `json:"instruct"`    // = instructions
	Description   string   `json:"description"` // = instructions, for design models
	ReferenceText string   `json:"ref_text"`
}

func (b speechBody) params() SpeechParams {
	p := SpeechParams{Text: b.Input, Model: b.Model, Voice: string(b.Voice), Language: b.Language,
		Instructions: firstNonEmpty(b.Instructions, b.Instruct, b.Description)}
	if b.Speed != nil {
		p.Speed = *b.Speed
	}
	if b.Seed != nil {
		p.Seed = *b.Seed
	}
	if b.Duration != nil {
		p.Duration = *b.Duration
	}
	if b.Steps != nil {
		p.Steps = *b.Steps
	} else if b.NumStep != nil {
		p.Steps = *b.NumStep
	}
	if b.Temperature != nil {
		p.Temperature = *b.Temperature
	}
	if b.TopP != nil {
		p.TopP = *b.TopP
	}
	if b.Guidance != nil {
		p.Guidance = *b.Guidance
	}
	if b.Exaggeration != nil {
		p.Exaggeration = *b.Exaggeration
	}
	if b.Normalize != nil {
		p.Normalize = *b.Normalize
	}
	return p
}

// readJSON decodes a JSON body (at most limit bytes).
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if err == io.EOF {
			return errInvalid("", "The request has no JSON body.")
		}
		return errInvalid("", "The JSON body is not valid: %v", err)
	}
	return nil
}

// checkFormat validates a response format for speech.
func (s *Server) checkFormat(format string) error {
	switch format {
	case FormatWAV, FormatPCM:
		return nil
	case FormatAAC, FormatM4A:
		if s.b.CanEncode() {
			return nil
		}
		return &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedFormat, Param: "response_format",
			Message: "This engine cannot encode AAC: use wav or pcm."}
	case FormatMP3, FormatOpus, FormatFLAC:
		if s.encodesFormat(format) { // M7a: ffmpeg (Settings → Audio tools)
			return nil
		}
		return &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedFormat, Param: "response_format",
			Message: fmt.Sprintf("%s needs ffmpeg (AudioSmith → Settings → Audio tools): use wav, pcm or aac.", format)}
	}
	return &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedFormat, Param: "response_format",
		Message: fmt.Sprintf("Unknown response_format %q: use wav, pcm or aac.", format)}
}

// v1Speech is POST /v1/audio/speech.
func (s *Server) v1Speech(w http.ResponseWriter, r *http.Request) {
	var b speechBody
	if err := readJSON(w, r, 1<<20, &b); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(b.Input) == "" {
		writeError(w, errInvalid("input", "input is empty."))
		return
	}
	if n := utf8.RuneCountInString(b.Input); n > MaxOpenAIInput {
		writeError(w, errInvalid("input", "input has %d characters; the limit is %d (use /api/generate for longer scripts).", n, MaxOpenAIInput))
		return
	}
	if b.Speed != nil && (*b.Speed < 0.25 || *b.Speed > 4) {
		writeError(w, errInvalid("speed", "speed must be between 0.25 and 4."))
		return
	}
	format := strings.ToLower(strings.TrimSpace(b.ResponseFormat))
	if format == "" {
		format = FormatWAV // OpenAI's default, mp3, needs an encoder AudioSmith lacks
	}
	if err := s.checkFormat(format); err != nil {
		writeError(w, err)
		return
	}
	sse := false
	switch strings.ToLower(b.StreamFormat) {
	case "", "audio":
	case "sse":
		sse = true
		if format != FormatPCM && format != FormatWAV {
			writeError(w, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedFormat, Param: "stream_format",
				Message: "stream_format sse sends pcm or wav."})
			return
		}
	default:
		writeError(w, errInvalid("stream_format", "stream_format is audio or sse."))
		return
	}
	p := b.params()
	p.Client = r.Header.Get(HeaderClient)
	sp, err := s.StartSpeech(r.Context(), p)
	if err != nil {
		writeError(w, err)
		return
	}
	s.answerSpeech(w, r, sp, format, sse, p.Normalize, len(b.Input))
}

// answerSpeech sends a take as format: streamed as it is made (pcm and
// wav, unless normalized: the whole take is needed first), as SSE events,
// or as a file once it is made.
func (s *Server) answerSpeech(w http.ResponseWriter, r *http.Request, sp *Speech, format string, sse, normalize bool, chars int) {
	ctx := r.Context()
	live := (format == FormatPCM || format == FormatWAV) && !normalize
	lw := &liveWriter{w: w, format: format, sse: sse, sp: sp}
	var onChunk func(proto.ChunkParams) error
	if live || sse {
		onChunk = func(c proto.ChunkParams) error {
			if !live {
				return nil
			}
			return lw.chunk(c)
		}
	}
	res, err := sp.Wait(ctx, onChunk)
	if err != nil {
		if lw.started {
			lw.fail(err)
			return
		}
		writeError(w, err)
		return
	}
	if lw.started {
		lw.finish(chars)
		return
	}
	// Not streamed: the whole file.
	path := res.Output
	switch format {
	case FormatPCM:
		data, err := filePCM(path, PCMRate)
		if err != nil {
			writeError(w, err)
			return
		}
		if sse {
			lw.start(PCMRate, 1)
			lw.send(data)
			lw.finish(chars)
			return
		}
		w.Header().Set("Content-Type", contentType(format))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		speechHeaders(w.Header(), sp)
		w.Header().Set("X-AudioSmith-Seed", strconv.FormatInt(res.Take.Seed, 10))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	case FormatAAC, FormatM4A:
		out := filepath.Join(s.b.TempDir(), "encoded", res.Take.ID+".m4a")
		if err := s.encode(ctx, path, out); err != nil {
			writeError(w, err)
			return
		}
		defer os.Remove(out)
		path = out
	case FormatMP3, FormatOpus, FormatFLAC: // M7a: ffmpeg behind media.encode
		out := filepath.Join(s.b.TempDir(), "encoded", res.Take.ID+"."+format)
		if err := s.encodeAs(ctx, path, out, format); err != nil {
			writeError(w, err)
			return
		}
		defer os.Remove(out)
		path = out
	}
	if sse { // wav, normalized; read a chunk at a time (thread #150)
		f, err := os.Open(path)
		if err != nil {
			writeError(w, err)
			return
		}
		defer f.Close()
		lw.headers()
		lw.started = true
		buf := make([]byte, 48000)
		for {
			n, err := io.ReadFull(f, buf)
			if n > 0 && lw.send(buf[:n]) != nil {
				return
			}
			if err != nil {
				break
			}
		}
		lw.finish(chars)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, err)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", contentType(format))
	if fi != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	}
	speechHeaders(w.Header(), sp)
	w.Header().Set("X-AudioSmith-Seed", strconv.FormatInt(res.Take.Seed, 10))
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}

// liveWriter streams a take's audio as it is made.
type liveWriter struct {
	w       http.ResponseWriter
	format  string
	sse     bool
	sp      *Speech
	started bool
	conv    *pcmConverter
}

func (lw *liveWriter) headers() {
	h := lw.w.Header()
	if lw.sse {
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
	} else {
		h.Set("Content-Type", contentType(lw.format))
	}
	h.Set("X-Accel-Buffering", "no")
	speechHeaders(h, lw.sp)
	lw.w.WriteHeader(http.StatusOK)
}

// start sends the headers (and the WAV header) for audio at rate.
func (lw *liveWriter) start(rate, channels int) {
	lw.headers()
	lw.started = true
	switch lw.format {
	case FormatWAV:
		lw.send(wavStreamHeader(rate, channels))
	case FormatPCM:
		lw.conv = newPCMConverter(rate, channels)
	}
}

func (lw *liveWriter) chunk(c proto.ChunkParams) error {
	if !lw.started {
		lw.start(c.SampleRate, c.Channels)
	}
	data := c.Data
	if lw.conv != nil {
		data = lw.conv.convert(data)
	}
	return lw.send(data)
}

func (lw *liveWriter) send(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var err error
	if lw.sse {
		ev, _ := json.Marshal(map[string]any{"type": "speech.audio.delta", "audio": base64.StdEncoding.EncodeToString(data)})
		_, err = fmt.Fprintf(lw.w, "data: %s\n\n", ev)
	} else {
		_, err = lw.w.Write(data)
	}
	if f, ok := lw.w.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

// finish ends the stream.
func (lw *liveWriter) finish(chars int) {
	if lw.conv != nil {
		lw.send(lw.conv.flush())
	}
	if lw.sse {
		ev, _ := json.Marshal(map[string]any{"type": "speech.audio.done",
			"usage": map[string]int{"input_tokens": chars, "output_tokens": 0, "total_tokens": chars}})
		fmt.Fprintf(lw.w, "data: %s\n\n", ev)
		if f, ok := lw.w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// fail ends a stream that already started: an error event for SSE, else
// the connection is cut so the client sees the audio is incomplete.
func (lw *liveWriter) fail(err error) {
	if lw.sse {
		ev, _ := json.Marshal(map[string]any{"type": "error", "error": errorBody(errorOf(err))["error"]})
		fmt.Fprintf(lw.w, "data: %s\n\n", ev)
		if f, ok := lw.w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	panic(http.ErrAbortHandler)
}

// encode makes an M4A (AAC) of a WAV through the media worker.
func (s *Server) encode(ctx context.Context, in, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	params := proto.EncodeParams{Input: in, Output: proto.Output{Path: out, Format: "m4a"}, Codec: "aac"}
	return s.runEngineJob(ctx, proto.MethodMediaEncode, params, "Encode audio for the API", nil)
}

// encodesFormat reports whether the backend writes MP3, Opus or FLAC.
func (s *Server) encodesFormat(format string) bool {
	fe, ok := s.b.(FormatEncoder)
	return ok && fe.EncodesFormat(format)
}

// encodeAs makes an MP3, Ogg Opus or FLAC of a WAV through media.encode
// (which the app runs on ffmpeg).
func (s *Server) encodeAs(ctx context.Context, in, out, format string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	params := proto.EncodeParams{Input: in, Output: proto.Output{Path: out, Format: format}}
	return s.runEngineJob(ctx, proto.MethodMediaEncode, params, "Encode audio for the API", nil)
}
