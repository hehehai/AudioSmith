# internal/llm and internal/translate

Pure-Go LLM layer (no cgo; stdlib, golang.org/x/text and the engine's wire types in internal/engine/proto only) and the text skills built on it:
dubbing translation (§2.5), dictation cleanup (§2.7), Directorial AI / Speech-rate fit tools (§2.8),
LLM providers + per-skill routing (§2.9). Everything blocks and is goroutine-safe: call from
goroutines / jobs executors, never from a build pass. Errors carry a code + params
(`*llm.Error`, `*translate.Error`), never user text; both unwrap to `core.Err*` sentinels.

## internal/llm

| Piece | API |
|---|---|
| Types | `Request{Model, Messages, Temperature, TopP, MaxTokens, Stop, Seed, Format, Schema, OnDelta, Timeout, Skill}`, `Response{Text, FinishReason, Model, Provider, Usage, Latency, Attempts, Repairs, JSON}`, `Usage`, `ModelInfo` |
| Backend (one attempt) | `Backend{Info(); Chat(ctx, Request); Models(ctx)}`; `OnDelta != nil` streams (SSE) |
| Transports | `NewOpenAI(preset, *http.Client, Secrets)` (chat/completions + /models; json_schema / json_object / prompt modes; relaxes `stream_options` and `temperature` when a server rejects them), `NewAnthropic(...)` (Messages API, x-api-key, SSE events), `EngineBackend{Call EngineCaller, DefaultModel, ListModels, MapError}` for the local MLX engine |
| Presets (data) | `presets.json`: openai, anthropic, openrouter, groq, deepseek, mistral, together, siliconflow(+intl), dashscope(+intl), moonshot(+intl), gemini (OpenAI-compat), xai, fireworks, cerebras, zhipu (static model list), nvidia, ollama, lmstudio (local, keyless), custom. `Presets()`, `LookupPreset`, `ProviderConfig{ID, Preset, Name, BaseURL, Model, Headers, JSONMode}.Resolve()` (loopback base URL ⇒ `Local`) |
| Keys | `Secrets{Secret(ctx, name)}` → `ErrNoSecret` when absent; names `llm.<provider>` (`Preset.SecretName()`); `StaticSecrets`, `EnvSecrets` (dev), `SecretsFunc` |
| Client | `Client{Backend, Timeout, Retry RetryPolicy, Meter, Sleep}`: `Chat` (per-attempt timeout, retries 429/5xx/529/timeouts/resets with full-jitter backoff, honours Retry-After up to 60 s, never retries after streamed text, strips `<think>` blocks incl. from deltas, estimates usage when absent), `JSON(ctx, req, &out, repairs)` (extract → validate `Schema` → decode, repair turns quoting the errors) |
| Schema | `Object/Array/String/Number/Integer/Boolean/Enum`, `.Range`, `.Count`, `.Describe`, `Nullable`, `Closed`; `Validate`, `ExtractJSON` |
| Usage | `Meter` (per provider/model/skill: calls, failures, attempts, tokens, elapsed; `OnRecord` hook for persisting), `EstimateTokens` |
| Routing | `Skill`: translate, translate_cinematic, fit_rewrite, glossary_extract, dictation_cleanup, direction_parse. `Routing{Default, Skills map[Skill][]Route}` (stored in settings; skill → parent → default: cinematic→translate, fit_rewrite→cinematic, glossary→translate). `Registry` (`Configure([]ProviderConfig)`, `Register(id, Backend)` for the engine / fakes, `Providers`, `Models`). `Router{Registry, Timeout, Retry, Meter}`: `Chat/JSON(ctx, skill, req…)` try routes in order (fallback on anything but cancellation / streamed output), `SetRouting`, `SetLocalOnly` (privacy: local backends only), `Routes/Available/ProvidersFor`, `DefaultTemperature(skill)` |
| Engine seam | `MethodGenerate = "llm.generate"` (params `GenerateParams`, result `GenerateResult`), notification `llm.token {job, text}`: aliases of `internal/engine/proto`'s types (protocol-v2.md §5.4); engine errors (`context_too_long`, `model_not_downloaded`, `busy`…) map to llm codes. The app registers it as provider `local` (`local_llm.go`) |

## internal/translate

| Piece | API |
|---|---|
| Translator | `Translator{Info(); Translate(ctx, Request) (Result, error)}`; `Request{Source, Target (BCP-47 incl. dialect), Segments []Segment{ID, Text, Speaker, Slot, Note}, Quality fast/cinematic/autofit, Glossary, Style, Formality, Context, ContextLines, SuggestShorter, Speed, Progress}` → `Result{Segments []Translated{ID, Text, Draft, Shorter, Fit, Warnings, Err}, Usage, Failed}`. Per-line failures are on `Translated.Err`; a returned error means the whole call failed (auth, network, no route, cancel) |
| LLM translator | `&LLM{Router, Predictor, BatchLines, BatchChars, Repairs}`: batches (20 lines / 2500 chars) with N neighbouring lines as context (earlier translations included), relevant glossary terms, style / register / dialect notes, `[tags]`+`[[overrides]]` masked as ⟦n⟧ and verified, ids verified, missing/broken lines retried one by one (lost tags finally re-inserted at the nearer edge + warning), fatal errors stop at once. Cinematic: draft → reflect → adapt (only lines with issues; `Draft` keeps the first pass). Autofit: slot budgets (maxSeconds/maxChars) in the prompt, then `fit_rewrite` for predicted overflows (`Draft` keeps the long one); `SuggestShorter` fills `Shorter` instead |
| MT adapters | `NewDeepL(MTConfig)`, `NewGoogle(MTConfig)` (v2, API key), `NewMicrosoft(MTConfig, region)`; keys `mt.deepl`, `mt.google`, `mt.microsoft`; `Info.Network/ThirdParty = true`; HTML mode with `translate="no"` spans protects tags and forces glossary targets; dialect code maps; batching; retries 429/5xx. `WithFit(t, &Fitter{Router})` adds fit badges + Autofit rewrites to any translator |
| Glossary | `Term{Source, Target, Note, Auto, CaseSensitive}`, `Glossary.Normalize/Merge(auto)/Relevant(texts…)` (manual wins; word-boundary matching, CJK substring), `Extractor{Router, Max}.Extract(ctx, src, tgt, segments, existing, style)` → `[]Candidate{Term, Count}` (hallucinated terms dropped, counted locally) |
| Cleanup | `Polish(text, lang, PolishOptions)` deterministic (fillers um/uh/嗯/呃, spacing, CJK spacing + full-width punctuation, capitalisation, i→I, terminal punctuation); `Cleaner{Router, Budget 4 s}.Clean(ctx, CleanupRequest{Text, Lang, Vocabulary, NoLLM})` → `CleanupResult{Text, Raw, Polished, LLM, Fallback, Detail, Warnings}`; never fails, rejects replies that answer/rewrite instead of clean |
| Direction | `ParseDirection(note)` keyword parser (en + zh, intensity "a bit"/"very"/有点/非常, negation "less"/不要); `Director{Router}.Parse(ctx, note)` LLM with fallback → `Direction{Emotions (Taxonomy), Instruct, Rate −0.5…0.5, Pitch ±4 st, Energy ±1, TranslateHint, Source, Fallback}` |
| Rate fit | `Units(text, lang)` (syllables / morae / Han chars / Hangul blocks / aksharas + punctuation, `[pause]`, non-verbal, `[slow]/[fast]`, `[[override]]` respelling), `Predictor{Rates, Thresholds}.Duration/Fit/Budget/Calibrate` (learns a voice's rate), `DefaultRates`; `Fitter.Check(ctx, FitRequest)` = the Speech-rate fit tool |
| Prompts | `prompts/*.tmpl` (embedded `text/template`, written for AudioSmith): translate, reflect, adapt, fit, glossary, cleanup, direction; per-language notes `notes:<variant>` (zh-Hans, zh-Hant (TW/HK), ja, ko, es / es-ES / es-MX / es-419, pt / pt-BR / pt-PT, en-GB / en-US, fr / fr-CA, de, ar, ru) |

## Wiring sketch (app side, not done here)

```go
secrets := llm.SecretsFunc(func(ctx context.Context, name string) (string, error) {
	v, err := hub.Keychain{Service: "AudioSmith", Account: name}.Get()
	if err == nil && v == "" { err = llm.ErrNoSecret }
	return v, err
})
reg := llm.NewRegistry(proxyClient /* see Decisions: needs hub to export its proxy func */, secrets)
reg.Configure(settings.LLM.Providers)
reg.Register("local", &llm.EngineBackend{Call: func(ctx context.Context, m string, p any, n func(string, json.RawMessage)) (json.RawMessage, error) {
	return sup.Call(ctx, proto.RoleGPU, "", m, p, func(msg proto.Message) { n(msg.Method, msg.Params) })
}, DefaultModel: "<catalogue llm entry>"})
router := &llm.Router{Registry: reg, Meter: meter}
router.SetRouting(settings.LLM.Routing)
router.SetLocalOnly(settings.Privacy.LocalOnly)
tr := &translate.LLM{Router: router}
```
