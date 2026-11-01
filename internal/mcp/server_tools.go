package mcp

// The MCP server's core tools: speech, cloning, design and description
// of voices, transcription, the voice, model and language lists, and
// health (addTools registers them); split from server.go.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type speechIn struct {
	Text         string  `json:"text" jsonschema:"What to say. Markup like [pause 500ms] works; [laughter] on models with non-verbal tags."`
	Voice        string  `json:"voice,omitempty" jsonschema:"A saved voice (id or name, see list_voices), a built-in speaker of the model, or an OpenAI voice name. Empty: this agent's voice from AudioSmith's settings, else the model's default."`
	Model        string  `json:"model,omitempty" jsonschema:"A model id from list_models. Empty: AudioSmith's default (or one that can speak the voice)."`
	Language     string  `json:"language,omitempty" jsonschema:"BCP-47 code (en, zh, fr…). Empty: the voice's or the model's."`
	Instructions string  `json:"instructions,omitempty" jsonschema:"How to say it (instruct models), or, without a voice on a design model, the voice to design."`
	Speed        float64 `json:"speed,omitempty" jsonschema:"Speaking rate, 0.5–2 (1 = normal)."`
	Seed         int64   `json:"seed,omitempty" jsonschema:"Seed, for a reproducible take."`
	Duration     float64 `json:"duration,omitempty" jsonschema:"Target length in seconds (OmniVoice)."`
	Steps        int     `json:"steps,omitempty" jsonschema:"Diffusion steps (OmniVoice): 8 draft, 16, 32 quality."`
	Normalize    bool    `json:"normalize,omitempty" jsonschema:"Even out volume (mastering)."`
	Output       string  `json:"output,omitempty" jsonschema:"inline (base64 audio in the result), file (a WAV in AudioSmith's MCP folder), or both. Empty: AudioSmith's setting."`
	Filename     string  `json:"filename,omitempty" jsonschema:"Name of the WAV written in file mode (no folders)."`
}

type speechOut struct {
	TakeID       string   `json:"take_id"`
	Model        string   `json:"model"`
	Voice        string   `json:"voice,omitempty"`
	AudioSeconds float64  `json:"audio_seconds"`
	GenSeconds   float64  `json:"generation_seconds"`
	SampleRate   int      `json:"sample_rate"`
	Seed         int64    `json:"seed,omitempty"`
	File         string   `json:"file,omitempty"`
	AudioURL     string   `json:"audio_url"`
	Ignored      []string `json:"ignored,omitempty"`
	Note         string   `json:"note,omitempty"`
	// ReplacedBy and Warning: the request named a removed model.
	ReplacedBy string `json:"replaced_by,omitempty"`
	Warning    string `json:"warning,omitempty"`
}

func (s *Server) generateSpeech(ctx context.Context, req *sdk.CallToolRequest, in speechIn) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	if in.Speed != 0 && (in.Speed < 0.25 || in.Speed > 4) {
		return nil, nil, errors.New("invalid_request: speed must be between 0.25 and 4")
	}
	mode := strings.ToLower(strings.TrimSpace(in.Output))
	if mode == "" {
		mode = s.o.Backend.Settings().API.Output()
	}
	if mode != "inline" && mode != "file" && mode != "both" {
		return nil, nil, errors.New("invalid_request: output is inline, file or both")
	}
	sp, err := s.o.API.StartSpeech(ctx, api.SpeechParams{Text: in.Text, Model: in.Model, Voice: in.Voice, Instructions: in.Instructions,
		Language: in.Language, Speed: in.Speed, Seed: in.Seed, Duration: in.Duration, Steps: in.Steps, Normalize: in.Normalize, Client: c.id})
	if err != nil {
		return nil, nil, toolError(err)
	}
	res, err := sp.Wait(ctx, nil)
	if err != nil {
		return nil, nil, toolError(err)
	}
	out := speechOut{TakeID: res.Take.ID, Model: sp.Model.ID, Voice: sp.VoiceName, AudioSeconds: round2(res.Take.AudioSeconds),
		GenSeconds: round2(res.Take.GenSeconds), SampleRate: res.Take.SampleRate, Seed: res.Take.Seed,
		AudioURL: "/api/takes/" + res.Take.ID + "/audio", Ignored: sp.Ignored}
	if r := sp.Replaced; r != nil {
		out.ReplacedBy, out.Warning = r.ReplacedBy, r.Warning
	}
	var extra []sdk.Content
	if mode == "inline" || mode == "both" {
		data, err := os.ReadFile(res.Output)
		if err != nil {
			return nil, nil, err
		}
		if len(data) > maxInline {
			out.Note = "The take is too long to send inline: it was written to a file."
			mode = "file"
		} else {
			extra = append(extra, &sdk.AudioContent{Data: data, MIMEType: "audio/wav"})
		}
	}
	if mode == "file" || mode == "both" {
		path, err := s.writeFile(res.Output, in.Filename, res.Take.ID)
		if err != nil {
			return nil, nil, err
		}
		out.File = path
	}
	return result(out, extra...)
}

