package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// toolsPage is /tools.
func (a *app) toolsPage(c *ui.Context) {
	t := c.Theme()
	tabs := []string{i18n.T("A/B voices"), i18n.T("Convert"), i18n.T("Rate fit"), i18n.T("Direction"), i18n.T("File probe"), i18n.T("Watermark")}
	narrow := a.pageWidth(c) < px(t, 720)
	pageHeader(c, i18n.T("Tools"), func() {
		if !narrow {
			return
		}
		// Too narrow for the six tabs: the tools in a menu.
		dropButton(c.Key("tools-menu"), icons.RulerCrossPen, tabs[min(a.toolTab, len(tabs)-1)]).Label(i18n.T("Tool")).Shrink(0).Menu(func(m *ui.Menu) {
			for i, l := range tabs {
				if m.Item(l).Checked(i == a.toolTab).Chosen() {
					a.toolTab = i
				}
			}
		})
	})
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 760)).Children(func() {
			if !narrow {
				ui.Row(c).Children(func() {
					underlineTabs(c.Key("tools-tab"), &a.toolTab, tabs...).Label(i18n.T("Tool")).Grow(1)
				})
			}
			switch a.toolTab {
			case 0:
				a.abTool(c)
			case 1:
				a.convertTool(c)
			case 2:
				a.rateFitTool(c)
			case 3:
				a.directionTool(c)
			case 5:
				a.watermarkTool(c) // watermark_app.go (thread #14)
			default:
				a.probeTool(c)
			}
		})
	})
}

func toolIntro(c *ui.Context, text string) {
	caption(c, text).Wrap()
}

// toolFile is a tool's chosen file: its name (or none) and Choose….
func toolFile(c *ui.Context, key, name string, busy bool) bool {
	t := c.Theme()
	k := tok(t)
	chosen := false
	fillCard(c.Key(key)).Padding(px(t, sp2), px(t, sp2), px(t, sp2), px(t, sp3)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.FileText.Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2).Shrink(0)
			truncText(c, name).Grow(1)
			chosen = secondaryIconButton(c.Key("choose"), icons.FolderOpen, i18n.T("Choose…")).Label(i18n.T("Choose…")).Disabled(busy).Clicked()
		})
	})
	return chosen
}

// runRow shows a preview's state and its Play button.
func (a *app) runRow(c *ui.Context, key, title string, run *toolRun) {
	t := c.Theme()
	card(c.Key(key)).Padding(px(t, sp3)).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			if run != nil && run.busy {
				ui.Column(c).Size(px(t, 32), px(t, 32)).Center().Shrink(0).Children(func() { ui.Spinner(c) })
			} else if playButton(c.Key("play"), run != nil && a.pl.Playing(run.key), true, 32).Disabled(run == nil || run.output == "").Clicked() {
				a.playRun(run)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				truncText(c, title).FontWeight(600)
				switch {
				case run == nil:
					caption(c, i18n.T("Not made yet"))
				case run.busy:
					caption(c, i18n.T("Generating with %s…", run.model))
				case run.err != "":
					ui.Text(c, run.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).Wrap()
				default:
					truncText(c, run.model).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2)
				}
			})
		})
	})
}

