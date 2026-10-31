package main

// The first-run wizard's view: the steps, header, footer, and the pages
// this Mac, models, privacy and dictation; split from onboarding.go.

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// onboarding is the wizard, over the window: step breadcrumbs with a thin
// ink progress line, a big title, one column, Back at the bottom left and
// Continue at the bottom right.
func (a *app) onboarding(c *ui.Context) {
	a.maybeFirstSound()
	if !a.obOpen {
		return
	}
	t := c.Theme()
	k := tok(t)
	w, h := c.Size()
	open := true
	ui.DialogBase(c.Key("onboarding"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35))
		panel.Width(min(px(t, 720), w-px(t, sp6))).Height(min(px(t, 600), h-px(t, sp6))).Radius(px(t, radiusCard)).Background(k.Bg).Border(1, k.Line).
			Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.3)).Label(i18n.T("Set up AudioSmith"))
		ui.Column(c).Fill().Children(func() {
			a.obHeader(c)
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(px(t, sp2), px(t, sp8), px(t, sp5)).Gap(px(t, sp3)).Children(func() {
					a.obTitle(c)
					switch a.obStep {
					case 0:
						a.obSystem(c)
					case 1:
						a.obModels(c)
					case 2:
						a.obPrivacy(c)
					default:
						a.obDictation(c)
					}
				})
			})
			ui.Box(c).Height(1).Background(k.Line)
			a.obFooter(c)
		})
	})
	if !open { // Esc: the setup can wait; Settings → General opens it
		a.skipOnboarding()
	}
}

// obSteps are the steps' titles.
func obSteps() []string {
	return []string{i18n.T("This Mac"), i18n.T("Models"), i18n.T("Privacy"), i18n.T("Dictation")}
}

// obHeader is the steps as breadcrumbs (done ones with a check), the
// language, and the progress line under them.
func (a *app) obHeader(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	steps := obSteps()
	ui.Row(c).Padding(px(t, sp3), px(t, sp5), px(t, sp3), px(t, sp8)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Grow(1).MinWidth(0).Wrap().Children(func() {
			for i, s := range steps {
				if i > 0 {
					ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text3)
				}
				ui.Row(c.Key("ob-step-" + itoa(i))).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
					col := k.Text3
					switch {
					case i == a.obStep:
						col = k.Text
					case i < a.obStep:
						col = k.Text2
						ui.Icon(c, icons.CheckCircle.Bold).Size(px(t, 12), px(t, 12)).TextColor(k.Accent)
					}
					tx := ui.Text(c, s).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
					if i == a.obStep {
						tx.FontWeight(600)
					}
				})
			}
		})
		langs := languageOptions()
		dropButton(c.Key("ob-lang"), icons.Global, a.settingsForm.language).Label(i18n.T("Language")).Tooltip(i18n.T("Language")).Shrink(0).Menu(func(m *ui.Menu) {
			for i, l := range langs {
				if m.Item(l).Checked(l == a.settingsForm.language).Chosen() {
					a.settingsForm.language = l
					tag := ""
					if i > 0 {
						tag = i18n.Supported()[i-1]
					}
					a.updateSettings(func(s *settings.Settings) { s.General.Language = tag })
				}
			}
		})
	})
	// The progress line: thin, in ink.
	frac := float32(a.obStep+1) / float32(len(steps))
	ui.Box(c).Height(2).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, k.Line, 0)
		p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, k.Ink, 0)
	})
}

// obTitle is the step's big title and its line of explanation.
func (a *app) obTitle(c *ui.Context) {
	t := c.Theme()
	title, sub := obSteps()[min(a.obStep, 3)], ""
	switch a.obStep {
	case 0:
		title = i18n.T("Welcome to AudioSmith")
		sub = i18n.T("Speech, voices and dubbing, made on this Mac. Four short steps; everything can be changed later in Settings.")
	case 1:
		sub = i18n.T("Choose what to download now. Each model downloads once, resumes if interrupted, and can be added or removed later in Models.")
	case 3:
		sub = i18n.T("Dictate into any app: press the shortcut, speak, and the text is typed where your cursor is. Speech stays on this Mac.")
	}
	ui.Column(c).Gap(px(t, sp1)).Margin(px(t, sp3), 0, px(t, sp2)).Children(func() {
		ui.Text(c, title).FontSize(px(t, fsTitle)).FontWeight(600)
		if sub != "" {
			ui.Text(c, sub).TextColor(tok(t).Text2)
		}
	})
}