// writeFile copies a take into the sandbox under a safe name.
func (s *Server) writeFile(src, name, id string) (string, error) {
	dir := s.sandbox()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" || strings.HasPrefix(name, ".") {
		name = id + ".wav"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".wav") {
		name += ".wav"
	}
	dst := filepath.Join(dir, name)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".audiosmith-*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return dst, nil
}

type cloneIn struct {
	Name        string `json:"name" jsonschema:"Name of the new voice."`
	Path        string `json:"path,omitempty" jsonschema:"Absolute path of a recording on this Mac (5–15 s of clean speech is best)."`
	AudioBase64 string `json:"audio_base64,omitempty" jsonschema:"Or the recording itself, base64 (a data: URL works)."`
	Filename    string `json:"filename,omitempty" jsonschema:"The recording's file name, for its format (with audio_base64)."`
	RefText     string `json:"ref_text,omitempty" jsonschema:"What the recording says. Empty lets the engine listen."`
	Language    string `json:"language,omitempty" jsonschema:"BCP-47 code of the speaker's language."`
	Style       string `json:"style,omitempty" jsonschema:"A delivery hint kept with the voice (calm, whisper…)."`
	Consent     bool   `json:"consent" jsonschema:"Confirm that the speaker agreed to having their voice cloned. Required."`
}

func (s *Server) cloneVoice(ctx context.Context, req *sdk.CallToolRequest, in cloneIn) (*sdk.CallToolResult, any, error) {
	// Saving voices is admin, as REST POST /api/voices (security review
	// #100, L7): not for LAN clients with the PIN only.
	c := s.caller(req)
	if err := c.local(); err != nil {
		return nil, nil, err
	}
	if !in.Consent {
		return nil, nil, errors.New("consent_required: clone only voices whose owner agreed; confirm with consent: true")
	}
	path, done, err := s.inputFile(c, in.Path, in.AudioBase64, in.Filename)
	if err != nil {
		return nil, nil, err
	}
	defer done()
	v, err := s.o.API.CloneVoice(ctx, api.CloneParams{Name: in.Name, File: path, RefText: in.RefText, Language: in.Language, Style: in.Style})
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"voice": voiceOut(v), "note": "Speak with it: generate_speech with voice " + v.ID + " (or its name)."})
}

type designIn struct {
	Description string `json:"description" jsonschema:"The voice in words: gender, age, pitch, accent or dialect, style (a warm older British man; 女，青年，四川话)."`
	Name        string `json:"name,omitempty" jsonschema:"Name of the new voice; empty: from the description."`
	Language    string `json:"language,omitempty" jsonschema:"BCP-47 code it will speak."`
	Seed        int64  `json:"seed,omitempty" jsonschema:"Seed of its identity; empty: a new one."`
	Model       string `json:"model,omitempty" jsonschema:"A model that designs voices (see list_models); empty: the first."`
}

