package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/sysinfo"
)

// stubMachine makes the system check see a 16 GB M4 on macOS 15 with
// Hugging Face reachable.
func stubMachine(t *testing.T, memGB int64, hfOK bool) {
	t.Helper()
	savedSys, savedReach := readSysInfo, reach
	t.Cleanup(func() { readSysInfo, reach = savedSys, savedReach })
	readSysInfo = func() sysInfo {
		return sysInfo{OS: "macOS 15.4", Chip: "Apple M4", Memory: memGB << 30, Cores: 10, Arch: "arm64"}
	}
	reach = func(_ *http.Client, host string) netCheck {
		if strings.Contains(host, "huggingface") && !hfOK {
			return netCheck{host: host, err: "connection refused"}
		}
		return netCheck{host: host, ok: true, latency: 50 * time.Millisecond}
	}
}

func TestModelPacks(t *testing.T) {
	for gb, want := range map[int]string{8: "fast", 16: "balanced", 18: "balanced", 24: "quality", 36: "quality", 64: "max"} {
		if got := recommendPack(gb); got != want {
			t.Errorf("%d GB: %s, want %s", gb, got, want)
		}
	}
	// Every id is in the catalogue and listed: a renamed entry fails here.
	for _, p := range modelPacks {
		for _, id := range p.adds {
			e, ok := catalog.Builtin().ByID(packModelID(id))
			if !ok || e.Hidden {
				t.Errorf("pack %s: %s is not listed", p.id, id)
			}
		}
	}
	fast, max := packEntries("fast"), packEntries("max")
	if len(fast) != 4 || len(max) != 4+3+5+2 {
		t.Errorf("fast %d, max %d", len(fast), len(max))
	}
	if !slices.ContainsFunc(max, func(e catalog.Entry) bool { return e.ID == "kokoro-82m-bf16" }) {
		t.Error("max lacks the fast pack")
	}
}

