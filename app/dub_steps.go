package main

// The Dub editor's steps (Source → Transcribe → Translate → Cast →
// Generate → Export) in its page header, after the dub's title (user,
// 11-01: the row under the header gave its height back to the workspace):
// one icon button each, no names; a tooltip says the step and its state.
// A done step's icon is in the accent; the current one stands on the
// pressed face; a running one spins, a failed one is red. A click shows
// the step's panel.

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// dubStepIcons are the steps' icons, in order.
var dubStepIcons = []icons.Icon{
	icons.VideoFramePlayHorizontal, // Source: the video
	icons.Subtitles,                // Transcribe
	icons.Global,                   // Translate
	icons.UsersGroupRounded,        // Cast: the speakers' voices
	icons.Soundwave,                // Generate: the dubbed speech
	icons.Export,                   // Export
}

// dubStepNames are the steps' names.
func dubStepNames() []string {
	return []string{i18n.T("Source"), i18n.T("Transcribe"), i18n.T("Translate"), i18n.T("Cast"), i18n.T("Generate"), i18n.T("Export")}
}

// dubStepState is a step's state for the stepper.
func (a *app) dubStepState(step int) (done, running, failed bool) {
	p := a.dub
	running = false
	for _, j := range a.dubJobs {
		st := dubStageOfKind(j.Kind)
		switch {
		case step == dubStepSource && (st == dub.StageFetch || st == dub.StageIngest),
			step == dubStepTranscribe && (st == dub.StageSeparate || st == dub.StageTranscribe || st == dub.StageReferences),
			step == dubStepTranslate && st == dub.StageTranslate,
			step == dubStepGenerate && (st == dub.StageSynthesize || st == dub.StageMix || st == dub.StageQC):
			running = true
		}
	}
	if step == dubStepSource && a.dubProbing() {
		running = true // reading the link's details, as the footer says
	}
	for k, st := range p.Stages {
		if st.Status != dub.StatusFailed {
			continue
		}
		s := dub.Stage(strings.Split(k, ":")[0])
		switch {
		case step == dubStepSource && (s == dub.StageFetch || s == dub.StageIngest),
			step == dubStepTranscribe && (s == dub.StageSeparate || s == dub.StageTranscribe || s == dub.StageReferences),
			step == dubStepTranslate && s == dub.StageTranslate,
			step == dubStepGenerate && (s == dub.StageSynthesize || s == dub.StageMix):
			failed = true
		}
	}
	switch step {
	case dubStepSource:
		done = p.Source.Path != "" && p.Media.Audio != ""
	case dubStepTranscribe:
		done = len(p.Segments) > 0
	case dubStepTranslate:
		done = p.AllTranslated()
	case dubStepCast:
		done = len(p.Segments) > 0 && p.Stage(dub.StageReferences, "").Status != ""
	case dubStepGenerate:
		done = p.TracksReady()
	case dubStepExport:
		done = a.dubExport.last != ""
	}
	return done, running, failed
}

// dubStepTip is a step's tooltip: its name and state (「转写 · 已完成」).
func (a *app) dubStepTip(step int, name string) string {
	done, running, failed := a.dubStepState(step)
	parts := []string{name}
	if a.dubStep == step {
		parts = append(parts, i18n.T("Current step"))
	}
	switch {
	case running:
		parts = append(parts, i18n.T("Running"))
	case failed:
		parts = append(parts, i18n.T("Failed"))
	case done:
		parts = append(parts, i18n.X("dub step", "Done"))
	case a.dubStep != step:
		parts = append(parts, i18n.T("Not done"))
	}
	return strings.Join(parts, " · ")
}

// dubSteps are the steps' icon buttons in the header, small chevrons
// between them; in a narrow window narrower buttons without chevrons,
// so the dub's title keeps some room.
func (a *app) dubSteps(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	side, w := headerControlH(t), headerControlH(t)
	narrow := pageNarrow(c)
	if narrow {
		w = px(t, 26)
	}
	names := dubStepNames()
	ui.Row(c.Key("dub-steps")).AlignItems(ui.Center).FocusGroup(ui.Horizontal).Shrink(0).Role(ui.RoleGroup).Label(i18n.T("Steps")).Children(func() {
		for i, name := range names {
			done, running, failed := a.dubStepState(i)
			cur := a.dubStep == i
			b := ghostBase(c.Key("step-"+strconv.Itoa(i))).Size(w, side).Center().Shrink(0).Label(i18n.T("Step %d: %s", i+1, name))
			tooltipBelow(c, b, a.dubStepTip(i, name))
			if cur {
				b.Background(k.Pressed)
			}
			b.Children(func() {
				col := k.Text3
				switch {
				case failed:
					col = k.Danger
				case done:
					col = k.AccentInk
				case cur || b.Hovered():
					col = k.Text
				}
				if running {
					ui.Spinner(c).Size(px(t, 14), px(t, 14))
					return
				}
				ic := dubStepIcons[i].Linear
				if cur {
					ic = dubStepIcons[i].Bold
				}
				ui.Icon(c, ic).Size(px(t, 16), px(t, 16)).TextColor(col)
			})
			if b.Clicked() {
				a.dubCommitEdit()
				a.dubStep = i
			}
			if i < len(names)-1 && !narrow {
				ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 10), px(t, 10)).TextColor(k.Text3).Shrink(0).Margin(0, px(t, 1))
			}
		}
	})
}