func (s *Server) designVoice(ctx context.Context, req *sdk.CallToolRequest, in designIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	d, err := s.o.API.DesignVoice(ctx, api.DesignParams{Name: in.Name, Description: in.Description, Language: in.Language, Seed: in.Seed, Model: in.Model})
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"voice": voiceOut(d.Voice), "model": d.Model.ID, "unmatched": d.Unmatched})
}

type describeIn struct {
	Description string `json:"description" jsonschema:"A voice description to read, without saving anything."`
}

func (s *Server) describeVoice(ctx context.Context, req *sdk.CallToolRequest, in describeIn) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	if strings.TrimSpace(in.Description) == "" {
		return nil, nil, errors.New("invalid_request: describe the voice")
	}
	r := vd.Parse(in.Description)
	matched := []map[string]any{}
	for _, m := range r.Matched {
		if m.Applied {
			matched = append(matched, map[string]any{"category": m.Category, "value": m.Value, "phrase": m.Phrase})
		}
	}
	var un []string
	for _, t := range r.Unmatched {
		un = append(un, t.Text)
	}
	return result(map[string]any{"attributes": r.Attrs.Canonical(), "matched": matched, "unmatched": un,
		"sentence": vd.Sentence(r.Attrs, vd.TextLang(in.Description))})
}

type transcribeIn struct {
	Path        string `json:"path,omitempty" jsonschema:"Absolute path of an audio or video file on this Mac."`
	AudioBase64 string `json:"audio_base64,omitempty" jsonschema:"Or the audio itself, base64."`
	Filename    string `json:"filename,omitempty" jsonschema:"Its file name, for its format (with audio_base64)."`
	Model       string `json:"model,omitempty" jsonschema:"A speech-to-text model from list_models; empty: AudioSmith's default."`
	Language    string `json:"language,omitempty" jsonschema:"BCP-47 code; empty detects it."`
	Prompt      string `json:"prompt,omitempty" jsonschema:"Names and terms to expect."`
	Timestamps  bool   `json:"timestamps,omitempty" jsonschema:"Return sentences with their times (and words)."`
	Speakers    bool   `json:"speakers,omitempty" jsonschema:"Tell who says what (a slower model): segments get speaker labels S1, S2…"`
	Mode        string `json:"mode,omitempty" jsonschema:"accurate (default) or fast: Transcribe's two modes (Settings → Transcribe names their models); ignored when model is given."`
}

func (s *Server) transcribe(ctx context.Context, req *sdk.CallToolRequest, in transcribeIn) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	path, done, err := s.inputFile(c, in.Path, in.AudioBase64, in.Filename)
	if err != nil {
		return nil, nil, err
	}
	defer done()
	title := filepath.Base(firstNonEmpty(in.Filename, in.Path, "audio")) + " (MCP)"
	model := in.Model
	switch strings.ToLower(strings.TrimSpace(in.Mode)) {
	case "", "accurate":
	case "fast":
		if model == "" {
			model = "gpt-4o-mini-transcribe" // the API's alias of Transcribe's Fast mode
		}
	default:
		return nil, nil, errors.New("invalid_request: mode is accurate or fast")
	}
	tr, err := s.o.API.Transcribe(ctx, api.TranscribeParams{File: path, Title: title, Model: model, Language: in.Language,
		Prompt: in.Prompt, Words: in.Timestamps, Speakers: in.Speakers})
	if err != nil {
		return nil, nil, toolError(err)
	}
	t := tr.Transcript
	out := map[string]any{"text": t.Text(), "language": t.Language, "duration": t.Duration, "transcript_id": t.ID}
	if r := tr.Replaced; r != nil {
		out["replaced_by"], out["warning"] = r.ReplacedBy, r.Warning
	}
	if in.Speakers {
		out["text"] = services.PlainText(t) // "S1: …" paragraphs
	}
	if in.Timestamps || in.Speakers {
		var segs []map[string]any
		for _, sg := range t.Segments {
			seg := map[string]any{"start": sg.Start, "end": sg.End, "text": sg.Text}
			if sg.Speaker != "" {
				seg["speaker"] = sg.Speaker
				if n := t.Speakers[sg.Speaker]; n != "" {
					seg["speaker_name"] = n
				}
			}
			if len(sg.Words) > 0 {
				var ws []map[string]any
				for _, w := range sg.Words {
					ws = append(ws, map[string]any{"word": w.Text, "start": w.Start, "end": w.End})
				}
				seg["words"] = ws
			}
			segs = append(segs, seg)
		}
		out["segments"] = segs
	}
	return result(out)
}

