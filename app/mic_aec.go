package main

// Echo cancellation (macOS voice processing) for every microphone
// recording (thread #153): the shared setting, its switch beside each
// input picker, the notice when the engine turns it on by itself, and the
// text of a recording that brought no sound.

import (
	"encoding/json"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// echoOn says whether recordings ask for echo cancellation.
func (a *app) echoOn() bool { return a.prefs.Recording.EchoCancellation }

// setEcho turns echo cancellation on or off for every recording.
func (a *app) setEcho(on bool) {
	a.updateSettings(func(s *settings.Settings) { s.Recording.EchoCancellation = on })
}

// echoHint says when to turn echo cancellation on, and what it costs.
func echoHint() string {
	return i18n.T("Turn on if the microphone records no sound or picks up the speakers; the voice may sound processed.")
}

// echoSettingHint is echoHint in Settings, where it applies to more than
// the page's own recordings.
func echoSettingHint() string {
	return i18n.T("Turn on if the microphone records no sound or picks up the speakers; the voice may sound processed. Applies to every recording.")
}

// echoSwitch is the setting's switch (key names it), off while busy.
func (a *app) echoSwitch(c *ui.Context, key string, busy bool) {
	on := a.echoOn()
	if ui.Switch(c.Key(key), &on).Label(i18n.T("Echo cancellation")).Tooltip(echoHint()).Disabled(busy).Changed() {
		a.setEcho(on)
	}
}

// echoControl is the setting as a checkbox with its hint beside a
// recorder's input picker (the clone recorder, the consent statement).
func (a *app) echoControl(c *ui.Context, key string, busy bool) {
	t := c.Theme()
	ui.Row(c.Key(key)).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		on := a.echoOn()
		if ui.Checkbox(c.Key("check"), &on, i18n.T("Echo cancellation")).Tooltip(echoHint()).Disabled(busy).Changed() {
			a.setEcho(on)
		}
		infoTip(c.Key("hint"), echoHint())
	})
}

// noteAutoEcho tells, once a recording (*seen holds its reason), that the
// engine turned echo cancellation on because the input brought no sound
// or kept stopping, and offers to keep it on for every recording; or that
// echo cancellation could not be used with this input (code review 1103
// F2/F3).
func (a *app) noteAutoEcho(l proto.LevelParams, seen *string) {
	if l.AECFallback != "" && *seen == "" {
		*seen = l.AECFallback
		a.toast(echoFallbackText(l.AECFallback))
		return
	}
	if !l.AECAuto || *seen != "" {
		return
	}
	*seen = firstOf(l.AECReason, proto.CaptureNoSound)
	title := i18n.T("The microphone brought no sound: echo cancellation is on for this recording.")
	if l.AECReason == proto.CaptureDeviceChanges {
		title = i18n.T("The microphone kept stopping as the audio devices changed: echo cancellation is on for this recording.")
	}
	if a.echoOn() {
		a.toast(title)
		return
	}
	a.toastAction(ui.Toast{
		ID: "echo-auto", Title: title, Description: i18n.T("Keep it on for every recording? You can change it next to the microphone."),
		Timeout: 10 * time.Second, Action: i18n.T("Keep it on"),
		OnAction: func() {
			a.setEcho(true)
			a.toast(i18n.T("Echo cancellation is on for every recording."))
		},
	})
}

// echoFallbackText says why a recording that asked for echo cancellation
// runs without it (proto.AECFallback*).
func echoFallbackText(why string) string {
	if why == proto.AECFallbackDevice {
		return i18n.T("Echo cancellation works with the system's default input only: this recording runs without it.")
	}
	return i18n.T("Echo cancellation does not work with this input: this recording runs without it.")
}

// noInputText explains a recording that brought no sound (no_input),
// with what to try.
func noInputText(e *jobs.Error) string {
	var d proto.NoInputData
	json.Unmarshal(e.Data, &d)
	name := firstOf(d.Name, i18n.T("the microphone"))
	what := i18n.T("No sound came from %s.", name)
	switch {
	case d.Reason == proto.CaptureDeviceChanges && d.AEC:
		what = i18n.T("%s kept stopping as the audio devices changed, even with echo cancellation.", name)
	case d.Reason == proto.CaptureDeviceChanges:
		what = i18n.T("%s kept stopping as the audio devices changed.", name)
	case d.AEC:
		what = i18n.T("No sound came from %s, even with echo cancellation.", name)
	}
	return i18n.T("%s Try: switch Echo cancellation on or off, choose another input, quit other apps that are recording, and check that AudioSmith may use the microphone (System Settings → Privacy & Security → Microphone).", what)
}

// micChannels are the file's channels for a recorder's choice (0 auto, 1
// mono, 2 stereo): one with echo cancellation, which gives one channel
// (code review 1103 F7).
func (a *app) micChannels(choice int) int {
	if a.echoOn() {
		return 1
	}
	return choice
}

// channelsHint says why the channel choice is off.
func channelsHint() string {
	return i18n.T("Echo cancellation records one channel: turn it off to record stereo.")
}

// noteRecordingEnd tells why a recording kept ended by itself: its
// device went away, or its input stopped for good; and when echo
// cancellation came on in a stereo recording, which then holds one
// channel twice.
func (a *app) noteRecordingEnd(res proto.CaptureResult) {
	if res.AECMono && res.Channels == 2 {
		a.toast(i18n.T("Echo cancellation came on during this recording, so both of its channels hold the same sound."))
	}
	switch res.Reason {
	case proto.CaptureDeviceLost:
		a.toast(i18n.T("The microphone went away: what was recorded is kept."))
	case proto.CaptureInputStopped:
		a.toast(i18n.T("The microphone stopped as the audio devices changed: what was recorded is kept."))
	}
}
