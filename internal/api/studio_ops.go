package api

// The Studio operations as REST and MCP call them: arguments checked
// (paths, folders, confirmations), then the app's Studio.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

// Projects lists the project library.
func (s *Server) Projects(ctx context.Context, q ProjectQuery) ([]ProjectJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return nil, err
	}
	q.Limit = min(max(q.Limit, 0), 500)
	if q.Limit == 0 {
		q.Limit = 50
	}
	q.Offset = max(q.Offset, 0)
	return st.Projects(ctx, q)
}

// CreateLongform makes a Stories or Audiobook project.
func (s *Server) CreateLongform(ctx context.Context, req LongformCreate) (LongformJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return LongformJSON{}, err
	}
	if req.Kind != "story" && req.Kind != "audiobook" {
		return LongformJSON{}, errInvalid("kind", "kind is story or audiobook.")
	}
	n := 0
	for _, has := range []bool{strings.TrimSpace(req.Path) != "", strings.TrimSpace(req.Text) != "", len(req.Lines) > 0} {
		if has {
			n++
		}
	}
	if n != 1 {
		what := "path or text"
		if req.Kind == "story" {
			what = "path, text or lines"
		}
		return LongformJSON{}, errInvalid("text", "Give one of %s.", what)
	}
	if req.Path != "" {
		if req.Path, err = InputFile("path", req.Path); err != nil {
			return LongformJSON{}, err
		}
	}
	if err := checkFormat("format", req.Format, "wav", "m4b"); err != nil {
		return LongformJSON{}, err
	}
	if req.Kind == "audiobook" && (len(req.Lines) > 0 || req.Split != "") {
		return LongformJSON{}, errInvalid("lines", "lines and split are for stories.")
	}
	if err := checkFormat("split", req.Split, "speakers", "paragraphs", "sentences", "chapters"); err != nil {
		return LongformJSON{}, err
	}
	return st.CreateLongform(ctx, req)
}

// checkFormat checks an enumerated argument ("" passes).
func checkFormat(param, v string, allowed ...string) error {
	if v == "" {
		return nil
	}
	for _, a := range allowed {
		if strings.EqualFold(v, a) {
			return nil
		}
	}
	return errInvalid(param, "%s is one of %s.", param, strings.Join(allowed, ", "))
}

// Longform reads a project.
func (s *Server) Longform(ctx context.Context, id string) (LongformJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return LongformJSON{}, err
	}
	if strings.TrimSpace(id) == "" {
		return LongformJSON{}, errInvalid("project_id", "project_id is empty.")
	}
	return st.Longform(ctx, id)
}

// UpdateLongform changes a project's voices and settings.
func (s *Server) UpdateLongform(ctx context.Context, req LongformUpdate) (LongformJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return LongformJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return LongformJSON{}, errInvalid("project_id", "project_id is empty.")
	}
	if req.Cover != "" {
		if req.Cover, err = InputFile("cover", req.Cover); err != nil {
			return LongformJSON{}, err
		}
		if fi, err := os.Stat(req.Cover); err == nil && fi.Size() > services.MaxCoverBytes {
			return LongformJSON{}, errInvalid("cover", "The cover is larger than 8 MB.")
		}
		switch strings.ToLower(filepath.Ext(req.Cover)) {
		case ".png", ".jpg", ".jpeg":
		default:
			return LongformJSON{}, errInvalid("cover", "The cover is a PNG or JPEG.")
		}
	}
	if err := checkFormat("format", req.Format, "wav", "m4b"); err != nil {
		return LongformJSON{}, err
	}
	if err := checkFormat("loudness", req.Loudness, "acx", "podcast", "off"); err != nil {
		return LongformJSON{}, err
	}
	if req.Speed != 0 && (req.Speed < 0.5 || req.Speed > 2) {
		return LongformJSON{}, errInvalid("speed", "speed is 0.5–2.")
	}
	return st.UpdateLongform(ctx, req)
}

// RenderLongform starts a render.
func (s *Server) RenderLongform(ctx context.Context, req LongformRender) (JobJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return JobJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return JobJSON{}, errInvalid("project_id", "project_id is empty.")
	}
	if err := s.engineGate(); err != nil {
		return JobJSON{}, err
	}
	return st.RenderLongform(ctx, req)
}

// ExportLongform saves a finished render's files.
func (s *Server) ExportLongform(ctx context.Context, req LongformExport) (ExportJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return ExportJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" && strings.TrimSpace(req.Render) == "" {
		return ExportJSON{}, errInvalid("project_id", "project_id is empty.")
	}
	if err := checkFormat("format", req.Format, "m4b", "wav", "mp3", "opus", "chapters", "cues", "stems"); err != nil {
		return ExportJSON{}, err
	}
	if req.Bits != 0 && req.Bits != 16 && req.Bits != 24 && req.Bits != 32 {
		return ExportJSON{}, errInvalid("bits", "bits is 16, 24 or 32.")
	}
	if req.Dir, err = s.OutputFolder(req.Dir); err != nil {
		return ExportJSON{}, err
	}
	return st.ExportLongform(ctx, req)
}

