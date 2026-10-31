package main

// The Calls page's open call: its timeline, the live controls, the
// details, the transcript and what happened after; split from
// calls_view.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/telephony"
)

// callView shows the selected call: live controls while it runs, the
// summary after.
func (a *app) callView(c *ui.Context) {
	t := c.Theme()
	call := a.callFull[a.callSel]
	if call == nil {
		emptyState(c, icons.PhoneCallingRounded, i18n.T("Choose a call, or place a new one."), "", nil)
		return
	}
	ui.Row(c.Key("cv-head")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		dir := i18n.T("Outgoing")
		if call.Direction == services.CallInbound {
			dir = i18n.T("Incoming")
		}
		ui.Text(c, telephony.MaskNumber(call.Number)).FontSize(px(t, fsSection)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
		tagChip(c, dir)
		a.callBadge(c, call.Phase, call.Outcome)
		ui.Spacer(c)
		if call.Live() {
			if recordButton(c.Key("cv-hangup"), i18n.T("Hang up")).Clicked() {
				a.hangupCall(call.ID)
			}
		}
	})
	a.callTimeline(c, call)
	if call.Live() {
		a.callLiveControls(c, call)
	}
	if call.Error != "" {
		ui.Text(c.Key("cv-err"), call.Error).TextColor(t.Danger).Wrap()
	}
	a.callDetails(c, call)
	a.callTranscript(c, call)
	if !call.Live() {
		a.callAfter(c, call)
	}
}

// callTimeline is Dialing → Ringing → In call → Ended.
func (a *app) callTimeline(c *ui.Context, call *services.Call) {
	t := c.Theme()
	steps := []string{services.CallDialing, services.CallRinging, services.CallLive, services.CallEnded}
	cur := 0
	switch call.Phase {
	case services.CallRinging:
		cur = 1
	case services.CallLive:
		cur = 2
	case services.CallEnded, services.CallFailed:
		cur = 3
	}
	k := tok(t)
	ui.Row(c.Key("cv-steps")).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
		for i, s := range steps {
			if i > 0 {
				ui.Box(c).Width(px(t, 20)).Height(1).Background(k.Line)
			}
			label := callPhaseText(s)
			if i == 3 && call.Phase == services.CallFailed {
				label = callPhaseText(services.CallFailed)
			}
			ui.Row(c).Gap(px(t, 4)).AlignItems(ui.Center).Children(func() {
				col := k.Text3
				switch {
				case i < cur, i == cur && i == 3 && call.Phase != services.CallFailed:
					col = k.Text2
					ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 13), px(t, 13)).TextColor(t.Success)
				case i == cur && i == 3:
					col = t.Danger
					ui.Icon(c, icons.CloseCircle.Linear).Size(px(t, 13), px(t, 13)).TextColor(t.Danger)
				case i == cur:
					col = k.Text
					statusDot(c, k.Accent)
				default:
					statusDot(c, k.G[5])
				}
				tx := ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
				if i == cur {
					tx.FontWeight(600)
				}
			})
		}
	})
}

// callLiveControls: the agent's state, the clock, take-over and Say.
func (a *app) callLiveControls(c *ui.Context, call *services.Call) {
	t := c.Theme()
	fillCard(c.Key("cv-live")).Gap(px(t, sp2)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			if call.Phase == services.CallLive && call.State != "" {
				col := tok(t).Accent
				if call.State == telephony.StateSpeaking {
					col = t.Success
				}
				statusDot(c, col)
				ui.Text(c, callStateText(call.State)).FontWeight(600)
			}
			ui.Spacer(c)
			if !call.Answered.IsZero() {
				ui.Text(c, callClock(time.Since(call.Answered))+" / "+callClock(time.Duration(call.MaxMinutes)*time.Minute)).
					Font(monoFont).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2)
			}
		})
		if call.Phase != services.CallLive {
			return
		}
		on := call.Takeover
		settingRow(c, "cv-takeover", i18n.T("Take over"), i18n.T("Pause the agent and type what to say; the caller is still transcribed."), func() {
			if ui.Switch(c.Key("cv-takeover-switch"), &on).Label(i18n.T("Take over")).Changed() {
				a.takeoverCall(call.ID, on)
			}
		})
		if call.Takeover {
			ui.Row(c.Key("cv-say")).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
				in := ui.TextInput(c.Key("cv-say-text"), &a.callSay).Placeholder(i18n.T("What to say next")).Label(i18n.T("What to say next")).Grow(1).AutoFocus()
				if in.Submitted() || ui.Button(c.Key("cv-say-go"), i18n.T("Say")).Disabled(strings.TrimSpace(a.callSay) == "").Clicked() {
					a.sayInCall(call.ID)
				}
			})
		}
	})
}

