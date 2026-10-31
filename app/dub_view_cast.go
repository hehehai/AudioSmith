package main

// The Dub page's Cast and Generate step panels (voice controls, timing),
// the confirmation dialog, Dub → Story, and small helpers; split from
// dub_view.go.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubCastPanel: the casting board.
func (a *app) dubCastPanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	locked := a.dubLocked()
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		dubPanelTitle(c, i18n.N(len(p.SpeakerLabels()), "Cast · %d speaker", "Cast · %d speakers", len(p.SpeakerLabels())))
		infoTip(c.Key("cast-tip"), i18n.T("Drag a voice onto a speaker, or pick one from the list. Auto clones each speaker's own voice."))
	})
	a.dubDialogueRow(c, locked)
	dl := a.dubDialogue()
	choices := a.dubVoiceChoices()
	// The chips.
	ui.Row(c.Key("cast-chips")).Gap(px(t, 4)).Wrap().Children(func() {
		for _, v := range choices {
			ui.Text(c.Key("chip-"+v.ref), v.label).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).SingleLine().Padding(px(t, 3), px(t, sp2)).
				Radius(px(t, radiusControl)).Background(tok(t).Hover).Cursor(ui.CursorGrab).Drag(dubVoiceChip{ref: v.ref})
		}
	})
	if len(p.SpeakerLabels()) == 0 {
		caption(c, i18n.T("Speakers appear once the source is transcribed."))
	}
	opts := a.dubVoiceOptions(choices)
	for _, sp := range p.SpeakerLabels() {
		card := fillCard(c.Key("cast-" + sp)).Gap(px(t, 6)).Padding(px(t, sp3))
		if _, over := ui.DragOver[dubVoiceChip](card); over {
			card.Border(2, t.Accent)
		}
		if chip, ok := ui.Drop[dubVoiceChip](card); ok && !locked {
			a.dubCast(sp, chip.ref)
		}
		card.Children(func() {
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.SpeakerName(sp)).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				if r, ok := p.References[sp]; ok {
					info := i18n.T("Own voice: %s–%s", tlClock(r.Start), tlClock(r.End))
					if r.Enhanced {
						info += " · " + i18n.T("cleaned")
					}
					ui.Text(c, info).FontSize(px(t, fsMicro)).TextColor(tok(t).Text2).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(info)
					path := a.dubSvc.Path(p.ID, r.File)
					id := "dubref:" + p.ID + ":" + sp
					if playToggle(c.Key("ref-play"), a.pl.Playing(id), i18n.T("Hear the voice sample"), i18n.T("Hear the voice sample")).Clicked() {
						a.dubStopPlayer()
						if err := a.pl.Toggle(id, path); err != nil {
							a.toast(i18n.T("Cannot play: %v", err))
						}
					}
				}
			})
			if r, ok := p.References[sp]; ok && refcheck.GradeOf(r.Hints, refcheck.UseClone) == refcheck.Poor { // the reference checker (#83)
				ui.Row(c.Key("ref-check")).Gap(px(t, 6)).Children(func() {
					iconMark(c, icons.DangerTriangle, t.Warning, "")
					caption(c, dubRefWarning(r.Hints)).TextColor(t.Warning).Grow(1).MinWidth(0)
				})
			}
			a.dubDialogueRefNote(c, dl, sp)
			if _, ok := p.References[sp]; !ok && p.Stage(dub.StageReferences, "").Status == dub.StatusDone {
				ui.Row(c).Gap(px(t, 6)).Children(func() {
					iconMark(c, icons.DangerTriangle, t.Warning, "")
					caption(c, i18n.T("Says too little to clone: uses the default voice unless cast.")).TextColor(t.Warning).Grow(1).MinWidth(0)
				})
			}
			cur := p.Cast[sp]
			o := voicePickerOpts{label: i18n.T("Voice of %s", p.SpeakerName(sp)), text: a.dubVoiceLabel(cur), locked: locked}
			if v, ok := a.voicePicker(c, "dub-cast-"+sp, cur, opts, o); ok {
				a.dubCast(sp, v)
			}
		})
	}
	match := 0
	if p.Settings.VoiceMatch == dub.MatchLine {
		match = 1
	}
	a.dubAdvanced(c, dubStepCast, locked, func() {
		fieldInfo(c, i18n.T("Voice match"), i18n.T("Consistent clones each speaker from one sample; Per line clones each line from its own audio (follows the delivery, less stable)."), func() {
			if segmentedFit(c.Key("cast-match"), &match, i18n.T("Consistent"), i18n.T("Per line")).Label(i18n.T("Voice match")).Disabled(locked).Changed() {
				m := dub.MatchConsistent
				if match == 1 {
					m = dub.MatchLine
				}
				a.dubSettings(func(s *dub.Settings) { s.VoiceMatch = m })
			}
		})
	})
}

