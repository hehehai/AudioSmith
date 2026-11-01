package mcp

// The toolkit's small tools (thread #135): design candidates (Design's
// three candidates), convert (Developers → Convert: a recording said
// again in another voice), model downloads, the watermark check, the
// file probe, Direction, Rate fit and the defaults agents need. They
// build on the API's operations; split from kit.go.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

type candidatesIn struct {
	Description string `json:"description" jsonschema:"The voice in words: gender, age, pitch, accent or dialect, style."`
	Text        string `json:"text,omitempty" jsonschema:"The line each candidate says; empty: a short line in the voice's language."`
	Language    string `json:"language,omitempty" jsonschema:"BCP-47 code it speaks; empty: from the description."`
	Model       string `json:"model,omitempty" jsonschema:"A model that designs voices (list_models, capability design); empty: the first."`
	Count       int    `json:"count,omitempty" jsonschema:"How many candidates, 1–3 (default 3), each with its own seed."`
	Output      string `json:"output,omitempty" jsonschema:"inline, file or both (empty: AudioSmith's setting); file gives each candidate a WAV in the agents' folder."`
}

// candidateLine is what a candidate says when the call gives no text.
func candidateLine(lang string) string {
	if strings.HasPrefix(lang, "zh") {
		return "你好，很高兴认识你。这是我的声音，听起来怎么样？"
	}
	return "Hello, it's nice to meet you. This is my voice: how does it sound?"
}

func (s *Server) designCandidates(ctx context.Context, req *sdk.CallToolRequest, in candidatesIn) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	if err := c.local(); err != nil {
		return nil, nil, err
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		return nil, nil, errors.New("invalid_request: describe the voice")
	}
	n := in.Count
	if n == 0 {
		n = 3
	}
	if n < 1 || n > 3 {
		return nil, nil, errors.New("invalid_request: count is 1–3")
	}
	mode, err := s.outputMode(in.Output)
	if err != nil {
		return nil, nil, err
	}
	model := strings.TrimSpace(in.Model)
	if model == "" {
		for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
			if e.Has(catalog.CapDesign) {
				model = e.ID
				break
			}
		}
	}
	lang := firstNonEmpty(in.Language, vd.Parse(desc).Attrs.Canonical().Language(), vd.TextLang(desc))
	text := firstNonEmpty(in.Text, candidateLine(lang))
	var cands []map[string]any
	var extra []sdk.Content
	for i := range n {
		seed := vd.NewSeed()
		sp, err := s.o.API.StartSpeech(ctx, api.SpeechParams{Text: text, Model: model, Voice: "default", Instructions: desc,
			Language: in.Language, Seed: seed, Client: c.id})
		if err != nil {
			return nil, nil, toolError(err)
		}
		if !sp.Model.Has(catalog.CapDesign) {
			return nil, nil, fmt.Errorf("%s: %s does not design voices: pick a model with the capability design (list_models)", api.CodeUnsupportedModel, sp.Model.ID)
		}
		res, err := sp.Wait(ctx, nil)
		if err != nil {
			return nil, nil, toolError(err)
		}
		ex, file, note, err := s.audio(mode, nil, res.Output, "audio/wav", fmt.Sprintf("candidate-%d-%d.wav", i+1, seed))
		if err != nil {
			return nil, nil, err
		}
		extra = append(extra, ex...)
		cand := map[string]any{"candidate": i + 1, "seed": seed, "take_id": res.Take.ID, "model": sp.Model.ID,
			"audio_seconds": round2(res.Take.AudioSeconds), "file": file}
		if note != "" {
			cand["note"] = note
		}
		cands = append(cands, cand)
	}
	return result(map[string]any{"description": desc, "text": text, "candidates": cands,
		"next": "Let the user listen and pick one, then design_voice with this description, a name and the chosen seed (and the same model) to save it."}, extra...)
}