func (a *app) abTool(c *ui.Context) {
	t := c.Theme()
	toolIntro(c, i18n.T("The same phrase in two voices, to compare them. Nothing is kept in the History."))
	ui.Field(c, i18n.T("Phrase"), func() {
		ui.TextArea(c.Key("ab-text"), &a.ab.text).Height(px(t, 64)).Label(i18n.T("Phrase"))
	})
	labels := toolLabels(a.toolChoices())
	if a.ab.a == "" || !contains(labels, a.ab.a) {
		a.ab.a = labels[0]
	}
	if a.ab.b == "" || !contains(labels, a.ab.b) {
		a.ab.b = labels[min(1, len(labels)-1)]
	}
	ui.Row(c).Gap(px(t, sp3)).Wrap().Children(func() {
		ui.Field(c, i18n.T("Voice A"), func() {
			ui.Select(c.Key("ab-a"), &a.ab.a, labels).Label(i18n.T("Voice A")).Width(px(t, 240))
		})
		ui.Field(c, i18n.T("Voice B"), func() {
			ui.Select(c.Key("ab-b"), &a.ab.b, labels).Label(i18n.T("Voice B")).Width(px(t, 240))
		})
	})
	busy := a.ab.runs[0] != nil && a.ab.runs[0].busy || a.ab.runs[1] != nil && a.ab.runs[1].busy
	ui.Row(c).Children(func() {
		if primaryButton(c.Key("ab-run"), i18n.T("Compare")).Disabled(busy || strings.TrimSpace(a.ab.text) == "").Clicked() {
			a.runAB()
		}
	})
	ui.Row(c).Gap(px(t, sp3)).Wrap().Children(func() {
		ui.Column(c).Grow(1).Basis(px(t, 240)).MinWidth(0).Children(func() { a.runRow(c, "ab-0", "A · "+a.ab.a, a.ab.runs[0]) })
		ui.Column(c).Grow(1).Basis(px(t, 240)).MinWidth(0).Children(func() { a.runRow(c, "ab-1", "B · "+a.ab.b, a.ab.runs[1]) })
	})
}

func (a *app) convertTool(c *ui.Context) {
	t := c.Theme()
	cv := &a.cv
	toolIntro(c, i18n.T("Say it in another voice: AudioSmith listens to a recording, then speaks what it heard in a saved voice. The take lands in the History."))
	name := cv.name
	if name == "" {
		name = i18n.T("No recording chosen")
	} else if cv.duration > 0 {
		name += " · " + fmt.Sprintf("%.1f s", cv.duration)
	}
	if toolFile(c, "cv-file", name, cv.stage != "") {
		a.chooseConvertFile()
	}
	labels := toolLabels(a.toolChoices())
	if cv.voice == "" || !contains(labels, cv.voice) {
		cv.voice = labels[0]
	}
	ui.Field(c, i18n.T("Voice"), func() {
		ui.Select(c.Key("cv-voice"), &cv.voice, labels).Label(i18n.T("Voice")).Width(px(t, 280)).Shrink(1).MinWidth(0)
	})
	settingRow(c, "cv-match", i18n.T("Match the recording's length"), i18n.T("%s aims at the length as it speaks; other models' takes are time-stretched to it, at the same pitch (up to twice as fast or slow).", modelRefID(idOmniVoice)), func() {
		inkSwitch(c.Key("cv-match-switch"), &cv.match).Label(i18n.T("Match the recording's length"))
	})
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		if primaryButton(c.Key("cv-run"), i18n.T("Convert")).Disabled(cv.file == "" || cv.stage != "").Clicked() {
			a.runConvert()
		}
		if cv.stage != "" {
			ui.Spinner(c)
			caption(c, cv.stage)
		}
	})
	if cv.err != "" {
		statusMark(c, icons.DangerCircle, t.Danger, cv.err).Tooltip(cv.err)
	}
	if cv.heard == "" && cv.note == "" && cv.take == nil {
		return
	}
	card(c.Key("cv-result")).Children(func() {
		if cv.heard != "" {
			sectionTitle(c, i18n.T("What it heard"))
			ui.Text(c.Key("cv-heard"), cv.heard).Wrap().Selectable()
		}
		if cv.note != "" {
			banner(c, icons.InfoCircle, tok(t).Text2, cv.note, nil)
		}
		if tk := cv.take; tk != nil {
			ui.Row(c.Key("cv-take")).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
				if playButton(c.Key("cv-play"), a.pl.Playing(tk.ID), true, 32).Clicked() {
					a.play(tk)
				}
				ui.Text(c, i18n.T("Take ready · %.1f s", tk.AudioSeconds)).Grow(1).MinWidth(0)
				if ghostIconButton(c.Key("cv-history"), icons.History, i18n.T("Open History")).Label(i18n.T("Open History")).Clicked() {
					a.navigate("/history")
				}
			})
		}
	})
}

