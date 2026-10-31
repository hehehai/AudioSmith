package main

// The Studio's inspector (thread #42, phase C): what most takes need —
// the model, the voice, the speed — and, folded under 「高级」, the rest
// (sampling, refinement, seed, output options), pre-filled with the
// model's recommended settings (studio_presets.go). The generate bar says
// which model and voice will speak.

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// studioInspectorState is the inspector's view state.
type studioInspectorState struct {
	refTranscribing string // the reference clip being transcribed ("" = none)
}

func (a *app) inspector(c *ui.Context) {
	t := c.Theme()
	p := a.preset()
	custom := a.modelLabel == catalog.CustomLabel

	// The voice first (0.5 F3): what most takes change.
	a.studioVoiceField(c, p)
	if studioNeedsDescription(p) && !a.studioUsesSavedVoice(p) {
		ui.Field(c, i18n.T("Voice description"), func() {
			ui.TextArea(c.Key("description"), &a.voice).Lines(2, 5).Placeholder(i18n.T("e.g. a calm, warm female narrator, mid-thirties")).
				Label(i18n.T("Voice description"))
		})
	}
	// What the model is for, then what it is (family, quantisation,
	// size, licence: thread #45 keeps them out of the picker's text).
	about := p.Description()
	if p.ID != "custom" {
		about += "\n\n" + p.TechnicalSummary()
	}
	ui.Column(c).Gap(px(t, 6)).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("Model")).FontWeight(600).Grow(1)
			infoTip(c.Key("model-about"), about)
		})
		a.modelPicker(c, "model", catalog.WorkflowStudio, p, func(e catalog.Entry) {
			if e.ID == "custom" {
				a.modelLabel = catalog.CustomLabel
			} else {
				a.modelLabel = e.Label
			}
			a.modelChanged()
		}, pickerOpts{Custom: true, Row: true})
		if custom {
			ui.TextInput(c.Key("repo"), &a.customRepo).Placeholder("owner/model-name").Label(i18n.T("Repo"))
		}
		if !p.Licence.Commercial {
			caption(c, i18n.T("Non-commercial use only")).TextColor(t.Warning).Tooltip(p.Licence.Name)
		}
	})
	if p.Has(catalog.CapSpeed) {
		a.speedField(c)
	}
	ui.Column(c).Gap(px(t, sp1)).Children(func() {
		a.settingsRows(c, p)
	})
	ui.Divider(c)
	a.advancedSection(c, "studio", a.studioChanges(), false, a.restoreRecommended, func() { a.studioAdvanced(c, p) })
}

// studioNeedsDescription: the model speaks a voice made from a
// description and has neither speakers nor cloning (Qwen3-TTS
// VoiceDesign), so the description is the voice.
func studioNeedsDescription(e catalog.Entry) bool {
	return e.Has(catalog.CapDesign) && len(e.Voices) == 0 && !e.Has(catalog.CapClone)
}

// splitVoice splits the voice field into a built-in speaker of ids and
// the style after it ("Vivian, calm and warm" → "Vivian", "calm and
// warm"); speaker is "" when the field names none.
func splitVoice(voice string, ids []string) (speaker, style string) {
	voice = strings.TrimSpace(voice)
	for _, id := range ids {
		if rest, ok := strings.CutPrefix(voice, id); ok && (rest == "" || strings.HasPrefix(rest, ",")) {
			return id, strings.TrimSpace(strings.TrimPrefix(rest, ","))
		}
	}
	return "", voice
}

// joinVoice is the voice field for a speaker and a style.
func joinVoice(speaker, style string) string {
	style = strings.TrimSpace(style)
	switch {
	case speaker == "":
		return style
	case style == "":
		return speaker
	}
	return speaker + ", " + style
}

// studioVoiceLabel names the Studio's voice for model e.
func (a *app) studioVoiceLabel(e catalog.Entry) string {
	if v := a.findVoice(a.studioVoice); v != nil {
		return v.Name
	}
	if a.studioClip != nil && e.Has(catalog.CapClone) {
		return i18n.T("Unsaved clip: %s", a.studioClip.Name)
	}
	if e.Has(catalog.CapClone) && strings.TrimSpace(a.refAudio) != "" {
		return i18n.T("Clip: %s", filepath.Base(a.refAudio))
	}
	if sp, _ := splitVoice(a.voice, e.AllVoiceIDs()); sp != "" {
		return sp
	}
	return i18n.T("Model default")
}