// dubThrough are the stages a run may go through.
var dubThrough = []string{"transcript", "translation", "dub", "verify"}

// CreateDub makes a dub of a file and starts it (unless through is none).
func (s *Server) CreateDub(ctx context.Context, req DubCreate) (DubJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return DubJSON{}, err
	}
	switch u := strings.TrimSpace(req.URL); {
	case u != "" && strings.TrimSpace(req.Path) != "":
		return DubJSON{}, errInvalid("url", "Give path or url, not both.")
	case u != "":
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return DubJSON{}, errInvalid("url", "url is an http or https link.")
		}
		req.URL = u
	default:
		if req.Path, err = InputFile("path", req.Path); err != nil {
			return DubJSON{}, err
		}
	}
	if err := checkFormat("through", req.Through, "transcript", "translation", "dub", "none"); err != nil {
		return DubJSON{}, err
	}
	if req.Speakers < 0 || req.Speakers > 20 {
		return DubJSON{}, errInvalid("speakers", "speakers is 1–20, or 0 to detect.")
	}
	if req.Through != "none" {
		if err := s.engineGate(); err != nil {
			return DubJSON{}, err
		}
	}
	return st.CreateDub(ctx, req)
}

// Dub reads a dub.
func (s *Server) Dub(ctx context.Context, req DubQuery) (DubJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return DubJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return DubJSON{}, errInvalid("dub_id", "dub_id is empty.")
	}
	return st.Dub(ctx, req)
}

// RunDub runs a dub's pipeline.
func (s *Server) RunDub(ctx context.Context, req DubRun) (JobJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return JobJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return JobJSON{}, errInvalid("dub_id", "dub_id is empty.")
	}
	if err := checkFormat("through", req.Through, dubThrough...); err != nil {
		return JobJSON{}, err
	}
	if err := s.engineGate(); err != nil {
		return JobJSON{}, err
	}
	return st.RunDub(ctx, req)
}

// EditDub edits a dub's lines and cast.
func (s *Server) EditDub(ctx context.Context, req DubEdit) (DubJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return DubJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return DubJSON{}, errInvalid("dub_id", "dub_id is empty.")
	}
	if len(req.Segments) == 0 && len(req.Cast) == 0 && len(req.Names) == 0 && req.Dialogue == "" && req.DialogueModel == "" {
		return DubJSON{}, errInvalid("segments", "Nothing to change: give segments, cast, names or dialogue.")
	}
	return st.EditDub(ctx, req)
}

// ExportDub queues a dub's export.
func (s *Server) ExportDub(ctx context.Context, req DubExport) (JobJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return JobJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return JobJSON{}, errInvalid("dub_id", "dub_id is empty.")
	}
	if err := checkFormat("format", req.Format, "mp4", "wav", "m4a", "mp3", "srt", "vtt", "ass", "stems", "segments", "burn"); err != nil || req.Format == "" {
		if err == nil {
			err = errInvalid("format", "format is empty.")
		}
		return JobJSON{}, err
	}
	if req.Bits != 0 && req.Bits != 16 && req.Bits != 24 && req.Bits != 32 {
		return JobJSON{}, errInvalid("bits", "bits is 16, 24 or 32.")
	}
	if req.Dir, err = s.OutputFolder(req.Dir); err != nil {
		return JobJSON{}, err
	}
	return st.ExportDub(ctx, req)
}

// BatchDub adds files to the batch queue.
func (s *Server) BatchDub(ctx context.Context, req BatchRequest) (BatchJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return BatchJSON{}, err
	}
	if len(req.Paths) == 0 && strings.TrimSpace(req.Folder) == "" {
		return BatchJSON{}, errInvalid("paths", "Give paths or a folder.")
	}
	if len(req.Paths) > 500 {
		return BatchJSON{}, errInvalid("paths", "At most 500 files at a time.")
	}
	if req.Folder != "" {
		if req.Folder, err = InputDir("folder", req.Folder); err != nil {
			return BatchJSON{}, err
		}
	}
	if err := s.engineGate(); err != nil {
		return BatchJSON{}, err
	}
	return st.BatchDub(ctx, req)
}

// Batch is the batch queue and its watch folder.
func (s *Server) Batch(ctx context.Context) (BatchJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return BatchJSON{}, err
	}
	return st.Batch(ctx)
}

// SearchGallery searches Discover voices.
func (s *Server) SearchGallery(ctx context.Context, q GalleryQuery) (GalleryPage, error) {
	st, err := s.Studio()
	if err != nil {
		return GalleryPage{}, err
	}
	q.Limit = min(max(q.Limit, 0), 100)
	if q.Limit == 0 {
		q.Limit = 20
	}
	q.Offset = max(q.Offset, 0)
	return st.SearchGallery(ctx, q)
}

