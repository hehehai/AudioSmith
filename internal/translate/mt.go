package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/actnowkit/redact"
)

// Online machine translation. Each service is a third-party network
// service with its own key (Secrets name "mt.<service>"). Text goes as
// HTML: [tags] (as ⟦n⟧ markers) and glossary targets are wrapped in
// <span translate="no" class="notranslate">, which DeepL, Google and
// Microsoft all leave untranslated, so tags survive and glossary terms
// come out as the glossary says.
//
// Files: mt.go (the shared adapter: protection, batching, retries, HTTP),
// mt_services.go (DeepL, Google, Microsoft).

// MTSecretPrefix starts MT key names: "mt.deepl", "mt.google", "mt.microsoft".
const MTSecretPrefix = "mt."

// MTConfig is what every MT adapter needs.
type MTConfig struct {
	Client  *http.Client // the app's proxy-aware client; nil: http.DefaultClient
	Secrets llm.Secrets  // keys; the app backs it with the Keychain
	BaseURL string       // overrides the service endpoint (tests, regional endpoints)
	// MaxAttempts per request, retrying 429/5xx/network errors (0: 3).
	MaxAttempts int
	Sleep       func(ctx context.Context, d time.Duration) error // tests
}

// mtService is one provider's wire format.
type mtService interface {
	id() string
	maxBatch() (texts, chars int)
	// code maps a language to the service's code; ok false if unsupported.
	code(l Lang, target bool) (string, bool)
	// call translates htmls (protected HTML) and returns them in order.
	call(ctx context.Context, m *mtTranslator, htmls []string, src, tgt string, req Request) ([]string, error)
}

type mtTranslator struct {
	cfg MTConfig
	svc mtService
}

func (m *mtTranslator) Info() Info {
	return Info{ID: m.svc.id(), Kind: "mt", Network: true, ThirdParty: true, Qualities: []Quality{Fast}, Glossary: true, Detects: true}
}

const noTranslateOpen, noTranslateClose = `<span translate="no" class="notranslate">`, `</span>`

var (
	reNoTranslate = regexp.MustCompile(`(?s)<span[^>]*notranslate[^>]*>(.*?)</span>`)
	reAnyTag      = regexp.MustCompile(`<[^>]+>`)
)

// protectHTML escapes masked text, wraps its markers and replaces glossary
// sources with wrapped targets.
func protectHTML(text string, g Glossary) string {
	type span struct {
		s, e   int
		target string
	}
	var spans []span
	taken := make([]bool, len(text))
	for _, t := range g { // Relevant: longest first
		for _, r := range t.match(text) {
			if anyUsed(taken, r[0], r[1]) {
				continue
			}
			for k := r[0]; k < r[1]; k++ {
				taken[k] = true
			}
			spans = append(spans, span{r[0], r[1], t.Target})
		}
	}
	// Order by position.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].s < spans[j-1].s; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	var b strings.Builder
	plain := func(s string) {
		esc := html.EscapeString(s)
		b.WriteString(markerRE.ReplaceAllStringFunc(esc, func(x string) string { return noTranslateOpen + x + noTranslateClose }))
	}
	last := 0
	for _, sp := range spans {
		plain(text[last:sp.s])
		b.WriteString(noTranslateOpen + html.EscapeString(sp.target) + noTranslateClose)
		last = sp.e
	}
	plain(text[last:])
	return b.String()
}

// unprotectHTML undoes protectHTML on a service's output.
func unprotectHTML(s string) string {
	s = reNoTranslate.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = reAnyTag.ReplaceAllString(s, "")
	return strings.TrimSpace(collapseSpaces(html.UnescapeString(s)))
}

func (m *mtTranslator) Translate(ctx context.Context, req Request) (Result, error) {
	if err := validate(&req); err != nil {
		return Result{}, err
	}
	if req.Quality != Fast {
		return Result{}, newErr(CodeUnsupported, "feature", "quality."+string(req.Quality))
	}
	tgt, _ := parseLang(req.Target)
	tcode, ok := m.svc.code(tgt, true)
	if !ok {
		return Result{}, newErr(CodeLanguage, "lang", req.Target, "service", m.svc.id())
	}
	scode := ""
	if req.Source != "" {
		src, _ := parseLang(req.Source)
		if scode, ok = m.svc.code(src, false); !ok {
			return Result{}, newErr(CodeLanguage, "lang", req.Source, "service", m.svc.id())
		}
	}
	res := Result{Translator: m.svc.id(), Segments: make([]Translated, len(req.Segments))}
	masks := make([]masked, len(req.Segments))
	htmls := make([]string, len(req.Segments))
	for i, s := range req.Segments {
		res.Segments[i].ID = s.ID
		masks[i] = mask(s.Text)
		htmls[i] = protectHTML(masks[i].Text, req.Glossary.Relevant(s.Text))
	}
	maxN, maxC := m.svc.maxBatch()
	done := 0
	for start := 0; start < len(htmls); {
		end, chars := start, 0
		for end < len(htmls) && end-start < maxN && (end == start || chars+len(htmls[end]) <= maxC) {
			chars += len(htmls[end])
			end++
		}
		if len(htmls[start]) > maxC {
			res.Segments[start].Err = lineErr(req.Segments[start].ID, newErr(CodeTooLong, "service", m.svc.id(), "limit", strconv.Itoa(maxC)))
			start = start + 1
			continue
		}
		// Empty lines skip the service.
		var idx []int
		var batch []string
		for i := start; i < end; i++ {
			if strings.TrimSpace(htmls[i]) == "" {
				continue
			}
			idx = append(idx, i)
			batch = append(batch, htmls[i])
		}
		if len(batch) > 0 {
			out, err := m.retry(ctx, func() ([]string, error) { return m.svc.call(ctx, m, batch, scode, tcode, req) })
			if err != nil {
				countFailed(&res)
				return res, err
			}
			if len(out) != len(batch) {
				return res, newErr(CodeBadResponse, "service", m.svc.id(), "detail", "count")
			}
			for k, i := range idx {
				text, ok := masks[i].unmask(unprotectHTML(out[k]))
				res.Segments[i].Text = text
				if !ok {
					res.Segments[i].Warnings = append(res.Segments[i].Warnings, WarnTagsRepaired)
				}
			}
		}
		done += end - start
		if req.Progress != nil {
			req.Progress(Progress{Stage: "translate", Done: done, Total: len(req.Segments)})
		}
		start = end
	}
	countFailed(&res)
	return res, nil
}