func (a *app) obFooter(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Padding(px(t, sp3), px(t, sp5)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if a.obStep > 0 && ghostIconButton(c.Key("ob-back"), icons.AltArrowLeft, i18n.T("Back")).Label(i18n.T("Back")).Clicked() {
			a.obStep--
		}
		ui.Spacer(c)
		if ghostButton(c.Key("ob-skip"), i18n.T("Skip setup")).Tooltip(i18n.T("Settings → General runs it again")).Clicked() {
			a.skipOnboarding()
		}
		if a.obStep < 3 {
			if primaryButton(c.Key("ob-next"), i18n.T("Continue")).Clicked() {
				a.obStep++
			}
			return
		}
		if primaryButton(c.Key("ob-finish"), i18n.T("Start using AudioSmith")).Clicked() {
			a.finishOnboarding()
		}
	})
}

// obCheckRow is a line of the system check: a status mark, what was
// checked, and a detail under it.
func obCheckRow(c *ui.Context, key, status, title, detail string, action func()) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, 6), 0).Children(func() {
		ui.Column(c).Size(px(t, 18), px(t, 18)).Center().Shrink(0).Children(func() {
			switch status {
			case "warn":
				ui.Icon(c, icons.DangerTriangle.Linear).Size(px(t, 18), px(t, 18)).TextColor(t.Warning)
			case "fail":
				ui.Icon(c, icons.CloseCircle.Linear).Size(px(t, 18), px(t, 18)).TextColor(t.Danger)
			case "wait":
				ui.Spinner(c).Size(px(t, 14), px(t, 14))
			default:
				ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 18), px(t, 18)).TextColor(t.Success)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
			ui.Text(c, title)
			if detail != "" {
				ui.Text(c, detail).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			}
		})
		if action != nil {
			action()
		}
	})
}

// obSystem is step 1: this Mac.
func (a *app) obSystem(c *ui.Context) {
	if !a.obChecked {
		a.runSystemCheck()
	}
	caption(c, i18n.T("AudioSmith needs no other software: the models run on the Mac's GPU with MLX."))
	s := a.sys
	if s == nil {
		obCheckRow(c, "ob-sys", "wait", i18n.T("Looking at this Mac…"), "", nil)
	} else {
		st := "pass"
		if major := s.macOSMajor(); major > 0 && major < 15 {
			st = "fail"
		}
		obCheckRow(c, "ob-os", st, orDash(s.OS), i18n.T("AudioSmith needs macOS 15 or later."), nil)
		st = "pass"
		if !s.appleSilicon() {
			st = "fail"
		}
		obCheckRow(c, "ob-chip", st, orDash(s.Chip), i18n.T("Models run on Apple silicon (M1 or later)."), nil)
		st, detail := "pass", i18n.T("The %s pack suits this Mac.", packTitle(recommendPack(s.MemoryGB())))
		if s.MemoryGB() < 16 {
			st = "warn"
			detail = i18n.T("Small models suit this Mac best; large ones may be slow.")
		}
		obCheckRow(c, "ob-mem", st, i18n.T("%d GB of memory", s.MemoryGB()), detail, nil)
	}
	if a.obFree > 0 {
		st := "pass"
		if a.obFree < 10<<30 {
			st = "warn"
		}
		obCheckRow(c, "ob-disk", st, i18n.T("%s free for models", humanBytes(a.obFree)), shortPath(modelCache(a.prefs)), nil)
	}
	// The network.
	var hf, mirror *netCheck
	for i := range a.netChecks {
		switch a.netChecks[i].host {
		case "https://huggingface.co":
			hf = &a.netChecks[i]
		case "https://hf-mirror.com":
			mirror = &a.netChecks[i]
		}
	}
	switch {
	case a.netTesting || hf == nil:
		obCheckRow(c, "ob-net", "wait", i18n.T("Checking the network…"), "", nil)
	case hf.ok:
		obCheckRow(c, "ob-net", "pass", i18n.T("Hugging Face answers"), i18n.T("Models download from huggingface.co (or the faster mirror)."), nil)
	case mirror != nil && mirror.ok:
		usesMirror := slices.Equal(a.prefs.Models.Endpoints, []string{"https://hf-mirror.com"})
		obCheckRow(c, "ob-net", "warn", i18n.T("huggingface.co does not answer; hf-mirror.com does"), hf.err, func() {
			if ghostButton(c.Key("ob-mirror"), i18n.T("Use the mirror")).Disabled(usesMirror).Clicked() {
				a.updateSettings(func(s *settings.Settings) { s.Models.Endpoints = []string{"https://hf-mirror.com"} })
			}
		})
	default:
		obCheckRow(c, "ob-net", "warn", i18n.T("No download source answers"), i18n.T("You can download models later; a proxy goes in Settings → Models & network."), func() {
			if ghostIconButton(c.Key("ob-net-retry"), icons.Restart, i18n.T("Try again")).Label(i18n.T("Try again")).Clicked() {
				a.testNetwork(nil)
			}
		})
	}
	// The engine.
	switch a.engState {
	case engineReady:
		obCheckRow(c, "ob-engine", "pass", i18n.T("The speech engine runs"), a.engVersion, nil)
	case engineFailed:
		obCheckRow(c, "ob-engine", "fail", i18n.T("The speech engine does not start"), a.engError, nil)
	default:
		obCheckRow(c, "ob-engine", "wait", i18n.T("The speech engine is starting…"), "", nil)
	}
	// ffmpeg (optional).
	switch {
	case a.mediaTool() != nil:
		obCheckRow(c, "ob-ffmpeg", "pass", i18n.T("ffmpeg is ready"), i18n.T("MP3, Opus and FLAC export, WebM and MKV videos."), nil)
	case len(a.ffSystem) > 0:
		p := a.ffSystem[0]
		obCheckRow(c, "ob-ffmpeg", "pass", i18n.T("ffmpeg found: %s", shortPath(p)), i18n.T("Optional: adds MP3, Opus and FLAC export, WebM and MKV videos."), func() {
			if ghostButton(c.Key("ob-ffmpeg-use"), i18n.T("Use it")).Clicked() {
				a.useFFmpeg(p, settings.MediaSystem)
			}
		})
	default:
		obCheckRow(c, "ob-ffmpeg", "pass", i18n.T("ffmpeg is not set up (optional)"), i18n.T("Add it later in Settings → Audio tools for MP3, Opus, FLAC and WebM."), nil)
	}
	// Permissions, asked for in the last step.
	if a.perms.checked {
		micText, micOK := permissionText(a.perms.mic)
		st := "pass"
		if !micOK {
			st = "warn"
		}
		obCheckRow(c, "ob-mic", st, i18n.T("Microphone: %s", micText), i18n.T("For recording voices and dictation; asked when first needed."), nil)
	}
}

