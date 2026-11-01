package main

// Writing help: the language models that clean up transcripts and
// dictations (internal/llm, internal/translate), and Settings → Writing
// help. Providers are settings.LLM (no keys: those live in the Keychain
// as "llm.<provider>"); HTTP goes through hub.NewHTTPClient with the
// download proxy setting and no response-header bound. Routing, as
// threads/13.md decided: unless the user chose, a local Ollama when it
// answers on 127.0.0.1:11434, else none; cloud providers only when chosen.
// "Keep text on this Mac" (settings.Privacy.LocalOnlyLLM) leaves only
// local backends. Settings → Writing help is in
// transcribe_llm_settings.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// ollamaURL is where a local Ollama answers.
var ollamaURL = "http://127.0.0.1:11434"

// llmSecrets reads API keys; tests replace it.
var llmSecrets llm.Secrets = llm.SecretsFunc(func(_ context.Context, name string) (string, error) {
	v, err := keychainItem(name).Get()
	if err == nil && v == "" {
		err = llm.ErrNoSecret
	}
	return v, err
})

// keychainItem is the Keychain item of an API key ("llm.openai").
func keychainItem(name string) hub.Keychain {
	return hub.Secret(name, "AudioSmith "+strings.TrimPrefix(name, llm.SecretPrefix)+" API key")
}

// llmState is the writing-help side: the registry and router the
// clean-ups use, what a local Ollama offers, and the settings form.
type llmState struct {
	llmReg    *llm.Registry
	llmRouter *llm.Router
	// llmOllama are the models a local Ollama offers; nil when it did not
	// answer (or was not asked yet).
	llmOllama  []string
	llmChecked bool
	llmProxy   string // the proxy setting the registry's client was made for
	llmForm    llmForm
	// localLLMHook replaces the engine call of the engine's own language
	// model (local_llm.go) in tests.
	localLLMHook llm.EngineCaller
	// llmCalls counts the engine language model's requests under way,
	// for the menu bar icon's dot (dictation_tray.go).
	llmCalls     atomic.Int32
	llmWarmState // local_llm_warm.go
}

// llmForm is Settings → Writing help. Controls bind to its fields, which
// syncLLMForm sets from the settings when they change.
type llmForm struct {
	add       string                   // the preset label chosen in "Add a provider"
	edits     map[string]*providerEdit // by provider id
	hasKey    map[string]bool          // a key is in the Keychain
	models    map[string][]string
	test      map[string]string // the last test's answer
	testErr   map[string]bool
	testing   map[string]bool
	route     string // "Use for writing help" label
	clean     string // "Dictation clean-up" label
	local     bool
	localPick string // "On this Mac (AudioSmith)" model label
}

// providerEdit is what a provider's card binds to.
type providerEdit struct {
	base, model string
	key         string // typed, not saved
}

// syncLLMForm sets the form from a.prefs.
func (a *app) syncLLMForm() {
	f := &a.llmForm
	if f.edits == nil {
		return
	}
	labels, ids := a.routeOptions()
	cur := "auto"
	if a.prefs.LLM.Manual {
		cur = ""
		if len(a.prefs.LLM.Default) > 0 {
			cur = a.prefs.LLM.Default[0].Provider
		}
	}
	f.route = labels[max(slices.Index(ids, cur), 0)]
	cleanLabels, cleanIDs := cleanOptions(labels, ids)
	curClean := ""
	if r := a.prefs.LLM.Skills[string(llm.SkillDictationCleanup)]; len(r) > 0 {
		curClean = r[0].Provider
	}
	f.clean = cleanLabels[max(slices.Index(cleanIDs, curClean), 0)]
	f.local = a.prefs.Privacy.LocalOnlyLLM
	for _, p := range a.prefs.LLM.Providers {
		e := f.edits[p.ID]
		if e == nil {
			e = &providerEdit{}
			f.edits[p.ID] = e
			if pr, ok := llm.LookupPreset(pid(llm.ProviderConfig{ID: p.ID, Preset: p.Preset})); ok {
				a.loadKeyInfo(p.ID, pr)
			}
		}
		e.base, e.model = p.BaseURL, p.Model
	}
}

// cleanOptions are the choices of "Dictation clean-up": the same as
// writing help, or one of the providers.
func cleanOptions(labels, ids []string) ([]string, []string) {
	return append([]string{i18n.T("The same")}, labels[2:]...), append([]string{""}, ids[2:]...)
}

