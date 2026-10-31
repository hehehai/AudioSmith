package main

// The Calls page's new call form, its examples, and the page's dialogs;
// split from calls_view.go.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/telephony"
)

// newCallForm is the new call.
func (a *app) newCallForm(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	f := &a.callForm
	sectionHeading(c, i18n.T("New call"))
	ui.Field(c, i18n.T("Phone number"), func() {
		ui.TextInput(c.Key("nc-number"), &f.number).Placeholder("+1 555 010 0199").Label(i18n.T("Phone number"))
	})
	num := telephony.NormalizeNumber(f.number)
	hint, bad := i18n.T("Include the country code, like +44 for the UK or +86 for China."), false
	switch telephony.CheckNumber(num) {
	case telephony.NumberOK:
		if r := callRegionName(telephony.Region(num)); r != "" {
			hint = i18n.T("Calling %s", r)
		} else {
			hint = ""
		}
	case telephony.NumberNoPlus:
		if len(f.number) > 3 {
			hint, bad = i18n.T("Start with + and the country code, like +1 or +44."), true
		}
	case telephony.NumberBadShape:
		if len(f.number) > 3 {
			hint, bad = i18n.T("That doesn't look like a full international number."), true
		}
	}
	if hint != "" {
		col := k.Text2
		if bad {
			col = t.Danger
		}
		ui.Text(c.Key("nc-number-hint"), hint).FontSize(px(t, fsCaption)).TextColor(col)
	}
	ui.Field(c, i18n.T("What should it do?"), func() {
		ui.TextArea(c.Key("nc-brief"), &f.brief).Height(px(t, 96)).Placeholder(i18n.T("Book a table for two this Friday at 7 pm under the name Sam. If 7 is taken, anything between 6 and 8 is fine.")).
			Label(i18n.T("What should it do?"))
	})
	ui.Row(c.Key("nc-chips")).Gap(px(t, sp1)).Wrap().Children(func() {
		for i, ex := range callExamples() {
			b := chip(c.Key(i), ex.title, "", f.brief == ex.brief)
			if f.brief != ex.brief && !b.Hovered() {
				b.Background(k.Hover)
			}
			if b.Clicked() {
				f.brief = ex.brief
			}
		}
	})
	labels, _ := a.callVoices()
	ui.Field(c, i18n.T("Voice"), func() {
		if len(labels) == 0 {
			ui.Column(c.Key("nc-novoice")).Gap(px(t, 4)).Children(func() {
				statusMark(c, icons.DangerTriangle, t.Warning, i18n.T("No voice can place calls yet."))
				caption(c, i18n.T("Calls speak only in designed voices, or in your own cloned voice once you record its consent statement.")).Wrap()
				ui.Row(c).Gap(px(t, sp2)).Children(func() {
					if ghostButton(c.Key("nc-verify"), i18n.T("Verify a voice")).Clicked() {
						a.navigate("/voices")
					}
					if ghostButton(c.Key("nc-design"), i18n.T("Design a voice")).Clicked() {
						a.navigate("/design")
					}
				})
			})
			return
		}
		a.formVoice()
		ui.Select(c.Key("nc-voice"), &f.voice, labels).Label(i18n.T("Voice")).Width(px(t, 300)).Shrink(1).MinWidth(0)
	})
	sectionTitle(c, i18n.T("What it will say first"))
	settingRow(c, "nc-disclose", i18n.T("AI disclosure"), i18n.T("Plays in full before the conversation starts; it can't be interrupted."), func() {
		inkSwitch(c.Key("nc-disclose-switch"), &f.disclose).Label(i18n.T("AI disclosure"))
	})
	if f.disclose {
		if !f.edited {
			f.disclosure, _ = a.formDisclosure()
		}
		if ui.TextArea(c.Key("nc-disclosure"), &f.disclosure).Height(px(t, 56)).Label(i18n.T("What it will say first")).Changed() {
			f.edited = true
		}
	} else {
		ui.Column(c.Key("nc-nodisclose")).Children(func() {
			banner(c, icons.DangerTriangle, t.Warning, i18n.T("Without a disclosure the other person may not know they are talking to an AI. In many places telling them is required."), nil)
		})
	}
	mins := make([]string, len(callMinutes))
	for i, n := range callMinutes {
		mins[i] = minutesLabel(n)
	}
	ui.Field(c, i18n.T("Maximum length"), func() {
		ui.Select(c.Key("nc-minutes"), &f.minutes, mins).Label(i18n.T("Maximum length")).Width(px(t, 140))
	})
	// The plan.
	fillCard(c.Key("nc-plan")).Gap(px(t, 6)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
		ui.Text(c, i18n.T("Plan preview")).FontWeight(600)
		if b := strings.TrimSpace(f.brief); b != "" {
			ui.Text(c, b).FontSize(px(t, fsCaption)).Wrap()
		} else {
			caption(c, i18n.T("Describe the task above and the plan appears here."))
		}
		var rules []string
		if f.disclose {
			rules = append(rules, i18n.T("Opens with the disclosure."))
		}
		rules = append(rules, i18n.T("Says it is an AI if asked, and never claims to be you."),
			i18n.T("Won't share payment details, passwords or ID numbers."),
			i18n.T("Asks you when it is unsure; you can take over at any time."),
			i18n.T("Ends politely, and hangs up after %d min at most.", a.formMinutes()))
		for _, r := range rules {
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.Check.Linear).Size(px(t, 13), px(t, 13)).TextColor(k.AccentInk).Shrink(0).Margin(px(t, 2), 0, 0)
				ui.Text(c, r).FontSize(px(t, fsCaption)).TextColor(k.Text2).Wrap().Grow(1).MinWidth(0)
			})
		}
	})
	ready, _ := callsReady(a.callChecks())
	problem := a.callFormProblem()
	label := i18n.T("Call")
	if telephony.ValidNumber(num) {
		label = i18n.T("Call %s", num)
	}
	if a.callBusy {
		label = i18n.T("Starting call…")
	}
	ui.Row(c.Key("nc-go")).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		if primaryButton(c.Key("nc-call"), label).Disabled(!ready || problem != "" || a.callBusy).Clicked() {
			a.callAsk = true
		}
		if a.callBusy {
			ui.Spinner(c)
		}
	})
	switch {
	case !ready:
		caption(c, i18n.T("Finish the setup steps above to place calls."))
	case problem != "":
		caption(c, problem)
	}
	if a.callErr != "" {
		ui.Text(c.Key("nc-err"), a.callErr).FontSize(px(t, fsCaption)).TextColor(t.Danger).Wrap()
	}
}

