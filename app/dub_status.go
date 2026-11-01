package main

// The Dub editor's status in its page header (thread #134, user 11-01:
// the footer went), just left of the step settings' toggle: what runs, as
// a short word and its count (「生成中 3/6」) with a progress ring, and
// Cancel; or the dub's phase (「已翻译」, 「完成」). Its tooltip has the
// sentence the footer used to say. In a narrow header only the mark
// shows, and while a step runs nothing: the step's own icon in the header
// turns into the ring, and ⋯ has Cancel.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubStatusView is what the header's status shows.
type dubStatusView struct {
	short, full string
	tone        string  // "busy", "ok", "fail" or "" (a phase)
	frac        float64 // the run's progress when known, else -1
	cancel      bool    // the run can be stopped
}

// dubStatusNow is the open dub's status.
func (a *app) dubStatusNow() dubStatusView {
	p := a.dub
	switch {
	case a.dubBusy():
		j := a.dubRunning()
		v := dubStatusView{short: dubStageShort(dubStageOfKind(j.Kind)), full: a.dubStatus(), tone: "busy", frac: determinate(j), cancel: true}
		if n := dubProgressCount(j.Progress.Message); n != "" {
			v.short += " " + n
		}
		if j.State != jobs.StateRunning {
			v.short = i18n.T("Waiting")
		}
		return v
	case a.dubProbing():
		return dubStatusView{short: i18n.T("Reading details"), full: i18n.T("Reading the video's details…"), tone: "busy", frac: -1, cancel: true}
	case a.dubExport.job != "":
		return dubStatusView{short: i18n.T("Exporting"), full: i18n.T("Exporting…"), tone: "busy", frac: -1}
	}
	ph := p.Phase()
	v := dubStatusView{short: dubPhaseText(ph), frac: -1}
	v.full = v.short
	switch ph {
	case dub.PhaseDone:
		v.tone = "ok"
		v.full = i18n.T("Every line is up to date.")
		if n := a.dubStaleCount(a.dubLang); n > 0 {
			v.tone = ""
			v.full = i18n.N(n, "%d line changed since it was generated.", "%d lines changed since they were generated.", n)
		}
	case dub.PhaseFailed:
		v.tone = "fail"
	}
	return v
}

// dubStageShort is a stage's word for the header's status.
func dubStageShort(s dub.Stage) string {
	switch s {
	case dub.StageFetch:
		return i18n.T("Downloading")
	case dub.StageIngest:
		return i18n.T("Reading")
	case dub.StageSeparate:
		return i18n.T("Separating")
	case dub.StageTranscribe:
		return i18n.T("Transcribing")
	case dub.StageReferences:
		return i18n.T("Picking voices")
	case dub.StageTranslate:
		return i18n.T("Translating")
	case dub.StageSynthesize:
		return i18n.T("Generating")
	case dub.StageMix:
		return i18n.T("Mixing")
	case dub.StageQC:
		return i18n.T("Checking")
	}
	return i18n.T("Working")
}

// dubProgressCount is the "3/6" of a job's progress message ("" none).
func dubProgressCount(msg string) string {
	for f := range strings.FieldsSeq(msg) {
		if i := strings.Index(f, "/"); i > 0 && i < len(f)-1 && len(f) <= 11 {
			return strings.Trim(f, "()[],.;:")
		}
	}
	return ""
}

