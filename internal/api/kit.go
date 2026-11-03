package api

// The toolkit (thread #135): what agents may also do beyond Studio — the
// Voice isolator, Assets, the sample projects, voice management (rename,
// duplicate, .asvoice export and import, preview clips), the reference
// check before cloning and Stories' story draft. The app implements
// Toolkit over the services its pages use; this file has the operations
// REST (kit_http.go) and MCP (internal/mcp/kit*.go) both call, after
// checking their arguments as studio_ops.go does. It also holds the
// small tools of /api/tools (probe, direction, rate fit, the watermark
// check, model downloads) as operations, for MCP.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// Toolkit is implemented by backends that offer the toolkit; without it
// its routes and tools answer 501 unsupported. Every method is
// goroutine-safe; arguments are checked (paths resolved, ids not empty).
type Toolkit interface {
	// StartIsolation queues an isolation of a media file (job owner
	// "isolate:<id>"); core.ErrNotDownloaded without the separator.
	StartIsolation(ctx context.Context, path string) (IsolationJSON, error)
	// Isolations lists the isolations, newest first; Isolation reads one.
	Isolations(ctx context.Context) ([]IsolationJSON, error)
	Isolation(ctx context.Context, id string) (IsolationJSON, error)
	DeleteIsolation(ctx context.Context, id string) error

	// Assets lists every output and imported file, newest first
	// (q.Limit and q.Offset already bounded).
	Assets(ctx context.Context, q AssetQuery) ([]AssetJSON, error)

	// Samples lists the sample projects; OpenSample copies one into the
	// library (a new copy each time) and says where.
	Samples(ctx context.Context) ([]SampleJSON, error)
	OpenSample(ctx context.Context, id string) (SampleOpened, error)

	// UpdateVoice, DuplicateVoice, ExportVoice (req.Dir checked) and
	// ImportVoice (path checked) manage the voice library as My voices'
	// menu does; VoicePreview is a voice's preview clip.
	UpdateVoice(ctx context.Context, req VoiceUpdate) (core.Voice, error)
	DuplicateVoice(ctx context.Context, req VoiceDuplicate) (core.Voice, error)
	ExportVoice(ctx context.Context, req VoiceExportRequest) (ExportJSON, error)
	ImportVoice(ctx context.Context, path string) (VoiceImported, error)
	VoicePreview(ctx context.Context, q VoicePreviewQuery) (VoicePreview, error)

	// CheckReference grades a recording (path checked) as a clone
	// reference, as Quick clone's checker does.
	CheckReference(ctx context.Context, req RefCheckRequest) (RefCheckJSON, error)

	// WriteStory drafts a story with the writing help's language model
	// (llm_unavailable without one) and, if asked, makes its project.
	WriteStory(ctx context.Context, req StoryDraftRequest) (StoryDraftJSON, error)
}

// Toolkit is the backend's Toolkit, or an error.
func (s *Server) Toolkit() (Toolkit, error) {
	if k, ok := s.b.(Toolkit); ok {
		return k, nil
	}
	return nil, errNoStudio()
}

// --- Voice isolator -----------------------------------------------------------------

// StartIsolation separates a file's voices from its music and effects.
func (s *Server) StartIsolation(ctx context.Context, req IsolateRequest) (IsolationJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return IsolationJSON{}, err
	}
	if req.Path, err = InputFile("path", req.Path); err != nil {
		return IsolationJSON{}, err
	}
	if err := s.engineGate(); err != nil {
		return IsolationJSON{}, err
	}
	return k.StartIsolation(ctx, req.Path)
}

// Isolations lists the isolations.
func (s *Server) Isolations(ctx context.Context) ([]IsolationJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return nil, err
	}
	return k.Isolations(ctx)
}

// Isolation reads one isolation.
func (s *Server) Isolation(ctx context.Context, id string) (IsolationJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return IsolationJSON{}, err
	}
	if strings.TrimSpace(id) == "" {
		return IsolationJSON{}, errInvalid("isolation_id", "isolation_id is empty.")
	}
	return k.Isolation(ctx, strings.TrimSpace(id))
}

// --- Assets ------------------------------------------------------------------------------

