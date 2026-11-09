package main

// Stories → inspector → Performance: how the story's dialogue flows
// (story_pacing.go in services): the pacing preset and merged turns, the
// scene, the AI director, and what the chosen model does with them (its
// continuity level). Personas sit on the cast cards, directions on the
// lines (stories_view.go).

import (
	"slices"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// pacingLabels are the pacing presets' names, in services.Pacings order.
func pacingLabels() []string {
	return []string{i18n.T("Dialogue (tight)"), i18n.T("Reading (standard)"), i18n.T("Relaxed"), i18n.T("Fixed gap")}
}

// pacingLabel is a document's pacing's name.
func pacingLabel(pacing string) string {
	i := slices.Index(services.Pacings, pacing)
	if i < 0 {
		i = len(services.Pacings) - 1
	}
	return pacingLabels()[i]
}

// storyContinuity is what the story's model does with scene, personas
// and directions (services.Continuity*).
func storyContinuity(model string) string {
	return services.ContinuityOf(modelInfo(model).Traits)
}

// continuityLabel names a continuity level; continuityTip explains it.
func continuityLabel(level string) string {
	switch level {
	case services.ContinuityInstruct:
		return i18n.T("Directed delivery")
	case services.ContinuityIntensity:
		return i18n.T("Directed intensity")
	}
	return i18n.T("Line by line")
}

func continuityTip(level string) string {
	switch level {
	case services.ContinuityInstruct:
		return i18n.T("This model hears each line's emotion as a short instruction (lines the first AI director directed: the scene, the persona and the direction), so the lines carry the moment's feeling. Never spoken.")
	case services.ContinuityIntensity:
		return i18n.T("This model hears how strong each line's emotion is (its exaggeration), not the emotion itself.")
	}
	if rec, ok := useModel(catalog.UseDirectedClone, catalog.WorkflowStory); ok {
		return i18n.T("This model says each line as written: scene, personas and directions are kept but not heard. Pacing and merged turns still apply. For directed delivery, use %s (it keeps cloned voices), %s or %s.", modelRef(rec), modelRefID(idCustomVoice), modelRefID(idVoiceDesign))
	}
	return i18n.T("This model says each line as written: scene, personas and directions are kept but not heard. Pacing and merged turns still apply. For directed delivery, use %s or %s.", modelRefID(idCustomVoice), modelRefID(idVoiceDesign))
}

// storyDirectedAdvice, under a model that doesn't hear a directed
// story: the models that do, the one recommended for cloned voices
// (presets: VoxCPM2, thread #167) first, with a link that picks it. The
// story's model changes only when the user follows it.
func (a *app) storyDirectedAdvice(c *ui.Context, p *lfPage, locked bool) {
	k := tok(c.Theme())
	rec, ok := useModel(catalog.UseDirectedClone, catalog.WorkflowStory)
	if !ok {
		caption(c.Key("perf-advice"), i18n.T("Switch to %s or %s (Setup) to hear the directions.", modelRefID(idCustomVoice), modelRefID(idVoiceDesign))).TextColor(k.Text3)
		return
	}
	ui.Column(c.Key("perf-advice")).Gap(px(c.Theme(), 2)).Children(func() {
		caption(c, i18n.T("To hear the directions, switch to %s (recommended: it keeps cloned voices), or to %s or %s.", modelRef(rec), modelRefID(idCustomVoice), modelRefID(idVoiceDesign))).TextColor(k.Text3)
		if !locked && linkButton(c.Key("perf-advice-use"), i18n.T("Use %s", rec.DisplayName())).AlignSelf(ui.Start).Clicked() {
			p.doc.Model, p.auto = rec.Repo, false
		}
	})
}

// storyPerformance is the Performance section.
func (a *app) storyPerformance(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	k := tok(t)
	d := &p.doc
	level := storyContinuity(d.Model)
	ui.Row(c.Key("perf-level")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		col := k.Text2
		ic := icons.Dialog2
		if level == services.ContinuityInstruct {
			col, ic = k.AccentInk, icons.MagicStick3
		}
		statusMark(c, ic, col, continuityLabel(level)).Shrink(1)
		infoTip(c.Key("perf-level-tip"), continuityTip(level))
	})
	if level == services.ContinuityLines && storyDirected(d) {
		a.storyDirectedAdvice(c, p, locked)
	}

	// Pacing.
	labels := pacingLabels()
	cur := pacingLabel(d.Production.Pacing)
	fieldInfo(c, i18n.T("Pacing"), i18n.T("How long the silences between lines are: shorter when a character goes on or answers a question, a little longer after a statement, shortest when a line is cut off (… —). Fixed gap: one gap for every line, as stories made before pacing have. A line's own pause after it wins."), func() {
		if ui.Select(c.Key("perf-pacing"), &cur, labels).Label(i18n.T("Pacing")).Disabled(locked).Changed() {
			if i := slices.Index(labels, cur); i >= 0 {
				d.Production.Pacing = services.Pacings[i]
			}
		}
	})
	a.storyDialogue(c, p, locked) // stories_dialogue_view.go
	// Scene and the AI director: only for models that hear them.
	if level == services.ContinuityLines {
		return
	}
	fieldInfo(c, i18n.T("Scene"), i18n.T("Where and when, who these people are to each other, the mood. Models that take instructions hear it with every line; it is never spoken."), func() {
		ui.TextArea(c.Key("perf-scene"), &d.Scene).Placeholder(i18n.T("Evening at home: the child wants a kitten, the mother hesitates but softens")).
			Label(i18n.T("Scene")).Lines(2, 5).MinHeight(0).Disabled(locked)
	})
	ui.Row(c.Key("perf-director")).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
		label := i18n.T("AI director")
		tip := i18n.T("A language model reads the whole story and plans its emotions: a baseline for each character, and for each line an emotion and its strength, mostly steady, with the turns of the scene marked. You can change any of them; directing again keeps what you set.")
		disabled := locked || p.directing || len(storyScript(d, nil).Lines) == 0
		if storyDirected(d) {
			dropButton(c.Key("perf-direct"), icons.MagicStick3, label).Tooltip(tip).Disabled(disabled).Menu(func(m *ui.Menu) {
				if m.Item(i18n.T("Fill in what is empty")).Chosen() {
					a.directStory(p, false)
				}
				if m.Item(i18n.T("Direct again (replace all)")).Chosen() {
					a.directStory(p, true)
				}
				m.Separator()
				if m.Item(i18n.T("Clear scene, personas, directions and emotions")).Chosen() {
					a.clearDirection(p)
				}
			})
		} else if secondaryIconButton(c.Key("perf-direct"), icons.MagicStick3, label).Label(label).Tooltip(tip).Disabled(disabled).Clicked() {
			a.directStory(p, false)
		}
		if p.directing {
			ui.Spinner(c)
			caption(c, i18n.T("Directing…"))
		} else if n := directedLines(d); n > 0 {
			caption(c, i18n.N(n, "%d line directed", "%d lines directed", n)).TextColor(k.Text3)
		}
	})
}

