package main

// Settings: the settings service seen from the UI (a snapshot in
// a.prefs, kept by settings.Changed), the language and the theme they
// drive, the settings form's state, and the actions of the shell's own
// sections (the model folder, the token, licences). The dialog with the
// sections under /settings/{section} is settings_dialog.go; the shell's
// own sections are settings_general.go.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// settingsForm is the settings pages' view state: the controls bind to
// these, and their changes go to the service.
type settingsForm struct {
	systemLocale string // systemLanguage(), read when the window opens
	appLocale    string // the language last given to mygo.App.SetLocale ("" follows the system)
	appLocaleSet bool

	language        string // label
	theme           int
	scale           int
	reduceMotion    bool
	source          string // label
	endpoint        string // custom endpoint
	proxyMode       string // label
	proxy           string // custom proxy
	cacheDir        string
	memoryGB        float64
	defaultModel    string // label
	autoplaySetting bool

	token     string // typed, not yet saved
	tokenInfo string // where the token in use comes from
	proxyInfo string // the proxy downloads use
}

// settingsSection is a page of the settings.
type settingsSection struct{ id, title, en string }

// settingsSections are Settings' sections, in order (the ActNow family's
// window spec §5: General first, the app's own, then models, privacy,
// advanced and about). The sections that became part of one keep their
// paths (settingsAliases).
func settingsSections() []settingsSection {
	return []settingsSection{
		{"general", i18n.T("General"), "General appearance theme language sidebar interface size"},
		{"studio", i18n.T("Studio"), "Studio pronunciation"}, // with Pronunciation (pronunciation_settings.go)
		dubSettingsSection(), // M4: dub_settings.go (thread #19)
		// M2 (thread #17): dictation_settings.go, transcribe_llm_settings.go, permissions.go.
		{"dictation", i18n.T("Dictation"), "Dictation permissions microphone accessibility"},
		{"writing", i18n.T("Writing help"), "Writing help language model LLM Ollama"},
		{"integrations", i18n.T("Integrations"), "Integrations Local API MCP agents"}, // M5: api_settings.go, settings_integrations.go
		// M7a (thread #22): settings_*.go, ffmpeg_settings.go.
		{"audio-tools", i18n.T("Audio tools"), "Audio tools ffmpeg"},
		{"models", i18n.T("Models & network"), "Models downloads network proxy mirror memory"},
		// 0.5 F1: the Engine and Logs pages (engine_view.go, shell_logs.go).
		{"engine", i18n.T("Engine"), "Engine processes restart memory GPU workers"},
		{"logs", i18n.T("Logs"), "Logs log lines diagnostics"},
		{"privacy", i18n.T("Privacy"), "Privacy history analytics"},
		{"advanced", i18n.T("Advanced"), "Advanced storage disk data folder reset usage statistics diagnostics self-check"},
		{"about", i18n.T("About"), "About version updates licence"},
	}
}

// settingsAliases are the sections that are part of another now, and
// that one: their paths ("/settings/network", deep links, notifications)
// open it.
var settingsAliases = map[string]string{
	"appearance":    "general",
	"pronunciation": "studio",
	"permissions":   "dictation",
	"api":           "integrations",
	"network":       "models",
	"storage":       "advanced",
	"usage":         "advanced",
}

// updateSettings changes the settings: at once in the snapshot, and in
// the service (saved in the background, then announced with Changed).
// fn must only set fields, as it runs on both.
func (a *app) updateSettings(fn func(*settings.Settings)) {
	fn(&a.prefs)
	st := a.settings
	a.async(func() {
		if err := st.Update(context.Background(), fn); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not save the settings: %v", err)) })
		}
	})
}

// settingsChanged applies saved settings to the app.
func (a *app) settingsChanged(old, new settings.Settings) {
	a.prefs = new
	if old.General.Language != new.General.Language {
		a.applyLocale()
	}
	if !slices.Equal(old.Models.Endpoints, new.Models.Endpoints) || old.Models.CacheDir != new.Models.CacheDir || old.Models.Proxy != new.Models.Proxy {
		a.resetHub() // the library resets it too; downloads running keep theirs
		if old.Models.Proxy != new.Models.Proxy {
			a.loadProxyInfo()
		}
		if old.Models.CacheDir != new.Models.CacheDir {
			a.refreshModels()
		}
	}
	a.applyEngineSettings(old, new)
	a.m2SettingsChanged(old, new)     // M2: dictation_settings.go
	a.ffmpegSettingsChanged(old, new) // M7a: ffmpeg_settings.go
	if old.Studio.Autoplay != new.Studio.Autoplay {
		a.autoplay = new.Studio.Autoplay
	}
	if beta := betaUpdates(new, a.version); beta != betaUpdates(old, a.version) {
		if a.updates.setPrereleases != nil {
			a.updates.setPrereleases(beta) // updates.go (thread #52)
		}
	}
	a.loadSettingsForm()
}

