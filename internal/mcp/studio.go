package mcp

// The tools over the app's modules (thread #35): Stories and Audiobook,
// dubbing and batch, voice discovery, exports, the project library and
// the History, saved workflows, jobs and deleting. Each calls an
// api.Server operation (internal/api/studio*.go), as REST does. They
// read and write files on this Mac, so only clients on this Mac (or with
// the API key) may call them. Long work returns a job_id at once:
// get_job follows it (wait_seconds long-polls), cancel_job stops it.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
)

// StudioTools are the tools of this file, in the order they are added.
var StudioTools = []string{"get_job", "cancel_job", "list_projects", "list_takes", "delete_item",
	"create_audiobook", "create_story", "get_longform", "update_longform", "render_longform", "export_longform",
	"create_dub", "get_dub", "run_dub", "edit_dub", "export_dub", "batch_dub", "list_batch",
	"search_voices", "use_gallery_voice", "export_audio", "export_transcript", "list_workflows", "run_workflow"}

// local refuses callers that may not touch this Mac's files or its voice
// library (LAN clients with the PIN only).
func (s *Server) local(req *sdk.CallToolRequest) error { return s.caller(req).local() }

func (c caller) local() error {
	if !c.trusted() {
		return errors.New(api.CodeUnauthorized + ": this tool reads and writes files or the voice library on the Mac: call it on the Mac, or with the API key")
	}
	return nil
}

// jobOut is a job as the tools return it.
type jobOut struct {
	JobID    string          `json:"job_id"`
	Kind     string          `json:"kind,omitempty"`
	State    string          `json:"state"`
	Done     bool            `json:"done"`
	Title    string          `json:"title,omitempty"`
	Stage    string          `json:"stage,omitempty"`
	Progress *float64        `json:"progress,omitempty"` // 0–1
	Message  string          `json:"message,omitempty"`
	Error    *errOut         `json:"error,omitempty"`
	Files    []string        `json:"files,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Jobs     []string        `json:"jobs,omitempty"`
	Next     string          `json:"next,omitempty"`
}

type errOut struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func jobOf(j api.JobJSON) jobOut {
	o := jobOut{JobID: j.ID, Kind: j.Kind, State: j.State, Done: j.Done, Title: j.Title, Files: j.Files, Result: j.Result, Jobs: j.Jobs}
	if p := j.Progress; p != nil {
		o.Stage, o.Message = p.Stage, p.Message
		if p.Fraction >= 0 {
			f := float64(int(p.Fraction*1000)) / 1000
			o.Progress = &f
		}
	}
	if j.Error != nil {
		o.Error = &errOut{Code: j.Error.Code, Message: j.Error.Message}
	}
	if !j.Done {
		o.Next = "get_job with job_id " + j.ID + " and wait_seconds (up to 60) until done."
	}
	return o
}

type getJobIn struct {
	JobID string `json:"job_id" jsonschema:"A job_id a tool returned: a queue job, or longform:…, dub:…, workflow:…"`
	Wait  int    `json:"wait_seconds,omitempty" jsonschema:"Wait up to this many seconds (≤ 60) for it to end before answering; 0 answers at once."`
}

func (s *Server) getJob(ctx context.Context, req *sdk.CallToolRequest, in getJobIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	j, err := s.o.API.Job(ctx, strings.TrimSpace(in.JobID), time.Duration(min(max(in.Wait, 0), 60))*time.Second)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(jobOf(j))
}

type cancelJobIn struct {
	JobID string `json:"job_id" jsonschema:"The job to cancel (a render stops after its current part; what was made is kept)."`
}

func (s *Server) cancelJob(ctx context.Context, req *sdk.CallToolRequest, in cancelJobIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	j, err := s.o.API.CancelJob(ctx, strings.TrimSpace(in.JobID))
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(jobOf(j))
}

func (s *Server) listProjects(ctx context.Context, req *sdk.CallToolRequest, in api.ProjectQuery) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	l, err := s.o.API.Projects(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"count": len(l), "items": l})
}

type listTakesIn struct {
	Search  string `json:"search,omitempty" jsonschema:"Words in the text."`
	VoiceID string `json:"voice_id,omitempty" jsonschema:"Only takes of this saved voice."`
	Model   string `json:"model,omitempty" jsonschema:"Only takes of this model."`
	Starred bool   `json:"starred,omitempty" jsonschema:"Only starred takes."`
	ID      string `json:"take_id,omitempty" jsonschema:"One take by id."`
	Limit   int    `json:"limit,omitempty" jsonschema:"At most this many, newest first (default 20, up to 200)."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Skip this many (paging)."`
}

