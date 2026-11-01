package llm

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"
)

// Skill is a task the app routes to a model.
type Skill string

const (
	SkillTranslate          Skill = "translate"           // dubbing / subtitle translation (Fast)
	SkillTranslateCinematic Skill = "translate_cinematic" // draft → reflect → adapt (Cinematic, Autofit)
	SkillFitRewrite         Skill = "fit_rewrite"         // shorten a line to fit its time slot
	SkillGlossaryExtract    Skill = "glossary_extract"    // propose glossary terms
	SkillDictationCleanup   Skill = "dictation_cleanup"   // fillers, self-corrections
	SkillDirectionParse     Skill = "direction_parse"     // "more urgent" → emotion / instruct / rate
)

// skillParent is the skill whose routes a skill falls back to before the default.
var skillParent = map[Skill]Skill{
	SkillTranslateCinematic: SkillTranslate,
	SkillFitRewrite:         SkillTranslateCinematic,
	SkillGlossaryExtract:    SkillTranslate,
}

// DefaultTemperature is the sampling temperature a skill uses when neither
// the request nor the route sets one.
func DefaultTemperature(s Skill) float64 {
	switch s {
	case SkillTranslateCinematic:
		return 0.6
	case SkillFitRewrite:
		return 0.4
	case SkillTranslate:
		return 0.3
	}
	return 0.1
}

// Route is one provider/model choice for a skill.
type Route struct {
	Provider    string   `json:"provider"`
	Model       string   `json:"model,omitempty"` // "": the provider's default
	Temperature *float64 `json:"temperature,omitempty"`
}

// Routing maps skills to ordered routes. Stored in settings. A skill with
// no routes of its own uses its parent's (translate_cinematic → translate,
// fit_rewrite → translate_cinematic, glossary_extract → translate), then
// Default. Later routes are fallbacks.
type Routing struct {
	Default []Route           `json:"default,omitempty"`
	Skills  map[Skill][]Route `json:"skills,omitempty"`
}

// Resolve returns the routes tried for s, deduplicated, in order: the
// skill's own, then its ancestors', then Default.
func (r Routing) Resolve(s Skill) []Route {
	var out []Route
	seen := map[[2]string]bool{}
	add := func(rs []Route) {
		for _, x := range rs {
			k := [2]string{x.Provider, x.Model}
			if x.Provider == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, x)
		}
	}
	for cur, depth := s, 0; cur != "" && depth < 5; cur, depth = skillParent[cur], depth+1 {
		add(r.Skills[cur])
	}
	add(r.Default)
	return out
}

// NewBackend builds the HTTP backend for cfg.
func NewBackend(cfg ProviderConfig, client *http.Client, secrets Secrets) (Backend, error) {
	p, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	switch p.Transport {
	case "openai":
		return NewOpenAI(p, client, secrets), nil
	case "anthropic":
		return NewAnthropic(p, client, secrets), nil
	}
	return nil, newErr(CodeConfig, "provider", p.ID, "field", "transport")
}

// Registry holds the configured backends by provider id. Goroutine-safe.
type Registry struct {
	client  *http.Client
	secrets Secrets

	mu         sync.RWMutex
	configured map[string]Backend // from Configure
	registered map[string]Backend // from Register (the local engine…); win over configured
}

// NewRegistry returns an empty registry whose HTTP backends use client
// (the app's proxy-aware client; nil: http.DefaultClient) and secrets.
func NewRegistry(client *http.Client, secrets Secrets) *Registry {
	return &Registry{client: client, secrets: secrets, configured: map[string]Backend{}, registered: map[string]Backend{}}
}

// Configure replaces the HTTP backends with cfgs. It builds what it can
// and returns the first error (the others are still configured).
func (r *Registry) Configure(cfgs []ProviderConfig) error {
	next := map[string]Backend{}
	var first error
	for _, c := range cfgs {
		b, err := NewBackend(c, r.client, r.secrets)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		next[b.Info().ID] = b
	}
	r.mu.Lock()
	r.configured = next
	r.mu.Unlock()
	return first
}

// Register adds (or with b nil removes) a backend under id: the local MLX
// engine ("local"), a test fake, a future CLI-agent backend.
func (r *Registry) Register(id string, b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b == nil {
		delete(r.registered, id)
		return
	}
	r.registered[id] = b
}

// Backend returns the backend for id.
func (r *Registry) Backend(id string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if b, ok := r.registered[id]; ok {
		return b, true
	}
	b, ok := r.configured[id]
	return b, ok
}

