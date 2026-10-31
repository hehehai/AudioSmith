package main

// Design a voice as a guided page (0.5 F4; after ElevenLabs' Voice
// Design): 描述 — the prompt box with the presets as chips under it, the
// line to hear, 「生成候选」 → 挑选 — three candidates (the same
// description, three seeds) to hear and pick → 保存 — a name and
// 「保存音色」 → 完成: 「试听」, 「用于文字转语音」. The details (attributes,
// seed) and the model fold under 细节与模型; a saved design reopens
// with its own recipe (openDesignFor).

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// designCandidates is how many candidates 「生成候选」 makes.
const designCandidates = 3

// designCand is a candidate: its take's id (voiceJobs) and seed.
type designCand struct {
	takeID string
	seed   int64
}

// Design's steps (designStep).
const (
	designDescribeStep = iota
	designPickStep
	designSaveStep
	designDoneStep
)

// designStep is where the draft is: no candidates yet, candidates to
// pick from, one picked, saved.
func (a *app) designStep() int {
	d := &a.design
	switch {
	case d.savedID != "" && a.findVoice(d.savedID) != nil && d.linked == d.savedID:
		return designDoneStep
	case d.picked >= 0 && d.picked < len(d.cands):
		return designSaveStep
	case len(d.cands) > 0:
		return designPickStep
	}
	return designDescribeStep
}

// startDesign opens Design: the draft as it was, or a new one after a
// voice was saved from it.
func (a *app) startDesign() {
	if a.designStep() == designDoneStep {
		script := a.design.script
		a.design.reset()
		a.design.script = script
	}
	a.navigate("/voices/new/design")
}

// candJob is candidate i's job, nil before it was asked for.
func (a *app) candJob(i int) *voiceJob {
	d := &a.design
	if i < 0 || i >= len(d.cands) {
		return nil
	}
	return a.voiceJobs[d.cands[i].takeID]
}

// candsRunning reports whether a candidate is still being made.
func (a *app) candsRunning() bool {
	for i := range a.design.cands {
		if a.candJob(i).running() {
			return true
		}
	}
	return false
}

// designGenerateCandidates makes three candidates of the draft: its seed,
// then two new ones. Picking one makes its seed the draft's.
func (a *app) designGenerateCandidates() {
	d := &a.design
	e, ok := voiceCatalog().ByLabel(d.model)
	if !ok || !canDesign(e) || strings.TrimSpace(d.script) == "" {
		return
	}
	lang := d.speechLang()
	base := d.recipe()
	d.cands, d.picked, d.savedID = nil, -1, ""
	for i := range designCandidates {
		r := base
		if i > 0 {
			r.Seed = vd.NewSeed()
		}
		low, err := vd.Lower(designTarget(e), r, vd.Options{Lang: designTextLang(lang)})
		if err != nil {
			a.toastWarn(err.Error())
			return
		}
		tk := core.Take{Text: strings.TrimSpace(d.script), Model: e.Repo, Voice: low.Voice, Seed: low.Seed,
			Language: modelLanguage(e, lang), VoiceID: d.linked}
		id := a.voiceGenerate("candidate", "cand-"+itoa(i), tk, a.designGenerateCandidates)
		if id == "" {
			return // the licence dialog: it runs again once accepted
		}
		d.cands = append(d.cands, designCand{takeID: id, seed: r.Seed})
	}
}

// pickCandidate makes candidate i the draft's voice: its seed.
func (a *app) pickCandidate(i int) {
	d := &a.design
	if i < 0 || i >= len(d.cands) {
		return
	}
	d.picked, d.seed = i, float64(d.cands[i].seed)
	if strings.TrimSpace(d.saveName) == "" {
		d.saveName = designNameGuess(d)
	}
}

// designNameGuess is a name for the draft: its preset's, or the start of
// its description.
func designNameGuess(d *designState) string {
	for _, p := range gallery.Presets() {
		if p.ID == d.preset {
			return p.Name.In(i18n.Locale())
		}
	}
	r := []rune(strings.TrimSpace(d.description))
	if len(r) > 16 {
		r = r[:16]
	}
	return string(r)
}