// callDetails are the call's fields.
func (a *app) callDetails(c *ui.Context, call *services.Call) {
	t := c.Theme()
	row := func(k, label, value string) {
		if value == "" {
			return
		}
		ui.Row(c.Key("cvd-" + k)).Gap(px(t, sp3)).Children(func() {
			ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Width(px(t, 90)).Shrink(0)
			ui.Text(c, value).FontSize(px(t, fsCaption)).Wrap().Grow(1).MinWidth(0).Selectable()
		})
	}
	ui.Column(c.Key("cv-details")).Gap(px(t, 4)).Children(func() {
		voice := call.VoiceName
		if call.Model != "" {
			voice += " · " + modelName(call.Model)
		}
		row("voice", i18n.T("Voice"), voice)
		row("when", i18n.T("When"), i18n.DateTime(call.Created))
		if call.Duration > 0 {
			row("dur", i18n.T("Duration"), callClock(time.Duration(call.Duration*float64(time.Second))))
		}
		task := call.Brief
		if call.Mode == telephony.ModeGreeting {
			task = i18n.T("Greeting only")
		}
		row("brief", i18n.T("Task"), task)
		row("disc", i18n.T("Said first"), call.Disclosure)
	})
}

// callTranscript shows the conversation as bubbles.
func (a *app) callTranscript(c *ui.Context, call *services.Call) {
	t := c.Theme()
	k := tok(t)
	sectionTitle(c, i18n.T("Transcript"))
	if len(call.Transcript) == 0 {
		msg := i18n.T("No transcript for this call.")
		if call.Live() {
			msg = i18n.T("The transcript appears here once the call is answered.")
		}
		caption(c, msg)
		return
	}
	ui.Column(c.Key("cv-transcript")).Gap(px(t, 8)).Children(func() {
		for i, l := range call.Transcript {
			if l.Text == "" {
				continue
			}
			mine := l.Speaker != telephony.SpeakerCaller
			who := i18n.T("Them")
			bg := k.Subtle
			switch l.Speaker {
			case telephony.SpeakerAgent:
				who, bg = "AudioSmith", k.AccentSoft
			case telephony.SpeakerUser:
				who, bg = i18n.T("You"), k.Pressed
			}
			ui.Row(c.Key(i)).Children(func() {
				if mine {
					ui.Spacer(c)
				}
				ui.Column(c).Gap(px(t, 2)).Padding(px(t, sp2), px(t, sp3)).Radius(px(t, radiusCard)).Background(bg).MaxWidth(px(t, 460)).Shrink(1).MinWidth(0).Children(func() {
					head := who + " · " + callClock(l.At)
					ui.Text(c, head).FontSize(px(t, fsMicro)).TextColor(k.Text2)
					text := l.Text
					if l.Interrupted {
						text += " —"
					}
					tx := ui.Text(c, text).Wrap().Selectable()
					if l.Interrupted {
						tx.TextColor(k.Text2)
					}
				})
				if !mine {
					ui.Spacer(c)
				}
			})
		}
	})
}

// callAfter is the summary and the actions of an ended call.
func (a *app) callAfter(c *ui.Context, call *services.Call) {
	t := c.Theme()
	k := tok(t)
	card(c.Key("cv-after")).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			sectionHeading(c, i18n.T("Summary")).Grow(1)
			a.callBadge(c, call.Phase, call.Outcome)
		})
		if call.Summary != "" {
			ui.Text(c, call.Summary).Wrap().Selectable()
		} else {
			caption(c, i18n.T("The summary appears here when it is ready."))
		}
		if call.Note != "" && call.Note != call.Summary {
			caption(c, i18n.T("Agent's note: %s", call.Note)).Wrap()
		}
		if s := callLatencyText(call.Turns); s != "" {
			ui.Text(c, s).FontSize(px(t, fsMicro)).TextColor(k.Text3).Wrap()
		}
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			if call.Direction == services.CallOutbound {
				if secondaryIconButton(c.Key("cv-again"), icons.PhoneCallingRounded, i18n.T("Call again")).Label(i18n.T("Call again")).Clicked() {
					a.callAgain(call)
				}
			}
			if iconButton(c.Key("cv-copy"), icons.Copy, i18n.T("Copy summary")).Disabled(call.Summary == "").Clicked() {
				c.Services().WriteClipboard(call.Summary)
				a.toast(i18n.T("Copied"))
			}
			if p := a.callSvc.RecordingPath(*call); p != "" {
				label, ic := i18n.T("Play recording"), icons.Play
				if a.pl.Playing("call-rec:" + call.ID) {
					label, ic = i18n.T("Stop"), icons.Pause
				}
				if iconButton(c.Key("cv-rec"), ic, label).Clicked() {
					if err := a.pl.Toggle("call-rec:"+call.ID, p); err != nil {
						a.toastWarn(i18n.T("Could not play: %v", err))
					}
				}
			}
			ui.Spacer(c)
			if iconButton(c.Key("cv-delete"), icons.TrashBinMinimalistic, i18n.T("Delete")).Clicked() {
				a.deleteCall(call.ID)
			}
		})
	})
}

// callLatencyText sums up the turns' latency (for tuning).
func callLatencyText(turns []telephony.TurnLatency) string {
	if len(turns) == 0 {
		return ""
	}
	var total, stt, llm, tts time.Duration
	for _, x := range turns {
		total += x.Total
		stt += x.STT
		llm += x.LLMFirst
		tts += x.TTSFirst
	}
	n := time.Duration(len(turns))
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f s", (d / n).Seconds()) }
	return i18n.T("Average response %s (listening %s, thinking %s, voice %s) over %d turns", ms(total), ms(stt), ms(llm), ms(tts), len(turns))
}