// dubVoiceChip is a voice dragged onto a speaker.
type dubVoiceChip struct{ ref string }

// dubGeneratePanel: the model, timing, background and voice controls.
func (a *app) dubGeneratePanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	locked := a.dubBusy()
	dubPanelTitle(c, i18n.T("Generate"))
	cur := entryFor(p.Settings.Model)
	if p.Settings.Model == "" {
		cur = entryFor(a.dubDefaultModel())
	}
	dl := a.dubDialogue()
	ui.Field(c, i18n.T("Voice model"), func() {
		a.modelPicker(c, "gen-model", catalog.WorkflowDub, cur, func(e catalog.Entry) {
			repo := e.Repo
			a.dubSettings(func(s *dub.Settings) { s.Model = repo })
		}, pickerOpts{Locked: locked, Dialogue: a.dubDialoguePick(dl)})
	})
	a.dubDialogueGenerateNote(c, dl)
	caps := dubCaps(p.Settings.Model)
	if caps.NeedsReference {
		caption(c.Key("gen-needs-ref"), i18n.T("%s only clones voices: each line speaks in its speaker's own voice or a saved cloned voice.", modelRef(entryFor(p.Settings.Model)))).TextColor(tok(t).Text3)
	}
	if caps.Duration {
		caption(c, i18n.T("This model can speak each line in exactly its time (Lip sync), but then it may change words: Smart fit is safer.")).TextColor(tok(t).Text3)
	}
	// Timing.
	timings := dub.Timings
	tnames := []string{i18n.T("Concise"), i18n.T("Smart fit"), i18n.T("Stretch video"), i18n.T("Lip sync")}
	tcur := tnames[max(slices.Index(timings, p.Settings.Timing), 0)]
	fieldInfo(c, i18n.T("Timing"), dubTimingHelp(p.Settings.Timing), func() {
		if ui.Select(c.Key("gen-timing"), &tcur, tnames).Label(i18n.T("Timing")).Disabled(locked).Changed() {
			tm := timings[slices.Index(tnames, tcur)]
			a.dubSettings(func(s *dub.Settings) { s.Timing = tm })
		}
	})
	keep := p.Settings.KeepBackground
	if dubSwitch(c, "gen-bg", i18n.T("Keep background (music and effects)"), "", &keep, locked) {
		a.dubSettings(func(s *dub.Settings) { s.KeepBackground = keep })
	}
	sectionTitle(c, i18n.T("Tracks"))
	// Tracks.
	for _, l := range p.Targets {
		tr := p.Track(l)
		state := i18n.T("not generated")
		if tr.File != "" {
			state = tlClock(tr.Seconds)
			if n := a.dubStaleCount(l); n > 0 {
				state += " · " + i18n.N(n, "%d changed", "%d changed", n)
			}
			if tr.QC != nil && !tr.QCStale() {
				state += " · " + i18n.N(tr.QC.Flagged, "%d to re-listen", "%d to re-listen", tr.QC.Flagged)
			}
		} else if n := len(tr.Lines); n > 0 {
			state = i18n.N(n, "%d line rendered", "%d lines rendered", n)
		}
		ui.Row(c.Key("track-" + l)).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, dubLangName(l)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			ui.Text(c, state).FontSize(px(t, fsMicro)).TextColor(tok(t).Text2).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(state)
		})
	}
	// Verify is in the editor's toolbar.
	a.dubAdvanced(c, dubStepGenerate, locked, func() { a.dubVoiceControls(c, caps, locked) })
}