func TestOnboarding(t *testing.T) {
	stubMachine(t, 16, true)
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload) // record downloads, do not run them
	tt := ui.NewTester(a.view, 1100, 800)
	if !a.settings.Get().Setup.Done && a.obOpen {
		t.Fatal("tests open the wizard by themselves")
	}
	a.openOnboarding()
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"Welcome to AudioSmith", "macOS 15.4", "Apple M4", "16 GB of memory", "Hugging Face answers", "The speech engine runs"} {
		if !tt.HasText(s) {
			t.Errorf("step 1 lacks %q: %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.obStep != 1 || a.obPack != "balanced" || !tt.HasText("Recommended for this Mac") {
		t.Fatalf("step 2: %d %q %q", a.obStep, a.obPack, tt.Texts())
	}
	if err := tt.Click("Quality"); err != nil {
		t.Fatal(err)
	}
	tt.Click("Continue")
	tt.Frame()
	if a.obStep != 2 || a.obPack != "quality" || !tt.HasText("Invisible watermark") {
		t.Fatalf("step 3: %d %q", a.obStep, a.obPack)
	}
	a.obKeep = 2
	tt.Click("Continue")
	tt.Frame()
	if a.obStep != 3 || !tt.HasText("Dictation shortcut") {
		t.Fatalf("step 4: %q", tt.Texts())
	}
	if err := tt.Click("Back"); err != nil || func() bool { tt.Frame(); return a.obStep != 2 }() {
		t.Fatalf("back: %d %v", a.obStep, err)
	}
	tt.Click("Continue")
	tt.Frame()
	if err := tt.Click("Start using AudioSmith"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	s := a.settings.Get()
	if !s.Setup.Done || s.Setup.Pack != "quality" || s.Studio.KeepTakes != 200 || a.obOpen {
		t.Fatalf("after: %+v keep %d open %v", s.Setup, s.Studio.KeepTakes, a.obOpen)
	}
	// Models on disk are not downloaded again (Kokoro is), and the first
	// sound plays at once with Kokoro.
	kokoro, _ := catalog.Builtin().ByID("kokoro-82m-bf16")
	kokoroRepo := kokoro.Repo
	for _, r := range eng.sent {
		if r.Spec.Kind == hub.KindDownload && (r.Model == kokoroRepo || !slices.ContainsFunc(packEntries("quality"), func(e catalog.Entry) bool { return e.Repo == r.Model })) {
			t.Errorf("downloaded %s", r.Model)
		}
	}
	if a.job == nil || !strings.Contains(a.text, "Welcome to AudioSmith") || !a.settings.Get().Setup.FirstSound || a.currentPage() != "studio" {
		t.Fatalf("first sound: job %v text %q", a.job, a.text)
	}
	if r := lastKind(t, eng, services.KindRender); !strings.Contains(r.SynthesizeParams.Text, "Welcome") || r.SynthesizeParams.Voice != "af_heart" {
		t.Errorf("first sound request %+v", r.SynthesizeParams)
	}
}

func TestOnboardingDownloadsAndMirror(t *testing.T) {
	simulateDarwin(t)
	stubMachine(t, 8, false)
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload) // record downloads, do not run them
	tt := ui.NewTester(a.view, 1100, 800)
	a.updateSettings(func(s *settings.Settings) { s.Models.CacheDir = t.TempDir() }) // nothing on disk
	a.openOnboarding()
	tt.Frame()
	tt.Frame()
	if !tt.HasText("huggingface.co does not answer; hf-mirror.com does") || !tt.HasText("Small models suit this Mac best") {
		t.Fatalf("step 1 %q", tt.Texts())
	}
	if err := tt.Click("Use the mirror"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if eps := a.settings.Get().Models.Endpoints; !slices.Equal(eps, []string{"https://hf-mirror.com"}) {
		t.Errorf("endpoints %v", eps)
	}
	tt.Click("Continue")
	tt.Frame()
	if a.obPack != "fast" || !tt.HasText("Still needed:") {
		t.Fatalf("pack %q %q", a.obPack, tt.Texts())
	}
	a.obStep = 3
	tt.Frame()
	if err := tt.Click("Start using AudioSmith"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	var got []string
	for _, r := range eng.sent {
		if r.Spec.Kind == hub.KindDownload {
			got = append(got, r.Model)
		}
	}
	if len(got) != 4 {
		t.Fatalf("downloads %v", got)
	}
	// Kokoro is not there yet: the first sound waits.
	if a.job != nil || !a.firstSound {
		t.Errorf("first sound before Kokoro: job %v pending %v", a.job, a.firstSound)
	}
	// Skip: the wizard closes and stays closed; Settings → General opens it.
	a.openOnboarding()
	tt.Frame()
	tt.Click("Skip setup")
	tt.Frame()
	if a.obOpen || !a.settings.Get().Setup.Done {
		t.Error("skip")
	}
	a.navigate("/settings/general")
	tt.Frame()
	if err := tt.Click("Run the setup again"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.obOpen {
		t.Error("not reopened")
	}
}

// TestPacksFollowTheLanguage: the fast pack and the first sound use the
// Studio's model for the interface's language (presets): Kokoro and
// af_heart in English, Qwen3-TTS CustomVoice and Serena in Chinese
// (Kokoro's Chinese voices are hidden).
func TestPacksFollowTheLanguage(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	for _, w := range []struct{ lang, model, voice string }{
		{"en", "kokoro-82m-bf16", "af_heart"},
		{"zh-Hans", "qwen3-tts-0.6b-customvoice-8bit", "Serena"},
	} {
		i18n.SetLocale(w.lang)
		fast := packEntries("fast")
		if !slices.ContainsFunc(fast, func(e catalog.Entry) bool { return e.ID == w.model }) {
			t.Errorf("%s: fast pack %v", w.lang, fast)
		}
		e, _ := catalog.Builtin().ByID(w.model)
		if tk := welcomeTake(); tk.Model != e.Repo || tk.Voice != w.voice {
			t.Errorf("%s: welcome %s %q", w.lang, tk.Model, tk.Voice)
		}
	}
}

// TestOnboardingLaterStays: "Download models later" is an empty pack; a
// frame must not put the recommended pack back (issue #1: the choice
// looked dead, and the wizard went on with the recommended pack).
func TestOnboardingLaterStays(t *testing.T) {
	stubMachine(t, 16, true)
	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload)
	tt := ui.NewTester(a.view, 1100, 800)
	a.openOnboarding()
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.obPack != "balanced" {
		t.Fatalf("the recommended pack is the default: %q", a.obPack)
	}
	if err := tt.Click("Download models later"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if a.obPack != "" {
		t.Fatalf("after 'Download models later' the pack is %q, not none", a.obPack)
	}
	if err := tt.Click("Quality"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.obPack != "quality" {
		t.Fatalf("choosing a pack after none: %q", a.obPack)
	}
}

func TestOnboardingAndTrayWindowsWording(t *testing.T) {
	prevOS := currentOS
	currentOS = func() string { return "windows" }
	t.Cleanup(func() {
		currentOS = prevOS
		i18n.SetLocale("en")
	})

	savedSys, savedReach := readSysInfo, reach
	t.Cleanup(func() { readSysInfo, reach = savedSys, savedReach })
	readSysInfo = func() sysInfo {
		return sysInfo{
			OS:     "Windows 11 (26100)",
			Chip:   "Intel(R) Core(TM) i7-1360P",
			Memory: 16 << 30,
			Cores:  12,
			Arch:   "amd64",
			GPUs: []sysinfo.GPU{{
				Name:       "Intel(R) Iris(R) Xe Graphics",
				Vendor:     sysinfo.VendorIntel,
				Integrated: true,
			}},
			GPUClass: sysinfo.GPUClassIntegrated,
		}
	}
	reach = func(_ *http.Client, host string) netCheck {
		return netCheck{host: host, ok: true, latency: 20 * time.Millisecond}
	}

	a, eng, _ := newTestApp(t)
	delete(eng.executors, hub.KindDownload)
	tt := ui.NewTester(a.view, 1100, 800)

	// Step 1: This PC, Windows 10/11, CPU + iGPU, ONNX/llama.cpp — no Mac/MLX copy.
	a.openOnboarding()
	tt.Frame()
	tt.Frame()
	for _, want := range []string{
		"This PC",
		"made on this PC",
		"ONNX Runtime (DirectML / CPU) and llama.cpp",
		"Windows 11 (26100)",
		"AudioSmith runs on Windows 10 or 11.",
		"Intel(R) Core(TM) i7-1360P",
		"Integrated GPU: Intel(R) Iris(R) Xe Graphics (DirectML / CPU).",
		"suits this PC.",
	} {
		if !tt.HasText(want) {
			t.Errorf("Windows step 1 missing %q: %q", want, tt.Texts())
		}
	}
	for _, forbid := range []string{"This Mac", "macOS", "Apple silicon", "MLX"} {
		if tt.HasText(forbid) {
			t.Errorf("Windows step 1 must not mention %q: %q", forbid, tt.Texts())
		}
	}

	// Step 2: Recommended for this PC.
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Recommended for this PC") || tt.HasText("Recommended for this Mac") {
		t.Errorf("Windows step 2 wording: %q", tt.Texts())
	}

	// Step 3: Privacy stays on this PC.
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("stays on this PC") || tt.HasText("stays on this Mac") {
		t.Errorf("Windows step 3 wording: %q", tt.Texts())
	}

	// Step 4: Dictation stays on this PC.
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Speech stays on this PC.") || tt.HasText("Speech stays on this Mac.") {
		t.Errorf("Windows step 4 wording: %q", tt.Texts())
	}

	// welcomeTake uses PC wording on Windows.
	if tk := welcomeTake(); !strings.Contains(tk.Text, "your PC") || strings.Contains(tk.Text, "Mac") {
		t.Errorf("Windows welcomeTake = %q", tk.Text)
	}

	// Settings → General uses system tray and PC setup wording.
	a.skipOnboarding()
	a.navigate("/settings/general")
	tt.Frame()
	if !tt.HasText("Keep running in the system tray") || tt.HasText("Keep running in the menu bar") {
		t.Errorf("Windows Settings → General tray wording: %q", tt.Texts())
	}
	if !tt.HasText("Check this PC") || tt.HasText("Check this Mac") {
		t.Errorf("Windows Settings → General setup wording: %q", tt.Texts())
	}

	// Verify zh-Hans and ja Windows tray + onboarding copy.
	for _, tc := range []struct {
		locale, tray, pc string
	}{
		{"zh-Hans", "在系统托盘中保持运行", "这台电脑"},
		{"ja", "システムトレイで実行し続ける", "このPC"},
	} {
		i18n.SetLocale(tc.locale)
		tt.Frame()
		if !tt.HasText(tc.tray) {
			t.Errorf("%s Windows tray missing %q: %q", tc.locale, tc.tray, tt.Texts())
		}
		if steps := obSteps(); steps[0] != tc.pc {
			t.Errorf("%s Windows obSteps[0] = %q, want %q", tc.locale, steps[0], tc.pc)
		}
	}
}

func TestSettingsGeneralMenuBarUnchangedOnDarwin(t *testing.T) {
	simulateDarwin(t)
	stubMachine(t, 16, true)
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1100, 800)
	a.navigate("/settings/general")
	tt.Frame()
	if !tt.HasText("Keep running in the menu bar") || tt.HasText("Keep running in the system tray") {
		t.Errorf("macOS Settings → General tray wording changed: %q", tt.Texts())
	}
	if !tt.HasText("Check this Mac") {
		t.Errorf("macOS Settings → General setup wording changed: %q", tt.Texts())
	}
}