// dubHeadStatus is the status in the header; narrow, its mark alone.
func (a *app) dubHeadStatus(c *ui.Context, narrow bool) {
	t := c.Theme()
	k := tok(t)
	if a.dub == nil {
		return
	}
	v := a.dubStatusNow()
	if v.short == "" || narrow && v.tone == "busy" {
		return // narrow, the running step's icon has the ring (dubSteps), ⋯ Cancel
	}
	box := ui.Row(c.Key("dub-status")).Height(headerControlH(t)).Gap(px(t, 6)).AlignItems(ui.Center).
		Shrink(1).MinWidth(0).Role(ui.RoleStatus).Label(v.full)
	if !narrow {
		box.Padding(0, px(t, 4))
	}
	tooltipBelow(c, box, v.full)
	box.Children(func() {
		side := px(t, 16)
		switch {
		case v.tone == "busy" && v.frac > 0:
			progressRing(c.Key("dub-ring"), v.frac, side)
		case v.tone == "busy":
			ui.Spinner(c).Size(px(t, 14), px(t, 14))
		case v.tone == "ok":
			ui.Icon(c, icons.CheckCircle.Linear).Size(side, side).TextColor(t.Success).Shrink(0)
		case v.tone == "fail":
			ui.Icon(c, icons.DangerCircle.Linear).Size(side, side).TextColor(t.Danger).Shrink(0)
		case narrow:
			ui.Box(c).Size(px(t, 7), px(t, 7)).Radius(px(t, 4)).Background(k.Text3).Shrink(0)
		}
		if !narrow {
			ui.Text(c.Key("dub-status-text"), v.short).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").
				MinWidth(0).MaxWidth(px(t, 150))
		}
	})
	// Cancel; narrow, in ⋯ (dubHeaderActions).
	if v.cancel && !narrow {
		b := iconButton(c.Key("dub-cancel"), icons.CloseCircle, i18n.T("Cancel")).Tooltip("")
		if tooltipBelow(c, b, i18n.T("Stop the run")).Clicked() {
			a.dubCancel()
		}
	}
}

// progressRing is a small ring that fills clockwise from the top as f
// goes from 0 to 1, side DIPs across.
func progressRing(c *ui.Context, f float64, side float32) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.Box(c).Size(side, side).Shrink(0).Role(ui.RoleProgress).Label(percent(f))
	b.Draw(func(p *ui.Painter, r ui.Rect) {
		w := max(side/8, 1.5)
		cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, min(r.W, r.H)/2-w/2
		var bg, fg ui.Path
		bg.Circle(cx, cy, rad)
		p.StrokePath(&bg, w, k.Line)
		ringPath(&fg, cx, cy, rad).Trim(0, float32(min(max(f, 0), 1)))
		p.StrokePath(&fg, w, k.Accent)
	})
	return b
}

// ringPath adds a circle that starts at the top and runs clockwise.
func ringPath(p *ui.Path, cx, cy, r float32) *ui.Path {
	const q = 0.5522847498
	p.MoveTo(cx, cy-r)
	p.CubeTo(cx+r*q, cy-r, cx+r, cy-r*q, cx+r, cy)
	p.CubeTo(cx+r, cy+r*q, cx+r*q, cy+r, cx, cy+r)
	p.CubeTo(cx-r*q, cy+r, cx-r, cy+r*q, cx-r, cy)
	p.CubeTo(cx-r, cy-r*q, cx-r*q, cy-r, cx, cy-r)
	return p
}

// dubStatus says what runs, in full (the header status's tooltip).
func (a *app) dubStatus() string {
	j := a.dubRunning()
	if j.ID == "" {
		return ""
	}
	if j.Kind == services.KindDubFetch && j.State == jobs.StateRunning {
		return dubFetchStatus(j.Progress)
	}
	st := dubStageName(dubStageOfKind(j.Kind))
	var p dubJobParams
	dubJSON(j.Params, &p)
	if p.Lang != "" {
		st = dubLangName(p.Lang) + " · " + st
	}
	if j.Progress.Message != "" && strings.Contains(j.Progress.Message, "/") {
		st += " " + j.Progress.Message
	}
	if f := determinate(j); f > 0 {
		st += " · " + percent(f)
	}
	if j.State != jobs.StateRunning {
		st = i18n.T("Waiting: %s", st)
	}
	return st
}

type dubJobParams struct {
	Lang string `json:"lang"`
}

// dubStartLabel names the first run's button.
func (a *app) dubStartLabel() string {
	if a.dub.Settings.Review {
		return i18n.T("Transcribe")
	}
	return i18n.T("Dub it")
}

func dubTranslateLabel(p *dub.Project) string {
	if len(p.Targets) > 1 {
		return i18n.N(len(p.Targets), "Translate", "Translate into %d languages", len(p.Targets))
	}
	return i18n.T("Translate")
}
