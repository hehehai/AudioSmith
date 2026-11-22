package main

// TestCuratedVoicesE2E makes and measures the curated voices of Discover
// voices (thread #94, internal/gallery/data/curated.json) with the real
// engine and the product's own code; scripts/make-curated-voices.sh runs
// its stages on the Mac. Nobody plays anything: picks are made by
// measurements, and the listening pack is for the user's ears.
//
// CURATE_MODE picks the stage, CURATE_OUT the working directory:
//
//	refs   each designed voice's reference candidates, for CURATE_SEEDS
//	       seeds (default 3): its persona with the conversational style,
//	       the reference texts of its language, on 1.7B VoiceDesign, as
//	       makeReference asks for them (TakeService, refTakeLimit); a
//	       voice with its own design model (DesignModel: Irodori-TTS for
//	       the Japanese ones, #172) on that model, its caption as written
//	       and its own texts (curateRefTexts), and its clones too.
//	pick   tidies, measures and has Qwen3-ASR hear every candidate, and
//	       keeps the best of each seed (refcheck.Pick): <id>/s<k>/ref.wav
//	       and ref.json.
//	clone  Base 1.7B (or the voice's own model) clones of the test lines (#91's narrative and
//	       conversational lines) from each seed's reference, "Even out
//	       volume" on, as the Studio makes them.
//	asr    Qwen3-ASR hears the clones: asr.json.
//	final  from CURATE_PICKS (JSON: id → seed index) and the encoded
//	       references (<id>.ref.ogg in CURATE_OUT/final, decoded here as
//	       the app does): the preview line and the listening-pack lines
//	       cloned on Base 1.7B; the built-in voices' pack lines on their
//	       own models.
//
// CURATE_ONLY limits it to some ids (comma-separated, without "c-").
//
//	CURATE_MODE=refs CURATE_OUT=/tmp/c94 AUDIOSMITH_E2E=1 go test -run TestCuratedVoicesE2E -v -timeout 6h .

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// curateVD and curateBase are the design and clone models (the gallery's
// and the Studio's with a voice).
const (
	curateVD   = "qwen3-tts-1.7b-voicedesign-4bit"
	curateBase = "qwen3-tts-1.7b-base-8bit"
)

// curateLines are #91's fixed test lines (no fillers): N narrative, C
// conversational, the same for every voice of a language.
var curateLines = map[string][2]string{
	"zh": {"那天傍晚雨刚停，巷口的小面馆亮起了灯。她在门口站了一会儿，还是推门走了进去。", "你怎么这么早就回来了？我还以为你今天要加班，晚饭都还没开始做呢。"},
	"en": {"By the time the rain stopped, the streetlights were on, and the little bookshop on the corner was the only place still open.", "Wait, you're home already? I thought you were working late tonight. I haven't even started dinner."},
	"ja": {"雨がやんだころには街灯がともり、角の小さな本屋だけがまだ開いていた。", "え、もう帰ってきたの？今日は残業だと思って、まだ晩ご飯を作ってないよ。"},
	"ko": {"비가 그쳤을 무렵 가로등이 켜졌고, 모퉁이의 작은 서점만 아직 문을 열어 두고 있었다.", "벌써 왔어? 오늘 야근하는 줄 알고 아직 저녁도 안 했는데."},
	"es": {"Cuando paró la lluvia, ya se habían encendido las farolas, y la pequeña librería de la esquina era lo único que seguía abierto.", "¿Ya estás en casa? Pensé que hoy salías tarde del trabajo. Ni siquiera he empezado la cena."},
	"fr": {"Quand la pluie s'est arrêtée, les réverbères étaient déjà allumés, et la petite librairie du coin était la seule encore ouverte.", "Quoi, tu es déjà rentré ? Je croyais que tu travaillais tard ce soir. Je n'ai même pas commencé le dîner."},
	"de": {"Als der Regen aufhörte, brannten schon die Straßenlaternen, und nur die kleine Buchhandlung an der Ecke hatte noch geöffnet.", "Du bist schon zu Hause? Ich dachte, du arbeitest heute länger. Ich habe noch nicht einmal mit dem Abendessen angefangen."},
	"it": {"Quando smise di piovere, i lampioni erano già accesi, e la piccola libreria all'angolo era l'unico posto ancora aperto.", "Sei già a casa? Pensavo che stasera lavorassi fino a tardi. Non ho ancora iniziato a preparare la cena."},
	"pt": {"Quando a chuva parou, os postes já estavam acesos, e a pequena livraria da esquina era o único lugar ainda aberto.", "Você já chegou? Achei que ia trabalhar até tarde hoje. Eu nem comecei o jantar ainda."},
	"ru": {"Когда дождь закончился, уже горели фонари, и только маленький книжный магазин на углу был ещё открыт.", "Ты уже дома? Я думала, ты сегодня задержишься на работе. Я даже ужин ещё не начала готовить."},
}