type convertIn struct {
	Path        string `json:"path" jsonschema:"Absolute path of the recording (audio or video) to say again."`
	Voice       string `json:"voice" jsonschema:"The voice to say it in: a saved voice (id or name), a built-in speaker, or a Discover voice saved with use_gallery_voice."`
	Model       string `json:"model,omitempty" jsonschema:"A speech model that speaks the voice (list_models); empty: the default for it."`
	Language    string `json:"language,omitempty" jsonschema:"BCP-47 code of the recording; empty: detected."`
	MatchLength bool   `json:"match_length,omitempty" jsonschema:"Aim at the recording's length (models with a target duration, such as OmniVoice)."`
	Output      string `json:"output,omitempty" jsonschema:"inline, file or both; empty: AudioSmith's setting."`
	Filename    string `json:"filename,omitempty" jsonschema:"The WAV's name in file mode (no folders)."`
}

func (s *Server) convertVoice(ctx context.Context, req *sdk.CallToolRequest, in convertIn) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	if err := c.local(); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(in.Voice) == "" {
		return nil, nil, errors.New("invalid_request: voice is empty: list_voices lists the saved voices")
	}
	mode, err := s.outputMode(in.Output)
	if err != nil {
		return nil, nil, err
	}
	path, err := s.readable(c, in.Path)
	if err != nil {
		return nil, nil, err
	}
	tr, err := s.o.API.Transcribe(ctx, api.TranscribeParams{File: path, Title: filepath.Base(path) + " (MCP convert)", Language: in.Language})
	if err != nil {
		return nil, nil, toolError(err)
	}
	text := strings.TrimSpace(tr.Transcript.Text())
	if text == "" {
		return nil, nil, errors.New("nothing_to_say: no speech was heard in the recording")
	}
	p := api.SpeechParams{Text: text, Model: in.Model, Voice: in.Voice, Language: firstNonEmpty(in.Language, tr.Transcript.Language), Client: c.id}
	if in.MatchLength {
		p.Duration = tr.Transcript.Duration
	}
	sp, err := s.o.API.StartSpeech(ctx, p)
	if err != nil {
		return nil, nil, toolError(err)
	}
	res, err := sp.Wait(ctx, nil)
	if err != nil {
		return nil, nil, toolError(err)
	}
	name := firstNonEmpty(in.Filename, res.Take.ID+".wav")
	if !strings.HasSuffix(strings.ToLower(name), ".wav") {
		name += ".wav"
	}
	extra, file, note, err := s.audio(mode, nil, res.Output, "audio/wav", name)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]any{"text": text, "language": tr.Transcript.Language, "source_seconds": round2(tr.Transcript.Duration),
		"take_id": res.Take.ID, "model": sp.Model.ID, "voice": sp.VoiceName, "audio_seconds": round2(res.Take.AudioSeconds), "file": file}
	if note != "" {
		out["note"] = note
	}
	if len(sp.Ignored) > 0 {
		out["ignored"] = sp.Ignored // duration on a model without a target length
	}
	return result(out, extra...)
}

