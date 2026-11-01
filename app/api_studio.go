package main

// The app's modules for agents (thread #35): api.Studio over the same
// services and helpers the pages use (longform*.go, dub*.go, batch.go,
// gallery.go, export*.go, workflow*.go, projects.go). Like the rest
// of apiBackend it runs on the API's goroutines and reads only what is
// goroutine-safe (the library, the services, settings), never view state.
//
// Files: api_studio.go (the library, jobs, deleting, voices),
// api_studio_longform*.go (Stories, Audiobook: create and read, _edit,
// _render), api_studio_dub*.go (dubs: create, read and run, _edit),
// api_studio_batch.go (the batch queue), api_studio_more.go (gallery,
// take exports, workflows).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

var _ api.Studio = (*apiBackend)(nil)

// studioParts are what the Studio operations use besides the library:
// the services main makes (nil in tests of the API alone).
type studioParts struct {
	dubs *services.Dubs
	wf   *services.Workflows

	jobMu sync.Mutex
	onEnd map[string]func(jobs.Job) // queue job id → what to do when it ends
	unsub func()
}

// useStudio gives the API the app's dub and workflow services (newAPIBackend).
func (b *apiBackend) useStudio(dubs *services.Dubs, wf *services.Workflows) {
	b.dubs, b.wf = dubs, wf
	b.onEnd = map[string]func(jobs.Job){}
	b.unsub = core.On(b.lib.bus, func(u jobs.Updated) {
		if u.Chunk != nil || !(u.Job.State.Terminal() || u.Job.State == jobs.StateInterrupted) {
			return
		}
		b.jobMu.Lock()
		fn := b.onEnd[u.Job.ID]
		delete(b.onEnd, u.Job.ID)
		b.jobMu.Unlock()
		if fn != nil {
			go fn(u.Job)
		}
	})
}

// whenDone runs fn once job id ends (a dub export's record).
func (b *apiBackend) whenDone(id string, fn func(jobs.Job)) {
	b.jobMu.Lock()
	if b.onEnd != nil {
		b.onEnd[id] = fn
	}
	b.jobMu.Unlock()
	// It may have ended already.
	if j, err := b.lib.sched.Get(context.Background(), id); err == nil && (j.State.Terminal() || j.State == jobs.StateInterrupted) {
		b.jobMu.Lock()
		fn, ok := b.onEnd[id]
		delete(b.onEnd, id)
		b.jobMu.Unlock()
		if ok {
			go fn(j)
		}
	}
}

// --- errors -----------------------------------------------------------------------

func apiErr(status int, code, param, format string, args ...any) *api.Error {
	return &api.Error{Status: status, Code: code, Param: param, Message: fmt.Sprintf(format, args...)}
}

func errBadRequest(param, format string, args ...any) *api.Error {
	return apiErr(http.StatusBadRequest, api.CodeInvalid, param, format, args...)
}

func errMissing(what, id string) *api.Error {
	return apiErr(http.StatusNotFound, api.CodeNotFound, "", "There is no %s %q.", what, id)
}

// errNotReady says a step must come first (409).
func errNotReady(format string, args ...any) *api.Error {
	return apiErr(http.StatusConflict, "not_ready", "", format, args...)
}

func errBusy(format string, args ...any) *api.Error {
	return apiErr(http.StatusConflict, "busy", "", format, args...)
}

func errNoModule(what string) *api.Error {
	return apiErr(http.StatusNotImplemented, api.CodeUnsupported, "", "%s is not available in this AudioSmith.", what)
}

// notFound maps the store's not-found to the API's, naming what.
func notFound(err error, what, id string) error {
	if errors.Is(err, core.ErrNotFound) || errors.Is(err, jobs.ErrNotFound) {
		return errMissing(what, id)
	}
	return err
}

// --- models and voices --------------------------------------------------------------