// curateSeed is candidate seed k of curated voice id.
func curateSeed(id string, k int) int64 {
	s := vd.SeedFrom("curated:" + id + ":" + strconv.Itoa(k))
	if s == 0 {
		s = 1
	}
	return s
}

// curateRefRecipe is what makeReference asks the design model for a
// curated voice: its persona with the conversational style, and the
// reference texts of its language and register. A voice with its own
// texts (curateRefTexts, thread #172) gets those, and its persona as
// written (a Japanese caption for Irodori-TTS).
func curateRefRecipe(a gallery.Archetype) (voice string, texts []string, lang string) {
	lang = a.Languages[0]
	if own, ok := curateRefTexts[strings.TrimPrefix(a.ID, gallery.CuratedPrefix)]; ok {
		return a.Persona, own, lang
	}
	voice = vd.WithStyle(a.Persona, vd.ConversationalStyle(designTextLang(lang)))
	reg := vd.RegisterAdult
	if a.Age == "child" {
		reg = vd.RegisterChild
	}
	return voice, vd.ReferenceTexts(lang, reg)[:refCandidates], lang
}

// curateModels are the models that design curated voice a's reference
// and clone from it: its own (DesignModel: Irodori-TTS does both), else
// 1.7B VoiceDesign and Base.
func curateModels(a gallery.Archetype) (design, clone catalog.Entry) {
	if a.DesignModel != "" {
		e, _ := catalog.Builtin().ByID(a.DesignModel)
		return e, e
	}
	design, _ = catalog.Builtin().ByID(curateVD)
	clone, _ = catalog.Builtin().ByID(curateBase)
	return design, clone
}

type curateCand struct {
	Text  string             `json:"text"`
	Heard string             `json:"heard"`
	CER   float64            `json:"cer"`
	Secs  float64            `json:"secs"`
	Check refcheck.Candidate `json:"check"`
}

type curateRef struct {
	ID    string       `json:"id"`
	Seed  int64        `json:"seed"`
	Best  int          `json:"best"`
	Text  string       `json:"text"`  // the kept take's text (refAccept may make it what was heard)
	OK    bool         `json:"ok"`    // refAccept kept it
	Grade string       `json:"grade"` // refcheck grade of the kept take
	Cands []curateCand `json:"cands"`
}