// openLLM builds the registry and router from the settings and looks for
// a local Ollama.
func (a *app) openLLM() {
	a.llmForm = llmForm{edits: map[string]*providerEdit{}, hasKey: map[string]bool{}, models: map[string][]string{},
		test: map[string]string{}, testErr: map[string]bool{}, testing: map[string]bool{}}
	a.llmProxy = a.prefs.Models.Proxy
	a.llmReg = llm.NewRegistry(hub.NewHTTPClient(a.llmProxy, 0), llmSecrets)
	a.llmRouter = &llm.Router{Registry: a.llmReg}
	a.applyLLM()
	a.syncLLMForm()
	a.checkOllama()
	a.checkLocalLLM() // local_llm.go
}

// applyLLM configures the registry and the router from a.prefs.
func (a *app) applyLLM() {
	if a.llmReg == nil {
		return
	}
	s := a.prefs
	if s.Models.Proxy != a.llmProxy {
		a.llmProxy = s.Models.Proxy
		a.llmReg = llm.NewRegistry(hub.NewHTTPClient(a.llmProxy, 0), llmSecrets)
		a.llmRouter.Registry = a.llmReg
	}
	var cfgs []llm.ProviderConfig
	hasOllama := false
	for _, p := range s.LLM.Providers {
		cfg := llm.ProviderConfig{ID: p.ID, Preset: p.Preset, Name: p.Name, BaseURL: p.BaseURL, Model: p.Model,
			Headers: p.Headers, JSONMode: llm.JSONMode(p.JSONMode)}
		if pid(cfg) == "ollama" {
			hasOllama = true
			if cfg.Model == "" {
				cfg.Model = pickOllamaModel(a.llmOllama)
			}
		}
		cfgs = append(cfgs, cfg)
	}
	auto := !s.LLM.Manual && a.llmOllama != nil
	if auto && !hasOllama {
		cfgs = append(cfgs, llm.ProviderConfig{ID: "ollama", Model: pickOllamaModel(a.llmOllama)})
	}
	a.llmReg.Configure(cfgs)
	local := a.registerLocalLLM() // local_llm.go: the engine's own model
	rt := llm.Routing{Default: routes(s.LLM.Default)}
	if len(s.LLM.Skills) > 0 {
		rt.Skills = map[llm.Skill][]llm.Route{}
		for k, v := range s.LLM.Skills {
			rt.Skills[llm.Skill(k)] = routes(v)
		}
	}
	if !s.LLM.Manual {
		rt.Default = nil
		if local {
			rt.Default = append(rt.Default, llm.Route{Provider: localProvider})
		}
		if auto {
			rt.Default = append(rt.Default, llm.Route{Provider: "ollama"})
		}
	}
	a.llmRouter.SetRouting(rt)
	a.llmRouter.SetLocalOnly(s.Privacy.LocalOnlyLLM)
}

func pid(cfg llm.ProviderConfig) string {
	if cfg.Preset != "" {
		return cfg.Preset
	}
	return cfg.ID
}

func routes(rs []settings.LLMRoute) []llm.Route {
	var out []llm.Route
	for _, r := range rs {
		out = append(out, llm.Route{Provider: r.Provider, Model: r.Model, Temperature: r.Temperature})
	}
	return out
}

// pickOllamaModel chooses a chat model among Ollama's: a known family
// first, never an embedding model.
func pickOllamaModel(models []string) string {
	var chat []string
	for _, m := range models {
		if !strings.Contains(strings.ToLower(m), "embed") {
			chat = append(chat, m)
		}
	}
	for _, fam := range []string{"qwen3", "qwen2.5", "gemma3", "llama3", "mistral", "phi"} {
		for _, m := range chat {
			if strings.HasPrefix(strings.ToLower(m), fam) {
				return m
			}
		}
	}
	if len(chat) > 0 {
		return chat[0]
	}
	return ""
}

// checkOllama asks a local Ollama for its models, in the background.
func (a *app) checkOllama() {
	a.background(func() {
		models, err := ollamaModels(context.Background())
		a.post(func() {
			a.llmChecked = true
			if err != nil {
				models = nil
			} else if models == nil {
				models = []string{}
			}
			a.llmOllama = models
			a.applyLLM()
		})
	})
}

// ollamaHTTP asks the local Ollama, without a proxy. One client: a new
// Transport per call kept its idle connection open as long as Ollama did
// (#101 L10).
var ollamaHTTP = &http.Client{Transport: &http.Transport{Proxy: nil, IdleConnTimeout: 30 * time.Second}}

// ollamaModels lists the models of the Ollama on this Mac (its own API,
// which answers even before a model is pulled).
func ollamaModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := ollamaHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := []string{}
	for _, m := range body.Models {
		out = append(out, m.Name)
	}
	return out, nil
}