type listVoicesIn struct {
	Search string `json:"search,omitempty" jsonschema:"Filter by name, style, description or tags."`
}

func (s *Server) listVoices(ctx context.Context, req *sdk.CallToolRequest, in listVoicesIn) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	vs, err := s.o.Backend.Voices(ctx, core.VoiceQuery{Search: in.Search})
	if err != nil {
		return nil, nil, toolError(err)
	}
	saved := []map[string]any{}
	for _, v := range vs {
		saved = append(saved, voiceOut(v))
	}
	out := map[string]any{"saved": saved,
		"openai_names": api.OpenAIVoiceNames(),
		"note":         "Built-in speakers of each model are in list_models (voices)."}
	if b, ok := s.o.Backend.Settings().API.Binding(c.id); ok && b.Voice != "" {
		out["your_voice"] = b.Voice
	}
	return result(out)
}

func voiceOut(v core.Voice) map[string]any {
	m := map[string]any{"id": v.ID, "name": v.Name, "kind": string(v.Kind)}
	if v.Language != "" {
		m["language"] = v.Language
	}
	if v.Style != "" {
		m["style"] = v.Style
	}
	if v.Design != "" {
		m["design"] = v.Design
	}
	if v.Model != "" {
		m["model"] = v.Model
	}
	if v.Verified() {
		m["verified"] = true
	}
	return m
}

type listModelsIn struct {
	Task string `json:"task,omitempty" jsonschema:"tts (speech) or stt (speech to text); empty: both."`
}

func (s *Server) listModels(ctx context.Context, req *sdk.CallToolRequest, in listModelsIn) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	models, err := s.o.Backend.Models(ctx)
	if err != nil {
		return nil, nil, toolError(err)
	}
	var out []map[string]any
	for _, m := range models {
		e := m.Entry
		if e.Hidden {
			continue
		}
		task := ""
		for _, t := range e.Tasks {
			if t == "tts" || t == "stt" {
				task = string(t)
			}
		}
		if task == "" || (in.Task != "" && in.Task != task) {
			continue
		}
		caps := []string{}
		for _, c := range e.Capabilities {
			caps = append(caps, string(c))
		}
		tags := []string{}
		for _, t := range e.DisplayTags(0) {
			tags = append(tags, string(t))
		}
		desc := e.Summary
		if desc == "" {
			desc = e.About
		}
		item := map[string]any{"id": e.ID, "label": e.Label, "display_name": e.Name, "ui_name": e.DisplayName(), "description": desc, "tags": tags,
			"task": task, "capabilities": caps, "downloaded": m.Downloaded, "licence": e.Licence.Name, "commercial_use": e.Licence.Commercial}
		if m.LicenceNeeded {
			item["licence_to_accept"] = true
		}
		if e.NeedsReference { // speaks cloned voices only
			item["needs_reference"] = true
		}
		var speakers []string
		for _, v := range e.Voices {
			speakers = append(speakers, v.ID)
		}
		if len(speakers) > 0 {
			item["voices"] = speakers
		}
		out = append(out, item)
	}
	return result(map[string]any{"models": out})
}

type listLanguagesIn struct {
	Model string `json:"model,omitempty" jsonschema:"A model id; empty: every speech model (models that speak more than 100 languages are named once, with their count)."`
	Query string `json:"query,omitempty" jsonschema:"Only languages whose code or name contains this (zh, Chinese, 中文…)."`
}

// manyLanguages is the language count past which a model is one of the
// "speaks almost every language" models: list_languages without a model
// or query names them once instead of on hundreds of rows (OmniVoice made
// the answer 650 rows, Mac test #136).
const manyLanguages = 100

