package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/llm"
)

type mtFake struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
	body []string
}

// newMTFake serves handler, recording requests.
func newMTFake(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte, n int)) *mtFake {
	f := &mtFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, r)
		f.body = append(f.body, string(b))
		n := len(f.reqs)
		f.mu.Unlock()
		handler(w, r, b, n)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

var spanRE = regexp.MustCompile(`(<span[^>]*notranslate[^>]*>.*?</span>)`)

// fakeTranslate upper-cases text outside no-translate spans and escapes
// an apostrophe as an entity, like the real services do.
func fakeTranslate(s string) string {
	parts := spanRE.Split(s, -1)
	spans := spanRE.FindAllString(s, -1)
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(strings.ReplaceAll(strings.ToUpper(p), "'", "&#39;"))
		if i < len(spans) {
			b.WriteString(spans[i])
		}
	}
	return b.String()
}

func mtCfg(f *mtFake, keys llm.StaticSecrets) MTConfig {
	return MTConfig{Client: f.srv.Client(), Secrets: keys, BaseURL: f.srv.URL, Sleep: func(context.Context, time.Duration) error { return nil }}
}

func TestDeepL(t *testing.T) {
	f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, _ int) {
		var in struct{ Text []string }
		_ = json.Unmarshal(body, &in)
		var out struct {
			Translations []map[string]string `json:"translations"`
		}
		for _, s := range in.Text {
			out.Translations = append(out.Translations, map[string]string{"text": fakeTranslate(s), "detected_source_language": "EN"})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	tr := NewDeepL(mtCfg(f, llm.StaticSecrets{"mt.deepl": "k:fx"}))
	if in := tr.Info(); !in.Network || !in.ThirdParty || in.ID != "deepl" {
		t.Fatal(in)
	}
	res, err := tr.Translate(context.Background(), Request{
		Source: "en", Target: "zh-TW", Formality: FormalityFormal, Context: "a heist film",
		Segments: []Segment{{ID: "a", Text: "Ocean's crew [laughter] meets Danny & co"}, {ID: "b", Text: ""}},
		Glossary: Glossary{{Source: "Danny", Target: "丹尼"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Segments[0].Text; got != "OCEAN'S CREW [laughter] MEETS 丹尼 & CO" {
		t.Fatalf("text %q", got)
	}
	if res.Segments[1].Text != "" || res.Segments[1].Err != nil {
		t.Fatalf("empty line %+v", res.Segments[1])
	}
	r := f.reqs[0]
	if r.URL.Path != "/v2/translate" || r.Header.Get("Authorization") != "DeepL-Auth-Key k:fx" {
		t.Fatalf("%s %v", r.URL.Path, r.Header)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.body[0]), &body)
	if body["target_lang"] != "ZH-HANT" || body["source_lang"] != "EN" || body["tag_handling"] != "html" || body["formality"] != "prefer_more" || body["context"] != "a heist film" {
		t.Fatalf("body %v", body)
	}
	if texts := body["text"].([]any); len(texts) != 1 || !strings.Contains(texts[0].(string), `notranslate">⟦1⟧</span>`) || !strings.Contains(texts[0].(string), "&amp;") {
		t.Fatalf("texts %v", texts)
	}
}

func TestDeepLCodes(t *testing.T) {
	d := deepl{}
	cases := map[string]string{"en": "EN-US", "en-GB": "EN-GB", "pt": "PT-BR", "pt-PT": "PT-PT", "zh": "ZH-HANS", "zh-Hant": "ZH-HANT", "es-MX": "ES-419", "es": "ES", "nb": "NB", "ja": "JA"}
	for tag, want := range cases {
		l, _ := parseLang(tag)
		if got, ok := d.code(l, true); !ok || got != want {
			t.Errorf("%s → %s, want %s", tag, got, want)
		}
	}
	l, _ := parseLang("sw")
	if _, ok := d.code(l, true); ok {
		t.Error("deepl has no Swahili")
	}
	f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, _ int) {})
	if _, err := NewDeepL(mtCfg(f, nil)).Translate(context.Background(), Request{Target: "sw", Segments: segs("x")}); Code(err) != CodeLanguage {
		t.Fatal(err)
	}
}

func TestGoogleV2Batches(t *testing.T) {
	f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, _ int) {
		var in struct {
			Q      []string
			Target string
			Format string
		}
		_ = json.Unmarshal(body, &in)
		var tr []map[string]string
		for _, q := range in.Q {
			tr = append(tr, map[string]string{"translatedText": fakeTranslate(q)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"translations": tr}})
	})
	var ss []Segment
	for i := range 130 {
		ss = append(ss, Segment{ID: fmt.Sprint(i), Text: fmt.Sprintf("line %d it's", i)})
	}
	var prog []Progress
	res, err := NewGoogle(mtCfg(f, llm.StaticSecrets{"mt.google": "g key"})).Translate(context.Background(), Request{Target: "he", Segments: ss, Progress: func(p Progress) { prog = append(prog, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 || res.Segments[129].Text != "LINE 129 IT'S" {
		t.Fatalf("%d requests, last %q", len(f.reqs), res.Segments[129].Text)
	}
	// The key in a header, never in the URL (it would reach error messages and logs).
	if f.reqs[0].Header.Get("X-Goog-Api-Key") != "g key" || f.reqs[0].URL.RawQuery != "" || f.reqs[0].URL.Path != "/language/translate/v2" {
		t.Fatal(f.reqs[0].URL)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.body[0]), &body)
	if body["target"] != "iw" || body["format"] != "html" || body["source"] != nil || len(body["q"].([]any)) != 100 {
		t.Fatalf("body target=%v format=%v", body["target"], body["format"])
	}
	if len(prog) != 2 || prog[1].Done != 130 {
		t.Fatal(prog)
	}
}

func TestMicrosoft(t *testing.T) {
	f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, _ int) {
		var in []struct{ Text string }
		_ = json.Unmarshal(body, &in)
		var out []any
		for _, x := range in {
			out = append(out, map[string]any{"translations": []any{map[string]string{"text": fakeTranslate(x.Text), "to": "pt"}}})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	res, err := NewMicrosoft(mtCfg(f, llm.StaticSecrets{"mt.microsoft": "ms-key"}), "westeurope").Translate(context.Background(), Request{Source: "en", Target: "pt-PT", Segments: segs("hi [pause 1s] there")})
	if err != nil || res.Segments[0].Text != "HI [pause 1s] THERE" {
		t.Fatal(err, res.Segments)
	}
	r := f.reqs[0]
	q := r.URL.Query()
	if r.URL.Path != "/translate" || q.Get("to") != "pt-pt" || q.Get("from") != "en" || q.Get("textType") != "html" || q.Get("api-version") != "3.0" {
		t.Fatal(r.URL)
	}
	if r.Header.Get("Ocp-Apim-Subscription-Key") != "ms-key" || r.Header.Get("Ocp-Apim-Subscription-Region") != "westeurope" {
		t.Fatal(r.Header)
	}
}

func TestMTErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
		calls  int
	}{
		{403, `{"message":"Wrong key"}`, CodeAuth, 1},
		{456, `{"message":"Quota exceeded"}`, CodeQuota, 1},
		{429, `{"message":"Too many requests"}`, CodeQuota, 3},
		{503, `oops`, CodeHTTP, 3},
		{400, `{"message":"bad target"}`, CodeHTTP, 1},
	}
	for _, c := range cases {
		f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, _ int) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		_, err := NewDeepL(mtCfg(f, llm.StaticSecrets{"mt.deepl": "k"})).Translate(context.Background(), Request{Target: "de", Segments: segs("x")})
		if Code(err) != c.code || len(f.reqs) != c.calls {
			t.Errorf("%d: %v after %d calls, want %s after %d", c.status, err, len(f.reqs), c.code, c.calls)
		}
	}
	// A retry that then succeeds.
	f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, n int) {
		if n == 1 {
			w.WriteHeader(502)
			return
		}
		_, _ = io.WriteString(w, `{"translations":[{"text":"ok"}]}`)
	})
	res, err := NewDeepL(mtCfg(f, llm.StaticSecrets{"mt.deepl": "k"})).Translate(context.Background(), Request{Target: "de", Segments: segs("x")})
	if err != nil || res.Segments[0].Text != "ok" {
		t.Fatal(err)
	}
	// No key.
	if _, err := NewMicrosoft(mtCfg(f, llm.StaticSecrets{}), "").Translate(context.Background(), Request{Target: "de", Segments: segs("x")}); Code(err) != CodeNoKey {
		t.Fatal(err)
	}
	// Quality other than Fast without WithFit.
	if _, err := NewGoogle(mtCfg(f, llm.StaticSecrets{"mt.google": "k"})).Translate(context.Background(), Request{Target: "de", Quality: Cinematic, Segments: segs("x")}); Code(err) != CodeUnsupported {
		t.Fatal(err)
	}
}

