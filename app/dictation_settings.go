package main

// Settings → Dictation: on/off, the shortcut (record one, check it
// against macOS's and AudioSmith's own, see it verified when pressed),
// toggle or hold, model, language, microphone, clean-up, vocabulary,
// typing or copying, sounds. Settings → General gains "Keep running in
// the menu bar".

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// dictSettingsForm is what Settings → Dictation binds to.
type dictSettingsForm struct {
	enabled   bool
	mode      int // 0 toggle, 1 hold
	model     string
	language  string // label
	device    string // label
	cleanup   bool
	vocab     string
	output    int // 0 type, 1 copy
	sounds    bool
	closeTray bool
	recording bool   // the shortcut recorder listens
	pending   string // the shortcut it heard
	recErr    string
	recHandle ui.Handle
}

// loadDictSettingsForm sets the form from a.prefs.
func (a *app) loadDictSettingsForm() {
	d := a.prefs.Dictation
	f := &a.dictSet
	f.enabled, f.cleanup, f.sounds = d.Enabled, d.Cleanup, d.Sounds
	f.mode, f.output = 0, 0
	if d.Mode == settings.DictationHold {
		f.mode = 1
	}
	if d.Output == settings.OutputCopy {
		f.output = 1
	}
	f.model = a.dictModel().Label
	f.language = dictLangLabel(d.Language)
	f.device = a.deviceLabel(d.Device)
	f.vocab = d.Vocabulary
	f.closeTray = a.prefs.General.CloseToTray
}

// dictLanguages are the languages dictation offers: Auto, then Whisper's.
func dictLanguages() (labels, codes []string) {
	labels, codes = []string{i18n.T("Auto-detect")}, []string{""}
	for _, l := range lang.All() {
		if l.Whisper {
			labels, codes = append(labels, strings.TrimSpace(l.Flag()+" "+l.NameIn(i18n.Locale()))), append(codes, l.Code)
		}
	}
	return labels, codes
}

func dictLangLabel(code string) string {
	labels, codes := dictLanguages()
	if i := slices.Index(codes, code); i >= 0 {
		return labels[i]
	}
	return labels[0]
}

// deviceLabel names a capture device ("" = the system's default).
func (a *app) deviceLabel(id string) string {
	for _, d := range a.clone.devices {
		if d.ID == id && id != "" {
			return d.Name
		}
	}
	return i18n.T("System default")
}

// m2SettingsChanged applies changed settings to transcription, dictation
// and writing help (shell_settings.go calls it).
func (a *app) m2SettingsChanged(old, new settings.Settings) {
	if old.Dictation.Shortcut != new.Dictation.Shortcut || old.Dictation.Enabled != new.Dictation.Enabled {
		a.applyDictShortcut()
	}
	a.applyLLM()
	a.syncLLMForm()
	if !a.dictSet.recording {
		a.loadDictSettingsForm()
	}
	if new.Transcribe.Mode != old.Transcribe.Mode {
		a.trMode = 1
		if new.Transcribe.Mode == settings.TranscribeFast {
			a.trMode = 0
		}
	}
	a.updateTray()
}

// appShortcuts are the window's own shortcuts, which a global one would
// take from it.
func appShortcuts() map[string]string {
	m := map[string]string{
		"CmdOrCtrl+K": "Command palette", "CmdOrCtrl+E": "Engine quick switch", "CmdOrCtrl+,": "Settings",
		"CmdOrCtrl+Enter": "Generate", "CmdOrCtrl+Shift+L": "Show or hide the logs panel",
	}
	for i := range shortcutPages {
		m["CmdOrCtrl+"+itoa(i+1)] = "Go to a page"
	}
	return m
}

// conflictText explains a conflict.
func conflictText(c dictation.Conflict) string {
	if c.Owner == "app" {
		return i18n.T("AudioSmith uses it (%s).", conflictWhat(c.What))
	}
	return i18n.T("macOS uses it (%s).", conflictWhat(c.What))
}