// designSample is the take to keep as the saved voice's sample: the
// picked candidate's (or a preview's) when it is of the draft as it is.
func (a *app) designSample() *core.Take {
	key := a.designKey()
	for i := range a.design.cands {
		if j := a.candJob(i); j != nil && j.take != nil && a.designTakeKey(j.take) == key {
			return j.take
		}
	}
	if j := a.voiceJob("design", ""); j != nil && j.take != nil && a.designTakeKey(j.take) == key {
		return j.take
	}
	return nil
}

// designPage is /voices/new/design.
func (a *app) designPage(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	d.reparse()
	a.syncLinked()
	title := i18n.T("Design a voice")
	if v := a.findVoice(d.linked); v != nil && a.designStep() != designDoneStep {
		title = i18n.T("Design · %s", v.Name)
	}
	pageHeaderBar(c, pageHead{title: title, parent: i18n.T("New voice"), up: "/voices/new", tools: func(bool) {
		if ghostIconButton(c.Key("design-new"), icons.AddCircle, i18n.T("New design")).Label(i18n.T("New design")).Clicked() {
			script := d.script
			d.reset()
			d.script = script
		}
		if iconButton(c.Key("design-gallery"), icons.Widget, i18n.T("Discover voices")).Tooltip(i18n.T("Start from a voice in Discover voices")).Clicked() {
			a.navigate("/voices/discover")
		}
	}})
	step := a.designStep()
	ui.Scroll(c.Key("design-main")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(px(t, sp2), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 780)).Children(func() {
			stepper(c, "design-steps", []string{i18n.T("Describe"), i18n.T("Pick"), i18n.T("Save"), i18n.T("Done")}, step, nil)
			if !a.designModelDefault() {
				a.designTarget(c) // says no model can design, with Models
			}
			a.designDescribe(c)
			a.designPresets(c)
			a.designScript(c, step)
			if len(d.cands) > 0 {
				a.designCandidatesView(c)
			}
			switch step {
			case designSaveStep:
				a.designSaveForm(c)
			case designDoneStep:
				a.designDoneView(c)
			}
			a.designFolded(c)
		})
	})
}

// designFolded are the details (attributes, seed), the model and the
// saved designs, folded under 细节与模型.
func (a *app) designFolded(c *ui.Context) {
	d := &a.design
	summary := d.attrs.Summary(i18n.Locale())
	if e, ok := voiceCatalog().ByLabel(d.model); ok {
		summary += " · " + e.DisplayName()
	}
	collapsible(c.Key("design-more"), i18n.T("Details and model"), &d.detailsOpen, func() {
		if !d.detailsOpen {
			truncText(c, summary).FontSize(px(c.Theme(), fsCaption)).TextColor(tok(c.Theme()).Text2).MaxWidth(px(c.Theme(), 320))
		}
	}, func() {
		ui.Column(c).Gap(px(c.Theme(), sp3)).Children(func() {
			a.designTarget(c)
			a.designDetails(c)
			a.designSavedVoices(c)
		})
	})
}

// designScript is the line the candidates say, its language, and
// 「生成候选」 (the primary until there are candidates).
func (a *app) designScript(c *ui.Context, step int) {
	t := c.Theme()
	d := &a.design
	ui.Field(c, i18n.T("Line to hear"), func() {
		ui.TextArea(c.Key("design-script"), &d.script).Lines(1, 4).Placeholder(i18n.T("What should this voice say?")).Label(i18n.T("Script"))
	})
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Global.Linear).Size(px(t, 16), px(t, 16)).TextColor(tok(t).Text2)
		ui.Autocomplete(c.Key("design-language"), &d.language, voiceLanguages()).Placeholder(i18n.T("Auto")).Label(i18n.T("Language")).
			Tooltip(i18n.T("Language")).Width(px(t, 140)).Shrink(1).MinWidth(px(t, 80))
		ui.Spacer(c)
		_, ok := voiceCatalog().ByLabel(d.model)
		gen := ui.Button
		label := i18n.T("Generate candidates")
		if step == designDescribeStep {
			gen = primaryButton
		} else {
			label = i18n.T("Generate again")
		}
		if gen(c.Key("design-generate"), label).Disabled(!ok || a.candsRunning() || strings.TrimSpace(d.script) == "").
			Tooltip(i18n.T("Three takes of this description, each with its own seed")).Clicked() {
			a.designGenerateCandidates()
		}
	})
}