// UseGalleryVoice saves a gallery voice to the voice library.
func (s *Server) UseGalleryVoice(ctx context.Context, id string) (GalleryUse, error) {
	st, err := s.Studio()
	if err != nil {
		return GalleryUse{}, err
	}
	if strings.TrimSpace(id) == "" {
		return GalleryUse{}, errInvalid("id", "id is empty.")
	}
	return st.UseGalleryVoice(ctx, id)
}

// ExportTake writes a take with the Export audio dialog's options.
func (s *Server) ExportTake(ctx context.Context, req TakeExport) (ExportJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return ExportJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return ExportJSON{}, errInvalid("take_id", "take_id is empty.")
	}
	if err := checkFormat("format", req.Format, "wav", "m4a", "mp3", "flac", "opus"); err != nil {
		return ExportJSON{}, err
	}
	if err := checkFormat("loudness", req.Loudness, "podcast", "speech", "broadcast", "off"); err != nil {
		return ExportJSON{}, err
	}
	switch {
	case req.Bits != 0 && req.Bits != 16 && req.Bits != 24 && req.Bits != 32:
		return ExportJSON{}, errInvalid("bits", "bits is 16, 24 or 32.")
	case req.SampleRate != 0 && (req.SampleRate < 8000 || req.SampleRate > 192000):
		return ExportJSON{}, errInvalid("sample_rate", "sample_rate is 8000–192000 (44100 or 48000), or 0 for the take's.")
	case req.Channels != 0 && req.Channels != 1 && req.Channels != 2:
		return ExportJSON{}, errInvalid("channels", "channels is 1 or 2.")
	case req.Bitrate < 0 || req.Bitrate > 512:
		return ExportJSON{}, errInvalid("bitrate_kbps", "bitrate_kbps is 0–512.")
	}
	if req.Dir, err = s.OutputFolder(req.Dir); err != nil {
		return ExportJSON{}, err
	}
	return st.ExportTake(ctx, req)
}

// ExportTranscript writes a transcript as TXT, SRT, VTT or JSON.
func (s *Server) ExportTranscript(ctx context.Context, req TranscriptExport) (ExportJSON, error) {
	if strings.TrimSpace(req.ID) == "" {
		return ExportJSON{}, errInvalid("transcript_id", "transcript_id is empty.")
	}
	format := strings.ToLower(firstNonEmpty(req.Format, services.ExportTXT))
	if err := checkFormat("format", format, services.ExportFormats...); err != nil {
		return ExportJSON{}, err
	}
	t, err := s.b.Transcript(ctx, req.ID)
	if err != nil {
		return ExportJSON{}, err
	}
	data, err := services.Export(t, format)
	if err != nil {
		return ExportJSON{}, err
	}
	dst, err := s.OutputFile(req.Dir, req.Filename, firstNonEmpty(baseTitle(t.Title), "transcript-"+t.ID), format)
	if err != nil {
		return ExportJSON{}, err
	}
	if err := writeAtomic(dst, data); err != nil {
		return ExportJSON{}, err
	}
	return ExportJSON{Files: []string{dst}, Format: format, Bytes: int64(len(data)), Seconds: t.Duration}, nil
}

// baseTitle is a title without a file extension and the " (API)" mark.
func baseTitle(title string) string {
	title = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(title), " (API)"), " (MCP)")
	if ext := filepath.Ext(title); len(ext) > 1 && len(ext) <= 5 {
		title = strings.TrimSuffix(title, ext)
	}
	return title
}

// writeAtomic writes data to path through a temporary file.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".audiosmith-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Workflows lists the saved workflows.
func (s *Server) Workflows(ctx context.Context) ([]WorkflowJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return nil, err
	}
	return st.Workflows(ctx)
}

// RunWorkflow runs a saved workflow.
func (s *Server) RunWorkflow(ctx context.Context, req WorkflowRun) (JobJSON, error) {
	st, err := s.Studio()
	if err != nil {
		return JobJSON{}, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return JobJSON{}, errInvalid("workflow_id", "workflow_id is empty.")
	}
	if len(req.Scripts) > 0 && len(req.Files) > 0 {
		return JobJSON{}, errInvalid("files", "Give scripts or files, not both.")
	}
	for i, f := range req.Files {
		if req.Files[i], err = InputFile("files", f); err != nil {
			return JobJSON{}, err
		}
	}
	if err := s.engineGate(); err != nil {
		return JobJSON{}, err
	}
	return st.RunWorkflow(ctx, req)
}

// TakeFile is a take's audio file (for agents' listings).
func (s *Server) TakeFile(t core.Take) string { return s.b.TakeAudio(t) }