// conflictWhat translates what a conflicting shortcut does.
func conflictWhat(what string) string {
	switch what {
	case "Spotlight":
		return i18n.T("Spotlight")
	case "Finder search":
		return i18n.T("Finder search")
	case "Select the previous input source":
		return i18n.T("Select the previous input source")
	case "Select the next input source":
		return i18n.T("Select the next input source")
	case "Emoji & Symbols":
		return i18n.T("Emoji & Symbols")
	case "Switch apps":
		return i18n.T("Switch apps")
	case "Switch windows":
		return i18n.T("Switch windows")
	case "Quit":
		return i18n.T("Quit")
	case "Close the window":
		return i18n.T("Close the window")
	case "Hide the app":
		return i18n.T("Hide the app")
	case "Minimize":
		return i18n.T("Minimize")
	case "Copy":
		return i18n.T("Copy")
	case "Paste":
		return i18n.T("Paste")
	case "Cut":
		return i18n.T("Cut")
	case "Undo":
		return i18n.T("Undo")
	case "Select all":
		return i18n.T("Select all")
	case "Screenshot", "Screenshot of a part", "Screenshot and recording":
		return i18n.T("Screenshots")
	case "Lock the screen":
		return i18n.T("Lock the screen")
	case "Force quit", "Force quit the front app":
		return i18n.T("Force quit")
	case "Show or hide the Dock":
		return i18n.T("Show or hide the Dock")
	case "Mission Control", "Application windows":
		return i18n.T("Mission Control")
	case "Full screen":
		return i18n.T("Full screen")
	case "Help menu":
		return i18n.T("Help menu")
	case "Settings":
		return i18n.T("Settings")
	case "Log out":
		return i18n.T("Log out")
	case "Empty the Trash":
		return i18n.T("Empty the Trash")
	case "Command palette":
		return i18n.T("Command palette")
	case "Engine quick switch":
		return i18n.T("Engine quick switch")
	case "Generate":
		return i18n.T("Generate")
	case "Show or hide the logs panel":
		return i18n.T("Show or hide the logs panel")
	case "Go to a page":
		return i18n.T("Go to a page")
	}
	return what
}

// startShortcutRecording listens for a new shortcut; the current one is
// released meanwhile, or pressing it would dictate.
func (a *app) startShortcutRecording() {
	f := &a.dictSet
	f.recording, f.pending, f.recErr = true, "", ""
	if a.dictKeys != nil && a.dictShortcut != "" {
		a.dictKeys.Unregister(a.dictShortcut)
		a.dictShortcut = ""
	}
}

// stopShortcutRecording ends listening and registers the setting's shortcut again.
func (a *app) stopShortcutRecording() {
	a.dictSet.recording = false
	a.applyDictShortcut()
}

// shortcutKeyNames name the keys a shortcut may use, as accelerators do.
var shortcutKeyNames = func() map[ui.Key]string {
	m := map[ui.Key]string{
		ui.KeySpace: "Space", ui.KeyEnter: "Enter", ui.KeyTab: "Tab", ui.KeyBackspace: "Backspace", ui.KeyDelete: "Delete",
		ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
		ui.KeyLeft: "Left", ui.KeyRight: "Right", ui.KeyUp: "Up", ui.KeyDown: "Down",
		ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/", ui.KeySemicolon: ";",
		ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
	}
	for i := range 26 {
		m[ui.KeyA+ui.Key(i)] = string(rune('A' + i))
	}
	for i := range 10 {
		m[ui.Key0+ui.Key(i)] = string(rune('0' + i))
	}
	for i := range 12 {
		m[ui.KeyF1+ui.Key(i)] = "F" + itoa(i+1)
	}
	return m
}()

// shortcutFromKey makes an accelerator from a key press.
func shortcutFromKey(key ui.Key, mods ui.Modifiers) (string, error) {
	name, ok := shortcutKeyNames[key]
	if !ok {
		return "", dictation.ErrBadKey
	}
	var parts []string
	if mods&ui.Super != 0 {
		parts = append(parts, "Cmd")
	}
	if mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	s, err := dictation.ParseShortcut(strings.Join(append(parts, name), "+"))
	if err != nil {
		return "", err
	}
	return s.String(), nil
}

// recordKey takes a key while the recorder listens.
func (a *app) recordKey(ev ui.InputEvent) bool {
	f := &a.dictSet
	if !f.recording || ev.Kind != ui.InputKeyDown {
		return false
	}
	if ev.Key == ui.KeyEscape && ev.Mods == 0 {
		a.stopShortcutRecording()
		return true
	}
	key := ev.Key
	if _, ok := shortcutKeyNames[key]; !ok && ev.PhysicalKey != ui.KeyUnknown {
		key = ev.PhysicalKey // a letter of another script: its key's place
	}
	acc, err := shortcutFromKey(key, ev.Mods)
	if err != nil {
		if _, known := shortcutKeyNames[key]; known || ev.Mods != 0 {
			f.recErr = shortcutErrorText(err)
		}
		return true
	}
	f.pending, f.recErr = acc, ""
	return true
}