// studioVoiceField is the voice: one list of the model's speakers, the
// saved voices it can speak with, a clip to clone (models that clone)
// and Manage voices…; under it, what the choice needs saying.
func (a *app) studioVoiceField(c *ui.Context, e catalog.Entry) {
	t := c.Theme()
	k := tok(t)
	ui.Column(c.Key("voice-field")).Gap(px(t, 6)).Children(func() {
		a.voiceHeader(c)
		a.studioVoicePicker(c, e)

		switch v := a.findVoice(a.studioVoice); {
		case v != nil:
			ui.Row(c.Key("studio-voice-info")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				col := k.Text2
				text := voiceKindLabel(v)
				if ok, why := voiceUsable(v, e); !ok {
					col, text = t.Warning, why
				}
				caption(c, text).TextColor(col).Grow(1).MinWidth(0)
				if ok, _ := voiceUsable(v, e); !ok && canMakePortable(v) && e.Has(catalog.CapClone) {
					if ghostButton(c.Key("studio-voice-portable"), i18n.T("Make portable")).Tooltip(i18n.T("Save an approximate clone of this voice that models that clone can speak with")).Clicked() {
						a.makePortable(v)
					}
				} else if ghostButton(c.Key("studio-voice-edit"), i18n.T("Edit")).Clicked() {
					a.openVoiceEditor(v.ID)
				}
			})
		case e.Has(catalog.CapClone) && a.studioClip == nil && strings.TrimSpace(a.refAudio) != "":
			ui.Row(c.Key("ref-clip")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				text := i18n.T("Cloned from this clip; its transcript is under Advanced.")
				if a.inspectorUI.refTranscribing == a.refAudio {
					text = i18n.T("Transcribing the clip…")
				}
				caption(c, text).Grow(1).MinWidth(0)
				if iconButton(c.Key("clear-ref"), icons.Close, i18n.T("Clear")).Tooltip(i18n.T("Stop cloning this clip")).Clicked() {
					a.refAudio, a.refText = "", ""
				}
			})
		case e.NeedsReference && !a.studioUsesSavedVoice(e):
			caption(c.Key("ref-required"), i18n.T("Required: %s only clones voices.", modelRef(e))).TextColor(t.Warning)
		}
	})
}

