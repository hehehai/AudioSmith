package main

// The Local API's view of the app (M5, thread #20): api.Backend over the
// library, the transcript service, the engine supervisor and the writing
// help's language models. Everything here runs on the API's goroutines:
// it reads only what is goroutine-safe (the library and its services, the
// settings service, the supervisor, the LLM router) and never the app's
// view state.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// apiBackend implements api.Backend.
type apiBackend struct {
	lib     *library
	tr      *services.Transcripts
	sup     engine.Supervisor
	router  *llm.Router
	version string
	tmp     string
	data    string
	// fill applies a saved voice to a take for a model (voices_studio.go's
	// fillVoice, which reads only the library).
	fill func(t *core.Take, v *core.Voice, e catalog.Entry)
	// ffmpeg is the optional ffmpeg in use (M7a: MP3, Opus, FLAC).
	ffmpeg func() *ffmpeg.Tool
	// cleanRef checks and cleans a new clone's reference afterwards, as an
	// import's (#83: voice_editor_ref.go); nil in tests.
	cleanRef    func(core.Voice)
	studioParts // thread #35: api_studio*.go
	kitParts    // thread #135: api_kit*.go
}

var _ api.Backend = (*apiBackend)(nil)

// newAPIBackend captures what the API may use; call it after
// connectEngine and openLLM.
func (a *app) newAPIBackend(dataDir string) *apiBackend {
	b := &apiBackend{lib: a.lib, tr: a.trSvc, router: a.llmRouter, version: a.version, data: dataDir,
		tmp: filepath.Join(dataDir, "api-tmp"), fill: a.fillVoice, ffmpeg: a.mediaTool,
		cleanRef: func(v core.Voice) { a.post(func() { a.cleanImportedReference(v) }) }}
	if a.eng != nil {
		b.sup = a.eng.sup
	}
	b.useStudio(a.dubSvc, a.wfSvc) // thread #35
	a.useKit(b)                    // thread #135
	return b
}

func (b *apiBackend) Status() api.Status {
	st := api.Status{State: api.StateStopped, Version: b.version}
	if b.sup == nil {
		st.State, st.Detail = api.StateFailed, "the engine is not connected"
		return st
	}
	for _, w := range b.sup.Status() {
		if w.Role != proto.RoleGPU {
			continue
		}
		switch w.State {
		case engine.WorkerReady:
			st.State = api.StateReady
		case engine.WorkerStarting:
			st.State = api.StateStarting
			st.RetryAfter = 5
		case engine.WorkerFailed:
			st.State, st.Detail = api.StateFailed, w.Err
			if !w.RestartAt.IsZero() {
				st.State = api.StateRestarting
				st.RetryAfter = max(int(time.Until(w.RestartAt).Seconds())+1, 1)
			}
		case engine.WorkerStopped:
			st.State = api.StateStopped
		}
		if h := w.Hello; h != nil {
			st.EngineVersion, st.Device, st.Memory = h.Version, h.Device.Name, h.Device.Memory
			st.Families = h.Families
		}
		if w.Stats != nil {
			for _, m := range w.Stats.Models {
				st.Loaded = append(st.Loaded, m.Model)
			}
		}
	}
	return st
}

func (b *apiBackend) Settings() settings.Settings { return b.lib.settings.Get() }

func (b *apiBackend) Models(ctx context.Context) ([]api.Model, error) {
	caches := modelCaches(b.lib.settings.Get())
	accepted, _ := b.lib.licences.List(ctx)
	var out []api.Model
	for _, e := range catalog.Builtin().Entries {
		m := api.Model{Entry: e}
		if loc, err := hub.StatusIn(caches, e); err == nil {
			m.Downloaded = loc.State == hub.Downloaded
		}
		m.LicenceNeeded = licence.Required(e) != nil && !licence.Covers(accepted, e)
		out = append(out, m)
	}
	return out, nil
}

func (b *apiBackend) DownloadModel(ctx context.Context, id string) (string, error) {
	e, ok := catalog.Builtin().ByID(id)
	if !ok {
		return "", fmt.Errorf("model %s: %w", id, core.ErrNotFound)
	}
	// One download per model: an unfinished one is the answer.
	if js, err := b.lib.sched.List(ctx, jobs.Filter{Owner: "model:" + e.Repo}); err == nil && len(js) > 0 {
		return js[0].ID, nil
	}
	return b.lib.sched.Submit(ctx, hub.DownloadSpec(e, i18n.T("Download %s", modelRef(e))))
}