// directedLines counts the lines with a direction or an emotion.
func directedLines(d *services.LongformDoc) int {
	n := 0
	for _, l := range d.Lines {
		if (l.Direction != "" || l.Emotion != "") && !l.Chapter {
			n++
		}
	}
	return n
}

// clearDirection empties the scene, personas, baselines, directions and
// emotions, with Undo.
func (a *app) clearDirection(p *lfPage) {
	before := p.doc
	before.Lines = slices.Clone(p.doc.Lines)
	before.Cast = slices.Clone(p.doc.Cast)
	p.doc.Scene = ""
	for i := range p.doc.Cast {
		p.doc.Cast[i].Persona, p.doc.Cast[i].Baseline = "", ""
	}
	for i := range p.doc.Lines {
		l := &p.doc.Lines[i]
		l.Direction, l.Intensity, l.Emotion, l.Turn, l.Note, l.EmotionByUser = "", 0, "", false, "", false
	}
	a.toastUndo(i18n.T("Cleared the scene, personas, directions and emotions"), func() {
		p.doc.Scene, p.doc.Lines, p.doc.Cast = before.Scene, before.Lines, before.Cast
	})
}

// intensityLabels are a direction's intensities (0 = not set).
func intensityLabels() []string {
	return []string{i18n.T("Intensity: —"), i18n.T("Restrained"), i18n.X("intensity", "Clear"), i18n.T("Strong")}
}

