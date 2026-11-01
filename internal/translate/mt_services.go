package translate

// The online machine translation services: DeepL, Google Cloud
// Translation (v2 and v3) and Microsoft Translator, each a service of
// mtTranslator (mt.go).

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// NewDeepL returns the DeepL adapter (key "mt.deepl"; ":fx" keys use the free API).
func NewDeepL(cfg MTConfig) Translator { return &mtTranslator{cfg: cfg, svc: deepl{}} }

type deepl struct{}

func (deepl) id() string                   { return "deepl" }
func (deepl) maxBatch() (texts, chars int) { return 50, 100_000 }

var deeplLangs = map[string]bool{"ar": true, "bg": true, "cs": true, "da": true, "de": true, "el": true, "en": true, "es": true, "et": true, "fi": true, "fr": true, "he": true, "hu": true, "id": true, "it": true, "ja": true, "ko": true, "lt": true, "lv": true, "nb": true, "nl": true, "pl": true, "pt": true, "ro": true, "ru": true, "sk": true, "sl": true, "sv": true, "th": true, "tr": true, "uk": true, "vi": true, "zh": true}

func (deepl) code(l Lang, target bool) (string, bool) {
	c := l.Code
	if c == "no" {
		c = "nb"
	}
	if !deeplLangs[c] {
		return "", false
	}
	up := strings.ToUpper(c)
	if !target {
		return up, true
	}
	switch c {
	case "en":
		if l.Region == "GB" || l.Region == "AU" || l.Region == "IE" || l.Region == "NZ" || l.Region == "IN" || l.Region == "ZA" {
			return "EN-GB", true
		}
		return "EN-US", true
	case "pt":
		if l.Region == "PT" || l.Region == "AO" || l.Region == "MZ" {
			return "PT-PT", true
		}
		return "PT-BR", true
	case "zh":
		if l.Script == "Hant" {
			return "ZH-HANT", true
		}
		return "ZH-HANS", true
	case "es":
		if l.Region != "" && l.Region != "ES" {
			return "ES-419", true
		}
	}
	return up, true
}

func (d deepl) call(ctx context.Context, m *mtTranslator, htmls []string, src, tgt string, req Request) ([]string, error) {
	key, err := m.key(ctx)
	if err != nil {
		return nil, err
	}
	def := "https://api.deepl.com"
	if strings.HasSuffix(key, ":fx") {
		def = "https://api-free.deepl.com"
	}
	body := map[string]any{"text": htmls, "target_lang": tgt, "tag_handling": "html", "preserve_formatting": true}
	if src != "" {
		body["source_lang"] = src
	}
	switch req.Formality {
	case FormalityFormal:
		body["formality"] = "prefer_more"
	case FormalityInformal:
		body["formality"] = "prefer_less"
	}
	if c := strings.TrimSpace(req.Context + " " + req.Style); c != "" {
		body["context"] = c
	}
	var out struct {
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
	}
	h := http.Header{"Authorization": {"DeepL-Auth-Key " + key}}
	if err := m.do(ctx, http.MethodPost, m.base(def)+"/v2/translate", h, body, &out); err != nil {
		return nil, err
	}
	res := make([]string, len(out.Translations))
	for i, t := range out.Translations {
		res[i] = t.Text
	}
	return res, nil
}

// ---- Google Cloud Translation ----

// NewGoogle returns the Google Cloud Translation v2 (basic) adapter, key "mt.google" (an API key).
func NewGoogle(cfg MTConfig) Translator { return &mtTranslator{cfg: cfg, svc: googleV2{}} }

func googleCode(l Lang) string {
	switch l.Code {
	case "zh":
		if l.Script == "Hant" {
			return "zh-TW"
		}
		return "zh-CN"
	case "pt":
		if l.Region == "PT" {
			return "pt-PT"
		}
		return "pt"
	case "fr":
		if l.Region == "CA" {
			return "fr-CA"
		}
	case "he":
		return "iw"
	case "jv":
		return "jw"
	}
	return l.Code
}

type googleV2 struct{}

func (googleV2) id() string                   { return "google" }
func (googleV2) maxBatch() (texts, chars int) { return 100, 25_000 }
func (googleV2) code(l Lang, _ bool) (string, bool) {
	return googleCode(l), l.Code != ""
}

func (g googleV2) call(ctx context.Context, m *mtTranslator, htmls []string, src, tgt string, _ Request) ([]string, error) {
	key, err := m.key(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"q": htmls, "target": tgt, "format": "html"}
	if src != "" {
		body["source"] = src
	}
	var out struct {
		Data struct {
			Translations []struct {
				TranslatedText string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}
	// The key goes in a header, not the URL: Go's errors carry the URL,
	// and they reach the logs (family audit S5).
	u := m.base("https://translation.googleapis.com") + "/language/translate/v2"
	if err := m.do(ctx, http.MethodPost, u, http.Header{"X-Goog-Api-Key": {key}}, body, &out); err != nil {
		return nil, err
	}
	res := make([]string, len(out.Data.Translations))
	for i, t := range out.Data.Translations {
		res[i] = t.TranslatedText
	}
	return res, nil
}

// ---- Microsoft Translator ----

// NewMicrosoft returns the Microsoft Translator v3 adapter, key
// "mt.microsoft"; region is the resource's Azure region ("" for global
// resources).
func NewMicrosoft(cfg MTConfig, region string) Translator {
	return &mtTranslator{cfg: cfg, svc: microsoft{region: region}}
}

type microsoft struct{ region string }

func (microsoft) id() string                   { return "microsoft" }
func (microsoft) maxBatch() (texts, chars int) { return 100, 40_000 }
func (microsoft) code(l Lang, _ bool) (string, bool) {
	switch l.Code {
	case "zh":
		if l.Script == "Hant" {
			return "zh-Hant", true
		}
		return "zh-Hans", true
	case "pt":
		if l.Region == "PT" {
			return "pt-pt", true
		}
		return "pt", true
	case "fr":
		if l.Region == "CA" {
			return "fr-ca", true
		}
	case "sr":
		if l.Script == "Latn" {
			return "sr-Latn", true
		}
		return "sr-Cyrl", true
	case "no":
		return "nb", true
	}
	return l.Code, l.Code != ""
}

func (ms microsoft) call(ctx context.Context, m *mtTranslator, htmls []string, src, tgt string, _ Request) ([]string, error) {
	key, err := m.key(ctx)
	if err != nil {
		return nil, err
	}
	body := make([]map[string]string, len(htmls))
	for i, h := range htmls {
		body[i] = map[string]string{"Text": h}
	}
	u := m.base("https://api.cognitive.microsofttranslator.com") + "/translate?api-version=3.0&textType=html&to=" + url.QueryEscape(tgt)
	if src != "" {
		u += "&from=" + url.QueryEscape(src)
	}
	h := http.Header{"Ocp-Apim-Subscription-Key": {key}}
	if ms.region != "" {
		h.Set("Ocp-Apim-Subscription-Region", ms.region)
	}
	var out []struct {
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
	}
	if err := m.do(ctx, http.MethodPost, u, h, body, &out); err != nil {
		return nil, err
	}
	res := make([]string, len(out))
	for i, t := range out {
		if len(t.Translations) > 0 {
			res[i] = t.Translations[0].Text
		}
	}
	return res, nil
}