func (m *mtTranslator) retry(ctx context.Context, f func() ([]string, error)) ([]string, error) {
	attempts := m.cfg.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	for a := 1; ; a++ {
		out, err := f()
		if err == nil {
			return out, nil
		}
		var r *retryable
		if !errors.As(err, &r) || a >= attempts || ctx.Err() != nil {
			if errors.As(err, &r) {
				return nil, r.err
			}
			return nil, err
		}
		d := r.after
		if d <= 0 {
			base := 500 * time.Millisecond << (a - 1)
			d = base/2 + time.Duration(rand.Float64()*float64(base/2))
		}
		if d > 30*time.Second {
			return nil, r.err
		}
		sleep := m.cfg.Sleep
		if sleep == nil {
			sleep = func(ctx context.Context, d time.Duration) error {
				t := time.NewTimer(d)
				defer t.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-t.C:
					return nil
				}
			}
		}
		if err := sleep(ctx, d); err != nil {
			return nil, cancelled(err)
		}
	}
}

// retryable marks an error worth retrying.
type retryable struct {
	err   *Error
	after time.Duration
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

func (m *mtTranslator) key(ctx context.Context) (string, error) {
	name := MTSecretPrefix + m.svc.id()
	if m.cfg.Secrets == nil {
		return "", newErr(CodeNoKey, "service", m.svc.id())
	}
	k, err := m.cfg.Secrets.Secret(ctx, name)
	if err != nil || strings.TrimSpace(k) == "" {
		e := newErr(CodeNoKey, "service", m.svc.id())
		if err != nil && !errors.Is(err, llm.ErrNoSecret) {
			e.Err = err
		}
		return "", e
	}
	return strings.TrimSpace(k), nil
}

// do sends a JSON request and decodes a 2xx JSON response into out.
func (m *mtTranslator) do(ctx context.Context, method, url string, header http.Header, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		e := newErr(CodeConfig, "service", m.svc.id(), "field", "baseURL")
		e.Err = err
		return e
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", llm.UserAgent)
	c := m.cfg.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return cancelled(ctx.Err())
		}
		e := newErr(CodeNetwork, "service", m.svc.id(), "detail", redact.Text(err.Error())) // no key or token in a URL
		e.Err = err
		var ne net.Error
		if !strings.Contains(err.Error(), "connection refused") || errors.As(err, &ne) && ne.Timeout() {
			return &retryable{err: e}
		}
		return e
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		if ctx.Err() != nil {
			return cancelled(ctx.Err())
		}
		e := newErr(CodeNetwork, "service", m.svc.id(), "detail", redact.Text(err.Error())) // no key or token in a URL
		return &retryable{err: e}
	}
	if resp.StatusCode/100 != 2 {
		return m.httpErr(resp, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		e := newErr(CodeBadResponse, "service", m.svc.id(), "detail", clipRunes(string(raw), 200))
		e.Err = err
		return e
	}
	return nil
}

func (m *mtTranslator) httpErr(resp *http.Response, raw []byte) error {
	id, st := m.svc.id(), resp.StatusCode
	detail := strings.ToLower(string(raw))
	status := strconv.Itoa(st)
	var after time.Duration
	if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		after = time.Duration(v) * time.Second
	}
	switch {
	case st == 456 || strings.Contains(detail, "quota") || strings.Contains(detail, "limit exceeded") || strings.Contains(detail, "ratelimit"):
		e := newErr(CodeQuota, "service", id, "status", status)
		if st == 429 || strings.Contains(detail, "ratelimitexceeded") || strings.Contains(detail, "too many") {
			return &retryable{err: e, after: after}
		}
		return e
	case st == 429:
		return &retryable{err: newErr(CodeQuota, "service", id, "status", status), after: after}
	case st == 401 || st == 403 || strings.Contains(detail, "api key not valid") || strings.Contains(detail, "invalid api key"):
		return newErr(CodeAuth, "service", id, "status", status)
	case st == 413 || st == 414:
		return newErr(CodeTooLong, "service", id, "limit", "")
	case st >= 500:
		return &retryable{err: newErr(CodeHTTP, "service", id, "status", status, "detail", clipRunes(string(raw), 200)), after: after}
	}
	return newErr(CodeHTTP, "service", id, "status", status, "detail", clipRunes(string(raw), 200))
}

func (m *mtTranslator) base(def string) string {
	if m.cfg.BaseURL != "" {
		return strings.TrimRight(m.cfg.BaseURL, "/")
	}
	return def
}