// ttsEntry finds a speech model by id, repo or label; "" is def. A
// removed model gives its replacement (clone: the work clones voices)
// and a warning for the answer.
func ttsEntry(name, def string, clone bool) (catalog.Entry, string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		n = def
	}
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		if e.Named(n) {
			return e, "", nil
		}
	}
	if e, r, ok := replaceRemoved(n, clone); ok {
		return e, r.Warning(e), nil
	}
	return catalog.Entry{}, "", apiErr(http.StatusNotFound, api.CodeModelNotFound, "model", "There is no speech model %q: list_models lists them.", name)
}

// defaultTTSRepo is the Studio's default model (Settings → Studio).
func (b *apiBackend) defaultTTSRepo() string {
	st := b.lib.settings.Get()
	if e, ok := catalog.Builtin().ByID(st.Studio.Model); ok {
		return e.Repo
	}
	if l := catalog.Builtin().ForTask(catalog.TaskTTS); len(l) > 0 {
		return l[0].Repo
	}
	return ""
}

// onDisk reports whether a model is downloaded (and whether that is known).
func (b *apiBackend) onDisk(e catalog.Entry) (ready, known bool) {
	loc, err := hub.StatusIn(modelCaches(b.lib.settings.Get()), e)
	if err != nil {
		return false, false
	}
	return loc.State == hub.Downloaded, true
}

// modelReady refuses a model that is not downloaded or whose licence is
// not accepted, as Generate does.
func (b *apiBackend) modelReady(ctx context.Context, e catalog.Entry) error {
	if ready, known := b.onDisk(e); known && !ready {
		return fmt.Errorf("%s: %w", e.Label, core.ErrNotDownloaded)
	}
	if b.lib.licences != nil {
		if err := b.lib.licences.Check(ctx, e.Repo); err != nil {
			return err
		}
	}
	return nil
}

// voices are the saved voices.
func (b *apiBackend) voices(ctx context.Context) ([]core.Voice, error) {
	return b.lib.svc.Voices.List(ctx, core.VoiceQuery{})
}

// savedVoice finds a saved voice by id, then by name (case-insensitive).
func savedVoice(voices []core.Voice, name string) (*core.Voice, error) {
	n := strings.TrimSpace(name)
	for i := range voices {
		if voices[i].ID == n {
			return &voices[i], nil
		}
	}
	var named []int
	for i := range voices {
		if strings.EqualFold(strings.TrimSpace(voices[i].Name), n) {
			named = append(named, i)
		}
	}
	switch len(named) {
	case 0:
		return nil, nil
	case 1:
		return &voices[named[0]], nil
	}
	ids := make([]string, len(named))
	for i, j := range named {
		ids[i] = voices[j].ID
	}
	return nil, &api.Error{Status: http.StatusConflict, Code: api.CodeAmbiguousVoice, Param: "voice",
		Message: "Several saved voices are called " + n + ": use one's id.", Data: map[string]any{"voices": ids}}
}

// isDefault reports whether a voice argument asks for "no voice of its
// own".
func isDefault(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "default" || v == "none"
}

// voiceRef resolves an agent's voice for model e: a saved voice (id or
// name; it must fit the model) → its id; a built-in speaker ("af_heart",
// "speaker:af_heart") → "speaker:<id>". "" stays "".
func voiceRef(voices []core.Voice, e catalog.Entry, name, param string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", nil
	}
	speakers := e.AllVoiceIDs() // hidden ones still work when named
	if id, ok := strings.CutPrefix(n, services.SpeakerRef); ok {
		for _, sp := range speakers {
			if strings.EqualFold(sp, id) {
				return services.SpeakerRef + sp, nil
			}
		}
		return "", apiErr(http.StatusBadRequest, api.CodeVoiceNotFound, param, "%s has no built-in speaker %q.", e.Label, id)
	}
	v, err := savedVoice(voices, n)
	if err != nil {
		return "", err
	}
	if v != nil {
		if ok, why := voiceUsable(v, e); !ok {
			// The voice and the models that can speak it, so an agent
			// switches model at once (Mac test #136: both agents cast a
			// designed voice on a clone model first).
			return "", apiErr(http.StatusBadRequest, api.CodeUnsupportedModel, param, "%s: %s Models that can speak it: %s.",
				promptName(v.Name), why, strings.Join(modelsFor(v), ", "))
		}
		return v.ID, nil
	}
	for _, sp := range speakers {
		if strings.EqualFold(sp, n) {
			return services.SpeakerRef + sp, nil
		}
	}
	return "", apiErr(http.StatusBadRequest, api.CodeVoiceNotFound, param, "No voice is called %s: list_voices lists the saved voices, list_models each model's built-in speakers.", n)
}