// obModels is step 2: a model pack.
func (a *app) obModels(c *ui.Context) {
	t := c.Theme()
	rec := "balanced"
	if a.sys != nil {
		rec = recommendPack(a.sys.MemoryGB())
	}
	// The recommended pack is the default only until the user chooses:
	// "Download models later" is an empty pack, which must stick.
	if !a.obPicked && a.prefs.Setup.Pack == "" {
		a.obPack = rec
	}
	if a.obSurveyFor != a.obPack {
		a.surveyPack(a.obPack) // family_reuse.go
	}
	a.reuseCard(c)
	k := tok(t)
	for _, p := range modelPacks {
		total, missing, names := a.packSizes(p.id)
		chosen := a.obPack == p.id
		card := ui.ButtonBase(c.Key("ob-pack-"+p.id)).Padding(px(t, sp3), px(t, sp4)).Radius(px(t, radiusCard)).Border(1, k.Line).Label(packTitle(p.id)).
			Checked(chosen).Gap(px(t, sp3)).AlignItems(ui.Center)
		switch {
		case chosen:
			card.Background(k.Pressed).Border(1, k.Pressed)
		case card.Hovered():
			card.Background(k.Hover)
		}
		card.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, sp1)).Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
					ui.Text(c, packTitle(p.id)).FontWeight(600)
					if p.id == rec {
						tagChip(c, i18n.T("Recommended for this Mac")).TextColor(k.AccentInk).Background(k.AccentSoft)
					}
					ui.Spacer(c)
					size := humanBytes(total)
					if missing < total {
						size = i18n.T("%s to download of %s", humanBytes(missing), humanBytes(total))
					}
					caption(c, size).SingleLine()
				})
				caption(c, packAbout(p.id))
				if chosen && len(names) > 0 {
					ui.Text(c, i18n.T("Still needed: %s", joinNames(names))).FontSize(px(t, fsMicro)).TextColor(k.Text2)
				}
			})
			ui.Column(c).Size(px(t, 20), px(t, 20)).Center().Shrink(0).Children(func() {
				if chosen {
					ui.Icon(c, icons.CheckCircle.Bold).Size(px(t, 20), px(t, 20)).TextColor(k.Accent)
				}
			})
		})
		if card.Clicked() {
			a.obPack, a.obPicked = p.id, true
		}
	}
	none := ui.ButtonBase(c.Key("ob-pack-none")).Padding(px(t, sp2), px(t, sp4)).Radius(px(t, radiusCard)).Label(i18n.T("Download models later")).
		Checked(a.obPack == "").Gap(px(t, sp3)).AlignItems(ui.Center)
	switch {
	case a.obPack == "":
		none.Background(k.Pressed)
	case none.Hovered():
		none.Background(k.Hover)
	}
	none.Children(func() {
		ui.Text(c, i18n.T("Download models later")).FontSize(px(t, fsCaption)).Grow(1)
		if a.obPack == "" {
			ui.Icon(c, icons.CheckCircle.Bold).Size(px(t, 20), px(t, 20)).TextColor(k.Accent)
		}
	})
	if none.Clicked() {
		a.obPack, a.obPicked = "", true
	}
	if a.obPack != "" {
		_, missing, _ := a.packSizes(a.obPack)
		if a.obFree > 0 && missing > a.obFree-(5<<30) {
			statusMark(c, icons.DangerCircle, t.Danger, i18n.T("Not enough free space: %s needed, %s free.", humanBytes(missing), humanBytes(a.obFree)))
		}
		if a.obPack == "max" {
			banner(c, icons.DangerTriangle, t.Warning, i18n.T("%s's licence (CC BY-NC 4.0) allows non-commercial use only: you are asked to accept it before it downloads.", modelRefID(idOmniVoice)), nil)
		}
		caption(c, i18n.T("Downloads start when you finish, and go on in the background."))
	}
}