func TestCuratedVoicesE2E(t *testing.T) {
	mode, out := os.Getenv("CURATE_MODE"), os.Getenv("CURATE_OUT")
	if mode == "" || out == "" || os.Getenv("AUDIOSMITH_E2E") == "" {
		t.Skip("set AUDIOSMITH_E2E=1, CURATE_MODE and CURATE_OUT")
	}
	os.MkdirAll(out, 0o755)
	bin := engine.Find("../resources")
	if bin == "" {
		t.Fatal("no engine: set AUDIOSMITH_ENGINE")
	}
	items, err := gallery.BuildCurated()
	if err != nil {
		t.Fatal(err)
	}
	only := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("CURATE_ONLY"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			only[gallery.CuratedPrefix+k] = true
		}
	}
	items = slices.DeleteFunc(items, func(a gallery.Archetype) bool { return len(only) > 0 && !only[a.ID] })
	seeds := 3
	if n, err := strconv.Atoi(os.Getenv("CURATE_SEEDS")); err == nil && n > 0 {
		seeds = n
	}
	// The test lines of each voice, for the report (curate-report.py).
	lines := map[string]map[string]string{}
	for _, v := range items {
		l := curateLines[v.Languages[0]]
		lines[v.ID] = map[string]string{"N": l[0], "C": l[1], "lang": v.Languages[0]}
	}
	if len(only) == 0 {
		b, _ := json.MarshalIndent(lines, "", " ")
		os.WriteFile(filepath.Join(out, "lines.json"), b, 0o644)
	}

	lib := openTestLibraryWith(t, filepath.Join(out, "lib"), libraryOptions{})
	h := &engineHost{bin: bin, logDir: filepath.Join(out, "logs")}
	os.MkdirAll(h.logDir, 0o755)
	a := newApp(h, lib)
	a.autoplay = false
	events := make(chan func(), 1<<16)
	a.enginePost = func(fn func()) { events <- fn }
	a.postQueue = a.enginePost
	h.sup = engine.NewSupervisor(engine.Config{Bin: bin, Bus: a.bus, LogDir: h.logDir, Offline: true, CacheDir: modelCache(a.prefs),
		OnDemand: []proto.Role{proto.RoleListen, proto.RoleMedia},
		Log: func(e core.LogEntry) {
			if e.Level != core.SeverityInfo && e.Level != core.SeverityDebug {
				t.Logf("%s %s: %s", e.Level, e.Source, e.Message)
			}
		}, Logf: t.Logf})
	a.connectEngine(h)
	defer h.Stop()
	waitFor := func(what string, timeout time.Duration, done func() bool) bool {
		t.Helper()
		deadline := time.After(timeout)
		for !done() {
			if a.engState == engineFailed {
				t.Fatalf("engine failed: %s\n%s", a.engError, a.engTail)
			}
			select {
			case fn := <-events:
				fn()
			case <-time.After(200 * time.Millisecond):
			case <-deadline:
				t.Logf("timed out waiting for %s", what)
				return false
			}
		}
		return true
	}
	a.startEngine()
	waitFor("ready", time.Minute, func() bool { return a.engState == engineReady })
	a.refreshModels()
	acceptLicences(t, a)
	ctx := context.Background()
	started := time.Now()
	logStep := func(f string, args ...any) {
		t.Logf("[%5.0f s] %s", time.Since(started).Seconds(), fmt.Sprintf(f, args...))
	}
	made := map[string]core.Take{}
	var genOnce func(what string, tk core.Take, dst string) bool
	core.On(a.bus, func(ev core.TakeCreated) { tc := ev.Take; events <- func() { made[tc.ID] = tc } })
	// gen renders a take through TakeService and copies it to dst; false
	// when it failed. Five failures in a row stop the stage (a model
	// missing, the engine gone).
	// A take the stage was stopped in twice (#91 saw single takes make
	// the Mac's free memory collapse, again and again) is skipped.
	fails := 0
	gen := func(what string, tk core.Take, dst string) bool {
		mark := dst + ".tries"
		b, _ := os.ReadFile(mark)
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if n >= 2 {
			t.Logf("%s skipped: the stage stopped in it %d times", what, n)
			return false
		}
		os.MkdirAll(filepath.Dir(dst), 0o755)
		os.WriteFile(mark, []byte(strconv.Itoa(n+1)), 0o644)
		ok := genOnce(what, tk, dst)
		os.Remove(mark)
		if fails = map[bool]int{true: 0, false: fails + 1}[ok]; fails >= 5 {
			t.Fatalf("%s: five failures in a row", what)
		}
		return ok
	}
	genOnce = func(what string, tk core.Take, dst string) bool {
		tk.ID = core.NewID(time.Now())
		jid, err := lib.svc.Takes.Generate(ctx, core.TakeRequest{Recipe: tk})
		if err != nil {
			t.Logf("%s failed: %v", what, err)
			return false
		}
		failed := ""
		ok := waitFor(what, 10*time.Minute, func() bool {
			if _, ok := made[tk.ID]; ok {
				return true
			}
			if j, err := lib.sched.Get(ctx, jid); err == nil && j.State.Terminal() && j.State != jobs.StateSucceeded {
				failed = fmt.Sprint(j.Err)
				return true
			}
			return false
		})
		if !ok || failed != "" {
			t.Logf("%s failed: %s", what, failed)
			return false
		}
		tc := made[tk.ID]
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := services.CopyFile(ctx, a.takeStore.AudioPath(tc), dst); err != nil {
			t.Fatal(err)
		}
		lib.svc.Takes.Delete(ctx, tc.ID)
		return true
	}
	have := func(p string) bool { _, err := os.Stat(p); return err == nil }
	writeJSON := func(p string, v any) {
		b, _ := json.MarshalIndent(v, "", " ")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	asr, _ := catalog.Builtin().ByID(catalog.DefaultSTT)
	transcribe := func(path, lang string) (string, error) {
		snap, err := lib.modelSnapshot(ctx, asr.Repo)
		if err != nil {
			return "", err
		}
		raw, err := h.sup.Call(ctx, proto.RoleGPU, core.NewID(time.Now()), proto.MethodSTTTranscribe,
			proto.TranscribeParams{Model: asr.Repo, ModelPath: snap, Audio: path, Language: lang}, nil)
		if err != nil {
			return "", err
		}
		var tr proto.TranscribeResult
		json.Unmarshal(raw, &tr)
		return tr.Text, nil
	}
	designed := slices.DeleteFunc(slices.Clone(items), func(a gallery.Archetype) bool { return a.Kind != gallery.KindDesigned })
	clone := func(what string, a gallery.Archetype, seed int64, ref, refText, text, dst string) bool {
		if have(dst) {
			return true
		}
		_, base := curateModels(a)
		return gen(what, core.Take{Text: text, Model: base.Repo, Seed: seed, Language: modelLanguage(base, a.Languages[0]),
			RefAudio: ref, RefText: refText, Render: &core.TakeRender{Bits: 24, Master: services.MasterDefault}}, dst)
	}

	switch mode {
	case "refs":
		for i, v := range designed {
			voice, texts, lang := curateRefRecipe(v)
			vdm, _ := curateModels(v)
			for k := range seeds {
				for j, text := range texts {
					dst := filepath.Join(out, v.ID, fmt.Sprintf("s%d", k), fmt.Sprintf("cand%d.wav", j))
					if have(dst) {
						continue
					}
					gen(fmt.Sprintf("ref %s s%d %d", v.ID, k, j), core.Take{Text: text, Model: vdm.Repo, Voice: voice, Seed: curateSeed(v.ID, k),
						Language: modelLanguage(vdm, lang), MaxTokens: refTakeLimit(vdm), Render: &core.TakeRender{Bits: 24}}, dst)
				}
			}
			logStep("refs %d/%d %s", i+1, len(designed), v.ID)
		}
	case "pick":
		for i, v := range designed {
			_, texts, lang := curateRefRecipe(v)
			vdm, _ := curateModels(v)
			for k := range seeds {
				dir := filepath.Join(out, v.ID, fmt.Sprintf("s%d", k))
				if have(filepath.Join(dir, "ref.json")) {
					continue
				}
				res := curateRef{ID: v.ID, Seed: curateSeed(v.ID, k), Best: -1}
				var cands []*refCand
				var checks []refcheck.Candidate
				for j, text := range texts {
					p := filepath.Join(dir, fmt.Sprintf("cand%d.wav", j))
					if !have(p) {
						continue
					}
					c := &refCand{text: text}
					c.path, c.secs, c.check = tidyCandidate(p, filepath.Join(dir, "tidy"), text)
					c.check.Truncated = refTakeLimit(vdm) > 0 && c.check.Raw.Seconds >= float64(refMaxTokens)/12.5-0.5
					if c.path == "" {
						continue
					}
					if heard, err := transcribe(c.path, vd.RefLang(lang)); err == nil {
						cer := refCER(text, heard)
						c.check.Raw.CER, c.check.Tidy.CER, c.heard = cer, cer, heard
					}
					cands, checks = append(cands, c), append(checks, c.check)
					res.Cands = append(res.Cands, curateCand{Text: text, Heard: c.heard, CER: c.check.Tidy.CER, Secs: c.secs, Check: c.check})
				}
				if best := refcheck.Pick(checks, 0); best >= 0 {
					res.Best = best
					c := cands[best]
					res.OK = refAccept(c)
					res.Text = c.text
					res.Grade = refQuality(c.check.Tidy, nil, c.check.Raw.Seconds).Grade
					if res.OK {
						services.CopyFile(ctx, c.path, filepath.Join(dir, "ref.wav"))
					}
				}
				writeJSON(filepath.Join(dir, "ref.json"), res)
				os.RemoveAll(filepath.Join(dir, "tidy"))
			}
			logStep("pick %d/%d %s", i+1, len(designed), v.ID)
		}
	case "clone":
		for i, v := range designed {
			lines := curateLines[v.Languages[0]]
			for k := range seeds {
				dir := filepath.Join(out, v.ID, fmt.Sprintf("s%d", k))
				var r curateRef
				if b, err := os.ReadFile(filepath.Join(dir, "ref.json")); err != nil || json.Unmarshal(b, &r) != nil || !r.OK {
					continue
				}
				for j, line := range lines {
					clone(fmt.Sprintf("clone %s s%d %c", v.ID, k, "NC"[j]), v, r.Seed, filepath.Join(dir, "ref.wav"), r.Text, line,
						filepath.Join(dir, string("NC"[j])+".wav"))
				}
			}
			logStep("clone %d/%d %s", i+1, len(designed), v.ID)
		}
	case "asr":
		res := map[string]string{}
		path := filepath.Join(out, "asr.json")
		if b, err := os.ReadFile(path); err == nil {
			json.Unmarshal(b, &res)
		}
		for i, v := range items {
			files, _ := filepath.Glob(filepath.Join(out, v.ID, "*", "[NC].wav"))
			more, _ := filepath.Glob(filepath.Join(out, "final", v.ID, "*.wav"))
			for _, f := range append(files, more...) {
				rel, _ := filepath.Rel(out, f)
				if _, ok := res[rel]; ok {
					continue
				}
				heard, err := transcribe(f, v.Languages[0])
				if err != nil {
					t.Logf("asr %s: %v", rel, err)
					continue
				}
				res[rel] = heard
			}
			writeJSON(path, res)
			logStep("asr %d/%d %s", i+1, len(items), v.ID)
		}
	case "final":
		var picks map[string]int
		b, err := os.ReadFile(os.Getenv("CURATE_PICKS"))
		if err != nil || json.Unmarshal(b, &picks) != nil {
			t.Fatalf("CURATE_PICKS: %v", err)
		}
		fin := filepath.Join(out, "final")
		for i, v := range items {
			dir := filepath.Join(fin, v.ID)
			lines := curateLines[v.Languages[0]]
			if v.Kind == gallery.KindBuiltin {
				e, ok := catalog.Builtin().ByID(v.Model)
				if !ok {
					t.Fatalf("%s: no model %s", v.ID, v.Model)
				}
				for j, line := range lines {
					dst := filepath.Join(dir, string("NC"[j])+".wav")
					if !have(dst) {
						tk := core.Take{Text: line, Model: e.Repo, Voice: v.Speaker, Language: modelLanguage(e, v.Languages[0]), Seed: 7,
							Render: &core.TakeRender{Bits: 24, Master: services.MasterDefault}}
						gen("builtin "+v.ID, tk, dst)
					}
				}
				continue
			}
			k, ok := picks[strings.TrimPrefix(v.ID, gallery.CuratedPrefix)]
			if !ok {
				continue
			}
			var r curateRef
			if b, err := os.ReadFile(filepath.Join(out, v.ID, fmt.Sprintf("s%d", k), "ref.json")); err != nil || json.Unmarshal(b, &r) != nil {
				t.Fatalf("%s: no ref.json for seed %d", v.ID, k)
			}
			// The reference as the app ships it: Ogg Vorbis, decoded.
			ref := filepath.Join(dir, "ref.wav")
			if !have(ref) {
				ogg, err := os.ReadFile(filepath.Join(fin, strings.TrimPrefix(v.ID, gallery.CuratedPrefix)+".ref.ogg"))
				if err != nil {
					t.Fatalf("%s: %v", v.ID, err)
				}
				os.MkdirAll(dir, 0o755)
				if _, err := decodeBundled(ogg, ref); err != nil {
					t.Fatalf("%s: %v", v.ID, err)
				}
			}
			clone("preview "+v.ID, v, r.Seed, ref, r.Text, v.Line, filepath.Join(dir, "preview.wav"))
			for j, line := range lines {
				clone(fmt.Sprintf("pack %s %c", v.ID, "NC"[j]), v, r.Seed, ref, r.Text, line, filepath.Join(dir, string("NC"[j])+".wav"))
			}
			logStep("final %d/%d %s", i+1, len(items), v.ID)
		}
	case "smoke":
		curateSmoke(t, a, ctx, waitFor, transcribe, out)
	default:
		t.Fatalf("unknown mode %s", mode)
	}
}