// voiceLabel names a voice reference.
func voiceLabel(voices []core.Voice, ref string) string {
	if id, ok := strings.CutPrefix(ref, services.SpeakerRef); ok {
		return id
	}
	for _, v := range voices {
		if v.ID == ref {
			return v.Name
		}
	}
	return ""
}

// --- the project library -------------------------------------------------------------

// Projects lists the project library (the Projects page's search).
func (b *apiBackend) Projects(ctx context.Context, q api.ProjectQuery) ([]api.ProjectJSON, error) {
	cats := []string{store.LibAll, store.LibDub, store.LibStory, store.LibAudiobook, store.LibVoice, store.LibTranscript, store.LibTake, store.LibExport, store.LibWorkflow, store.LibCall}
	cat := strings.ToLower(strings.TrimSpace(q.Category))
	if !slices.Contains(cats, cat) {
		return nil, errBadRequest("category", "category is one of %s.", strings.Join(cats[1:], ", "))
	}
	items, err := b.lib.svc.Library.Search(ctx, store.LibraryQuery{Category: cat, Search: q.Search, Limit: q.Limit, Offset: q.Offset})
	if err != nil {
		return nil, err
	}
	out := make([]api.ProjectJSON, 0, len(items))
	for _, it := range items {
		p := api.ProjectJSON{ID: it.ID, Source: it.Source, Category: it.Category, Title: it.Title, Detail: it.Detail, Updated: it.Updated}
		if it.Source == "project" && strings.HasPrefix(strings.TrimSpace(it.Detail), "{") {
			p.Summary, p.Detail = []byte(it.Detail), ""
		}
		switch {
		case it.Category == store.LibStory || it.Category == store.LibAudiobook:
			p.Tool = "get_longform"
			if it.Source == "render" {
				p.Tool = "get_job longform:" + it.ID
			}
		case it.Category == store.LibDub:
			p.Tool = "get_dub"
		case it.Category == store.LibWorkflow:
			p.Tool = "list_workflows"
		case it.Category == store.LibTake:
			p.Tool = "list_takes"
		case it.Category == store.LibVoice:
			p.Tool = "list_voices"
		case it.Category == store.LibTranscript:
			p.Tool = "export_transcript"
		}
		out = append(out, p)
	}
	return out, nil
}

// --- jobs ---------------------------------------------------------------------------

// Task is a group job's state.
func (b *apiBackend) Task(ctx context.Context, id string) (api.JobJSON, error) {
	switch {
	case strings.HasPrefix(id, services.OwnerLongform):
		return b.renderTask(ctx, strings.TrimPrefix(id, services.OwnerLongform))
	case strings.HasPrefix(id, services.DubOwner):
		return b.dubTask(ctx, strings.TrimPrefix(id, services.DubOwner))
	case strings.HasPrefix(id, services.WorkflowOwner):
		return b.workflowTask(ctx, strings.TrimPrefix(id, services.WorkflowOwner))
	}
	return api.JobJSON{}, errMissing("job", id)
}

// CancelTask stops a group job: a render (its chapters made are kept), a
// dub's run, a workflow's run (both resume later).
func (b *apiBackend) CancelTask(ctx context.Context, id string) error {
	switch {
	case strings.HasPrefix(id, services.OwnerLongform):
		return b.lib.svc.Longform.Stop(ctx, strings.TrimPrefix(id, services.OwnerLongform))
	case strings.HasPrefix(id, services.DubOwner):
		if b.dubs == nil {
			return errNoModule("Dubbing")
		}
		return b.dubs.Cancel(ctx, strings.TrimPrefix(id, services.DubOwner))
	case strings.HasPrefix(id, services.WorkflowOwner):
		if b.wf == nil {
			return errNoModule("Workflows")
		}
		return b.wf.Cancel(ctx, strings.TrimPrefix(id, services.WorkflowOwner))
	}
	return errMissing("job", id)
}