// llmAvailable reports whether clean-up has a model, and which ("Ollama
// · qwen3:4b").
func (a *app) llmAvailable() (bool, string) {
	if a.llmRouter == nil {
		return false, ""
	}
	rs := a.llmRouter.Routes(llm.SkillDictationCleanup)
	if len(rs) == 0 {
		return false, ""
	}
	return true, a.routeName(rs[0])
}

// routeName names a route: the provider's name and its model.
func (a *app) routeName(r llm.Route) string {
	name := providerName(r.Provider, a.prefs.LLM.Providers)
	model := r.Model
	if model == "" {
		model = a.providerModel(r.Provider)
	}
	if model != "" {
		return name + " · " + model
	}
	return name
}

// providerModel is the model a provider uses by default.
func (a *app) providerModel(id string) string {
	for _, p := range a.prefs.LLM.Providers {
		if p.ID == id {
			cfg := llm.ProviderConfig{ID: p.ID, Preset: p.Preset, BaseURL: p.BaseURL, Model: p.Model}
			if p.Model == "" && pid(cfg) == "ollama" {
				return pickOllamaModel(a.llmOllama)
			}
			if pr, err := cfg.Resolve(); err == nil {
				return pr.DefaultModel
			}
		}
	}
	if id == "ollama" {
		return pickOllamaModel(a.llmOllama)
	}
	if id == localProvider {
		return a.localLLMEntry().Label
	}
	return ""
}

// providerName is a provider's display name.
func providerName(id string, ps []settings.LLMProvider) string {
	if id == localProvider {
		return i18n.T("On this Mac (AudioSmith)")
	}
	for _, p := range ps {
		if p.ID == id && p.Name != "" {
			return p.Name
		}
		if p.ID == id && p.Preset != "" {
			id = p.Preset
		}
	}
	if pr, ok := llm.LookupPreset(id); ok {
		return pr.Name
	}
	return id
}

// vocabulary is the dictation vocabulary hint as a list of terms.
func vocabulary(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' || r == ';' || r == '，' || r == '；' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// cleanTranscript asks the language model to clean the transcript shown,
// a part (about 1500 characters of sentences) at a time; parts it cannot
// clean are polished. It runs in the background and can be stopped.
func (a *app) cleanTranscript() {
	tr := a.trDetail
	if tr == nil || a.trClean != nil {
		return
	}
	ok, who := a.llmAvailable()
	if !ok {
		return
	}
	var parts []string
	var cur []core.TranscriptSegment
	size := 0
	for _, s := range tr.Segments {
		cur = append(cur, s)
		size += len([]rune(s.Text))
		if size >= 1500 {
			parts = append(parts, core.Transcript{Segments: cur}.Text())
			cur, size = nil, 0
		}
	}
	if len(cur) > 0 {
		parts = append(parts, core.Transcript{Segments: cur}.Text())
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := tr.ID
	a.trClean = &trCleaning{id: id, cancel: cancel}
	cleaner := &translate.Cleaner{Router: a.llmRouter, Budget: 45 * time.Second}
	language, vocab := tr.Language, vocabulary(a.prefs.Dictation.Vocabulary)
	a.async(func() {
		var out []core.TranscriptSegment
		fallbacks := 0
		for _, p := range parts {
			if ctx.Err() != nil {
				break
			}
			res := cleaner.Clean(ctx, translate.CleanupRequest{Text: p, Lang: language, Vocabulary: vocab})
			if !res.LLM && res.Fallback != translate.FallbackTooShort {
				fallbacks++
			}
			out = append(out, core.TranscriptSegment{Text: res.Text})
		}
		stopped := ctx.Err() != nil
		cancel()
		text := core.Transcript{Segments: out}.Text()
		a.post(func() {
			if a.trClean == nil || a.trClean.id != id {
				return
			}
			a.trClean = nil
			switch {
			case stopped:
				a.toast(i18n.T("Clean-up stopped"))
				return
			case fallbacks == len(parts):
				a.toast(i18n.T("The language model did not answer: nothing was cleaned up. Check it in Settings → Writing help."))
				return
			case fallbacks > 0:
				a.toast(i18n.T("Some parts could not be cleaned up: they are only polished."))
			}
			if a.trDetail != nil && a.trDetail.ID == id {
				a.updateTranscript(func(t *core.Transcript) { t.Clean, t.CleanBy = text, who })
				a.trOrig = false
			}
		})
	})
}