// Providers describes every backend, sorted by id.
func (r *Registry) Providers() []BackendInfo {
	r.mu.RLock()
	seen := map[string]BackendInfo{}
	for id, b := range r.configured {
		seen[id] = b.Info()
	}
	for id, b := range r.registered {
		seen[id] = b.Info()
	}
	r.mu.RUnlock()
	out := make([]BackendInfo, 0, len(seen))
	for _, i := range seen {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

// Models lists provider id's models.
func (r *Registry) Models(ctx context.Context, id string) ([]ModelInfo, error) {
	b, ok := r.Backend(id)
	if !ok {
		return nil, newErr(CodeUnknown, "provider", id)
	}
	return b.Models(ctx)
}

// Router runs skills over the registry's backends with fallback.
// Goroutine-safe; configure fields before first use, routing any time.
type Router struct {
	Registry *Registry
	Timeout  time.Duration // per attempt; see Client
	Retry    RetryPolicy
	Meter    *Meter
	Sleep    func(ctx context.Context, d time.Duration) error // tests

	mu        sync.RWMutex
	routing   Routing
	localOnly bool
}

// SetRouting replaces the routing table.
func (r *Router) SetRouting(rt Routing) {
	r.mu.Lock()
	r.routing = rt
	r.mu.Unlock()
}

// Routing returns the routing table.
func (r *Router) Routing() Routing {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.routing
}

// SetLocalOnly restricts routing to local backends (Ollama, LM Studio, the
// engine): Settings → Privacy "never send text off this Mac".
func (r *Router) SetLocalOnly(v bool) {
	r.mu.Lock()
	r.localOnly = v
	r.mu.Unlock()
}

// Routes returns the usable routes for s (registered providers, local-only honoured).
func (r *Router) Routes(s Skill) []Route {
	r.mu.RLock()
	rt, local := r.routing, r.localOnly
	r.mu.RUnlock()
	var out []Route
	for _, x := range rt.Resolve(s) {
		b, ok := r.Registry.Backend(x.Provider)
		if !ok || (local && !b.Info().Local) {
			continue
		}
		out = append(out, x)
	}
	return out
}

// Available reports whether s has at least one usable route.
func (r *Router) Available(s Skill) bool {
	return r != nil && r.Registry != nil && len(r.Routes(s)) > 0
}

func (r *Router) client(b Backend) *Client {
	return &Client{Backend: b, Timeout: r.Timeout, Retry: r.Retry, Meter: r.Meter, Sleep: r.Sleep}
}

// Chat runs req for skill s: each route in turn (with the Client's
// retries) until one succeeds. It stops early on cancellation, or once a
// streaming route has emitted text. The error is the last route's.
func (r *Router) Chat(ctx context.Context, s Skill, req Request) (Response, error) {
	return r.run(ctx, s, req, func(c *Client, req Request) (Response, error) { return c.Chat(ctx, req) })
}

// JSON is Chat for JSON output; see Client.JSON.
func (r *Router) JSON(ctx context.Context, s Skill, req Request, out any, repairs int) (Response, error) {
	return r.run(ctx, s, req, func(c *Client, req Request) (Response, error) { return c.JSON(ctx, req, out, repairs) })
}

func (r *Router) run(ctx context.Context, s Skill, req Request, call func(*Client, Request) (Response, error)) (Response, error) {
	if r == nil || r.Registry == nil {
		return Response{}, newErr(CodeNoRoute, "skill", string(s))
	}
	routes := r.Routes(s)
	if len(routes) == 0 {
		return Response{}, newErr(CodeNoRoute, "skill", string(s))
	}
	req.Skill = s
	streamed := false
	if user := req.OnDelta; user != nil {
		req.OnDelta = func(d string) { streamed = true; user(d) }
	}
	var last error
	var used Usage
	for _, rt := range routes {
		b, ok := r.Registry.Backend(rt.Provider)
		if !ok {
			continue
		}
		q := req
		q.Model = rt.Model
		if q.Temperature == nil {
			q.Temperature = rt.Temperature
		}
		if q.Temperature == nil {
			q.Temperature = F64(DefaultTemperature(s))
		}
		resp, err := call(r.client(b), q)
		used = used.Add(resp.Usage)
		if err == nil {
			resp.Usage = used
			return resp, nil
		}
		last = err
		if streamed || ctx.Err() != nil || errors.Is(err, context.Canceled) || Code(err) == CodeCancelled {
			break
		}
	}
	return Response{Usage: used}, last
}

// ProvidersFor returns the provider ids that s could use, in order (for the
// settings page's "will use" hint).
func (r *Router) ProvidersFor(s Skill) []string {
	var out []string
	for _, x := range r.Routes(s) {
		if !slices.Contains(out, x.Provider) {
			out = append(out, x.Provider)
		}
	}
	return out
}