// emotionLabels are the emotions' names, in services.Emotions order.
func emotionLabels() []string {
	return []string{i18n.X("emotion", "Steady"), i18n.X("emotion", "Happy"), i18n.X("emotion", "Excited"), i18n.X("emotion", "Tender"),
		i18n.X("emotion", "Pleading"), i18n.X("emotion", "Hesitant"), i18n.X("emotion", "Worried"), i18n.X("emotion", "Hurt"),
		i18n.X("emotion", "Sad"), i18n.X("emotion", "Angry"), i18n.X("emotion", "Scared"), i18n.X("emotion", "Surprised"),
		i18n.X("emotion", "Apologetic")}
}

// emotionLabel names an emotion ("" for none).
func emotionLabel(e string) string {
	if i := slices.Index(services.Emotions, services.NormEmotion(e)); i >= 0 {
		return emotionLabels()[i]
	}
	return ""
}

// emotionIntensityLabels are an emotion's intensities, 0–3.
func emotionIntensityLabels() []string {
	return []string{i18n.X("intensity", "Barely"), i18n.T("Restrained"), i18n.X("intensity", "Clear"), i18n.T("Strong")}
}

// directionChip is a line's direction as a chip ("pleading · strong",
// "hurt · 2 · turn"), greyed when the model does not hear it.
func directionChip(c *ui.Context, l *services.StoryLine, heard bool) ui.Element {
	t := c.Theme()
	k := tok(t)
	text := l.Direction
	switch {
	case l.Emotion != "":
		if text == "" {
			text = emotionLabel(l.Emotion)
		}
		text += " · " + strconv.Itoa(min(max(l.Intensity, 0), 3))
		if l.Turn {
			text += " · " + i18n.X("emotion", "turn")
		}
	case l.Intensity == services.IntensityLight || l.Intensity == services.IntensityStrong:
		text += " · " + intensityLabels()[l.Intensity] // "clear" is the usual: unsaid
	}
	col, bg := k.AccentInk, k.AccentSoft
	tip := i18n.T("Direction: %s", text)
	if l.Note != "" {
		tip += "\n" + l.Note
	}
	if !heard {
		col, bg = k.Text3, k.Hover
		tip = i18n.T("Direction: %s (this model does not hear it)", text)
	}
	return ui.Text(c, text).FontSize(px(t, fsMicro)).TextColor(col).SingleLine().Ellipsis("…").Padding(px(t, 1), px(t, 6)).
		Radius(px(t, 4)).Background(bg).Shrink(1).MinWidth(0).MaxWidth(px(t, 180)).Tooltip(tip)
}

// lineDirection is the direction row of a line's options: its emotion,
// intensity and turn (the second AI director's), or the first
// director's direction and intensity. What the user changes there the
// director keeps.
func (a *app) lineDirection(c *ui.Context, p *lfPage, l *services.StoryLine, locked bool) {
	t := c.Theme()
	if l.Emotion != "" || !storyDirected(&p.doc) || storyHasEmotions(&p.doc) {
		a.lineEmotion(c, l, locked)
		return
	}
	formRow(c, "opt-direction", i18n.X("story", "Direction"), i18n.T("How this line is said: “pleading”, “softly, holding back tears”. Models that take instructions hear it; it is never spoken."), func() {
		ui.TextInput(c.Key("line-direction"), &l.Direction).Placeholder(i18n.T("e.g. pleading")).Label(i18n.X("story", "Direction")).
			Width(px(t, 150)).Shrink(1).MinWidth(px(t, 90)).Disabled(locked)
		labels := intensityLabels()
		cur := labels[min(max(l.Intensity, 0), len(labels)-1)]
		if ui.Select(c.Key("line-intensity"), &cur, labels).Label(i18n.T("Intensity")).Width(px(t, 110)).Shrink(1).MinWidth(px(t, 80)).Disabled(locked || l.Direction == "").Changed() {
			l.Intensity = slices.Index(labels, cur)
		}
	})
}