func (s *Server) listLanguages(ctx context.Context, req *sdk.CallToolRequest, in listLanguagesIn) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	langs, err := s.o.API.Languages(in.Model)
	if err != nil {
		return nil, nil, toolError(err)
	}
	if q := strings.ToLower(strings.TrimSpace(in.Query)); q != "" {
		langs = slices.DeleteFunc(langs, func(l api.LanguageJSON) bool {
			return !strings.Contains(strings.ToLower(l.Code+" "+l.Name+" "+l.Native), q)
		})
		return result(map[string]any{"count": len(langs), "languages": langs})
	}
	if in.Model != "" {
		return result(map[string]any{"count": len(langs), "languages": langs})
	}
	return result(compactLanguages(langs))
}

// compactLanguages lists every speech model's languages without the
// models that speak more than manyLanguages: those come once, by count.
func compactLanguages(langs []api.LanguageJSON) map[string]any {
	count := map[string]int{}
	for _, l := range langs {
		for _, m := range l.Models {
			count[m]++
		}
	}
	var many []map[string]any
	for m, n := range count {
		if n > manyLanguages {
			many = append(many, map[string]any{"model": m, "languages": n})
		}
	}
	if len(many) == 0 {
		return map[string]any{"count": len(langs), "languages": langs}
	}
	slices.SortFunc(many, func(a, b map[string]any) int { return strings.Compare(a["model"].(string), b["model"].(string)) })
	var out []api.LanguageJSON
	for _, l := range langs {
		var ms []string
		for _, m := range l.Models {
			if count[m] <= manyLanguages {
				ms = append(ms, m)
			}
		}
		if len(ms) > 0 {
			l.Models = ms
			out = append(out, l)
		}
	}
	return map[string]any{"count": len(out), "languages": out, "multilingual_models": many,
		"note": "multilingual_models speak these languages too and many more: list_languages with their model (or a query) lists theirs."}
}

func (s *Server) checkHealth(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	return result(s.o.API.Health())
}

func (s *Server) addTools() {
	sdk.AddTool(s.srv, &sdk.Tool{Name: "generate_speech", Title: "Generate speech",
		Description: "Speak text with a voice on this Mac. Returns the audio (WAV) inline and/or as a file, and the take's details. Only to hear what a voice sounds like, preview_voice is instant."}, s.generateSpeech)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "clone_voice", Title: "Clone a voice",
		Description: "Save a voice cloned from a recording (a path on this Mac, or base64). Only with the speaker's consent."}, s.cloneVoice)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "design_voice", Title: "Design a voice",
		Description: "Save a new voice made from a description (gender, age, pitch, accent or dialect, style)."}, s.designVoice)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "describe_voice", Title: "Read a voice description",
		Description: "Show how AudioSmith reads a voice description (attributes and words it cannot map), without saving anything."}, s.describeVoice)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "transcribe", Title: "Transcribe",
		Description: "Turn speech in an audio or video file into text, optionally with times."}, s.transcribe)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "list_voices", Title: "List voices",
		Description: "The saved voices, and OpenAI voice names that map to built-in speakers."}, s.listVoices)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "list_models", Title: "List models",
		Description: "Speech and speech-to-text models: their friendly names (display_name in English; ui_name as AudioSmith's UI shows it, which is what the user knows: say it, pass the id), a description and tags, what they can do, their built-in voices, whether they are downloaded, their licence, and whether they speak cloned voices only (needs_reference)."}, s.listModels)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "list_languages", Title: "List languages",
		Description: "Languages a model speaks (or every speech model)."}, s.listLanguages)
	sdk.AddTool(s.srv, &sdk.Tool{Name: "check_health", Title: "Check health",
		Description: "Whether AudioSmith's engine is ready, starting or down, and its device."}, s.checkHealth)
	s.addStudioTools() // thread #35: studio.go
	s.addKitTools()    // thread #135: kit.go, kit_more.go
}