// applyEngineSettings hands changed engine settings to the supervisor:
// the memory budget and the idle limit apply at once; a new model folder
// with the next start of the engine, which happens now unless it works.
func (a *app) applyEngineSettings(old, new settings.Settings) {
	if a.eng == nil || a.eng.sup == nil {
		return
	}
	sup := a.eng.sup
	if old.Engine.MemoryBudgetGB != new.Engine.MemoryBudgetGB {
		sup.SetMemoryBudget(gb(new.Engine.MemoryBudgetGB))
	}
	if old.Models.AutoUnload != new.Models.AutoUnload {
		sup.SetIdleUnload(time.Duration(new.Models.AutoUnload) * time.Minute)
	}
	if dir := modelCache(new); dir != modelCache(old) {
		sup.SetCacheDir(dir)
		if a.job == nil && len(a.activeJobs()) == 0 && a.engState == engineReady {
			a.restartEngine()
		} else {
			a.toast(i18n.T("The engine uses the new model folder after its next restart."))
		}
	}
}

// systemLanguage is the user's first language in System Settings, which
// the app follows unless Settings names one. (Before MyGo 0.5,
// App.Locale returned it; now it is the language the app shows.)
func systemLanguage() string {
	if langs := mygo.App.PreferredLanguages(); len(langs) > 0 {
		return langs[0]
	}
	return "en"
}

// applyLocale switches the UI to the settings' language, or the system's,
// and rebuilds the native menu in it.
func (a *app) applyLocale() {
	tag := a.prefs.General.Language
	if tag == "" {
		tag = a.systemLocale
	}
	i18n.SetLocale(tag)
	// MyGo's own strings (controls, dialogs), the text
	// engine's glyphs and AppKit's texts follow MyGo's language: give it
	// the one chosen, or none to follow the system as we do.
	if want := a.prefs.General.Language; a.hasMenu && (!a.appLocaleSet || want != a.appLocale) {
		a.appLocale, a.appLocaleSet = want, true
		mygo.App.SetLocale(want)
	}
	if i18n.Locale() != a.menuLocale {
		a.buildMenu()
	}
	a.loadSettingsForm() // its labels are translated
}

// applyTheme sets the frame's theme from the appearance settings: a
// forced light or dark theme, the UI scale, which grows the text and the
// spacing (sizes of our own scale with px), and the design tokens
// (ui_tokens.go) over MyGo's colours.
func (a *app) applyTheme(c *ui.Context) {
	ap := a.prefs.Appearance
	sys := c.Theme()
	th := *sys
	if (ap.Theme == "light" && sys.Dark) || (ap.Theme == "dark" && !sys.Dark) {
		if ap.Theme == "dark" {
			th = *ui.DarkTheme()
		} else {
			th = *ui.LightTheme()
		}
		if s := c.Preferences().TextScale; s > 0 {
			th.FontSize *= s
		}
	}
	if s := float32(ap.Scale); s > 0 && s != 1 {
		th.FontSize *= s
		th.Spacing = th.Space(1) * s
	}
	styleTheme(&th)
	c.SetTheme(&th)
}

// The options of the selects, and what they mean.
func languageOptions() []string {
	opts := []string{i18n.T("Follow the system")}
	for _, tag := range i18n.Supported() {
		opts = append(opts, i18n.Name(tag))
	}
	return opts
}

// sourceOptions: the download source covers models (Hugging Face) and
// tools (GitHub); the wordings are actnowkit's mirror.TextSource*.
// "Custom…" names Hugging Face endpoints; tools then come from both
// github.com and the ActNow mirror.
func sourceOptions() []string {
	return []string{
		i18n.T("Automatic (the fastest of the official sites and mirrors)"),
		i18n.T("Official sites only (huggingface.co, github.com)"),
		i18n.T("Mirrors only (hf-mirror.com, mirror.actnow.dev)"),
		i18n.T("Custom…"),
	}
}

func proxyOptions() []string {
	return []string{i18n.T("Automatic (system proxy)"), i18n.T("None"), i18n.T("Custom…")}
}

var scaleLabels = []string{"90%", "100%", "110%", "125%"}