// personaSummary is the Performance section's summary: the pacing.
func personaSummary(d *services.LongformDoc) string { return pacingLabel(d.Production.Pacing) }

// storyHasEmotions says whether any line of a story has an emotion.
func storyHasEmotions(d *services.LongformDoc) bool {
	for _, l := range d.Lines {
		if l.Emotion != "" {
			return true
		}
	}
	return false
}

// lineEmotion is the emotion row of a line's options: emotion, intensity,
// turn, the user's own wording, and the director's note.
func (a *app) lineEmotion(c *ui.Context, l *services.StoryLine, locked bool) {
	t := c.Theme()
	k := tok(t)
	formRow(c, "opt-emotion", i18n.T("Emotion"), i18n.T("How this line feels, in the AI director's words: most lines are steady (the character's baseline goes on), a few carry a strong feeling. Models that take instructions hear a short instruction made from it; it is never spoken."), func() {
		labels := append([]string{i18n.T("Emotion: —")}, emotionLabels()...)
		cur := labels[0]
		if i := slices.Index(services.Emotions, l.Emotion); i >= 0 {
			cur = labels[i+1]
		}
		if ui.Select(c.Key("line-emotion"), &cur, labels).Label(i18n.T("Emotion")).Width(px(t, 120)).Shrink(1).MinWidth(px(t, 80)).Disabled(locked).Changed() {
			l.Emotion, l.EmotionByUser = "", true
			if i := slices.Index(labels, cur); i > 0 {
				l.Emotion = services.Emotions[i-1]
			}
		}
		il := emotionIntensityLabels()
		ci := il[min(max(l.Intensity, 0), 3)]
		if ui.Select(c.Key("line-emotion-intensity"), &ci, il).Label(i18n.T("Intensity")).Width(px(t, 100)).Shrink(1).MinWidth(px(t, 70)).
			Disabled(locked || l.Emotion == "").Changed() {
			l.Intensity, l.EmotionByUser = slices.Index(il, ci), true
		}
		if ui.Checkbox(c.Key("line-turn"), &l.Turn, i18n.X("emotion", "Turn")).Tooltip(i18n.T("A turn of the scene: the feeling may change at once here (elsewhere it moves a step at a time).")).
			Disabled(locked || l.Emotion == "").Changed() {
			l.EmotionByUser = true
		}
	})
	if l.Emotion == "" {
		return
	}
	formRow(c, "opt-own-words", i18n.T("Own words"), i18n.T("Say how the line is delivered in your own words (“撒娇”, “softly”) in place of the emotion's wording; at most about ten characters."), func() {
		if ui.TextInput(c.Key("line-direction"), &l.Direction).Placeholder(services.EmotionInstruct(l.Emotion, l.Intensity, "", false, i18n.Locale() == "zh-Hans")).
			Label(i18n.T("Own words")).Width(px(t, 180)).Shrink(1).MinWidth(px(t, 90)).Disabled(locked).Changed() {
			l.EmotionByUser = true
		}
	})
	if l.Note != "" {
		caption(c.Key("line-note"), l.Note).TextColor(k.Text3)
	}
}

// baselineSelect picks a character's baseline emotion.
func baselineSelect(c *ui.Context, m *services.CastMember, locked bool) {
	t := c.Theme()
	k := tok(t)
	labels := append([]string{"—"}, emotionLabels()...)
	cur := labels[0]
	if i := slices.Index(services.Emotions, m.Baseline); i >= 0 {
		cur = labels[i+1]
	}
	ui.Row(c.Key("baseline-row")).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		caption(c, i18n.T("Usual mood")).TextColor(k.Text2).Shrink(0)
		if ui.Select(c.Key("baseline"), &cur, labels).Label(i18n.T("Baseline mood of %s", m.Name)).
			Tooltip(i18n.T("The character's usual feeling in this story: their steady lines are said in it, faintly.")).
			Grow(1).Shrink(1).MinWidth(px(t, 90)).Disabled(locked).Changed() {
			m.Baseline = ""
			if i := slices.Index(labels, cur); i > 0 {
				m.Baseline = services.Emotions[i-1]
			}
		}
	})
}