type takeOut struct {
	ID           string  `json:"take_id"`
	CreatedAt    string  `json:"created_at"`
	Text         string  `json:"text"`
	Model        string  `json:"model"`
	VoiceID      string  `json:"voice_id,omitempty"`
	Language     string  `json:"language,omitempty"`
	AudioSeconds float64 `json:"audio_seconds"`
	Starred      bool    `json:"starred,omitempty"`
	Origin       string  `json:"origin,omitempty"`
	File         string  `json:"file"`
}

func (s *Server) listTakes(ctx context.Context, req *sdk.CallToolRequest, in listTakesIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	var ts []core.Take
	if id := strings.TrimSpace(in.ID); id != "" {
		t, err := s.o.Backend.Take(ctx, id)
		if err != nil {
			return nil, nil, toolError(err)
		}
		ts = []core.Take{t}
	} else {
		q := core.TakeQuery{Search: in.Search, VoiceID: in.VoiceID, Starred: in.Starred, Limit: min(max(in.Limit, 0), 200), Offset: min(max(in.Offset, 0), api.MaxOffset)}
		if q.Limit == 0 {
			q.Limit = 20
		}
		if m := strings.TrimSpace(in.Model); m != "" {
			q.Model = m
			for _, e := range catalog.Builtin().Entries {
				if e.Named(m) {
					q.Model = e.Repo
				}
			}
		}
		var err error
		if ts, err = s.o.Backend.Takes(ctx, q); err != nil {
			return nil, nil, toolError(err)
		}
	}
	out := []takeOut{}
	for _, t := range ts {
		model := t.Model
		if e, ok := catalog.Builtin().ByRepo(t.Model); ok {
			model = e.ID
		}
		out = append(out, takeOut{ID: t.ID, CreatedAt: t.CreatedAt.Format(time.RFC3339), Text: t.Text, Model: model, VoiceID: t.VoiceID,
			Language: t.Language, AudioSeconds: round2(t.AudioSeconds), Starred: t.Starred, Origin: t.Origin, File: s.o.Backend.TakeAudio(t)})
	}
	return result(map[string]any{"count": len(out), "takes": out})
}