// Assets lists the outputs and imported files.
func (s *Server) Assets(ctx context.Context, q AssetQuery) ([]AssetJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return nil, err
	}
	q.Kind = strings.ToLower(strings.TrimSpace(q.Kind))
	if err := checkFormat("kind", q.Kind, AssetKinds...); err != nil {
		return nil, err
	}
	q.Limit = min(max(q.Limit, 0), 500)
	if q.Limit == 0 {
		q.Limit = 50
	}
	q.Offset = min(max(q.Offset, 0), MaxOffset) // as REST (code review 1103 M2)
	return k.Assets(ctx, q)
}

// --- Samples -------------------------------------------------------------------------------

// Samples lists the sample projects.
func (s *Server) Samples(ctx context.Context) ([]SampleJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return nil, err
	}
	return k.Samples(ctx)
}

// OpenSample copies a sample into the library.
func (s *Server) OpenSample(ctx context.Context, id string) (SampleOpened, error) {
	k, err := s.Toolkit()
	if err != nil {
		return SampleOpened{}, err
	}
	if strings.TrimSpace(id) == "" {
		return SampleOpened{}, errInvalid("sample_id", "sample_id is empty: list_samples lists them.")
	}
	return k.OpenSample(ctx, strings.TrimSpace(id))
}

// --- voices --------------------------------------------------------------------------------

// UpdateVoice changes a saved voice's name, style, language, tags or
// reference text.
func (s *Server) UpdateVoice(ctx context.Context, req VoiceUpdate) (core.Voice, error) {
	k, err := s.Toolkit()
	if err != nil {
		return core.Voice{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return core.Voice{}, errInvalid("voice_id", "voice_id is empty.")
	}
	if req.Name == "" && req.Style == "" && req.Language == "" && req.RefText == "" && req.Tags == nil {
		return core.Voice{}, errInvalid("name", "Nothing to change: give name, style, language, ref_text or tags.")
	}
	if req.Name != "" && core.CleanName(req.Name) == "" {
		return core.Voice{}, errInvalid("name", "The name is empty once cleaned.")
	}
	return k.UpdateVoice(ctx, req)
}

// DuplicateVoice copies a saved voice.
func (s *Server) DuplicateVoice(ctx context.Context, req VoiceDuplicate) (core.Voice, error) {
	k, err := s.Toolkit()
	if err != nil {
		return core.Voice{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return core.Voice{}, errInvalid("voice_id", "voice_id is empty.")
	}
	return k.DuplicateVoice(ctx, req)
}

// ExportVoice writes a saved voice as an .asvoice file.
func (s *Server) ExportVoice(ctx context.Context, req VoiceExportRequest) (ExportJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return ExportJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return ExportJSON{}, errInvalid("voice_id", "voice_id is empty.")
	}
	if req.Dir, err = s.OutputFolder(req.Dir); err != nil {
		return ExportJSON{}, err
	}
	return k.ExportVoice(ctx, req)
}

// ImportVoice reads an .asvoice (or .ovsvoice) file into My voices.
func (s *Server) ImportVoice(ctx context.Context, req VoiceImportRequest) (VoiceImported, error) {
	k, err := s.Toolkit()
	if err != nil {
		return VoiceImported{}, err
	}
	if req.Path, err = InputFile("path", req.Path); err != nil {
		return VoiceImported{}, err
	}
	switch strings.ToLower(req.Path[strings.LastIndex(req.Path, ".")+1:]) {
	case "asvoice", "ovsvoice":
	default:
		return VoiceImported{}, &Error{Status: http.StatusBadRequest, Code: CodeUnsupportedFormat, Param: "path",
			Message: "A voice file is an .asvoice (or VoiceStudio .ovsvoice) file."}
	}
	return k.ImportVoice(ctx, req.Path)
}

// VoicePreview is a voice's preview clip: a saved voice's, a Discover
// voice's or a built-in speaker's.
func (s *Server) VoicePreview(ctx context.Context, q VoicePreviewQuery) (VoicePreview, error) {
	k, err := s.Toolkit()
	if err != nil {
		return VoicePreview{}, err
	}
	n := 0
	for _, v := range []string{q.VoiceID, q.Gallery, q.Model} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	if n != 1 {
		return VoicePreview{}, errInvalid("voice_id", "Give one of voice_id, gallery_id, or model with speaker.")
	}
	if q.Model != "" && strings.TrimSpace(q.Speaker) == "" {
		return VoicePreview{}, errInvalid("speaker", "speaker is empty: list_models lists each model's voices.")
	}
	return k.VoicePreview(ctx, q)
}

// CheckReference grades a recording as a clone reference.
func (s *Server) CheckReference(ctx context.Context, req RefCheckRequest) (RefCheckJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return RefCheckJSON{}, err
	}
	if req.Path, err = InputFile("path", req.Path); err != nil {
		return RefCheckJSON{}, err
	}
	return k.CheckReference(ctx, req)
}