// dubVoiceControls are the Generate step's voice controls: steps,
// guidance, speed and style, as the model takes them.
func (a *app) dubVoiceControls(c *ui.Context, caps services.DubModelCaps, locked bool) {
	p := a.dub
	// Unset: the model's recommended values for dubbing (presets).
	pre, _ := catalog.PresetFor(entryFor(p.Settings.Model).ID)
	rec := pre.ParamsFor(catalog.WorkflowDub)
	if caps.Steps {
		steps := float64(p.Settings.Steps)
		if steps == 0 {
			steps = 32
			if rec.Steps != nil {
				steps = float64(*rec.Steps)
			}
		}
		ui.Field(c, i18n.T("Steps: %d", int(steps)), func() {
			if slimStepSlider(c.Key("gen-steps"), &steps, 8, 64, 1).Label(i18n.T("Steps")).Disabled(locked).Changed() {
				v := int(steps)
				a.dubSettings(func(s *dub.Settings) { s.Steps = v })
			}
		})
	}
	if caps.Guidance {
		g := shown(p.Settings.Guidance, rec.Guidance, 2)
		ui.Field(c, i18n.T("Guidance: %.1f", g), func() {
			if slimStepSlider(c.Key("gen-cfg"), &g, 0, 4, 0.1).Label(i18n.T("Guidance")).Disabled(locked).Changed() {
				v := dubRound(g)
				a.dubSettings(func(s *dub.Settings) { s.Guidance = v })
			}
		})
	}
	speed := p.Settings.Speed
	if speed == 0 {
		speed = 1
	}
	ui.Field(c, i18n.T("Speed: %.2f×", speed), func() {
		if slimStepSlider(c.Key("gen-speed"), &speed, 0.5, 2, 0.05).Label(i18n.T("Speed")).Disabled(locked || !caps.Speed).Changed() {
			v := dubRound(speed)
			if v == 1 {
				v = 0
			}
			a.dubSettings(func(s *dub.Settings) { s.Speed = v })
		}
	})
	if caps.Instruct {
		style := p.Settings.Instruct
		ui.Field(c, i18n.T("Voice style"), func() {
			if ui.TextInput(c.Key("gen-style"), &style).Placeholder(i18n.T("e.g. calm documentary narrator")).Label(i18n.T("Voice style")).Disabled(locked).Changed() {
				v := strings.TrimSpace(style)
				a.dubSettings(func(s *dub.Settings) { s.Instruct = v })
			}
		})
	}
}

// dubTimingHelp explains a timing strategy.
func dubTimingHelp(tm dub.Timing) string {
	switch tm {
	case dub.TimingConcise:
		return i18n.T("Lines are never sped up: a line too long for its time is flagged for a shorter rewrite.")
	case dub.TimingSmart:
		return i18n.T("A line may run into the pause after it; only what is left is sped up (at most 1.25×).")
	case dub.TimingStretchVideo:
		return i18n.T("As Smart fit, sped up at most 1.1×; the video slows under lines that still do not fit (needs an engine update: until then those lines overflow).")
	}
	return i18n.T("Each line is fitted to its time: sped up (at most 1.5×) or slowed slightly, pitch kept; what still does not fit is cut.")
}

// --- confirmations ------------------------------------------------------------------------------