func joinNames(names []string) string { return strings.Join(names, " · ") }

// obPrivacy is step 3.
func (a *app) obPrivacy(c *ui.Context) {
	t := c.Theme()
	banner(c, icons.ShieldCheck, t.Success, i18n.T("Everything you write, record and generate stays on this Mac. AudioSmith has no analytics and no account."), nil)
	local := a.prefs.Privacy.LocalOnlyLLM
	settingRowNote(c, "ob-local", i18n.T("Local language models only"), i18n.T("Writing help and translation never use a cloud provider, even one you add later."), func() {
		if inkSwitch(c.Key("ob-local"), &local).Label(i18n.T("Keep text on this Mac")).Changed() {
			on := local
			a.updateSettings(func(s *settings.Settings) { s.Privacy.LocalOnlyLLM = on })
		}
	})
	wm := !a.prefs.Privacy.NoWatermark
	settingRowNote(c, "ob-watermark", i18n.T("Invisible watermark"), i18n.T("Marks generated audio so it can be recognised as made by AudioSmith. Needs a small model."), func() {
		if inkSwitch(c.Key("ob-watermark"), &wm).Label(i18n.T("Add an invisible watermark to generated audio")).Changed() {
			on := wm
			a.updateSettings(func(s *settings.Settings) { s.Privacy.NoWatermark = !on })
		}
	})
	settingRowNote(c, "ob-keep", i18n.T("History"), i18n.T("Older takes that are not starred are deleted beyond the limit."), func() {
		segmented(c.Key("ob-keep"), &a.obKeep, i18n.T("Keep every take"), i18n.T("Last 1000"), i18n.T("Last 200")).Label(i18n.T("History"))
	})
	caption(c, i18n.T("Settings → Privacy says exactly what may use the network, and when."))
}

// obDictation is step 4.
func (a *app) obDictation(c *ui.Context) {
	f := &a.dictSet
	f.enabled = a.prefs.Dictation.Enabled
	settingRowNote(c, "ob-dict", i18n.T("Dictation shortcut"), i18n.T("Works in every app while AudioSmith runs."), func() {
		if inkSwitch(c.Key("ob-dict-switch"), &f.enabled).Label(i18n.T("Dictation shortcut")).Changed() {
			on := f.enabled
			a.updateSettings(func(s *settings.Settings) { s.Dictation.Enabled = on })
			a.applyDictShortcut()
		}
	})
	if a.prefs.Dictation.Enabled {
		a.shortcutRow(c) // dictation_settings.go
		if a.dictSys != nil {
			a.permissionsSettings(c) // permissions.go: microphone, accessibility
		}
	}
	caption(c, i18n.T("When you finish, the Studio says hello as soon as its voice is downloaded."))
}

// obPackNames lists a pack's models (tests, the palette).
func obPackNames(id string) []string {
	var out []string
	for _, e := range packEntries(id) {
		out = append(out, e.DisplayName())
	}
	return out
}