// WriteStory drafts a story with the writing help.
func (s *Server) WriteStory(ctx context.Context, req StoryDraftRequest) (StoryDraftJSON, error) {
	k, err := s.Toolkit()
	if err != nil {
		return StoryDraftJSON{}, err
	}
	if strings.TrimSpace(req.Description) == "" {
		return StoryDraftJSON{}, errInvalid("description", "description is empty: say what the story is about.")
	}
	if len([]rune(req.Description)) > 2000 {
		return StoryDraftJSON{}, errInvalid("description", "description is longer than 2,000 characters: give a short brief, or write the script yourself and use create_story.")
	}
	return k.WriteStory(ctx, req)
}

// --- small tools --------------------------------------------------------------------------

// Probe describes a media file (the engine's media worker).
func (s *Server) Probe(ctx context.Context, req ProbeRequest) (ProbeJSON, error) {
	p, err := InputFile("path", req.Path)
	if err != nil {
		return ProbeJSON{}, err
	}
	return s.b.Probe(ctx, p)
}

// Direct reads a performance note: emotions, an instruction, rate,
// pitch and energy.
func (s *Server) Direct(ctx context.Context, req DirectRequest) (translate.Direction, error) {
	if strings.TrimSpace(req.Note) == "" {
		return translate.Direction{}, errInvalid("note", "note is empty.")
	}
	return s.b.Direct(ctx, req.Note), nil
}

// Fit predicts whether a line fits a slot (and may shorten it).
func (s *Server) Fit(ctx context.Context, req FitRequest) (FitJSON, error) {
	if strings.TrimSpace(req.Text) == "" || req.Slot <= 0 {
		return FitJSON{}, errInvalid("slot", "text and a slot in seconds (> 0) are needed.")
	}
	res, err := s.b.Fit(ctx, translate.FitRequest{Text: req.Text, Lang: req.Language, Slot: req.Slot, Speed: req.Speed,
		Source: req.Source, SourceLang: req.SourceLang, Rewrite: req.Rewrite})
	if err != nil {
		var te *translate.Error
		if errors.As(err, &te) {
			return FitJSON{}, errInvalid("", "%v", err)
		}
		return FitJSON{}, err
	}
	out := FitJSON{Fit: res.Fit}
	if res.Rewrite != "" {
		out.Rewrite, out.RewriteFit = res.Rewrite, res.RewriteFit
	}
	return out, nil
}

// DetectWatermark checks a file for AudioSmith's invisible watermark.
func (s *Server) DetectWatermark(ctx context.Context, path string) (WatermarkReport, error) {
	if err := s.engineGate(); err != nil {
		return WatermarkReport{}, err
	}
	res, err := s.b.DetectWatermark(ctx, path)
	if err != nil {
		return WatermarkReport{}, err
	}
	return Report(res), nil
}

// DownloadModel queues a model's download and returns its job id.
func (s *Server) DownloadModel(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if rm, ok := catalog.Builtin().RemovedModel(id); ok {
		return "", &Error{Status: http.StatusGone, Code: CodeModelRemoved, Param: "id",
			Message: rm.Label + " was removed from AudioSmith: download " + rm.ReplacedBy + " instead.",
			Data:    map[string]any{"model": rm.ID, "replaced_by": rm.ReplacedBy}}
	}
	if _, ok := catalog.Builtin().ByID(id); !ok {
		return "", errNotFound(CodeModelNotFound, "There is no model %q: list_models lists them.", id)
	}
	return s.b.DownloadModel(ctx, id)
}