// dubConfirmDialog asks before removing work.
func (a *app) dubConfirmDialog(c *ui.Context) {
	if !a.dubConfirmOpen {
		return
	}
	what := a.dubConfirm
	var title, msg, yes string
	switch {
	case what == "remove":
		title, msg, yes = i18n.T("Remove the source?"), i18n.T("Its transcript, translations and generated tracks go with it. This cannot be undone."), i18n.T("Remove")
	case what == "transcribe":
		title, msg, yes = i18n.T("Transcribe again?"), i18n.T("The transcript is made again from the audio: your edits, translations and generated tracks are lost."), i18n.T("Transcribe again")
	case what == "story":
		title, msg, yes = i18n.T("Replace the Stories script?"), i18n.T("This replaces the current Stories script and cast. Your dub is not changed."), i18n.T("Replace")
	case strings.HasPrefix(what, "delete:"):
		title, msg, yes = i18n.T("Delete this dub?"), i18n.T("The project and every file made for it are deleted. The source file is kept."), i18n.T("Delete")
	default:
		a.dubConfirmOpen = false
		return
	}
	switch ui.AlertDialog(c.Key("dub-confirm"), &a.dubConfirmOpen, title, msg, yes, i18n.T("Cancel")) {
	case 0:
		a.dubConfirmOpen = false
		switch {
		case what == "remove":
			a.dubRemoveSource()
		case what == "transcribe":
			a.dubRun(services.RunRequest{Through: dub.StageReferences, Force: []dub.Stage{dub.StageTranscribe}})
		case what == "story":
			a.dubToStory()
		case strings.HasPrefix(what, "delete:"):
			a.deleteDub(strings.TrimPrefix(what, "delete:"))
		}
	case 1:
		a.dubConfirmOpen = false
	}
}

// --- Dub → Story ---------------------------------------------------------------------------------

// dubAskStory makes a Stories script of the dub, asking first when the
// Stories page has a script.
func (a *app) dubAskStory() {
	if a.lfStories != nil && !a.lfStories.empty() {
		a.dubConfirm, a.dubConfirmOpen = "story", true
		return
	}
	a.dubToStory()
}

// dubToStory hands the dub to the Stories page: one character per
// speaker (with their cast voice where Stories can use it), one line
// per segment, in the language shown.
func (a *app) dubToStory() {
	p := a.dub
	if p == nil || a.lfStories == nil {
		return
	}
	st := p.ToStory(a.dubLang)
	model := p.Settings.Model
	d := services.NewLongformDoc(core.ProjectStory, model)
	d.Title, d.Language = st.Title, st.Lang
	ids := map[string]string{}
	for i, ch := range st.Characters {
		id := "c" + strconv.Itoa(i+1)
		ids[ch.Name] = id
		d.Cast = append(d.Cast, services.CastMember{ID: id, Name: ch.Name, Voice: ch.Voice})
	}
	for i, l := range st.Lines {
		d.Lines = append(d.Lines, services.StoryLine{ID: "l" + strconv.Itoa(i+1), Text: l.Text, Character: ids[l.Character], Voice: l.Voice, Speed: l.Speed})
	}
	a.setLfDoc(a.lfStories, d, "", "")
	a.navigate("/stories")
	a.toast(i18n.T("The dub is a Stories script now."))
}

// sortedKeys are a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// dubSwitch is a switch with its label beside it (and a ⓘ with info,
// "" for none); it reports a change.
func dubSwitch(c *ui.Context, key, label, info string, on *bool, disabled bool) bool {
	changed := false
	formRow(c, key+"-row", label, info, func() {
		changed = inkSwitch(c.Key(key), on).Label(label).Disabled(disabled).Changed()
	})
	return changed
}

// dubAnyTranslated reports whether any line has a translation (or failed one).
func dubAnyTranslated(p *dub.Project) bool {
	for _, s := range p.Segments {
		if len(s.Lines) > 0 {
			return true
		}
	}
	return false
}

// dubRefWarning says why a speaker's own voice sample is poor (it comes
// from the source: nothing to record again).
func dubRefWarning(hints []string) string {
	switch {
	case slices.Contains(hints, refcheck.CodeVeryNoisy):
		return i18n.T("Their voice sample is very noisy: the clone may carry the noise. Cast a saved voice if it does.")
	case slices.Contains(hints, refcheck.CodeClipping):
		return i18n.T("Their voice sample is distorted: the clone may be too. Cast a saved voice if it is.")
	}
	return i18n.T("Their voice sample is short: the clone may sound unlike them. Cast a saved voice if it does.")
}