// curateSmoke is the Mac smoke of thread #94, through the app as a user
// would go: Discover voices → Use in Studio on a curated voice, then a
// take with no reference to wait for (its bundled one); then a voice saved
// from an earlier gallery id before (its own recipe, no reference): the
// Studio makes its reference first, then the take. Qwen3-ASR hears both.
func curateSmoke(t *testing.T, a *app, ctx context.Context, waitFor func(string, time.Duration, func() bool) bool,
	transcribe func(path, lang string) (string, error), out string) {
	line := "那天傍晚雨刚停，巷口的小面馆亮起了灯。"
	take := func(what string) *core.Take {
		before := a.lastClipID
		a.text = line
		a.generate()
		waitFor(what, 15*time.Minute, func() bool {
			return a.lastClipID != before && a.job == nil || a.jobError != "" || a.refFailed(a.studioVoice) != ""
		})
		if a.jobError != "" || a.refFailed(a.studioVoice) != "" {
			t.Fatalf("%s: %s %s", what, a.jobError, a.refFailed(a.studioVoice))
		}
		tk := a.lastClip()
		heard, err := transcribe(a.takeStore.AudioPath(*tk), "zh")
		t.Logf("%s: %.1f s, heard %q (%v), CER %.1f %%", what, tk.AudioSeconds, heard, err, 100*refCER(line, heard))
		services.CopyFile(ctx, a.takeStore.AudioPath(*tk), filepath.Join(out, "smoke-"+strings.ReplaceAll(what, " ", "-")+".wav"))
		return tk
	}
	arch, ok := gallery.Default().ByID("c-grandma-osmanthus")
	if !ok {
		t.Fatal("no c-grandma-osmanthus")
	}
	a.navigate("/gallery")
	a.useArchetype(arch, true)
	waitFor("use voice", time.Minute, func() bool { return a.studioVoice != "" })
	v := a.findVoice(a.studioVoice)
	t.Logf("curated: saved %q (ref %.1f s, checked %v), Studio model %s", v.Name, v.RefSeconds, v.RefCheck != nil && v.RefCheck.TextChecked, a.preset().ID)
	if a.refWork(v) {
		t.Fatal("a curated voice has reference work")
	}
	start := time.Now()
	take("curated take")
	if len(a.refMakes) != 0 {
		t.Errorf("a reference was made for the curated voice: %v", a.refMakes)
	}
	t.Logf("curated take in %.0f s, no reference made", time.Since(start).Seconds())

	old, _ := gallery.Default().ByID("f-grandma-osmanthus")
	attrs, _ := json.Marshal(old.Attrs)
	// As the earlier Use voice saved it: attributes, seed, the model it
	// was previewed with.
	vdm, _ := catalog.Builtin().ByID(curateVD)
	saved, err := a.lib.svc.Voices.SaveFiles(ctx, core.Voice{Name: "桂花奶奶（旧）", Kind: core.VoiceDesigned, DesignAttrs: attrs, Seed: old.Seed,
		Language: "zh", Origin: "gallery:" + old.ID, CreatedWith: vdm.Repo}, core.VoiceFiles{})
	if err != nil {
		t.Fatal(err)
	}
	a.voiceChanged(core.VoiceChanged{Voice: saved})
	// Its own recipe on the design model, as before #94 (with neither a
	// sample nor a reference, clone models can't speak it).
	a.selectModel(vdm)
	a.studioVoice, a.studioClip = saved.ID, nil
	start = time.Now()
	take("old voice take")
	t.Logf("old voice on %s: take in %.0f s", vdm.ID, time.Since(start).Seconds())
	// And Make reference gives it one from that recipe: then clone models
	// speak it too.
	ov := a.findVoice(saved.ID)
	done := false
	a.makeReference(ov, func(bool) { done = true })
	waitFor("old voice reference", 15*time.Minute, func() bool { return done })
	ov = a.findVoice(saved.ID)
	t.Logf("old voice: reference made (%.1f s, checked %v, %q) %s", ov.RefSeconds, ov.RefCheck != nil && ov.RefCheck.TextChecked, ov.RefText, a.refFailed(ov.ID))
	if ov.RefAudio == "" {
		t.Error("the old voice got no reference")
	}
}