// saveShortcut keeps the shortcut heard, unless something else uses it.
func (a *app) saveShortcut(acc string) {
	f := &a.dictSet
	s, err := dictation.ParseShortcut(acc)
	if err != nil {
		f.recErr = shortcutErrorText(err)
		return
	}
	if cs := dictation.Conflicts(s, appShortcuts()); len(cs) > 0 {
		f.recErr = conflictText(cs[0])
		return
	}
	f.recording, f.pending = false, ""
	a.updateSettings(func(st *settings.Settings) { st.Dictation.Shortcut, st.Dictation.Enabled = s.String(), true })
	a.noteUsedBy(a.shortcutUsedBy(s.String())) // family_coord.go: the user saw "Also used by…"
	a.applyDictShortcut()
	if a.dictRegErr == "" {
		a.toast(i18n.T("Dictation shortcut: %s", s.Glyphs()))
	}
}

func (a *app) dictationSettings(c *ui.Context) {
	t := c.Theme()
	f := &a.dictSet
	d := a.prefs.Dictation
	ui.Text(c, i18n.T("Dictate into any app: press the shortcut, speak, and the text is typed where your cursor is. Speech stays on this Mac.")).
		FontSize(px(t, 12)).TextColor(t.TextMuted)
	settingGroup(c, "", func() {
		settingRow(c, "dict-enabled", i18n.T("Dictation shortcut"), i18n.T("Works in every app while AudioSmith runs."), func() {
			if inkSwitch(c.Key("dict-enabled-switch"), &f.enabled).Label(i18n.T("Dictation shortcut")).Changed() {
				on := f.enabled
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Enabled = on })
				a.applyDictShortcut()
			}
		})
		if d.Enabled {
			settingBlock(c, "dict-shortcut-block", func() { a.shortcutRow(c) })
		}
		settingRow(c, "dict-mode", i18n.T("Mode"), i18n.T("Toggle: press once to start, again to stop. Hold: dictate while the keys are held; a quick tap keeps listening."), func() {
			if segmented(c.Key("dict-mode-seg"), &f.mode, i18n.T("Toggle"), i18n.T("Hold")).Label(i18n.T("Mode")).Changed() {
				m := settings.DictationToggle
				if f.mode == 1 {
					m = settings.DictationHold
				}
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Mode = m })
			}
		})

	})
	settingGroup(c, i18n.T("Speech recognition"), func() {
		e := a.dictModel()
		desc := i18n.T("%s answers fastest; %s covers the other languages.", modelRef(catalog.Builtin().STTFor("", false)), modelRef(catalog.Builtin().STTFor("", true)))
		if ready, known := a.modelOnDisk(e); known && !ready {
			desc = i18n.T("%s is not downloaded yet.", modelRef(e))
		}
		settingRowNote(c, "dict-model", i18n.T("Model"), desc, func() {
			cur, _ := catalog.Builtin().ByLabel(f.model)
			if x, ok := modelSelect(c, "dict-model-select", cur, sttEntries(), i18n.T("Model"), px(t, 260)); ok {
				f.model = x.Label
				id := x.ID
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Model = id })
			}
			if ready, known := a.modelOnDisk(e); known && !ready {
				m := a.model(e)
				label := i18n.T("Download")
				if m.downloading {
					label = i18n.T("Downloading…")
				}
				if secondaryButton(c.Key("dict-model-download"), label).Disabled(m.downloading).Clicked() {
					a.download(e)
				}
			}
		})
		settingRow(c, "dict-lang", i18n.T("Language"), i18n.T("Choosing your language makes short dictations more accurate."), func() {
			labels, codes := dictLanguages()
			if ui.Select(c.Key("dict-lang-select"), &f.language, labels).Label(i18n.T("Language")).Width(px(t, 260)).Changed() {
				code := codes[max(slices.Index(labels, f.language), 0)]
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Language = code })
			}
		})
		settingRow(c, "dict-device", i18n.T("Microphone"), "", func() {
			a.loadDevices()
			labels, ids := []string{i18n.T("System default")}, []string{""}
			for _, dv := range a.clone.devices {
				labels, ids = append(labels, dv.Name), append(ids, dv.ID)
			}
			if !slices.Contains(labels, f.device) {
				f.device = labels[max(slices.Index(ids, d.Device), 0)]
			}
			if ui.Select(c.Key("dict-device-select"), &f.device, labels).Label(i18n.T("Microphone")).Width(px(t, 260)).Changed() {
				id := ids[max(slices.Index(labels, f.device), 0)]
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Device = id })
			}
		})
		n := utf8.RuneCountInString(f.vocab)
		settingRow(c, "dict-vocab", i18n.T("Vocabulary"), i18n.T("Names and terms to expect, separated by commas or lines; clean-up keeps them."), func() {})
		settingFoot(c, "dict-vocab-foot", func() { a.vocabField(c, n) })
	})
	a.dictTextSettings(c)
}

