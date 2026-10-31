package main

// The first-run wizard (M7a; VoiceStudio's four steps, without a Python
// runtime to install): 1 this Mac (macOS, chip, memory, disk, network,
// engine, ffmpeg, permissions), 2 a model pack chosen by memory (Fast,
// Balanced, Quality, Max: catalogue ids, downloaded as jobs), 3 privacy,
// 4 dictation; then the first sound: the Studio says a welcome line with
// Kokoro as soon as it is downloaded. A fresh install opens it; Settings
// → General runs it again. The view is in onboarding_view.go.

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// onboardingState is the wizard's state.
type onboardingState struct {
	obOpen    bool
	obStep    int    // 0 this Mac, 1 models, 2 privacy, 3 dictation
	obPack    string // the pack chosen; "" = none
	obPicked  bool   // the user chose a pack or none: the recommended one is no longer the default
	obFree    int64  // free space on the model folder's disk
	obChecked bool   // the system check ran
	obKeep    int    // history: 0 every take, 1 the last 1000, 2 the last 200

	firstSound   bool // the welcome line waits for Kokoro and the engine
	fsRefreshed  bool
	demoResource string // a bundled welcome clip, when the app ships one
}

// modelPack is a set of catalogue models for a kind of Mac.
type modelPack struct {
	id    string
	minGB int      // memory it is recommended from
	adds  []string // catalogue ids, on top of the smaller packs
}

// modelPacks go from small to large; each holds the ones before it.
var modelPacks = []modelPack{
	{"fast", 0, []string{packStudioModel, catalog.DefaultDictation, catalog.DefaultVAD, catalog.DefaultWatermark}},
	{"balanced", 16, []string{"qwen3-tts-0.6b-base-8bit", catalog.DefaultAlign, catalog.DefaultEnhance}},
	{"quality", 24, []string{"qwen3-tts-1.7b-base-8bit", "qwen3-tts-1.7b-voicedesign-4bit", catalog.DefaultSeparate, catalog.DefaultSpeakers, catalog.DefaultLLM}},
	{"max", 48, []string{"omnivoice-bf16", catalog.FallbackSTT}},
}

// packStudioModel stands for the Studio's model of a new user in the
// interface's language (presets: Kokoro in English, Qwen3-TTS
// CustomVoice in Chinese).
const packStudioModel = "studio-default"

// packModelID resolves a pack's model id.
func packModelID(id string) string {
	if id != packStudioModel {
		return id
	}
	e, _ := catalog.Builtin().DefaultModel(catalog.WorkflowStudio, i18n.Locale(), false)
	return e.ID
}

// packTitle and packAbout describe a pack.
func packTitle(id string) string {
	switch id {
	case "fast":
		return i18n.T("Fast")
	case "balanced":
		return i18n.T("Balanced")
	case "quality":
		return i18n.T("Quality")
	case "max":
		return i18n.T("Max")
	}
	return i18n.T("None")
}

func packAbout(id string) string {
	switch id {
	case "fast":
		if packModelID(packStudioModel) != "kokoro-82m-bf16" {
			return i18n.T("Ready-made Chinese and English voices, and dictation.")
		}
		return i18n.T("Natural English voices, and dictation. Light on memory.")
	case "balanced":
		return i18n.T("Adds voice cloning, word times for transcripts, and noise removal.")
	case "quality":
		return i18n.T("Adds high-fidelity cloning and voice design, dubbing (voice separation, speakers) and a local writing assistant.")
	case "max":
		return i18n.T("Adds a multilingual voice model (600+ languages, non-commercial licence) and transcription of 99 languages.")
	}
	return ""
}

// recommendPack is the pack for a Mac with memGB of memory.
func recommendPack(memGB int) string {
	id := modelPacks[0].id
	for _, p := range modelPacks {
		if memGB >= p.minGB {
			id = p.id
		}
	}
	return id
}

// packEntries are the catalogue entries of a pack (with the smaller
// packs'), the ones the catalogue lists.
func packEntries(id string) []catalog.Entry {
	var out []catalog.Entry
	for _, p := range modelPacks {
		for _, eid := range p.adds {
			if e, ok := catalog.Builtin().ByID(packModelID(eid)); ok && !e.Hidden && !slices.ContainsFunc(out, func(x catalog.Entry) bool { return x.ID == e.ID }) {
				out = append(out, e)
			}
		}
		if p.id == id {
			break
		}
	}
	return out
}

// packSizes are a pack's size and what of it is still to download.
func (a *app) packSizes(id string) (total, missing int64, names []string) {
	for _, e := range packEntries(id) {
		total += e.Size
		if m := a.model(e); !m.known || m.local.State != hub.Downloaded {
			missing += e.Size
			names = append(names, e.DisplayName())
		}
	}
	return total, missing, names
}

// openOnboarding opens the wizard at its first step.
func (a *app) openOnboarding() {
	a.obOpen, a.obStep, a.obChecked, a.obPicked = true, 0, false, false
	a.loadSysInfo()
	a.refreshModels()
	if !a.ffScanned {
		a.ffScanned = true
		a.scanFFmpeg()
	}
	if a.prefs.Setup.Pack != "" {
		a.obPack, a.obPicked = a.prefs.Setup.Pack, true
	}
	switch n := a.prefs.Studio.KeepTakes; {
	case n == 0:
		a.obKeep = 0
	case n > 200:
		a.obKeep = 1
	default:
		a.obKeep = 2
	}
}