func (b *apiBackend) Voices(ctx context.Context, q core.VoiceQuery) ([]core.Voice, error) {
	return b.lib.svc.Voices.List(ctx, q)
}

func (b *apiBackend) Voice(ctx context.Context, id string) (core.Voice, error) {
	return b.lib.svc.Voices.Get(ctx, id)
}

func (b *apiBackend) SaveVoice(ctx context.Context, v core.Voice, f core.VoiceFiles) (core.Voice, error) {
	saved, err := b.lib.svc.Voices.SaveFiles(ctx, v, f)
	if err == nil && f.Reference != "" && f.Original == "" && b.cleanRef != nil {
		b.cleanRef(saved)
	}
	return saved, err
}

func (b *apiBackend) DeleteVoice(ctx context.Context, id string) error {
	return b.lib.svc.Voices.Delete(ctx, id)
}

func (b *apiBackend) VoiceFile(v core.Voice, name string) string {
	return b.lib.svc.Voices.FilePath(v, name)
}

func (b *apiBackend) VoiceFits(v core.Voice, e catalog.Entry) (bool, string) {
	return voiceUsable(&v, e)
}

// Generate makes the take's recipe as the Studio would for the same
// choices, and queues it.
func (b *apiBackend) Generate(ctx context.Context, req api.SpeakRequest) (core.Take, string, error) {
	t := apiRecipe(req, b.fill, b.lib.svc.Voices.FilePath)
	id, err := b.lib.svc.Takes.Generate(ctx, core.TakeRequest{Recipe: t, Background: req.Background})
	return t, id, err
}

// apiRecipe builds a take's recipe from a resolved API request.
func apiRecipe(req api.SpeakRequest, fill func(*core.Take, *core.Voice, catalog.Entry), path func(core.Voice, string) string) core.Take {
	e := req.Model
	t := core.Take{ID: req.ID, Text: req.Text, Model: e.Repo, Speed: req.Speed, Temperature: req.Temperature, TopP: req.TopP,
		Seed: req.Seed, Duration: req.Duration, Steps: req.Steps, Guidance: req.Guidance,
		RefAudio: req.RefAudio, RefText: req.RefText, Origin: core.TakeAPI, Client: req.Client}
	master := ""
	if req.Normalize {
		master = services.MasterDefault
	}
	t.Render = &core.TakeRender{Bits: req.Bits, Master: master, SeedPinned: req.Seed != 0}
	t.Language = apiModelLanguage(e, req.Language)
	instr := strings.TrimSpace(req.Instructions)
	switch {
	case req.Voice != nil:
		v := *req.Voice
		if ok, _ := voiceUsable(&v, e); ok {
			fill(&t, &v, e)
			if e.Has(catalog.CapVoicePrompt) && v.Prompts[e.Repo] != "" && v.Kind == core.VoiceCloned && !(v.Locked && v.Sample != "") {
				t.VoicePrompt = path(v, v.Prompts[e.Repo])
			}
		}
		if req.Seed != 0 {
			t.Seed = req.Seed
		}
	case req.Speaker != "":
		t.Voice = req.Speaker
	case req.Gender != "":
		for _, sp := range e.Voices {
			if sp.Gender == req.Gender {
				t.Voice = sp.ID
				break
			}
		}
	}
	if instr == "" {
		return t
	}
	eng, known := vd.EngineFor(e.Family, e.Repo)
	switch {
	case req.Voice == nil && canDesign(e):
		// The instructions describe the voice to design.
		r := vd.Recipe{Description: instr, Attrs: vd.Parse(instr).Attrs.Canonical(), Seed: vd.FoldSeed(req.Seed)}
		lang := firstOf(req.Language, r.Attrs.Language(), vd.TextLang(req.Text))
		if low, err := vd.Lower(designTarget(e), r, vd.Options{Lang: designTextLang(lang)}); err == nil {
			t.Voice = low.Voice
		}
	case known && eng.Vocabulary == vd.VocabSpeaker:
		// CustomVoice: "Speaker, instruction".
		sp := t.Voice
		if sp == "" && len(e.Voices) > 0 {
			sp = e.Voices[0].ID
		}
		if sp != "" {
			t.Voice = sp + ", " + instr
		}
	default:
		if s := styleFor(e, instr); s != "" {
			t.Voice = s
		}
	}
	return t
}