// vocabField is the vocabulary's text area, its count and Save.
func (a *app) vocabField(c *ui.Context, n int) {
	t := c.Theme()
	f := &a.dictSet
	d := a.prefs.Dictation
	ui.TextArea(c.Key("dict-vocab-area"), &f.vocab).Lines(2, 5).Placeholder(i18n.T("AudioSmith, MLX, Kubernetes, Ana Lucía")).Label(i18n.T("Vocabulary"))
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		color := t.TextMuted
		if n > settings.MaxVocabulary {
			color = t.Danger
		}
		ui.Text(c, i18n.T("%d / %d characters", n, settings.MaxVocabulary)).FontSize(px(t, 11)).TextColor(color).Grow(1)
		if secondaryButton(c.Key("dict-vocab-save"), i18n.T("Save")).Disabled(f.vocab == d.Vocabulary || n > settings.MaxVocabulary).Clicked() {
			v := f.vocab
			a.updateSettings(func(s *settings.Settings) { s.Dictation.Vocabulary = v })
			a.toast(i18n.T("Vocabulary saved"))
		}
	})
}

// dictTextSettings is Dictation's Text group: clean-up, the result,
// sounds, Try it.
func (a *app) dictTextSettings(c *ui.Context) {
	t := c.Theme()
	f := &a.dictSet
	settingGroup(c, i18n.T("Text"), func() {
		ok, who := a.llmAvailable()
		cleanDesc := i18n.T("No language model: only the built-in polish runs. Set one up in Writing help.")
		if ok {
			cleanDesc = i18n.T("%s removes fillers and applies self-corrections; after 4 seconds the polished text is used instead.", who)
		}
		settingRowNote(c, "dict-cleanup", i18n.T("Clean up with a language model"), cleanDesc, func() {
			if inkSwitch(c.Key("dict-cleanup-switch"), &f.cleanup).Label(i18n.T("Clean up with a language model")).Changed() {
				on := f.cleanup
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Cleanup = on })
				if on {
					a.warmLocalLLM(llm.SkillDictationCleanup) // local_llm_warm.go
				}
			}
			if !ok {
				if secondaryButton(c.Key("dict-writing"), i18n.T("Writing help…")).Clicked() {
					a.navigate("/settings/writing")
				}
			}
		})
		outDesc := i18n.T("Typing uses the clipboard and ⌘V, then puts back what you had copied. It needs Accessibility.")
		if !a.dictSys.AccessibilityTrusted() {
			outDesc = i18n.T("Typing needs Accessibility (Settings → Dictation): until then the text is copied.")
		}
		settingRowNote(c, "dict-output", i18n.T("Result"), outDesc, func() {
			if segmented(c.Key("dict-output-seg"), &f.output, i18n.T("Type it"), i18n.T("Copy only")).Label(i18n.T("Result")).Changed() {
				o := settings.OutputPaste
				if f.output == 1 {
					o = settings.OutputCopy
				}
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Output = o })
			}
		})
		settingRow(c, "dict-sounds", i18n.T("Sounds"), i18n.T("A sound when listening starts and stops, and when something goes wrong. Choosing a sound plays it."), func() {
			if inkSwitch(c.Key("dict-sounds-switch"), &f.sounds).Label(i18n.T("Sounds")).Changed() {
				on := f.sounds
				a.updateSettings(func(s *settings.Settings) { s.Dictation.Sounds = on })
			}
		})
		if f.sounds {
			a.dictSoundRows(c)
		}
		settingRow(c, "dict-try", i18n.T("Try it"), i18n.T("Dictates here: the text is copied, since AudioSmith is in front."), func() {
			label := i18n.T("Start dictation")
			if s := a.dict; s != nil && (s.phase == dictStarting || s.phase == dictListening) {
				label = i18n.T("Stop dictation")
			}
			if secondaryButton(c.Key("dict-try-button"), label).Clicked() {
				a.dictToggle()
			}
		})
		settingFoot(c, "dict-not-yet", func() {
			ui.Text(c, i18n.T("Not yet: live text while you speak, and dictating while AudioSmith plays audio (echo cancellation).")).
				FontSize(px(t, fsMicro)).TextColor(t.TextMuted)
		})
	})
}