// studioAdvanced are the Studio's 「高级」 controls, those the model takes.
func (a *app) studioAdvanced(c *ui.Context, p catalog.Entry) {
	t := c.Theme()
	saved := a.studioUsesSavedVoice(p)
	if p.Has(catalog.CapVoiceStyle) && !studioNeedsDescription(p) && !saved {
		if ids := p.AllVoiceIDs(); len(ids) > 0 {
			speaker, style := splitVoice(a.voice, ids)
			fieldInfo(c, i18n.T("Style"), i18n.T("How the speaker sounds, in a few words: “calm and warm”, “excited”."), func() {
				if ui.TextInput(c.Key("style"), &style).Placeholder(i18n.T("e.g. calm and warm")).Label(i18n.T("Style")).Changed() {
					a.voice = joinVoice(speaker, style)
				}
			})
		} else {
			ui.Field(c, i18n.T("Voice description"), func() {
				ui.TextArea(c.Key("description"), &a.voice).Lines(1, 4).Placeholder(i18n.T("Optional: describe the voice to make")).Label(i18n.T("Voice description"))
			})
		}
	}
	if p.ID == "custom" && !saved {
		ui.Field(c, i18n.T("Voice"), func() {
			ui.TextInput(c.Key("custom-voice"), &a.voice).Placeholder(i18n.T("Model default")).Label(i18n.T("Voice"))
		})
	}
	if p.Has(catalog.CapClone) && strings.TrimSpace(a.refAudio) != "" && !saved && a.studioClip == nil {
		ui.Field(c, i18n.T("Reference transcript"), func() {
			ui.TextArea(c.Key("ref-text"), &a.refText).Lines(2, 4).Placeholder(i18n.T("What the reference clip says")).Label(i18n.T("Reference transcript"))
		})
	}
	if p.Has(catalog.CapDuration) {
		fieldInfo(c, i18n.T("Duration (seconds, 0 = natural)"), i18n.T("A duration overrides the speed."), func() {
			numberInput(c.Key("duration"), &a.duration, 0, 600, 0.5, numOpts{Label: i18n.T("Duration")})
		})
	}
	if a.traits().Steps {
		tier := slices.Index(tierSteps, a.shownSteps())
		fieldInfo(c, i18n.T("Voice refinement"), i18n.T("More steps sound cleaner and take longer."), func() {
			if segmentedFit(c.Key("tier"), &tier, i18n.T("Fast"), i18n.T("Balanced"), i18n.T("Quality")).Label(i18n.T("Voice refinement")).Changed() && tier >= 0 {
				a.steps = tierSteps[tier]
			}
		})
	}
	rec := a.recommended()
	if p.Has(catalog.CapGuidance) {
		g := shown(a.guidance, rec.Guidance, 2)
		ui.Field(c, i18n.T("Prompt guidance  %.1f", g), func() {
			if snapped(ui.Slider(c.Key("guidance"), &g, 1, 4).Label(i18n.T("Prompt guidance")), &g, 0.1).Changed() {
				a.guidance = g
			}
		})
	}
	if p.Has(catalog.CapSampling) {
		temp, topP := shown(a.temperature, rec.Temperature, 0.9), shown(a.topP, rec.TopP, 1)
		ui.Field(c, i18n.T("Temperature  %.2f", temp), func() {
			if snapped(ui.Slider(c.Key("temperature"), &temp, 0.05, 1.5).Label(i18n.T("Temperature")), &temp, 0.05).Changed() {
				a.temperature = temp
			}
		})
		ui.Field(c, i18n.T("Top P  %.2f", topP), func() {
			if snapped(ui.Slider(c.Key("topp"), &topP, 0.05, 1).Label(i18n.T("Top P")), &topP, 0.05).Changed() {
				a.topP = topP
			}
		})
	}
	formRow(c, "seed-pin-row", i18n.T("Pin the seed"), i18n.T("The same seed and settings make the same take."), func() {
		ui.Switch(c.Key("seed-pin"), &a.seedPinned).Label(i18n.T("Pin the seed"))
	})
	if a.seedPinned {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			numberInput(c.Key("seed"), &a.seed, 1, 1<<31-1, 1, numOpts{Label: i18n.T("Seed"), Grow: true})
			if iconButton(c.Key("new-seed"), icons.Refresh, i18n.T("New seed")).Clicked() {
				a.newSeed()
			}
		})
	}
	formRow(c, "normalize-row", i18n.T("Read numbers as words"), i18n.T("Numbers, times, dates, money and abbreviations, before the model reads them."), func() {
		ui.Switch(c.Key("normalize"), &a.normalize).Label(i18n.T("Read numbers as words"))
	})
	formRow(c, "even-out-row", i18n.T("Even out volume"), i18n.T("Masters the take to broadcast loudness (−18 LUFS)."), func() {
		ui.Switch(c.Key("even-out"), &a.evenOut).Label(i18n.T("Even out volume"))
	})
}

// studioSummary is the generate bar's caption: the model and the voice
// that will speak, the speed, how many settings differ from the
// recommended ones. A click shows the panel's settings.
func (a *app) studioSummary(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := a.preset()
	parts := []string{p.DisplayName(), a.studioVoiceLabel(p)}
	if p.ID == "custom" {
		parts[0] = catalog.ShortRepo(p.Repo)
	}
	if p.Has(catalog.CapSpeed) && a.speed > 0 && a.speed != 1 {
		parts = append(parts, strconv.FormatFloat(a.speed, 'f', 2, 64)+"×")
	}
	changes := slices.DeleteFunc(a.studioChanges(), func(s string) bool { return s == i18n.T("Speed") })
	if len(changes) > 0 {
		parts = append(parts, changesLabel(changes))
	}
	text := strings.Join(parts, " · ")
	b := ghostBase(c.Key("studio-summary")).Padding(px(t, 4), px(t, 6)).Label(text).Tooltip(i18n.T("Model and voice: change them in Settings")).MinWidth(0).Shrink(1)
	b.Children(func() {
		ui.Text(c, text).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
	})
	if b.Clicked() {
		a.showStudioPanel(studioTabSettings)
	}
}

// transcribeRef fills the reference transcript of a clip just chosen, in
// the background, if the user has not typed one meanwhile.
func (a *app) transcribeRef(path string) {
	if strings.TrimSpace(a.refText) != "" {
		return
	}
	a.inspectorUI.refTranscribing = path
	a.transcribeClip(path, "", func(text string, err error) {
		a.post(func() {
			if a.inspectorUI.refTranscribing == path {
				a.inspectorUI.refTranscribing = ""
			}
			if err == nil && a.refAudio == path && strings.TrimSpace(a.refText) == "" {
				a.refText = text
			}
		})
	})
}