// activeJobs are an owner's unfinished queue jobs, their ids, and the
// one running (or the first) for the stage.
func (b *apiBackend) activeJobs(ctx context.Context, owner string) ([]string, *jobs.Job) {
	js, err := b.lib.sched.List(ctx, jobs.Filter{Owner: owner})
	if err != nil {
		return nil, nil
	}
	var ids []string
	var cur *jobs.Job
	for i := range js {
		ids = append(ids, js[i].ID)
		if cur == nil || js[i].State == jobs.StateRunning && cur.State != jobs.StateRunning {
			cur = &js[i]
		}
	}
	return ids, cur
}

// Delete deletes a Stories or Audiobook project, or a dub.
func (b *apiBackend) Delete(ctx context.Context, kind, id string) error {
	switch kind {
	case api.DeleteProject:
		p, err := b.lib.svc.Projects.Get(ctx, id)
		if err != nil {
			return notFound(err, "project", id)
		}
		if p.Kind != core.ProjectStory && p.Kind != core.ProjectAudiobook {
			return errBadRequest("kind", "%s is a %s project: delete it in AudioSmith.", id, p.Kind)
		}
		return b.lib.svc.Projects.Delete(ctx, id)
	case api.DeleteDub:
		if b.dubs == nil {
			return errNoModule("Dubbing")
		}
		if _, err := b.dubs.Get(ctx, id); err != nil {
			return notFound(err, "dub", id)
		}
		return b.dubs.Delete(ctx, id)
	}
	return errBadRequest("kind", "kind %q cannot be deleted here.", kind)
}

// tool is the ffmpeg in use, nil for none.
func (b *apiBackend) tool() *ffmpeg.Tool {
	if b.ffmpeg == nil {
		return nil
	}
	return b.ffmpeg()
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

// since is a time as RFC 3339, "" for zero.
func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// AgentSkill serves a file of an agent's skill (GET /api/agent-skill), as
// the Integrations page makes it: the live port, voices and folder, the
// UI's language, never the key.
func (b *apiBackend) AgentSkill(ctx context.Context, agent, file string) (string, error) {
	if _, ok := promptAgents[agent]; !ok {
		var slugs []string
		for k := range promptAgents {
			slugs = append(slugs, k)
		}
		slices.Sort(slugs)
		return "", apiErr(http.StatusNotFound, api.CodeNotFound, "agent", "There is no agent %q: agent is one of %s.", agent, strings.Join(slugs, ", "))
	}
	text, ok := agentSkillFile(agent, file, b.skillEnv(ctx))
	if !ok {
		return "", errBadRequest("file", "file is SKILL.md or %s.", skillRefPath)
	}
	return text, nil
}

// skillEnv is the skill's environment from the settings and the voice
// library (promptEnvOf without the view state).
func (b *apiBackend) skillEnv(ctx context.Context) promptEnv {
	st := b.lib.settings.Get()
	voices, _ := b.voices(ctx)
	sortVoices(voices)
	dir := strings.TrimSpace(st.API.MCPDir)
	if dir == "" {
		dir = filepath.Join(b.data, "mcp")
	}
	e := integEnv{Base: "http://127.0.0.1:" + strconv.Itoa(st.API.LoopbackPort()), Exe: appExecutable(), NeedKey: st.API.RequireKey}
	return promptEnvFrom(e, st, voices, dir)
}

// modelsFor lists the speech models (ids) that can speak v.
func modelsFor(v *core.Voice) []string {
	var ids []string
	for _, e := range catalog.Builtin().ForTask(catalog.TaskTTS) {
		if ok, _ := voiceUsable(v, e); ok && !e.Hidden {
			ids = append(ids, e.ID)
		}
	}
	if len(ids) == 0 {
		return []string{"none"}
	}
	return ids
}