type deleteIn struct {
	Kind    string `json:"kind" jsonschema:"take, voice, transcript, project (a story or audiobook), dub or isolation."`
	ID      string `json:"id" jsonschema:"Its id."`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"true only after you asked the user about deleting this item and they said yes (a request to delete is not yet the yes); without it nothing is deleted (confirm_required)."`
}

func (s *Server) deleteItem(ctx context.Context, req *sdk.CallToolRequest, in deleteIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	if in.Confirm {
		if err := askUser(ctx, req, s.deletePrompt(ctx, strings.TrimSpace(in.Kind), strings.TrimSpace(in.ID))); err != nil {
			return nil, nil, err
		}
	}
	if err := s.o.API.Delete(ctx, strings.TrimSpace(in.Kind), strings.TrimSpace(in.ID), in.Confirm); err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"kind": in.Kind, "id": in.ID, "deleted": true})
}

func (s *Server) createAudiobook(ctx context.Context, req *sdk.CallToolRequest, in api.AudiobookCreate) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	p, err := s.o.API.CreateLongform(ctx, in.Longform())
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(p)
}

func (s *Server) createStory(ctx context.Context, req *sdk.CallToolRequest, in api.StoryCreate) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	p, err := s.o.API.CreateLongform(ctx, in.Longform())
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(p)
}

type projectIn struct {
	ID string `json:"project_id" jsonschema:"The story or audiobook project."`
}

func (s *Server) getLongform(ctx context.Context, req *sdk.CallToolRequest, in projectIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	p, err := s.o.API.Longform(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(p)
}

func (s *Server) updateLongform(ctx context.Context, req *sdk.CallToolRequest, in api.LongformUpdate) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	p, err := s.o.API.UpdateLongform(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(p)
}

func (s *Server) renderLongform(ctx context.Context, req *sdk.CallToolRequest, in api.LongformRender) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	j, err := s.o.API.RenderLongform(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(jobOf(j))
}

func (s *Server) exportLongform(ctx context.Context, req *sdk.CallToolRequest, in api.LongformExport) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	out, err := s.o.API.ExportLongform(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(out)
}

func (s *Server) createDub(ctx context.Context, req *sdk.CallToolRequest, in api.DubCreate) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	d, err := s.o.API.CreateDub(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(d)
}

func (s *Server) getDub(ctx context.Context, req *sdk.CallToolRequest, in api.DubQuery) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	d, err := s.o.API.Dub(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(d)
}

func (s *Server) runDub(ctx context.Context, req *sdk.CallToolRequest, in api.DubRun) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	j, err := s.o.API.RunDub(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(jobOf(j))
}

func (s *Server) editDub(ctx context.Context, req *sdk.CallToolRequest, in api.DubEdit) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	d, err := s.o.API.EditDub(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(d)
}

func (s *Server) exportDub(ctx context.Context, req *sdk.CallToolRequest, in api.DubExport) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	j, err := s.o.API.ExportDub(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(jobOf(j))
}

func (s *Server) batchDub(ctx context.Context, req *sdk.CallToolRequest, in api.BatchRequest) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	b, err := s.o.API.BatchDub(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(b)
}

func (s *Server) listBatch(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	b, err := s.o.API.Batch(ctx)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(b)
}

func (s *Server) searchVoices(ctx context.Context, req *sdk.CallToolRequest, in api.GalleryQuery) (*sdk.CallToolResult, any, error) {
	s.caller(req)
	p, err := s.o.API.SearchGallery(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	if in.ID != "" && len(p.Items) == 0 {
		return nil, nil, errors.New(api.CodeNotFound + ": no gallery voice has the id " + in.ID)
	}
	return result(p)
}

func (s *Server) useGalleryVoice(ctx context.Context, req *sdk.CallToolRequest, in api.GalleryUseIn) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	u, err := s.o.API.UseGalleryVoice(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(u)
}

func (s *Server) exportAudio(ctx context.Context, req *sdk.CallToolRequest, in api.TakeExport) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	out, err := s.o.API.ExportTake(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(out)
}

func (s *Server) exportTranscript(ctx context.Context, req *sdk.CallToolRequest, in api.TranscriptExport) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	out, err := s.o.API.ExportTranscript(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(out)
}

func (s *Server) listWorkflows(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	l, err := s.o.API.Workflows(ctx)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(map[string]any{"count": len(l), "workflows": l})
}

func (s *Server) runWorkflow(ctx context.Context, req *sdk.CallToolRequest, in api.WorkflowRun) (*sdk.CallToolResult, any, error) {
	if err := s.local(req); err != nil {
		return nil, nil, err
	}
	j, err := s.o.API.RunWorkflow(ctx, in)
	if err != nil {
		return nil, nil, toolError(err)
	}
	return result(jobOf(j))
}

func (s *Server) addStudioTools() {
	add := func(name, title, desc string) *sdk.Tool {
		return &sdk.Tool{Name: name, Title: title, Description: desc}
	}
	sdk.AddTool(s.srv, add("get_job", "Follow a job",
		"The state of a long job (render, dub, export, batch, workflow): progress, stage, error code, and the absolute paths of the files it made. wait_seconds long-polls."), s.getJob)
	sdk.AddTool(s.srv, add("cancel_job", "Cancel a job", "Stop a job; what was made so far is kept."), s.cancelJob)
	sdk.AddTool(s.srv, add("list_projects", "List projects",
		"AudioSmith's project library (the Projects page): stories, audiobooks, dubs, workflows, voices, transcripts, takes, exports."), s.listProjects)
	sdk.AddTool(s.srv, add("list_takes", "List takes", "The History: takes (generated audio) with their text, voice and WAV path, newest first."), s.listTakes)
	sdk.AddTool(s.srv, add("delete_item", "Delete", "Delete a take, voice, transcript, story or audiobook project, dub, or isolation (its stems). Needs confirm: true, only after the user agreed."), s.deleteItem)
	sdk.AddTool(s.srv, add("create_audiobook", "Create an audiobook",
		"Import a book (EPUB, TXT, Markdown, DOCX) or a manuscript into an Audiobook project with chapters, a narrator and a cast. Render it with render_longform."), s.createAudiobook)
	sdk.AddTool(s.srv, add("create_story", "Create a story",
		"Make a Stories project (several voices) from a script ('Name: line'), a file, or lines. Render it with render_longform."), s.createStory)
	sdk.AddTool(s.srv, add("get_longform", "Read a story or audiobook",
		"A story or audiobook project: chapters, cast and voices, problems that stop a render, and the last render's files."), s.getLongform)
	sdk.AddTool(s.srv, add("update_longform", "Change a story or audiobook",
		"Set a story's or audiobook's narrator, cast voices, model, language, format (wav or m4b), loudness and book details; a story's scene, personas, line directions and pacing, or let the AI director write them (direct)."), s.updateLongform)
	sdk.AddTool(s.srv, add("render_longform", "Render a story or audiobook",
		"Render a story or audiobook (or some chapters) in the background; returns a job_id for get_job."), s.renderLongform)
	sdk.AddTool(s.srv, add("export_longform", "Export a story or audiobook",
		"Save a finished render: M4B with chapters, WAV, MP3 or Opus, one WAV per chapter, chapter times or stems."), s.exportLongform)
	sdk.AddTool(s.srv, add("create_dub", "Create a dub",
		"Dub a video or audio file into other languages (transcribe, translate, speak in each speaker's voice, mix). Returns the dub and its job_id."), s.createDub)
	sdk.AddTool(s.srv, add("get_dub", "Read a dub", "A dub: its stages, speakers, tracks, and its lines with their translations."), s.getDub)
	sdk.AddTool(s.srv, add("run_dub", "Run a dub", "Run a dub's pipeline again (after edits only the changed lines are made again); returns a job_id."), s.runDub)
	sdk.AddTool(s.srv, add("edit_dub", "Edit a dub", "Change a dub's source or translated lines, a line's speaker or voice, and the speakers' voices and names."), s.editDub)
	sdk.AddTool(s.srv, add("export_dub", "Export a dub", "Save a dub as MP4, a dubbed track (WAV, M4A, MP3), subtitles (SRT, VTT, ASS) or ZIPs; returns a job_id."), s.exportDub)
	sdk.AddTool(s.srv, add("batch_dub", "Batch dub", "Dub several files (or a folder) with the Batch queue, one dub per file at background priority; returns their job_ids."), s.batchDub)
	sdk.AddTool(s.srv, add("list_batch", "List the batch queue", "The Batch queue's dubs, its settings and its watch folder (read only)."), s.listBatch)
	sdk.AddTool(s.srv, add("search_voices", "Discover voices",
		"Search Discover voices (about 35 curated voices, ready to speak) with the page's filters: use case (category), gender, age, language."), s.searchVoices)
	sdk.AddTool(s.srv, add("use_gallery_voice", "Use a gallery voice",
		"Save a voice from Discover voices to My voices (as its Use voice does) and say which models speak it."), s.useGalleryVoice)
	sdk.AddTool(s.srv, add("export_audio", "Export a take",
		"Export a take with the Export audio dialog's options: format, sample rate, channels, loudness target, trim, bit depth or bitrate, tags, script and subtitles."), s.exportAudio)
	sdk.AddTool(s.srv, add("export_transcript", "Export a transcript", "Save a transcript as TXT, SRT, VTT or JSON."), s.exportTranscript)
	sdk.AddTool(s.srv, add("list_workflows", "List workflows", "The saved workflows (WorkflowStudio): their steps, inputs and last run."), s.listWorkflows)
	sdk.AddTool(s.srv, add("run_workflow", "Run a workflow",
		"Run a saved workflow, with new input scripts or files if given; returns a job_id whose result lists each item's files."), s.runWorkflow)
}
