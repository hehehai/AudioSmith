package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/voicevox"
	"github.com/hehehai/AudioSmith/internal/voicevox/voicevoxtest"
)

// useCharacterEngines points the library at fake VOICEVOX and AivisSpeech
// engines (nil: an address nothing answers at).
func useCharacterEngines(t *testing.T, lib *library, vv, av *voicevoxtest.Fake) {
	t.Helper()
	ce := settings.CharacterEngines{VoicevoxURL: "http://127.0.0.1:1", AivisURL: "http://127.0.0.1:1"}
	if vv != nil {
		ce.VoicevoxURL = vv.URL
	}
	if av != nil {
		ce.AivisURL = av.URL
	}
	if err := lib.settings.Update(context.Background(), func(s *settings.Settings) { s.Models.Characters = ce }); err != nil {
		t.Fatal(err)
	}
}

func zundamon(style int64) string {
	return voicevox.Voice{Kind: voicevox.VOICEVOX, Speaker: voicevoxtest.ZundamonUUID, Style: style}.String()
}

// The Local API speaks with a character named by name and style, returns
// its credit (header and take) and lists the characters while an engine
// answers (thread #171).
func TestCharacterVoicesAPI(t *testing.T) {
	vv, av := voicevoxtest.New(voicevoxtest.VOICEVOX), voicevoxtest.New(voicevoxtest.Aivis)
	defer vv.Close()
	defer av.Close()
	e := newAPIEnv(t)
	useCharacterEngines(t, e.lib, vv, av)

	resp, body := e.post(t, "/v1/audio/speech", `{"model":"voicevox","input":"こんにちは。今日はいい天気ですね。","voice":"ずんだもん/あまあま","response_format":"wav"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("speech %d: %.300s", resp.StatusCode, body)
	}
	credit, _ := url.PathUnescape(resp.Header.Get("X-AudioSmith-Credit"))
	if credit != "VOICEVOX:ずんだもん" {
		t.Fatalf("credit header %q", resp.Header.Get("X-AudioSmith-Credit"))
	}
	if len(vv.Queries()) == 0 {
		t.Fatal("VOICEVOX synthesized nothing")
	}
	id := resp.Header.Get("X-AudioSmith-Take-Id")
	var take core.Take
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if tk, err := e.lib.svc.Takes.Get(context.Background(), id); err == nil {
			take = tk
			break
		}
	}
	if take.Credit != "VOICEVOX:ずんだもん" || take.Voice != zundamon(voicevoxtest.ZundamonSweet) || take.Language != "ja" {
		t.Fatalf("take %+v", take)
	}

	// AivisSpeech under ACML: no credit needed.
	resp, body = e.post(t, "/v1/audio/speech", `{"model":"voicevox","input":"おはようございます。","voice":"まい","response_format":"wav"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-AudioSmith-Credit") != "" || len(av.Queries()) != 1 {
		t.Fatalf("AivisSpeech %d %v: %.200s", resp.StatusCode, resp.Header, body)
	}
	// An unknown character.
	resp, body = e.post(t, "/v1/audio/speech", `{"model":"voicevox","input":"あ","voice":"だれか","response_format":"wav"}`)
	if resp.StatusCode != 400 || !strings.Contains(string(body), "voice_not_found") {
		t.Fatalf("unknown character %d: %.300s", resp.StatusCode, body)
	}

	// The model's characters, with their credits and terms.
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/models/voicevox", nil)
	req.Header.Set("Authorization", "Bearer sk-ignored")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var m struct {
		Voices []struct{ ID, Name, Style, Credit, Use string }
	}
	json.NewDecoder(r.Body).Decode(&m)
	found := false
	for _, v := range m.Voices {
		if v.Name == "No.7" && v.Use == "noncommercial" && v.Credit == "VOICEVOX:No.7" {
			found = true
		}
	}
	if !found || len(m.Voices) != 6 {
		t.Fatalf("voices %+v", m.Voices)
	}
}

// Without an engine the model is refused with how to fix it.
func TestCharacterVoicesOffline(t *testing.T) {
	e := newAPIEnv(t)
	useCharacterEngines(t, e.lib, nil, nil)
	resp, body := e.post(t, "/v1/audio/speech", `{"model":"voicevox","input":"こんにちは。","response_format":"wav"}`)
	if resp.StatusCode < 400 || !strings.Contains(string(body), "VOICEVOX") {
		t.Fatalf("offline %d: %.300s", resp.StatusCode, body)
	}
}

// The Studio offers the characters while an engine answers, asks once
// before a non-commercial one, sends the character, Japanese and the
// scales, and never makes the connector the default model.
func TestStudioCharacters(t *testing.T) {
	vv := voicevoxtest.New(voicevoxtest.VOICEVOX)
	defer vv.Close()
	a, eng, tt := newTestApp(t)
	a.vvProbe = true
	useCharacterEngines(t, a.lib, vv, nil)

	// The connector's row of the model picker.
	tt.Frame()
	a.pickerOpen = map[string]bool{"model": true}
	a.pickerAll = map[string]bool{"model": true}
	tt.Frame()
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	if !strings.Contains(texts, "Japanese character voices (VOICEVOX / AivisSpeech)") {
		t.Fatalf("picker lacks the connector: %s", texts)
	}
	// Searched, it is the only row (the full list scrolls past it).
	a.pickerFind["model"] = "VOICEVOX"
	tt.Frame()
	if err := tt.Click("Japanese character voices (VOICEVOX / AivisSpeech)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.preset().IsExternal() {
		t.Fatalf("model %s", a.preset().ID)
	}
	if a.lib.settings.Get().Studio.Model == "voicevox" {
		t.Fatal("the connector became the default model")
	}

	// The voices: the characters' styles, with what their terms allow.
	a.voicePickerOpen(studioVoiceKey, true)
	tt.Frame()
	texts = strings.Join(tt.Texts(), "|")
	for _, want := range []string{"ずんだもん · VOICEVOX", "あまあま", "No.7 · VOICEVOX", "Non-commercial only", "Commercial use OK · credit required"} {
		if !strings.Contains(texts, want) {
			t.Errorf("voices lack %q: %s", want, texts)
		}
	}
	a.voicePickerOpen(studioVoiceKey, false)

	// No.7 asks first; confirmed, the take goes with its scales.
	a.voice = voicevox.Voice{Kind: voicevox.VOICEVOX, Speaker: voicevoxtest.No7UUID, Style: voicevoxtest.No7Normal}.String()
	a.vvScales = core.Scales{Intonation: 1.3, PostSilence: 0.5}
	a.text = "こんにちは。"
	a.script.set(a.text)
	n := len(eng.sent)
	a.generate()
	tt.Frame()
	if a.vvAsk == nil || len(eng.sent) != n {
		t.Fatalf("no confirmation before No.7 (ask %v, %d sent)", a.vvAsk, len(eng.sent)-n)
	}
	a.vvAsk.agree = true
	tt.Frame()
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	p := renderOf(t, eng.last(t))
	if p.Take.Voice != a.voice || p.Take.Language != "ja" || p.Take.Scales == nil || p.Take.Scales.Intonation != 1.3 || p.Take.Model != "external/voicevox" {
		t.Fatalf("recipe %+v", p.Take)
	}
	if !a.characterConfirmed(a.voice) {
		t.Fatal("the confirmation was not kept")
	}
	// Asked once.
	n = len(eng.sent)
	a.job = nil
	a.generate()
	tt.Frame()
	if a.vvAsk != nil || len(eng.sent) == n {
		t.Fatal("asked again")
	}
}

// Settings → Models shows the engines with their state and addresses.
func TestCharacterSettings(t *testing.T) {
	vv := voicevoxtest.New(voicevoxtest.VOICEVOX)
	defer vv.Close()
	a, _, tt := newTestApp(t)
	a.vvProbe = true
	useCharacterEngines(t, a.lib, vv, nil)
	a.prefs = a.lib.settings.Get()
	a.lib.vv.Refresh(context.Background(), 0)
	tt.Frame()
	openModels(a)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "|")
	for _, want := range []string{"Connected · 0.26.2 · 2 characters", "Not running", "Get AivisSpeech"} {
		if !strings.Contains(texts, want) {
			t.Errorf("settings lack %q: %s", want, texts)
		}
	}
}

