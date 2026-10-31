package main

// Quick clone's steps (0.5 F4): one guided page with a step indicator
// and one primary per step. 添加录音 (a file or the microphone) → 检查
// (the part kept, the reference check and cleaning, the transcript;
// 「下一步」) → 命名保存 (name, portrait, style and language; 「保存音色」)
// → 完成: 「试听」, 「用于文字转语音」, the detail, another clone. A clip
// read moves to 检查 by itself; a step done is a link back to it.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// Quick clone's steps (cloneState.step).
const (
	cloneAdd = iota
	cloneCheck
	cloneName
	cloneDone
)

// syncStep keeps the step in line with the clip: no clip is step 1
// (unless a voice was just saved); a clip read moves on to 检查.
func (s *cloneState) syncStep() {
	switch {
	case s.step == cloneDone:
	case !s.hasClip():
		s.step = cloneAdd
	case s.step == cloneAdd:
		s.step = cloneCheck
	}
}

// cloneStepper is the step indicator.
func (a *app) cloneStepper(c *ui.Context) {
	s := &a.clone
	last := i18n.T("Name and save")
	if s.replaceFor != "" {
		last = i18n.T("Replace")
	}
	labels := []string{i18n.T("Add a recording"), i18n.T("Check"), last, i18n.T("Done")}
	var back func(int)
	if s.step != cloneDone {
		back = func(i int) {
			if i == cloneAdd {
				a.cloneChangeClip()
				return
			}
			s.step = i
		}
	}
	stepper(c, "clone-steps", labels, s.step, back)
}

// cloneChangeClip drops the clip: back to step 1.
func (a *app) cloneChangeClip() {
	s := &a.clone
	a.pl.Stop()
	s.gen++
	s.work, s.original, s.peaks, s.err, s.busy = "", "", nil, "", ""
	s.step = cloneAdd
}

// cloneCheckFoot is 检查's foot: why the clip can't go on yet, and
// 「下一步」.
func (a *app) cloneCheckFoot(c *ui.Context) {
	t := c.Theme()
	s := &a.clone
	problem := a.cloneProblem()
	ui.Row(c.Key("clone-check-foot")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if ghostButton(c.Key("clone-prev"), "‹ "+i18n.T("Change clip")).Clicked() {
			a.cloneChangeClip()
		}
		ui.Spacer(c)
		if problem != "" {
			caption(c, problem).Shrink(1).MinWidth(0)
		}
		if primaryButton(c.Key("clone-next"), i18n.T("Next")+" ›").Disabled(problem != "").Clicked() {
			s.step = cloneName
		}
	})
}

// cloneDoneView is the last step: the voice saved, its reference's
// grade, and the hand-offs.
func (a *app) cloneDoneView(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	s := &a.clone
	v := a.findVoice(s.savedID)
	if v == nil {
		s.step = cloneAdd
		return
	}
	id := v.ID
	ui.Column(c.Key("clone-done")).Gap(px(t, sp3)).Padding(px(t, sp5)).Radius(px(t, radiusCard)).Background(k.Subtle).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			ui.Avatar(c, v.Name, a.portrait(v)).Size(px(t, 48), px(t, 48)).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				statusMark(c, icons.CheckCircle, t.Success, i18n.T("Saved to My voices"))
				ui.Text(c, v.Name).FontSize(px(t, fsTitle)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
			})
		})
		if v.RefCheck != nil {
			refCheckView(c, "done-check", v.RefCheck, refcheck.UseClone)
		}
		handoffRow(c, "clone-handoffs", func() {
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
	ui.Row(c).Children(func() {
		if ghostIconButton(c.Key("done-again"), icons.AddCircle, i18n.T("Clone another voice")).Label(i18n.T("Clone another voice")).Clicked() {
			a.resetClone("")
		}
	})
}