func (a *app) rateFitTool(c *ui.Context) {
	t := c.Theme()
	rf := &a.rf
	toolIntro(c, i18n.T("Will a line fit its slot when spoken? A prediction from the language's speaking rate; a language model can propose a shorter version."))
	ui.Field(c, i18n.T("Line"), func() {
		ui.TextArea(c.Key("rf-text"), &rf.text).Height(px(t, 64)).Label(i18n.T("Line"))
	})
	labels, _ := rateFitLangs()
	if rf.lang == "" {
		rf.lang = rateLangDefault()
	}
	ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.End).Wrap().Children(func() {
		ui.Field(c, i18n.T("Language"), func() {
			ui.Select(c.Key("rf-lang"), &rf.lang, labels).Label(i18n.T("Language")).Width(px(t, 200))
		})
		ui.Field(c, i18n.T("Slot (seconds)"), func() {
			numberInput(c.Key("rf-slot"), &rf.slot, 0.1, 600, 0.1, numOpts{Label: i18n.T("Slot (seconds)")})
		})
		ui.Field(c, i18n.T("Speed"), func() {
			numberInput(c.Key("rf-speed"), &rf.speed, 0.5, 2, 0.05, numOpts{Label: i18n.T("Speed")})
		})
	})
	canRewrite := a.llmCan(llm.SkillFitRewrite)
	desc := i18n.T("Asks the writing-help model for a version that fits, when the line overflows.")
	if !canRewrite {
		desc = i18n.T("Set up a language model in Settings → Writing help to get shorter versions.")
	}
	settingRowNote(c, "rf-rewrite", i18n.T("Suggest a shorter version"), desc, func() {
		inkSwitch(c.Key("rf-rewrite-switch"), &rf.rewrite).Disabled(!canRewrite).Label(i18n.T("Suggest a shorter version"))
	})
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		if primaryButton(c.Key("rf-run"), i18n.T("Check")).Disabled(rf.busy || strings.TrimSpace(rf.text) == "" || rf.slot <= 0).Clicked() {
			a.runRateFit()
		}
		if rf.busy {
			ui.Spinner(c)
		}
	})
	if rf.err != "" {
		statusMark(c, icons.DangerCircle, t.Danger, rf.err).Tooltip(rf.err)
	}
	if res := rf.res; res != nil {
		card(c.Key("rf-result")).Children(func() {
			fitRow(c, "rf-fit", i18n.T("As written"), res.Fit)
			if res.Rewrite != "" {
				ui.Box(c).Height(1).Background(tok(t).Line)
				ui.Text(c.Key("rf-rw"), res.Rewrite).Wrap().Selectable()
				if res.RewriteFit != nil {
					fitRow(c, "rf-rw-fit", i18n.T("Shorter version"), *res.RewriteFit)
				}
			}
		})
	}
}

// fitRow shows a prediction.
func fitRow(c *ui.Context, key, title string, f translate.Fit) {
	t := c.Theme()
	col, verdict, ic := t.Success, i18n.T("Fits"), icons.CheckCircle
	switch f.Verdict {
	case translate.FitTight:
		col, verdict, ic = t.Warning, i18n.T("Tight"), icons.DangerTriangle
	case translate.FitOverflow:
		col, verdict, ic = t.Danger, i18n.T("Too long"), icons.CloseCircle
	}
	ui.Row(c.Key(key)).Gap(px(t, sp3)).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, title).FontWeight(600).Width(px(t, 120))
		statusMark(c, ic, col, verdict).Width(px(t, 90))
		caption(c, i18n.T("%.1f s spoken for a %.1f s slot (%.0f%%)", f.Predicted, f.Slot, f.Ratio*100)).Grow(1).Basis(px(t, 180))
	})
}

