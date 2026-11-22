package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// TestVoicePreviewsE2E renders the bundled previews of the built-in
// speakers (thread #60, assets/voice-previews): one short line per
// speaker in its main language, with the real engine and the
// recommended sampling (Qwen3-TTS temperature 0.7, top-p 0.8, a fixed
// seed), trimmed and normalised to −18 LUFS, written as WAV to
// AUDIOSMITH_PREVIEW_DIR/<family>-<speaker>.wav; then Qwen3-ASR listens
// to each (report.md: duration, loudness, what it heard), since nobody
// plays them. scripts/make-voice-previews.sh runs it on the Mac and
// encodes the WAVs to Ogg Vorbis. AUDIOSMITH_PREVIEW_ONLY limits it to
// some speakers (comma-separated ids).
//
//	AUDIOSMITH_E2E=1 AUDIOSMITH_PREVIEW_DIR=/tmp/as-vp go test -run TestVoicePreviewsE2E -v -timeout 1h .
func TestVoicePreviewsE2E(t *testing.T) {
	if os.Getenv("AUDIOSMITH_E2E") == "" {
		t.Skip("set AUDIOSMITH_E2E=1 to run the real engine")
	}
	bin := engine.Find("../resources")
	if bin == "" {
		t.Fatal("no engine: run scripts/build-engine.sh")
	}
	dir := os.Getenv("AUDIOSMITH_PREVIEW_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	only := map[string]bool{}
	for _, s := range strings.Split(os.Getenv("AUDIOSMITH_PREVIEW_ONLY"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			only[s] = true
		}
	}
	raw := filepath.Join(dir, "raw")
	os.MkdirAll(raw, 0o755)
	lib := openTestLibraryWith(t, filepath.Join(dir, "data"), libraryOptions{})
	h := &engineHost{bin: bin, logDir: filepath.Join(dir, "logs")}
	os.MkdirAll(h.logDir, 0o755)
	a := newApp(h, lib)
	a.autoplay = false
	v := &v02{a: a, h: h, events: make(chan func(), 1<<16), dir: dir, mem: newMemSampler()}
	defer v.mem.stop()
	a.enginePost = func(fn func()) { v.events <- fn }
	a.postQueue = a.enginePost
	h.sup = engine.NewSupervisor(engine.Config{Bin: bin, Bus: a.bus, LogDir: h.logDir, Offline: true, CacheDir: modelCache(a.prefs),
		OnDemand: []proto.Role{proto.RoleMedia}, StatsEvery: time.Second, Logf: t.Logf})
	a.connectEngine(h)
	defer h.Stop()
	a.startEngine()
	v.wait(t, "engine ready", time.Minute, func() bool { return a.engState == engineReady })
	a.refreshModels()
	v.drain()
	acceptLicences(t, a)
	ctx := context.Background()
	run := func(method string, params, out any) error {
		b, _ := json.Marshal(params)
		res, err := engine.KindExecutor(h.sup, method).Run(ctx, jobs.Job{ID: core.NewID(time.Now()), Spec: jobs.Spec{Kind: method, Params: b}}, func(jobs.Progress) {}, nil)
		if err == nil && out != nil {
			err = json.Unmarshal(res, out)
		}
		return err
	}
	// fresh restarts the gpu worker: one model in memory at a time.
	fresh := func() {
		h.sup.Restart(proto.RoleGPU)
		time.Sleep(time.Second)
		v.wait(t, "engine ready", time.Minute, func() bool { return a.engState == engineReady })
	}

	type row struct {
		file, lang, text, heard string
		seconds, lufs           float64
	}
	var rows []row
	for _, id := range []string{"qwen3-tts-0.6b-customvoice-8bit", "kokoro-82m-bf16"} {
		e := v.model(t, id)
		snap, err := lib.modelSnapshot(ctx, e.Repo)
		if err != nil {
			t.Fatal(err)
		}
		fresh()
		for _, sp := range e.VoiceIDs() {
			if len(only) > 0 && !only[sp] {
				continue
			}
			lang := previewLanguage(e, sp)
			text, ok := previewLines[lang]
			if !ok {
				t.Logf("%s: no line in %q, skipped", sp, lang)
				continue
			}
			name := previewName(e.Family, sp)
			p := proto.SynthesizeParams{Model: e.Repo, ModelPath: snap, Text: text, Voice: sp, Language: modelLanguage(e, lang),
				Output: proto.Output{Path: filepath.Join(raw, name+".wav"), Format: "wav", Bits: 16}}
			if e.Has(catalog.CapSampling) {
				seed, temp, topP := int64(7), 0.7, 0.8
				if n, ok := previewSeeds[sp]; ok {
					seed = n
				}
				if v := os.Getenv("AUDIOSMITH_PREVIEW_SEED"); v != "" {
					seed, _ = strconv.ParseInt(v, 10, 64)
				}
				p.Seed, p.Temperature, p.TopP = &seed, &temp, &topP
			}
			if err := run(proto.MethodTTSSynthesize, p, nil); err != nil {
				t.Fatalf("%s: %v", sp, err)
			}
			b, _, err := wav.ReadFile(p.Output.Path)
			if err != nil {
				t.Fatal(err)
			}
			s, end := trimSilence(b)
			b = b.Slice(s, end)
			b, _ = loudness.Normalize(b, loudness.Target{Integrated: -18, TruePeak: -1.5}, loudness.NormalizeOptions{})
			out := filepath.Join(dir, name+".wav")
			if err := wav.WriteFile(out, b, wav.PCM16, wav.Options{}); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row{file: name, lang: lang, text: text, seconds: float64(b.Frames()) / float64(b.Rate), lufs: loudness.Integrated(b)})
		}
	}
	// Listen to them (another model: the TTS models are unloaded first).
	asr := v.model(t, catalog.DefaultSTT)
	snap, err := lib.modelSnapshot(ctx, asr.Repo)
	if err != nil {
		t.Fatal(err)
	}
	fresh()
	for i := range rows {
		var res proto.TranscribeResult
		if err := run(proto.MethodSTTTranscribe, proto.TranscribeParams{Model: asr.Repo, ModelPath: snap, Audio: filepath.Join(dir, rows[i].file+".wav")}, &res); err != nil {
			rows[i].heard = "(" + err.Error() + ")"
			continue
		}
		rows[i].heard = res.Language + ": " + res.Text
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Voice previews, %s\n\nengine %s\n\n| file | language | seconds | LUFS | line | heard (Qwen3-ASR) |\n|---|---|---|---|---|---|\n", time.Now().Format(time.RFC3339), a.engVersion)
	for _, r := range rows {
		fmt.Fprintf(&sb, "| %s | %s | %.2f | %.1f | %s | %s |\n", r.file, r.lang, r.seconds, r.lufs, r.text, r.heard)
	}
	os.WriteFile(filepath.Join(dir, "report.md"), []byte(sb.String()), 0o644)
	t.Logf("\n%s", sb.String())
}

// previewSeeds are the seeds of the speakers whose take with seed 7 was
// not right (Qwen3-ASR heard words missing).
var previewSeeds = map[string]int64{"Ryan": 23}

// previewLines are the previews' lines by language. Japanese is for
// Qwen3-TTS's Ono_Anna (#172): Kokoro's Japanese voices are unintelligible
// in the engine (Qwen3-ASR heard 「幸運にちん晴れ晴り…」 for
// 「こんにちは、はじめまして。」, 2026-10-16), hidden, with no preview.
var previewLines = map[string]string{
	"zh": "你好，很高兴认识你，这是我的声音。",
	"ja": "こんにちは、はじめまして。これが私の声です。",
	"en": "Hi, nice to meet you. This is what I sound like.",
	"es": "Hola, mucho gusto. Así suena mi voz.",
	"fr": "Bonjour ! Voici à quoi ressemble ma voix.",
	"it": "Ciao, piacere di conoscerti. Questa è la mia voce.",
	"pt": "Olá, prazer em te conhecer. Esta é a minha voz.",
	"hi": "नमस्ते, आपसे मिलकर खुशी हुई। यह मेरी आवाज़ है।",
}

// previewLanguage is a speaker's main language (its base code): the
// catalogue's, else Qwen3-TTS CustomVoice's speakers' own (its model
// card: Ryan and Aiden speak English, the others Chinese).
func previewLanguage(e catalog.Entry, speaker string) string {
	if l := e.SpeakerLanguage(speaker); l != "" {
		return l
	}
	switch speaker {
	case "Ryan", "Aiden":
		return "en"
	}
	return "zh"
}
