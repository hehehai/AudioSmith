// Package translate turns segments of text into another language and
// holds the text skills built on internal/llm: translation (LLM with Fast /
// Cinematic / Autofit quality, and online MT adapters: DeepL, Google Cloud,
// Microsoft), glossaries, dictation cleanup, direction parsing and the
// speech-rate fit predictor.
//
// Everything here blocks and is goroutine-safe: call it off the UI thread
// (normally from a jobs executor on the net or cpu lane). Errors are
// *Error / *llm.Error values with codes and params, never user text.
package translate

import (
	"context"
	"errors"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/llm"
)

// Segment is one line to translate.
type Segment struct {
	ID      string  `json:"id"` // unique within a request
	Text    string  `json:"text"`
	Speaker string  `json:"speaker,omitempty"`
	Slot    float64 `json:"slot,omitempty"` // seconds available to speak it; 0 = unknown
	Note    string  `json:"note,omitempty"` // direction / context for this line ("whispered", "on-screen sign")
}

// Quality is the translation mode.
type Quality string

const (
	Fast      Quality = "fast"      // one pass
	Cinematic Quality = "cinematic" // draft → reflect → adapt
	Autofit   Quality = "autofit"   // one slot-aware pass, then rewrite lines predicted to overflow their slot
)

// Formality is the register asked for.
type Formality string

const (
	FormalityAuto     Formality = ""
	FormalityFormal   Formality = "formal"
	FormalityInformal Formality = "informal"
)

// Request is a translation job.
type Request struct {
	Source string // BCP-47 ("en", "zh-Hans"); "" or "auto" lets the translator detect it
	// Target is the BCP-47 target including any dialect: "es-MX",
	// "pt-BR", "zh-Hant-TW", "en-GB", "fr-CA".
	Target   string
	Segments []Segment
	Quality  Quality // "" = Fast

	Glossary  Glossary
	Style     string    // free-form style notes (≤ MaxStyle runes), e.g. "noir thriller, terse, period slang"
	Formality Formality // register
	Context   string    // what the material is (title, synopsis), ≤ MaxStyle runes

	// ContextLines is how many neighbouring lines each side are shown to
	// the model as context (0: DefaultContextLines; <0: none).
	ContextLines int
	// SuggestShorter fills Translated.Shorter for lines predicted to
	// overflow their slot (Fast and Cinematic; Autofit replaces the text).
	SuggestShorter bool
	// Speed is the TTS speed factor the fit prediction assumes (0: 1).
	Speed float64

	// Progress, if set, is called after each stage of each batch. It must not block.
	Progress func(Progress)
}

// Limits.
const (
	MaxStyle            = 5000
	DefaultContextLines = 2
)

// Progress reports translation progress.
type Progress struct {
	Stage string // "translate", "reflect", "adapt", "retry", "fit"
	Done  int    // segments finished in this stage
	Total int
}

// Translated is one output line, in the order of Request.Segments.
type Translated struct {
	ID   string `json:"id"`
	Text string `json:"text"` // "" when Err is set
	// Draft is the first-pass text when a later pass (Cinematic adapt,
	// Autofit rewrite) replaced it.
	Draft string `json:"draft,omitempty"`
	// Shorter is a shorter alternative for a line predicted to overflow
	// (SuggestShorter), "" when there is none.
	Shorter  string   `json:"shorter,omitempty"`
	Fit      *Fit     `json:"fit,omitempty"` // nil when the segment has no slot
	Warnings []string `json:"warnings,omitempty"`
	Err      *Error   `json:"error,omitempty"` // this line failed; the others may have succeeded
}

// Warning codes on Translated.Warnings.
const (
	WarnTagsRepaired = "translate.tags_repaired" // the model dropped/garbled [tags]; they were put back at the line's edge
	WarnUnchanged    = "translate.unchanged"     // output equals the source (names-only lines are fine)
	WarnFitFailed    = "translate.fit_failed"    // the shortening rewrite failed or was rejected (mixed scripts, cut too far); the line may overflow
	WarnAdaptFailed  = "translate.adapt_failed"  // Cinematic adapt failed for this line; the draft is kept
)

// Result is a finished translation.
type Result struct {
	Segments   []Translated
	Translator string    // Info.ID
	Usage      llm.Usage // LLM translators only
	Failed     int       // lines with Err set
}

// Info describes a translator.
type Info struct {
	ID         string    // "llm", "deepl", "google", "microsoft"
	Kind       string    // "llm" | "mt"
	Network    bool      // sends text over the network (an LLM route may be local)
	ThirdParty bool      // a third-party service
	Qualities  []Quality // supported modes
	Glossary   bool      // honours Request.Glossary
	Detects    bool      // accepts Source "" / "auto"
}