// shortcutRow shows the shortcut, its state, and the recorder.
func (a *app) shortcutRow(c *ui.Context) {
	t := c.Theme()
	f := &a.dictSet
	d := a.prefs.Dictation
	status, color := i18n.T("Press it anywhere to check it works."), t.TextMuted
	switch {
	case a.dictRegErr != "":
		status, color = a.dictRegErr, t.Danger
	case a.dictVerified:
		status, color = i18n.T("Verified: it works on this Mac."), t.Success
	case a.dictKeys == nil:
		status = i18n.T("It is registered when AudioSmith runs.")
	}
	ui.Row(c.Key("dict-shortcut")).Gap(px(t, 16)).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			ui.Text(c, dictation.GlyphsOf(d.Shortcut)).FontSize(px(t, 15)).Bold()
			ui.Text(c.Key("dict-shortcut-status"), status).FontSize(px(t, 12)).TextColor(color)
		})
		rec := ui.ButtonBase(c.Key("dict-record")).Padding(px(t, 5), px(t, 12)).Radius(6).Border(1, t.Border).
			Label(i18n.T("Record a shortcut")).Bind(&f.recHandle)
		if f.recording {
			rec.Border(2, t.Accent)
			rec.HandleInput(a.recordKey)
		}
		if rec.Clicked() && !f.recording {
			a.startShortcutRecording()
		}
		rec.Children(func() {
			label := i18n.T("Record a shortcut")
			switch {
			case f.recording && f.pending != "":
				label = dictation.GlyphsOf(f.pending)
			case f.recording:
				label = i18n.T("Press keys…")
			}
			ui.Text(c, label)
		})
		if f.recording {
			if primaryButton(c.Key("dict-record-save"), i18n.T("Save")).Disabled(f.pending == "").Clicked() {
				a.saveShortcut(f.pending)
			}
			if secondaryButton(c.Key("dict-record-cancel"), i18n.T("Cancel")).Clicked() {
				a.stopShortcutRecording()
			}
		} else if secondaryButton(c.Key("dict-reset"), i18n.T("Reset")).Tooltip(dictation.GlyphsOf(settings.DefaultShortcut)).
			Disabled(d.Shortcut == settings.DefaultShortcut).Clicked() {
			a.saveShortcut(settings.DefaultShortcut)
		}
	})
	if f.recording {
		hint := i18n.T("Press the keys together, e.g. ⌥Space or ⌃⇧D. Esc cancels.")
		col := t.TextMuted
		if f.recErr != "" {
			hint, col = f.recErr, t.Danger
		} else if f.pending != "" {
			if s, err := dictation.ParseShortcut(f.pending); err == nil {
				if cs := dictation.Conflicts(s, appShortcuts()); len(cs) > 0 {
					hint, col = conflictText(cs[0]), t.Warning
				} else if hits := a.shortcutUsedBy(f.pending); len(hits) > 0 {
					hint, col = usedByText(hits), t.Warning // another ActNow app's (family_coord.go)
				}
			}
		}
		ui.Text(c.Key("dict-record-hint"), hint).FontSize(px(t, 12)).TextColor(col)
	} else {
		a.familyShortcutWarnings(c) // family_coord.go
	}
}

// m2GeneralSettings adds M2's rows to Settings → General.
func (a *app) m2GeneralSettings(c *ui.Context) {
	f := &a.dictSet
	settingRow(c, "close-tray", i18n.T("Keep running in the menu bar"), i18n.T("Closing the window keeps AudioSmith in the menu bar, so dictation keeps working."), func() {
		if inkSwitch(c.Key("close-tray-switch"), &f.closeTray).Label(i18n.T("Keep running in the menu bar")).Changed() {
			on := f.closeTray
			a.updateSettings(func(s *settings.Settings) { s.General.CloseToTray = on })
		}
	})
}

// dictationGlyphs shows the registered shortcut, "" when none.
func dictationGlyphs(acc string) string {
	if acc == "" {
		return ""
	}
	return dictation.GlyphsOf(acc)
}