// callExample is an example task.
type callExample struct{ title, brief string }

func callExamples() []callExample {
	return []callExample{
		{i18n.T("Book a restaurant"), i18n.T("Book a table for two this Friday at 7 pm under the name Sam. If 7 is taken, anything between 6 and 8 is fine. Ask whether they have outdoor seating.")},
		{i18n.T("Reschedule an appointment"), i18n.T("I have an appointment on Tuesday at 10 am under the name Sam. Ask to move it to Thursday morning or Friday afternoon, and confirm the new time.")},
		{i18n.T("Ask opening hours"), i18n.T("Ask what their opening hours are this weekend and whether they are open on the public holiday on Monday.")},
	}
}

// callDialogs are the confirmation and the incoming-calls settings.
func (a *app) callDialogs(c *ui.Context) {
	t := c.Theme()
	if a.callAsk {
		f := &a.callForm
		num := telephony.NormalizeNumber(f.number)
		voice := strings.SplitN(f.voice, " · ", 2)[0]
		ui.Modal(c.Key("calls-confirm"), &a.callAsk, func() {
			ui.Column(c.Key("cc")).Width(px(t, 440)).Gap(px(t, 10)).Padding(px(t, 18)).Children(func() {
				sectionHeading(c, i18n.T("Call %s now?", num))
				ui.Text(c, i18n.T("AudioSmith will call as %s and follow this task for up to %d min:", voice, a.formMinutes())).Wrap()
				fillCard(c).Padding(px(t, sp3)).Children(func() {
					ui.Text(c, strings.TrimSpace(f.brief)).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Wrap().MaxLines(6).Ellipsis("…")
				})
				ui.Row(c).Gap(px(t, 8)).Children(func() {
					ui.Spacer(c)
					if secondaryButton(c.Key("cc-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
						a.callAsk = false
					}
					if primaryButton(c.Key("cc-call"), i18n.T("Call")).Clicked() {
						a.placeCall()
					}
				})
			})
		})
	}
	if a.callInOpen {
		ui.Modal(c.Key("calls-inbound-modal"), &a.callInOpen, func() {
			ui.Column(c.Key("ci")).Width(px(t, 460)).Gap(px(t, 10)).Padding(px(t, 18)).Children(func() {
				sectionHeading(c, i18n.T("Incoming calls"))
				caption(c, i18n.T("Choose what happens when someone calls your Twilio number.")).Wrap()
				segmentedFit(c.Key("ci-mode"), &a.callInMode, i18n.T("Greeting"), i18n.T("AI agent")).Label(i18n.T("When someone calls"))
				if a.callInMode == 0 {
					ui.Text(c, i18n.T("Play your greeting, then hang up.")).FontSize(px(t, 12)).Wrap()
				} else {
					ui.Text(c, i18n.T("Talk with the caller in your voice, following the brief below.")).FontSize(px(t, 12)).Wrap()
					ui.Field(c, i18n.T("What the agent should do"), func() {
						ui.TextArea(c.Key("ci-brief"), &a.callInBrief).Height(px(t, 80)).
							Placeholder(i18n.T("Take a message: ask who is calling and why, and when to call back.")).Label(i18n.T("What the agent should do"))
					})
				}
				caption(c, i18n.T("The voice, greeting and disclosure are set in the Twilio setup.")).Wrap()
				ui.Row(c).Gap(px(t, sp2)).Children(func() {
					if ghostButton(c.Key("ci-setup"), i18n.T("Open the setup")).Clicked() {
						a.callInOpen = false
						a.navigate("/calls/setup")
					}
					ui.Spacer(c)
					if secondaryButton(c.Key("ci-cancel"), i18n.T("Cancel")).Clicked() {
						a.callInOpen = false
					}
					if primaryButton(c.Key("ci-save"), i18n.T("Save")).Clicked() {
						a.saveInbound()
					}
				})
			})
		})
	}
}