func TestMTWithFit(t *testing.T) {
	f := newMTFake(t, func(w http.ResponseWriter, r *http.Request, body []byte, _ int) {
		_, _ = io.WriteString(w, `{"translations":[{"text":"Una frase muy pero que muy larga para un hueco tan corto <span translate=\"no\" class=\"notranslate\">⟦1⟧</span>"}]}`)
	})
	m := &model{handle: func(sys, user string) (string, error) {
		var lines []lineJSON
		jsonAfterNoT(user, "Lines to shorten:\n", &lines)
		if len(lines) != 1 || !strings.Contains(lines[0].Text, "⟦1⟧") {
			return "", fmt.Errorf("fit got %+v", lines)
		}
		return linesReply(lines, func(lineJSON) string { return "Frase corta ⟦1⟧" }), nil
	}}
	tr := WithFit(NewDeepL(mtCfg(f, llm.StaticSecrets{"mt.deepl": "k"})), &Fitter{Router: router(m)})
	if in := tr.Info(); len(in.Qualities) != 2 || in.Qualities[1] != Autofit {
		t.Fatal(in)
	}
	res, err := tr.Translate(context.Background(), Request{Target: "es", Quality: Autofit, Segments: []Segment{{ID: "1", Text: "Short [sigh]", Slot: 1.2}}})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Segments[0]
	if s.Text != "Frase corta [sigh]" || !strings.HasPrefix(s.Draft, "Una frase") || s.Fit == nil || res.Usage.TotalTokens == 0 {
		t.Fatalf("%+v", s)
	}
}

func TestProtectHTML(t *testing.T) {
	g := Glossary{{Source: "New York", Target: "纽约"}, {Source: "York", Target: "约克"}}.Relevant("New York and York <b>")
	h := protectHTML("New York and York <b> ⟦1⟧", g)
	want := `<span translate="no" class="notranslate">纽约</span> and <span translate="no" class="notranslate">约克</span> &lt;b&gt; <span translate="no" class="notranslate">⟦1⟧</span>`
	if h != want {
		t.Fatalf("got  %s\nwant %s", h, want)
	}
	if got := unprotectHTML(h); got != "纽约 and 约克 <b> ⟦1⟧" {
		t.Fatal(got)
	}
}