// loadSettingsForm sets the controls from a.prefs.
func (a *app) loadSettingsForm() {
	s := a.prefs
	f := &a.settingsForm
	f.language = languageOptions()[0]
	for i, tag := range i18n.Supported() {
		if tag == s.General.Language {
			f.language = languageOptions()[i+1]
		}
	}
	f.theme = max(0, slices.Index(settings.Themes, s.Appearance.Theme))
	f.scale = max(0, slices.Index(settings.Scales, s.Appearance.Scale))
	f.reduceMotion = s.Appearance.ReduceMotion
	src := sourceOptions()
	switch {
	case len(s.Models.Endpoints) == 0:
		f.source = src[0]
	case len(s.Models.Endpoints) == 1 && s.Models.Endpoints[0] == "https://huggingface.co":
		f.source = src[1]
	case len(s.Models.Endpoints) == 1 && s.Models.Endpoints[0] == "https://hf-mirror.com":
		f.source = src[2]
	default:
		f.source, f.endpoint = src[3], strings.Join(s.Models.Endpoints, " ")
	}
	px := proxyOptions()
	switch s.Models.Proxy {
	case "":
		f.proxyMode = px[0]
	case hub.ProxyDirect:
		f.proxyMode = px[1]
	default:
		f.proxyMode, f.proxy = px[2], s.Models.Proxy
	}
	f.cacheDir = s.Models.CacheDir
	f.defaultModel = catalog.Builtin().ForTask(catalog.TaskTTS)[0].Label
	if e, ok := catalog.Builtin().ByID(s.Studio.Model); ok {
		f.defaultModel = e.Label
	}
	f.autoplaySetting = s.Studio.Autoplay
	f.memoryGB = s.Engine.MemoryBudgetGB
}

// loadTokenInfo finds which HF token downloads use, in the background
// (the Keychain blocks).
func (a *app) loadTokenInfo() {
	shared := a.lib.sharedHFToken() // family_keys.go: the family's, when used
	a.async(func() {
		tok, src := hub.TokenFrom(shared, hub.DefaultKeychain)
		info := i18n.T("No token: gated models cannot download.")
		switch src {
		case hub.TokenEnv:
			info = i18n.T("Using %s from $HF_TOKEN.", hub.MaskToken(tok))
		case hub.TokenShared, hub.TokenKeychain: // both in the Keychain
			info = i18n.T("Using %s from the Keychain.", hub.MaskToken(tok))
		case hub.TokenCLI:
			info = i18n.T("Using %s from huggingface-cli login.", hub.MaskToken(tok))
		}
		a.post(func() { a.tokenInfo = info })
	})
}

// loadProxyInfo finds the proxy downloads use, in the background.
func (a *app) loadProxyInfo() {
	mode := a.prefs.Models.Proxy
	a.async(func() {
		p := hub.DescribeProxy(mode)
		info := i18n.T("Downloads connect directly.")
		if p != "" {
			info = i18n.T("Downloads go through %s.", p)
		}
		a.post(func() { a.proxyInfo = info })
	})
}

// chooseCacheDir asks for the model folder; the dialog blocks, so it runs
// on a goroutine of its own.
func (a *app) chooseCacheDir() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            a.win,
			Title:             i18n.T("Model folder"),
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() {
			dir := paths[0]
			a.updateSettings(func(s *settings.Settings) { s.Models.CacheDir = dir })
		})
	}()
}

// saveToken saves the HF token in the Keychain ("" removes it).
func (a *app) saveToken(tok string) {
	tok = strings.TrimSpace(tok)
	a.token = ""
	if a.lib.keys != nil {
		// family_keys.go: shared with the other ActNow apps when the user
		// lets them; removing asks first when they use it.
		if tok == "" {
			a.removeFamilyKey(hfKeySlot(a.prefs))
		} else {
			a.saveFamilyKey(hfKeySlot(a.prefs), tok)
		}
		return
	}
	a.async(func() {
		err := hub.DefaultKeychain.Set(tok)
		a.post(func() {
			switch {
			case err != nil:
				a.toastWarn(i18n.T("Could not save the token: %v", err))
			case tok == "":
				a.toast(i18n.T("Token removed"))
			default:
				a.toast(i18n.T("Token saved in the Keychain"))
			}
		})
		if err == nil {
			a.loadTokenInfo()
		}
	})
}

// loadAcceptances refreshes the snapshot of accepted licences.
func (a *app) loadAcceptances() {
	gate := a.licences
	a.async(func() {
		list, err := gate.List(context.Background())
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the licences: %v", err))
				return
			}
			a.accepted = list
		})
	})
}

func (a *app) revokeLicence(acc core.LicenceAcceptance) {
	gate := a.licences
	a.async(func() {
		if err := gate.Revoke(context.Background(), acc.Licence, acc.Model); err != nil {
			a.post(func() { a.toastWarn(fmt.Sprint(err)) })
		}
	})
}
