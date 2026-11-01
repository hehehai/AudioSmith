package main

// The toolkit's sample projects and story draft (thread #135): Home's
// 示例 cards (samples*.go) and Stories' 「帮我写」 (stories_write.go) for
// agents. The cards and the copy's inputs live on the UI thread, so the
// samples go through it (onUI); the draft runs on the API's goroutine.

import (
	"context"
	"net/http"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// onUI runs fn on the UI thread and waits for it (or ctx).
func (a *app) onUI(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	a.post(func() {
		defer close(done)
		fn()
	})
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Samples lists the sample projects (kitParts' hook).
func (b *apiBackend) Samples(ctx context.Context) ([]api.SampleJSON, error) {
	if b.samples == nil {
		return nil, errNoModule("The sample projects")
	}
	return b.samples(ctx)
}

// OpenSample copies a sample into the library (kitParts' hook).
func (b *apiBackend) OpenSample(ctx context.Context, id string) (api.SampleOpened, error) {
	if b.openSample == nil {
		return api.SampleOpened{}, errNoModule("The sample projects")
	}
	return b.openSample(ctx, id)
}

// apiSamples are the sample cards for agents.
func (a *app) apiSamples(ctx context.Context) ([]api.SampleJSON, error) {
	var out []api.SampleJSON
	err := a.onUI(ctx, func() {
		for _, s := range a.sampleCards() {
			out = append(out, api.SampleJSON{ID: s.key, Kind: s.kind, Title: s.title, Description: s.about,
				Available: s.bundled != nil && !s.soon, Copy: a.prefs.Setup.Samples[s.key]})
		}
	})
	return out, err
}

// apiOpenSample makes a new copy of a sample, as its card does (without
// opening a page: the agent reads it with the tool it names).
func (a *app) apiOpenSample(ctx context.Context, id string) (api.SampleOpened, error) {
	var (
		card  sampleCard
		found bool
		in    sampleInputs
	)
	if err := a.onUI(ctx, func() {
		if card, found = a.sampleCardOf(id); !found || card.bundled == nil {
			return
		}
		in = sampleInputs{data: a.dataDir(), voice: a.lfStories.doc.Voice}
		if card.bundled.Kind == sampleAudiobook {
			in.voice = a.lfBook.doc.Voice
		}
		in.model = a.preset().Repo
		in.voices = a.lfVoices(entryFor(in.model))
	}); err != nil {
		return api.SampleOpened{}, err
	}
	switch {
	case !found:
		return api.SampleOpened{}, apiErr(http.StatusNotFound, api.CodeNotFound, "sample_id", "There is no sample %q: list_samples lists them.", id)
	case card.bundled == nil:
		return api.SampleOpened{}, apiErr(http.StatusNotImplemented, api.CodeUnsupported, "sample_id", "The sample %q is not in this build yet.", id)
	}
	oplog.Info("sample.copy", "sample", id, "via", "api")
	ref, err := a.copyBundledSample(ctx, samplesFS, *card.bundled, in)
	if err != nil {
		return api.SampleOpened{}, err
	}
	out := api.SampleOpened{ID: id, Kind: card.kind, Ref: ref}
	if ref == "" {
		out.Tool = "generate_speech" // a narration without audio: its script is the sample
		return out, nil
	}
	a.onUI(context.WithoutCancel(ctx), func() {
		a.rememberSample(id, ref)
		a.projects.dirty = true
	})
	kind, rid, _ := strings.Cut(ref, ":")
	switch kind {
	case sampleStory, sampleAudiobook:
		out.Tool = "get_longform project_id " + rid
	case sampleDub:
		out.Tool = "get_dub dub_id " + rid
	case sampleTranscriptKind:
		out.Tool = "export_transcript transcript_id " + rid
	case "export":
		out.Tool = "list_assets"
		if e, err := a.lib.svc.Library.Export(ctx, rid); err == nil && e.Path != "" {
			out.Files = []string{e.Path}
		}
	}
	return out, nil
}

// WriteStory drafts a story with the writing help's model, as Stories'
// 「帮我写」 does, and makes its project when asked.
func (b *apiBackend) WriteStory(ctx context.Context, req api.StoryDraftRequest) (api.StoryDraftJSON, error) {
	if b.router == nil {
		return api.StoryDraftJSON{}, errNoLLM()
	}
	cctx, cancel := context.WithTimeout(ctx, storyWriteTimeout)
	defer cancel()
	resp, err := b.router.Chat(cctx, storyWriteSkill, llm.Request{
		Messages:    []llm.Message{{Role: llm.System, Content: storyWritePrompt}, {Role: llm.User, Content: strings.TrimSpace(req.Description)}},
		Temperature: llm.F64(0.7), MaxTokens: 1200,
	})
	if err != nil {
		if c := llm.Code(err); c == llm.CodeNoRoute || translate.Code(err) == translate.CodeNoLLM {
			return api.StoryDraftJSON{}, errNoLLM()
		}
		return api.StoryDraftJSON{}, apiErr(http.StatusBadGateway, api.CodeDirectorFailed, "description", "The writing help could not write a draft: %v", err)
	}
	draft := cleanStoryDraft(resp.Text)
	if draft == "" {
		return api.StoryDraftJSON{}, apiErr(http.StatusBadGateway, api.CodeDirectorFailed, "description", "The draft had no lines for voices: try again or rephrase the idea.")
	}
	out := api.StoryDraftJSON{Script: draft}
	if first, _, _ := strings.Cut(draft, "\n"); strings.HasPrefix(first, "#") {
		out.Title = strings.TrimSpace(strings.TrimLeft(first, "#"))
	}
	if !req.Create {
		return out, nil
	}
	e, warn, err := ttsEntry(req.Model, b.defaultTTSRepo(), false)
	if err != nil {
		return api.StoryDraftJSON{}, err
	}
	voices, err := b.voices(ctx)
	if err != nil {
		return api.StoryDraftJSON{}, err
	}
	d := services.NewLongformDoc(core.ProjectStory, e.Repo)
	if t, _ := storyFromMarkup(&d, draft, lfVoicesOf(voices, e)); t != "" {
		d.Title = t
	}
	if strings.TrimSpace(d.Title) == "" {
		d.Title = firstWords(strings.TrimSpace(req.Description), 6)
	}
	saved, err := b.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: core.ProjectStory, Title: d.Title}, d)
	if err != nil {
		return api.StoryDraftJSON{}, err
	}
	p, err := b.longformJSON(ctx, saved, d, voices)
	if err != nil {
		return api.StoryDraftJSON{}, err
	}
	p.Warning = firstOf(p.Warning, warn)
	out.Project, out.Title = &p, d.Title
	return out, nil
}

// errNoLLM says the writing help has no language model.
func errNoLLM() *api.Error {
	return apiErr(http.StatusBadRequest, api.CodeLLMUnavailable, "description",
		"Writing a draft needs a language model: ask the user to set one up in AudioSmith's Settings → Writing help (Qwen3 4B runs on this Mac), or write the script yourself and use create_story.")
}