func (a *app) directionTool(c *ui.Context) {
	t := c.Theme()
	d := &a.dir
	toolIntro(c, i18n.T("Direct a line as you would an actor: “more urgent, a little scared”. AudioSmith turns the note into emotions, an instruction for models that take one, and rate, pitch and energy."))
	ui.Field(c, i18n.T("Note"), func() {
		ui.TextInput(c.Key("dir-note"), &d.note).Placeholder(i18n.T("warm and slow, like a bedtime story")).Label(i18n.T("Note"))
	})
	if strings.TrimSpace(d.note) != "" {
		a.warmLocalLLM(llm.SkillDirectionParse) // a note is being written: load the model meanwhile (local_llm_warm.go)
	}
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		if primaryButton(c.Key("dir-run"), i18n.T("Read the note")).Disabled(d.busy || strings.TrimSpace(d.note) == "").Clicked() {
			a.runDirection()
		}
		if d.busy {
			ui.Spinner(c)
			switch {
			case d.waiting:
				caption(c, i18n.T("Waiting for the current job to finish…"))
			case d.warming:
				caption(c, i18n.T("Loading model…"))
			}
		}
		via := i18n.T("Keywords (set up a language model in Settings → Writing help for finer readings)")
		if a.llmCan(llm.SkillDirectionParse) {
			via = i18n.T("With the writing-help language model")
		}
		infoTip(c.Key("dir-via"), via)
	})
	res := d.res
	if res == nil {
		return
	}
	if res.Fallback == coachFallbackBusy {
		caption(c, i18n.T("The engine was busy with another job, so the note was read by keywords."))
	}
	card(c.Key("dir-res")).Gap(px(t, 6)).Children(func() {
		row := func(k, label, value string) {
			ui.Row(c.Key(k)).Gap(px(t, sp3)).Children(func() {
				ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Width(px(t, 120)).Shrink(0)
				ui.Text(c, value).Wrap().Grow(1).MinWidth(0).Selectable()
			})
		}
		row("emo", i18n.T("Emotions"), orDash(strings.Join(res.Emotions, ", ")))
		row("ins", i18n.T("Instruction"), orDash(res.Instruct))
		row("rate", i18n.T("Rate"), fmt.Sprintf("%+.0f%%", res.Rate*100))
		row("pitch", i18n.T("Pitch"), fmt.Sprintf("%+.1f st", res.Pitch))
		row("energy", i18n.T("Energy"), fmt.Sprintf("%+.2f", res.Energy))
		if res.TranslateHint != "" {
			row("hint", i18n.T("For translators"), res.TranslateHint)
		}
	})
	e := directionModel(a.preset())
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		if secondaryIconButton(c.Key("dir-preview"), icons.Play, i18n.T("Hear it")).Label(i18n.T("Hear it")).Disabled(d.preview != nil && d.preview.busy || res.Instruct == "").Clicked() {
			a.previewDirection()
		}
		caption(c, i18n.T("A sample line with %s", modelRef(e))).SingleLine().Ellipsis("…").MinWidth(0)
	})
	if d.preview != nil {
		a.runRow(c, "dir-run-row", i18n.T("Sample"), d.preview)
	}
}

func (a *app) probeTool(c *ui.Context) {
	t := c.Theme()
	p := &a.prb
	toolIntro(c, i18n.T("What a media file holds: container, codec, sample rate, channels, length, video. Read by the engine's media worker."))
	name := p.file
	if name == "" {
		name = i18n.T("No file chosen")
	}
	if toolFile(c, "prb-file", name, p.busy) {
		a.chooseProbeFile()
	}
	if p.busy {
		ui.Spinner(c)
	}
	if p.err != "" {
		statusMark(c, icons.DangerCircle, t.Danger, p.err).Tooltip(p.err)
	}
	if p.out != "" {
		a.codeBlock(c, "probe", integBlock{Title: i18n.T("Result"), Lang: "json", Code: p.out})
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