// maybeOnboard opens the wizard on a fresh install (openWindow).
func (a *app) maybeOnboard() {
	if !a.prefs.Setup.Done {
		a.openOnboarding()
	}
	if res, err := mygo.App.Path(mygo.PathResources); err == nil {
		// A bundled welcome clip plays while Kokoro downloads, when the
		// app ships one: resources/demo/welcome-<locale>.wav (none yet).
		for _, name := range []string{"welcome-" + i18n.Locale() + ".wav", "welcome-en.wav"} {
			if p := filepath.Join(res, "demo", name); fileExists(p) {
				a.demoResource = p
				break
			}
		}
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// runSystemCheck measures what the first step shows (free space, the
// network through the proxy) in the background.
func (a *app) runSystemCheck() {
	a.obChecked = true
	dir := modelCache(a.prefs)
	a.background(func() {
		free := diskFree(dir)
		a.post(func() { a.obFree = free })
	})
	a.testNetwork(nil)
	a.recheckPermissions()
}

// startPack downloads a pack's missing models (the licence dialog comes
// first for those that need it, so they go last).
func (a *app) startPack(id string) {
	entries := packEntries(id)
	slices.SortStableFunc(entries, func(x, y catalog.Entry) int {
		gx, gy := x.Licence.RequiresAcceptance, y.Licence.RequiresAcceptance
		switch {
		case gx == gy:
			return 0
		case gx:
			return 1
		}
		return -1
	})
	for _, e := range entries {
		if m := a.model(e); m.known && m.local.State == hub.Downloaded {
			continue
		}
		a.download(e)
	}
}

// finishOnboarding closes the wizard and asks for the first sound.
func (a *app) finishOnboarding() {
	pack := a.obPack
	keep := []int{0, 1000, 200}[a.obKeep]
	a.updateSettings(func(s *settings.Settings) {
		s.Setup.Done, s.Setup.Pack = true, pack
		s.Studio.KeepTakes = keep
	})
	a.obOpen = false
	choose := a.obReuse == reuseChoose
	if pack != "" {
		if choose {
			a.startPack(pack)
		} else {
			// The pack's models already on this Mac are AudioSmith's too
			// ("Use them", the reuse card's default; family_reuse.go).
			a.reusePack(pack, func() { a.startPack(pack) })
		}
	}
	if !a.prefs.Setup.FirstSound && !choose { // the welcome line would leave Models for the Studio
		a.firstSound, a.fsRefreshed = true, false
		if a.demoResource != "" {
			a.pl.Toggle("demo:welcome", a.demoResource)
		}
	}
	if choose {
		a.navigate("/models") // "Choose models…": the installed ones say who installed them
		return
	}
	a.navigate("/studio")
}

// skipOnboarding closes the wizard for good (Settings → General opens it).
func (a *app) skipOnboarding() {
	a.obOpen = false
	a.updateSettings(func(s *settings.Settings) { s.Setup.Done = true })
}

// welcomeTake is the first sound: a line in the UI's language by the
// Studio's model for it (the fast pack's), in its recommended voice.
func welcomeTake() core.Take {
	t := core.Take{Text: i18n.T("Welcome to AudioSmith. This voice was made on your Mac, and nothing left it."), Speed: 1}
	if e, ok := catalog.Builtin().ByID(packModelID(packStudioModel)); ok {
		t.Model = e.Repo
		if p, ok := catalog.PresetFor(e.ID); ok {
			t.Voice = p.VoiceFor(i18n.Locale())
		}
	}
	return t
}

// maybeFirstSound says the welcome line once Kokoro is downloaded and
// the engine is free. Called each frame while it waits: cheap, and it
// acts once.
func (a *app) maybeFirstSound() {
	if !a.firstSound || a.obOpen || a.licenceOpen {
		return
	}
	e, ok := catalog.Builtin().ByRepo(welcomeTake().Model)
	if !ok {
		a.firstSound = false
		return
	}
	m := a.model(e)
	if !m.known {
		if !a.fsRefreshed {
			a.fsRefreshed = true
			a.refreshModel(e)
		}
		return
	}
	if m.local.State != hub.Downloaded || a.engState != engineReady || a.job != nil {
		return
	}
	a.firstSound = false
	tk := welcomeTake()
	a.reuse(&tk)
	a.navigate("/studio")
	a.generate()
	a.updateSettings(func(s *settings.Settings) { s.Setup.FirstSound = true })
}

// onboardingRow is Settings → General's way back to the wizard.
func (a *app) onboardingRow(c *ui.Context) {
	settingRow(c, "setup-again", i18n.T("Setup"), i18n.T("Check this Mac, choose a model pack, privacy and dictation, as on the first day."), func() {
		if secondaryButton(c.Key("setup-again"), i18n.T("Run the setup again")).Clicked() {
			a.openOnboarding()
		}
	})
}