// findCharacter reads ids, names and name/style.
func TestFindCharacter(t *testing.T) {
	es := []voicevox.Engine{{Kind: voicevox.VOICEVOX, Online: true, Speakers: []voicevox.Speaker{{Name: "ずんだもん", UUID: voicevoxtest.ZundamonUUID,
		Styles: []voicevox.Style{{Name: "ノーマル", ID: 3}, {Name: "あまあま", ID: 1}}}}}}
	for in, want := range map[string]string{"ずんだもん": zundamon(3), "ずんだもん/あまあま": zundamon(1), "ずんだもん（あまあま）": zundamon(1),
		"ずんだもん (あまあま)": zundamon(1), zundamon(1): zundamon(1), "ずんだもん/なみだめ": "", zundamon(9): ""} {
		got, ok := findCharacter(es, in)
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
}

// Without an engine the picker says how to get one instead of offering
// the connector.
func TestCharacterPickerOffline(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.vvProbe = true
	useCharacterEngines(t, a.lib, nil, nil)
	tt.Frame()
	a.pickerOpen = map[string]bool{"model": true}
	a.pickerFind["model"] = "VOICEVOX"
	tt.Frame()
	tt.Frame()
	for _, want := range []string{"Install and open VOICEVOX or AivisSpeech to use their Japanese characters here.", "Get VOICEVOX", "Get AivisSpeech"} {
		if !tt.HasText(want) {
			t.Errorf("offline picker lacks %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Japanese character voices (VOICEVOX / AivisSpeech)"); err == nil {
		tt.Frame()
	}
	if a.preset().IsExternal() {
		t.Fatal("the connector was picked while no engine runs")
	}
}

// Stories and Audiobook: the cast offers the characters, a book's voice
// reference resolves to the character, and a non-commercial one is
// confirmed before the render.
func TestCharacterLongform(t *testing.T) {
	vv := voicevoxtest.New(voicevoxtest.VOICEVOX)
	defer vv.Close()
	a, _, _ := newTestApp(t)
	a.vvProbe = true
	useCharacterEngines(t, a.lib, vv, nil)
	e := charactersEntry()
	vs := a.lfVoices(e)
	if len(vs) != 3 || vs[0].ref != services.SpeakerRef+zundamon(voicevoxtest.ZundamonNormal) || vs[0].label != "ずんだもん（ノーマル）" {
		t.Fatalf("cast voices %+v", vs)
	}
	if got := a.lfVoiceLabel(vs[1].ref); got != "ずんだもん（あまあま）" {
		t.Fatalf("label %q", got)
	}
	resolve := a.lfResolver(&services.LongformDoc{Model: e.Repo})
	ref, tk, ok := resolve(vs[1].ref)
	if !ok || ref != vs[1].ref || tk.Voice != zundamon(voicevoxtest.ZundamonSweet) {
		t.Fatalf("resolved %q %+v %v", ref, tk, ok)
	}
	no7 := voicevox.Voice{Kind: voicevox.VOICEVOX, Speaker: voicevoxtest.No7UUID, Style: voicevoxtest.No7Normal}.String()
	spec := services.LongformSpec{Model: e.Repo, Voices: map[string]core.Take{"n": {Voice: zundamon(3)}, "s": {Voice: no7}},
		Chapters: []services.ChapterSpec{{Lines: []services.LineSpec{{Spans: []services.SpanSpec{{Voice: "n"}, {Voice: "s"}}}}}}}
	if got := a.unconfirmedCharacter(spec); got != no7 {
		t.Fatalf("unconfirmed %q", got)
	}
	a.confirmCharacter(no7)
	if got := a.unconfirmedCharacter(spec); got != "" {
		t.Fatalf("still unconfirmed %q", got)
	}
}