// designCandidatesView lists the candidates: ▶, its seed and length, and
// 「选这个」 (a check once picked).
func (a *app) designCandidatesView(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	d := &a.design
	ui.Column(c.Key("design-cands")).Gap(px(t, sp1)).Children(func() {
		ui.Text(c, i18n.T("Candidates")).FontWeight(600).Role(ui.RoleHeading)
		caption(c, i18n.T("Listen, then pick the one you like."))
		for i := range d.cands {
			j := a.candJob(i)
			on := d.picked == i
			row := ui.Row(c.Key("cand-"+itoa(i))).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2)).Radius(px(t, radiusCard)).Border(1, k.Line)
			if on {
				row.Background(k.Selected).Border(1, k.Accent)
			}
			row.Children(func() {
				name := i18n.T("Candidate %d", i+1)
				switch {
				case j == nil:
					return
				case j.running():
					ui.Spinner(c)
					ui.Text(c, name).FontWeight(600)
					caption(c, j.status).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				case j.err != "":
					ui.Icon(c, icons.DangerCircle.Linear).Size(px(t, 16), px(t, 16)).TextColor(t.Danger).Shrink(0)
					ui.Text(c, name).FontWeight(600)
					ui.Text(c, j.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).Grow(1).MinWidth(0).MaxLines(2).Ellipsis("…").Tooltip(j.err)
					if j.needDownload != nil && secondaryIconButton(c.Key("download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Clicked() {
						a.download(*j.needDownload)
					}
				default:
					take := j.take
					if playButton(c.Key("play"), a.pl.Playing(take.ID), true, 32).Clicked() {
						a.play(take)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
						ui.Text(c, name).FontWeight(600)
						caption(c, i18n.T("%.1f s · seed %d", take.AudioSeconds, d.cands[i].seed))
					})
					if on {
						statusMark(c, icons.CheckCircle, k.AccentInk, i18n.T("Picked"))
					} else if ui.Button(c.Key("pick"), i18n.T("Pick this one")).Clicked() {
						a.pickCandidate(i)
					}
				}
			})
		}
	})
}

// designSaveForm is 保存: the name and 「保存音色」 (the primary).
func (a *app) designSaveForm(c *ui.Context) {
	t := c.Theme()
	d := &a.design
	ui.Row(c.Key("design-save-row")).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		ui.TextInput(c.Key("design-name"), &d.saveName).Placeholder(i18n.T("Name")).Label(i18n.T("Profile name")).Grow(1).Basis(px(t, 200)).MinWidth(0).MaxWidth(px(t, 360))
		label := i18n.T("Save voice")
		if v := a.findVoice(d.linked); v != nil && v.Name == strings.TrimSpace(d.saveName) {
			label = i18n.T("Update voice")
		}
		if primaryButton(c.Key("design-save"), label).Disabled(strings.TrimSpace(d.saveName) == "").Clicked() {
			a.designSave()
		}
	})
}

// designDoneView is 完成: the voice saved, and the hand-offs.
func (a *app) designDoneView(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	v := a.findVoice(a.design.savedID)
	if v == nil {
		return
	}
	id := v.ID
	ui.Column(c.Key("design-done")).Gap(px(t, sp3)).Padding(px(t, sp5)).Radius(px(t, radiusCard)).Background(k.Subtle).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, v.Name, a.portrait(v)).Size(px(t, 48), px(t, 48)).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				statusMark(c, icons.CheckCircle, t.Success, i18n.T("Saved to My voices"))
				ui.Text(c, v.Name).FontSize(px(t, fsTitle)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
			})
		})
		handoffRow(c, "design-handoffs", func() {
			playing := a.savedPlaying(v)
			label, ic := i18n.T("Listen"), icons.Play
			if playing {
				label, ic = i18n.T("Stop"), icons.Pause
			}
			if secondaryIconButton(c.Key("done-play"), ic, label).Label(label).Clicked() {
				a.previewSaved(v)
			}
			if primaryButton(c.Key("done-tts"), i18n.T("Use in Text to speech")).Clicked() {
				if v := a.findVoice(id); v != nil {
					a.useVoiceInStudio(v)
				}
			}
		}, func() {
			if ghostButton(c.Key("done-detail"), i18n.T("Voice details")+" ›").Clicked() {
				a.openVoiceEditor(id)
			}
		})
	})
}