// Translator translates segments. A returned error means nothing usable
// came back (bad request, auth, network…); per-line failures are on
// Translated.Err with a nil error.
type Translator interface {
	Info() Info
	Translate(ctx context.Context, req Request) (Result, error)
}

// Error codes. Params in comments.
const (
	CodeNoSegments   = "translate.no_segments"   //
	CodeDuplicateID  = "translate.duplicate_id"  // id
	CodeLanguage     = "translate.language"      // lang, service: not supported / unknown
	CodeSameLanguage = "translate.same_language" // lang
	CodeLineFailed   = "translate.line_failed"   // id, reason (a code)
	CodeMissing      = "translate.missing_line"  // id: the model never returned it
	CodeTagsLost     = "translate.tags_lost"     // id
	CodeNoKey        = "translate.no_key"        // service
	CodeAuth         = "translate.auth"          // service, status
	CodeQuota        = "translate.quota"         // service, status: quota / rate limit
	CodeHTTP         = "translate.http"          // service, status, detail
	CodeNetwork      = "translate.network"       // service, detail
	CodeBadResponse  = "translate.bad_response"  // service, detail
	CodeTooLong      = "translate.too_long"      // service, limit
	CodeCancelled    = "translate.cancelled"     //
	CodeUnsupported  = "translate.unsupported"   // feature
	CodeNoLLM        = "translate.no_llm"        // skill: no LLM route configured
	CodeConfig       = "translate.config"        // service, field
)

// Error is this package's error.
type Error struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
	Err    error             `json:"-"`
}

func (e *Error) Error() string { return llm.ErrorText(e.Code, e.Params, e.Err) }

func (e *Error) Unwrap() []error {
	var out []error
	if e.Err != nil {
		out = append(out, e.Err)
	}
	switch e.Code {
	case CodeCancelled:
		out = append(out, core.ErrCancelled)
	case CodeNoSegments, CodeDuplicateID, CodeLanguage, CodeSameLanguage, CodeTooLong, CodeConfig:
		out = append(out, core.ErrInvalid)
	case CodeUnsupported, CodeNoLLM:
		out = append(out, core.ErrUnsupported)
	case CodeQuota:
		out = append(out, core.ErrBusy)
	}
	return out
}

// Is matches by code (errors.Is(err, &translate.Error{Code: translate.CodeAuth})).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Params == nil
}

func newErr(code string, params ...string) *Error {
	return &Error{Code: code, Params: llm.Params(params...)}
}

// Code returns the code of err: a *translate.Error's, else an *llm.Error's, else "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return llm.Code(err)
}

// lineErr wraps a cause into a per-line error.
func lineErr(id string, cause error) *Error {
	reason := Code(cause)
	if reason == "" {
		reason = "unknown"
	}
	e := newErr(CodeLineFailed, "id", id, "reason", reason)
	e.Err = cause
	return e
}

// isCancel reports whether err is a cancellation.
func isCancel(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, core.ErrCancelled)
}

// validate checks the request shape shared by every translator.
func validate(req *Request) error {
	if len(req.Segments) == 0 {
		return newErr(CodeNoSegments)
	}
	seen := make(map[string]bool, len(req.Segments))
	for _, s := range req.Segments {
		if s.ID == "" || seen[s.ID] {
			return newErr(CodeDuplicateID, "id", s.ID)
		}
		seen[s.ID] = true
	}
	if req.Quality == "" {
		req.Quality = Fast
	}
	if req.Speed <= 0 {
		req.Speed = 1
	}
	if req.Source == "auto" {
		req.Source = ""
	}
	if _, ok := parseLang(req.Target); !ok {
		return newErr(CodeLanguage, "lang", req.Target)
	}
	if req.Source != "" {
		src, ok := parseLang(req.Source)
		if !ok {
			return newErr(CodeLanguage, "lang", req.Source)
		}
		tgt, _ := parseLang(req.Target)
		// Same language unless the target names another variant (en-US → en-GB, zh-Hans → zh-Hant).
		if src.Code == tgt.Code && (src.Variant() == tgt.Variant() || tgt.Variant() == tgt.Code) {
			return newErr(CodeSameLanguage, "lang", req.Target)
		}
	}
	req.Style = clipRunes(req.Style, MaxStyle)
	req.Context = clipRunes(req.Context, MaxStyle)
	return nil
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func countFailed(res *Result) {
	res.Failed = 0
	for _, s := range res.Segments {
		if s.Err != nil {
			res.Failed++
		}
	}
}
