package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestRealCharacterEngines runs the connector against real engines
// (thread #171's Mac check): AUDIOSMITH_VOICEVOX_URL and/or
// AUDIOSMITH_AIVIS_URL name them, AUDIOSMITH_VOICEVOX_OUT is where the
// takes and a report (report.json) go. Skipped without them.
func TestRealCharacterEngines(t *testing.T) {
	vvURL, avURL, out := os.Getenv("AUDIOSMITH_VOICEVOX_URL"), os.Getenv("AUDIOSMITH_AIVIS_URL"), os.Getenv("AUDIOSMITH_VOICEVOX_OUT")
	if vvURL == "" && avURL == "" || out == "" {
		t.Skip("set AUDIOSMITH_VOICEVOX_URL / AUDIOSMITH_AIVIS_URL and AUDIOSMITH_VOICEVOX_OUT")
	}
	e := newAPIEnv(t)
	dead := "http://127.0.0.1:1"
	ce := settings.CharacterEngines{VoicevoxURL: firstOf(vvURL, dead), AivisURL: firstOf(avURL, dead)}
	if err := e.lib.settings.Update(context.Background(), func(s *settings.Settings) { s.Models.Characters = ce }); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(out, 0o755)
	type entry struct {
		File, Engine, Character, Style, Voice, Credit, Use, Text string
		AudioSeconds, WallSeconds                                float64
	}
	var report []entry
	for _, en := range e.lib.vv.Engines() {
		t.Logf("%s at %s: online %v, %s %s, %d characters, %d styles (%s)", en.Kind, en.URL, en.Online, en.Name, en.Version, len(en.Speakers), en.Styles(), en.Err)
	}
	// The characters and what the API says of them.
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/models/voicevox", nil)
	req.Header.Set("Authorization", "Bearer x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Voices []struct{ ID, Name, Style, Credit, Use string }
	}
	json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	t.Logf("%d voices", len(m.Voices))
	want := strings.Split(firstOf(os.Getenv("AUDIOSMITH_VOICEVOX_PICK"), "ずんだもん/ノーマル,四国めたん/ノーマル,春日部つむぎ/ノーマル"), ",")
	if avURL != "" {
		seen := map[string]bool{}
		for _, v := range m.Voices {
			if strings.HasPrefix(v.ID, "aivis:") && !seen[v.Name] && len(seen) < 2 {
				seen[v.Name] = true
				want = append(want, v.Name+"/"+v.Style)
			}
		}
	}
	texts := []string{
		"こんにちは。今日は二〇二六年十一月二十二日、とても良い天気ですね。",
		"東京駅から新幹線に乗って、三時間ほどで京都に着きました。",
		"ねえ、この新しいアプリ、もう試してみた？声がすごく自然なんだよ。",
	}
	for i, name := range want {
		if strings.TrimSpace(name) == "" {
			continue
		}
		text := texts[i%len(texts)]
		body, _ := json.Marshal(map[string]any{"model": "voicevox", "voice": name, "input": text, "response_format": "wav"})
		start := time.Now()
		resp, data := e.post(t, "/v1/audio/speech", string(body))
		wall := time.Since(start).Seconds()
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d %.300s", name, resp.StatusCode, data)
			continue
		}
		credit, _ := url.PathUnescape(resp.Header.Get("X-AudioSmith-Credit"))
		id := resp.Header.Get("X-AudioSmith-Take-Id")
		var tk core.Take
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if tk, err = e.lib.svc.Takes.Get(context.Background(), id); err == nil {
				break
			}
		}
		if tk.Credit != credit {
			t.Errorf("%s: take credit %q, header %q", name, tk.Credit, credit)
		}
		use := ""
		for _, v := range m.Voices {
			if v.ID == tk.Voice {
				use = v.Use
			}
		}
		file := fmt.Sprintf("%02d-%s.wav", i+1, strings.NewReplacer("/", "-", " ", "_").Replace(name))
		os.WriteFile(filepath.Join(out, file), data, 0o644)
		report = append(report, entry{File: file, Engine: strings.SplitN(tk.Voice, ":", 2)[0], Character: strings.SplitN(name, "/", 2)[0], Style: name,
			Voice: tk.Voice, Credit: credit, Use: use, Text: text, AudioSeconds: tk.AudioSeconds, WallSeconds: wall})
		t.Logf("%s: %.2f s of audio in %.2f s, credit %q, %s", name, tk.AudioSeconds, wall, credit, use)
	}
	// The Studio's scales and silences, through the services.
	if vvURL != "" && len(report) > 0 {
		rec := core.Take{ID: core.NewID(time.Now()), Text: "抑揚を強くして、少しゆっくり話します。", Model: "external/voicevox", Voice: report[0].Voice, Language: "ja",
			Speed: 0.9, Scales: &core.Scales{Intonation: 1.6, Pitch: 0.04, PreSilence: 0.3, PostSilence: 0.5}}
		start := time.Now()
		if _, err := e.lib.svc.Takes.Generate(context.Background(), core.TakeRequest{Recipe: rec}); err != nil {
			t.Fatal(err)
		}
		var tk core.Take
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if tk, err = e.lib.svc.Takes.Get(context.Background(), rec.ID); err == nil {
				break
			}
		}
		if tk.ID == "" || tk.Credit == "" {
			t.Fatalf("scaled take %+v", tk)
		}
		data, _ := os.ReadFile(e.lib.store.Takes().AudioPath(tk))
		file := fmt.Sprintf("%02d-scales.wav", len(report)+1)
		os.WriteFile(filepath.Join(out, file), data, 0o644)
		report = append(report, entry{File: file, Engine: "voicevox", Character: report[0].Character, Style: "scales: speed 0.9, intonation 1.6, pitch +0.04, silences 0.3/0.5 s",
			Voice: tk.Voice, Credit: tk.Credit, Use: report[0].Use, Text: rec.Text, AudioSeconds: tk.AudioSeconds, WallSeconds: time.Since(start).Seconds()})
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(filepath.Join(out, "report.json"), b, 0o644)
}