// apiModelLanguage is the value of a model's language parameter for a
// request's language: a BCP-47 code mapped to the model's value, or the
// model's own value as given.
func apiModelLanguage(e catalog.Entry, l string) string {
	l = strings.TrimSpace(l)
	if l == "" {
		return ""
	}
	for _, x := range e.Languages {
		if strings.EqualFold(x.Value, l) {
			return x.Value
		}
	}
	if v := modelLanguage(e, l); v != "" {
		return v
	}
	if e.Has(catalog.CapLanguage) {
		return l
	}
	return ""
}

func (b *apiBackend) Takes(ctx context.Context, q core.TakeQuery) ([]core.Take, error) {
	return b.lib.svc.Takes.List(ctx, q)
}

func (b *apiBackend) Take(ctx context.Context, id string) (core.Take, error) {
	return b.lib.svc.Takes.Get(ctx, id)
}

func (b *apiBackend) TakeAudio(t core.Take) string { return b.lib.svc.Takes.AudioPath(t) }

func (b *apiBackend) DeleteTake(ctx context.Context, id string) error {
	return b.lib.svc.Takes.Delete(ctx, id)
}

func (b *apiBackend) Transcribe(ctx context.Context, req services.TranscribeRequest) (string, error) {
	if b.tr == nil {
		return "", fmt.Errorf("transcripts: %w", core.ErrUnsupported)
	}
	_, id, err := b.tr.Transcribe(ctx, req)
	return id, err
}

func (b *apiBackend) Transcripts(ctx context.Context, q core.TranscriptQuery) ([]core.Transcript, error) {
	return b.tr.List(ctx, q)
}

func (b *apiBackend) Transcript(ctx context.Context, id string) (core.Transcript, error) {
	return b.tr.Get(ctx, id)
}

func (b *apiBackend) DeleteTranscript(ctx context.Context, id string) error {
	return b.tr.Delete(ctx, id)
}

func (b *apiBackend) Translate(ctx context.Context, req translate.Request) (translate.Result, error) {
	if b.router == nil {
		return translate.Result{}, fmt.Errorf("no language model: %w", core.ErrUnsupported)
	}
	return (&translate.LLM{Router: b.router}).Translate(ctx, req)
}

func (b *apiBackend) Direct(ctx context.Context, note string) translate.Direction {
	return (&translate.Director{Router: b.router}).Parse(ctx, note)
}

func (b *apiBackend) Fit(ctx context.Context, req translate.FitRequest) (translate.FitResult, error) {
	return (&translate.Fitter{Router: b.router}).Check(ctx, req)
}

// DetectWatermark checks a file for the watermark (watermark_app.go, thread #14).
func (b *apiBackend) DetectWatermark(ctx context.Context, path string) (proto.WatermarkDetectResult, error) {
	return b.lib.detectWatermark(ctx, path)
}

func (b *apiBackend) Probe(ctx context.Context, path string) (proto.ProbeResult, error) {
	if b.sup == nil {
		return proto.ProbeResult{}, fmt.Errorf("probe: %w", core.ErrEngine)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return engine.Probe(ctx, b.sup, path)
}

// EncodesFormat is api.FormatEncoder (M7a): MP3, Opus and FLAC through
// the optional ffmpeg (ffmpeg_engine.go).
func (b *apiBackend) EncodesFormat(format string) bool {
	return b.ffmpeg != nil && b.ffmpeg().CanEncode(format)
}

func (b *apiBackend) CanEncode() bool {
	if !slices.Contains(engine.ExecutorKinds, proto.MethodMediaEncode) {
		return false
	}
	if b.sup == nil {
		return false
	}
	for _, w := range b.sup.Status() {
		if w.Role == proto.RoleMedia && w.Hello != nil && !slices.Contains(w.Hello.Methods, proto.MethodMediaEncode) {
			return false
		}
	}
	return true
}

func (b *apiBackend) TempDir() string { return b.tmp }
func (b *apiBackend) DataDir() string { return b.data }

// cleanTemp removes what an earlier run left in the API's temporary folder.
func (b *apiBackend) cleanTemp() {
	os.RemoveAll(b.tmp)
}