type downloadIn struct {
	Model   string `json:"model" jsonschema:"The model's id (list_models)."`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"true only after the user agreed to the download (models are 0.1–25 GB); without it nothing is downloaded (confirm_required)."`
}

func (s *Server) downloadModel(ctx context.Context, req *sdk.CallToolRequest, in downloadIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	id := strings.TrimSpace(in.Model)
	e, ok := catalog.Builtin().ByID(id)
	if !in.Confirm {
		size := ""
		if ok && e.Size > 0 {
			size = fmt.Sprintf(" (%.1f GB)", float64(e.Size)/1e9)
		}
		return nil, nil, fmt.Errorf("%s: downloading %s%s takes disk space and time: ask the user, then call again with confirm: true", api.CodeConfirmRequired, id, size)
	}
	job, err := s.o.API.DownloadModel(ctx, id)
	if err != nil {
		return nil, nil, toolError(err)
	}
	out := map[string]any{"model": id, "job_id": job, "next": "get_job with job_id " + job + " and wait_seconds until done; list_models then says downloaded."}
	if ok && e.Licence.Name != "" {
		out["licence"] = e.Licence.Name
	}
	return result(out)
}

func (s *Server) detectWatermark(ctx context.Context, req *sdk.CallToolRequest, in api.WatermarkRequest) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	if err := c.local(); err != nil {
		return nil, nil, err
	}
	path, err := s.readable(c, in.Path)
	if err != nil {
		return nil, nil, err
	}
	rep, err := s.o.API.DetectWatermark(ctx, path)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(rep)
}

func (s *Server) probeMedia(ctx context.Context, req *sdk.CallToolRequest, in api.ProbeRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	p, err := s.o.API.Probe(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(p)
}

func (s *Server) directPerformance(ctx context.Context, req *sdk.CallToolRequest, in api.DirectRequest) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	d, err := s.o.API.Direct(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(d)
}

func (s *Server) checkLineFit(ctx context.Context, req *sdk.CallToolRequest, in api.FitRequest) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	f, err := s.o.API.Fit(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(f)
}

func (s *Server) getSettings(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
	c := s.caller(req)
	st := s.o.Backend.Settings()
	id := func(model string) string {
		if e, ok := catalog.Builtin().ByID(model); ok {
			return e.ID
		}
		if e, ok := catalog.Builtin().ByRepo(model); ok {
			return e.ID
		}
		return model
	}
	out := map[string]any{
		"speech_model":  id(st.Studio.Model),
		"transcribe":    map[string]any{"accurate_model": id(st.Transcribe.AccurateModel), "fast_model": id(st.Transcribe.FastModel), "language": st.Transcribe.Language},
		"dubbing":       map[string]any{"targets": st.Dubbing.Targets, "model": id(st.Dubbing.Model), "link_import": st.Dubbing.URLImport && st.Dubbing.YtDlp != ""},
		"output":        st.API.Output(),
		"agents_folder": s.sandbox(),
		"watermark":     !st.Privacy.NoWatermark,
		"note":          "Read only: the user changes settings in AudioSmith → Settings. The pronunciation dictionary (Settings → Pronunciation) applies to every take by itself.",
	}
	if b, ok := st.API.Binding(c.id); ok && b.Voice != "" {
		out["your_voice"] = b.Voice
	}
	return result(out)
}

func (s *Server) addMoreTools() {
	add := func(name, title, desc string) *sdk.Tool {
		return &sdk.Tool{Name: name, Title: title, Description: desc}
	}
	sdk.AddTool(s.srv, add("design_voice_candidates", "Hear designed candidates",
		"Design's candidates: up to three short takes of a described voice, each with its own seed, to listen to before saving one with design_voice (same description and seed). Nothing is saved to My voices."), s.designCandidates)
	sdk.AddTool(s.srv, add("convert_voice", "Say a recording in another voice",
		"Developers → Convert: transcribe a recording, then say the same words in another voice (optionally matching its length). Returns the text and the new audio."), s.convertVoice)
	sdk.AddTool(s.srv, add("download_model", "Download a model",
		"Download a model of list_models (needs confirm: true after the user agreed). Returns a job_id for get_job. Licences are accepted by the user in AudioSmith → Models."), s.downloadModel)
	sdk.AddTool(s.srv, add("detect_watermark", "Check for the watermark",
		"Check an audio file for AudioSmith's invisible watermark (whether this Mac's AudioSmith made it)."), s.detectWatermark)
	sdk.AddTool(s.srv, add("probe_media", "Probe a media file",
		"What an audio or video file holds: duration, streams, codecs, sample rate, channels."), s.probeMedia)
	sdk.AddTool(s.srv, add("direct_performance", "Read a performance note",
		"Direction: turn a performance note (\"whispered, getting angry\") into emotions, an instruction for instruct models, and rate, pitch and energy hints."), s.directPerformance)
	sdk.AddTool(s.srv, add("check_line_fit", "Will a line fit its time?",
		"Rate fit: predict how long a line takes to say against a slot in seconds (ok, tight, overflow), and optionally get a shorter version from the writing help."), s.checkLineFit)
	sdk.AddTool(s.srv, add("get_settings", "Read AudioSmith's defaults",
		"The defaults that shape your calls (read only): default speech and transcription models, dubbing targets and whether link import is on, the output mode, the agents' folder, your own voice, the watermark."), s.getSettings)
}
