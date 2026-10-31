package main

import (
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// clonePage is /clone (the sidebar's Clone, also /voices/new): a voice
// from a clip.
func (a *app) clonePage(c *ui.Context) {
	t := c.Theme()
	s := &a.clone
	title := i18n.T("Clone a voice")
	if v := a.findVoice(s.replaceFor); v != nil {
		title = i18n.T("Replace the reference of “%s”", v.Name)
	}
	pageHeader(c, title, func() {
		if ghostButton(c.Key("clone-back"), i18n.T("Cancel")).Clicked() {
			a.micCancel(&s.mic)
			a.navigate("/voices")
		}
	})
	if s.busy != "" || s.mic.phase != "" {
		c.After(100 * time.Millisecond) // the timer, the level
	}
	page := ui.Scroll(c.Key("clone-scroll")).Grow(1).MinHeight(0)
	if files := page.DroppedFiles(); files != nil {
		a.cloneDropped(files)
	}
	page.Children(func() {
		ui.Column(c).Padding(px(t, sp1), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).MaxWidth(px(t, 780)).Children(func() {
			if !s.hasClip() {
				a.cloneSources(c)
			} else {
				a.cloneClip(c)
				a.cloneDetails(c)
				a.cloneSaveForm(c)
			}
		})
	})
}

// cloneSources are the two ways in: a file, or the microphone.
func (a *app) cloneSources(c *ui.Context) {
	t := c.Theme()
	s := &a.clone
	k := tok(t)
	caption(c, i18n.T("Use 5–15 seconds of clean speech: one speaker, no music, little room echo."))
	if s.busy != "" {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			ui.Text(c, s.busy)
		})
		return
	}
	ui.Row(c).Wrap().Gap(px(t, sp4)).AlignItems(ui.Stretch).Children(func() {
		// Upload zone.
		zone := ui.ButtonBase(c.Key("clone-upload")).Grow(1).Basis(px(t, 260)).MinHeight(px(t, 180)).Padding(px(t, sp4)).
			Radius(px(t, radiusCard)).Border(1, k.G[5]).BorderStyle(ui.BorderDashed).Label(i18n.T("Choose a file"))
		if files := zone.DroppedFiles(); files != nil {
			a.cloneDropped(files)
		}
		switch {
		case zone.FileDragOver():
			zone.Border(2, k.Accent).Background(k.AccentSoft)
		case zone.Hovered():
			zone.Background(k.Subtle)
		}
		if zone.Clicked() {
			a.chooseCloneFile()
		}
		zone.Children(func() {
			ui.Column(c).Grow(1).Center().Gap(px(t, sp2)).Children(func() {
				ui.Icon(c, icons.UploadMinimalistic.Linear).Size(px(t, 32), px(t, 32)).TextColor(k.Text3)
				ui.Text(c, i18n.T("Drop a clip here, or click to choose")).FontWeight(600).TextAlign(ui.Center)
				caption(c, i18n.T("WAV, MP3, M4A, FLAC, OGG, AAC — or a video")).TextAlign(ui.Center)
			})
		})
		// Record zone.
		ui.Column(c.Key("clone-record")).Grow(1).Basis(px(t, 260)).MinHeight(px(t, 180)).Padding(px(t, sp4)).Gap(px(t, sp2)).
			Radius(px(t, radiusCard)).Border(1, k.Line).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			a.micControls(c, &s.mic, func(path string) { a.cloneLoad(path, i18n.T("Recording"), true) })
		})
	})
	a.recordingTips(c)
	if s.err != "" {
		ui.Text(c, s.err).TextColor(t.Danger).FontSize(px(t, fsCaption)).Selectable()
	}
}

// micControls are a recorder's button, timer, level and device pickers.
func (a *app) micControls(c *ui.Context, m *micState, done func(path string)) {
	t := c.Theme()
	a.micEnded(m)
	switch m.phase {
	case "recording":
		secs := time.Since(m.started).Seconds()
		if recordButton(c.Key("mic-stop"), i18n.T("Stop recording")).Clicked() {
			a.micStop(m)
		}
		ui.Text(c, i18n.T("%.1f s", secs)).FontSize(px(t, fsTitle)).FontWeight(600)
		ui.Meter(c, float64(levelFraction(m.level)), 0, 1, nil).Width(px(t, 160)).Label(i18n.T("Input level"))
		if inputDetected(m.level) {
			statusMark(c, icons.CheckCircle, t.Success, i18n.T("Input detected"))
		} else {
			caption(c, i18n.T("No input detected"))
		}
	case "starting", "stopping":
		ui.Spinner(c)
		label := i18n.T("Starting…")
		if m.phase == "stopping" {
			label = i18n.T("Finishing…")
		}
		caption(c, label)
	default:
		if recordButton(c.Key("mic-start"), i18n.T("Record")).Clicked() {
			a.micStart(m, done)
		}
		caption(c, i18n.T("Your microphone is never played back.")).TextAlign(ui.Center)
	}
	busy := m.phase != ""
	ui.Row(c).Wrap().Justify(ui.Center).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		labels, ids := []string{i18n.T("System default")}, []string{""}
		for _, d := range a.clone.devices {
			labels, ids = append(labels, d.Name), append(ids, d.ID)
		}
		dev := labels[max(slices.Index(ids, m.device), 0)]
		if ui.Select(c.Key("mic-device"), &dev, labels).Label(i18n.T("Input device")).Disabled(busy).MaxWidth(px(t, 220)).Changed() {
			if i := slices.Index(labels, dev); i >= 0 {
				m.device = ids[i]
			}
		}
		ch := m.channels
		if segmented(c.Key("mic-channels"), &ch, i18n.T("Auto"), i18n.T("Mono"), i18n.T("Stereo")).Label(i18n.T("Channels")).Disabled(busy).Changed() {
			m.channels = ch
		}
	})
	if m.err != "" {
		ui.Text(c, m.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).TextAlign(ui.Center)
	} else if a.clone.devicesErr != "" && m.phase == "" {
		caption(c, a.clone.devicesErr).TextAlign(ui.Center)
	}
}

// cloneClip is the loaded clip: waveform, trimmer, length advice, its
// quality and cleaning.
func (a *app) cloneClip(c *ui.Context) {
	t := c.Theme()
	s := &a.clone
	card(c.Key("clone-clip")).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, s.srcName).FontWeight(600).SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Tooltip(s.srcName)
			caption(c, i18n.T("%.1f s", s.seconds)).SingleLine()
			if ghostButton(c.Key("clone-change"), i18n.T("Change clip")).Clicked() {
				a.pl.Stop()
				s.gen++
				s.work, s.original, s.peaks, s.err, s.busy = "", "", nil, "", ""
			}
		})
		a.trimWave(c, s)
		ui.RangeSlider(c.Key("clone-trim"), &s.selLo, &s.selHi, 0, s.seconds, 0.1).Label(i18n.T("Part kept"))
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Wrap().Children(func() {
			label, ic := i18n.T("Play the part kept"), icons.Play
			if a.pl.Playing("clone-sel") {
				label, ic = i18n.T("Stop"), icons.Stop
			}
			if iconButton(c.Key("clone-play"), ic, label).Clicked() {
				if a.pl.Playing("clone-sel") {
					a.pl.Stop()
				} else {
					a.cloneSelection(func(path string, _ float64, err error) {
						if err == nil {
							a.pl.Toggle("clone-sel", path)
						}
					})
				}
			}
			ui.Text(c, i18n.T("%.1f–%.1f s · %.1f s kept", s.selLo, s.selHi, s.sel().Len())).FontSize(px(t, fsCaption)).SingleLine().Grow(1)
			if ghostButton(c.Key("clone-best"), i18n.T("Best part")).Tooltip(i18n.T("Keep the best 10–14 seconds of speech")).Clicked() {
				w := refWindow(s.speech, s.seconds)
				s.selLo, s.selHi = w.Start, w.End
			}
			if ghostButton(c.Key("clone-all"), i18n.T("Keep all")).Clicked() {
				s.selLo, s.selHi = 0, s.seconds
			}
		})
		note, tone := refAdvice(s.sel().Len(), s.transcript != "")
		switch tone {
		case "warning":
			statusMark(c, icons.DangerTriangle, t.Warning, note).Tooltip(note)
		case "danger":
			statusMark(c, icons.DangerCircle, t.Danger, note).Tooltip(note)
		default:
			caption(c, note)
		}
		a.cloneCleanup(c) // clone_check.go: quality, cleaning, Undo
	})
}

// trimWave draws the clip's waveform with the part kept highlighted; a
// click plays from there.
func (a *app) trimWave(c *ui.Context, s *cloneState) {
	t := c.Theme()
	w := ui.ButtonBase(c.Key("clone-wave")).Height(px(t, 72)).Label(i18n.T("Waveform")).Tooltip(i18n.T("Click to listen from here"))
	if w.Clicked() {
		if x, _, ok := w.PointerPosition(); ok && s.work != "" {
			if a.pl.Current() != "clone" {
				a.pl.Toggle("clone", s.work)
			}
			a.pl.Seek("clone", x/max(w.Bounds().W, 1))
		}
	}
	peaks, total, lo, hi := s.peaks, s.seconds, s.selLo, s.selHi
	accent, muted := t.Accent, t.TextMuted.Alpha(0.45)
	pl := &a.pl
	w.Draw(func(p *ui.Painter, r ui.Rect) {
		if total <= 0 {
			return
		}
		x0, x1 := r.X+r.W*float32(lo/total), r.X+r.W*float32(hi/total)
		p.Fill(ui.Rect{X: x0, Y: r.Y, W: max(x1-x0, 1), H: r.H}, accent.Alpha(0.12), 4)
		n := len(peaks)
		if n == 0 {
			p.Line(r.X, r.Y+r.H/2, r.X+r.W, r.Y+r.H/2, 1, muted)
			return
		}
		step := r.W / float32(n)
		bar := max(step*0.6, 1)
		for i, v := range peaks {
			h := max(v*r.H, 2)
			x := r.X + float32(i)*step
			color := muted
			if x >= x0 && x <= x1 {
				color = accent
			}
			p.Fill(ui.Rect{X: x, Y: r.Y + (r.H-h)/2, W: bar, H: h}, color, bar/2)
		}
		if prog := pl.Progress("clone"); pl.Playing("clone") {
			x := r.X + r.W*prog
			p.Line(x, r.Y, x, r.Y+r.H, 1.5, accent)
			p.AnimationFrame()
		}
	})
}

// cloneDetails are the transcript, the style hint and the language.
func (a *app) cloneDetails(c *ui.Context) {
	t := c.Theme()
	s := &a.clone
	ui.Field(c, i18n.T("Transcript"), func() {
		ui.TextArea(c.Key("clone-transcript"), &s.transcript).Lines(2, 6).
			Placeholder(i18n.T("(Optional) What the clip says. Leave it empty to let the engine listen.")).Label(i18n.T("Transcript"))
	})
	ui.Row(c.Key("clone-asr")).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		switch s.asr {
		case "running":
			ui.Spinner(c)
			caption(c, i18n.T("Transcribing…"))
		case "noasr":
			ui.Text(c, i18n.T("No speech-to-text model is installed: transcribing needs one.")).FontSize(px(t, fsCaption)).TextColor(t.Warning).Grow(1).Basis(px(t, 200))
			if ghostButton(c.Key("asr-models"), i18n.T("Models")).Clicked() {
				a.navigate("/models")
			}
			if ghostIconButton(c.Key("asr-retry"), icons.Restart, i18n.T("Retry transcription")).Label(i18n.T("Retry transcription")).Clicked() {
				a.cloneTranscribe()
			}
		case "failed":
			statusMark(c, icons.DangerCircle, t.Danger, i18n.T("Transcription failed")).Grow(1).Tooltip(s.asrErr)
			if ghostIconButton(c.Key("asr-retry"), icons.Restart, i18n.T("Retry transcription")).Label(i18n.T("Retry transcription")).Clicked() {
				a.cloneTranscribe()
			}
		default:
			if ghostIconButton(c.Key("asr-run"), icons.Subtitles, i18n.T("Transcribe the part kept")).Label(i18n.T("Transcribe the part kept")).Clicked() {
				a.cloneTranscribe()
			}
		}
	})
	// Optional: a style hint and the language (detected otherwise).
	var changes []string
	if strings.TrimSpace(s.style) != "" {
		changes = append(changes, i18n.T("Style"))
	}
	if strings.TrimSpace(s.language) != "" {
		changes = append(changes, i18n.T("Language"))
	}
	a.advancedSection(c, "clone", changes, false, func() { s.style, s.language = "", "" }, func() {
		a.styleField(c, "clone-style", &s.style)
		ui.Field(c, i18n.T("Language"), func() {
			ui.Autocomplete(c.Key("clone-language"), &s.language, voiceLanguages()).Placeholder(i18n.T("Auto")).Label(i18n.T("Language"))
		})
	})
}

// styleField is a style hint input, with the presets' hints in a menu.
func (a *app) styleField(c *ui.Context, key string, style *string) {
	t := c.Theme()
	ui.Field(c, i18n.T("Style"), func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key(key), style).Placeholder(i18n.T("e.g. whisper, or calm and slow")).Label(i18n.T("Style")).Grow(1)
			loc := i18n.Locale()
			dropButton(c.Key(key+"-presets"), icons.Tuning2, i18n.T("Presets")).Shrink(0).Menu(func(m *ui.Menu) {
				for _, p := range gallery.Presets() {
					hint := p.StyleHint.In(loc)
					if hint != "" && m.Item(p.Name.In(loc)+" — "+hint).Checked(*style == hint).Chosen() {
						*style = hint
					}
				}
			}).Label(i18n.T("Style presets"))
		})
	})
}

// cloneSaveForm is the name and portrait, and what to do with the clip.
func (a *app) cloneSaveForm(c *ui.Context) {
	t := c.Theme()
	s := &a.clone
	problem := a.cloneProblem()
	ui.Box(c).Height(1).Background(tok(t).Line)
	if s.replaceFor != "" {
		banner(c, icons.InfoCircle, tok(t).Text2, i18n.T("Saving replaces this voice's reference. Its lock and its ownership verification are cleared, because the new clip may be another speaker."), nil)
		ui.Row(c).Gap(px(t, sp2)).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if problem != "" {
				caption(c, problem).Grow(1).MinWidth(0)
			}
			if primaryButton(c.Key("clone-replace"), i18n.T("Replace reference")).Disabled(problem != "" || s.saving).Clicked() {
				a.cloneSave()
			}
		})
		return
	}
	sectionHeading(c, i18n.T("Save as voice profile"))
	ui.Row(c).Gap(px(t, sp4)).AlignItems(ui.Center).Children(func() {
		a.portraitPicker(c, "clone-portrait", s.name, s.portraitBmp, a.clonePortrait, func() { s.portrait, s.portraitBmp = nil, nil })
		ui.Field(c, i18n.T("Profile name"), func() {
			ui.TextInput(c.Key("clone-name"), &s.name).Placeholder(i18n.T("e.g. My narrator voice")).Label(i18n.T("Profile name"))
		}).Grow(1)
	})
	ui.Row(c).Gap(px(t, sp2)).Justify(ui.End).AlignItems(ui.Center).Wrap().Children(func() {
		if problem != "" {
			caption(c, problem).Grow(1).Basis(px(t, 200))
		}
		if secondaryButton(c.Key("clone-once"), i18n.T("Use without saving")).Disabled(problem != "").
			Tooltip(i18n.T("Use the clip in the Studio once, without adding it to your voices")).Clicked() {
			a.cloneUseOnce()
		}
		if primaryButton(c.Key("clone-save"), i18n.T("Save voice")).Disabled(problem != "" || s.saving).Clicked() {
			a.cloneSave()
		}
	})
}

// portraitPicker is an avatar that takes a dropped or chosen image, with
// Remove when there is one.
func (a *app) portraitPicker(c *ui.Context, key, name string, bmp *ui.Bitmap, use func(path string), remove func()) {
	t := c.Theme()
	ui.Column(c.Key(key)).Gap(px(t, 4)).AlignItems(ui.Center).Children(func() {
		b := ui.ButtonBase(c.Key(key + "-pick")).Radius(px(t, 32)).Label(i18n.T("Choose a portrait")).
			Tooltip(i18n.T("Click or drop an image: PNG, JPG or WebP up to 5 MB"))
		if files := b.DroppedFiles(); files != nil {
			use(files[0])
		}
		if b.FileDragOver() {
			b.Border(2, tok(t).Accent)
		}
		if b.Clicked() {
			a.choosePortrait(use)
		}
		if name == "" {
			name = "?"
		}
		b.Children(func() {
			if bmp != nil {
				ui.Image(c, bmp).Size(px(t, 64), px(t, 64)).Fit(ui.Cover).Radius(px(t, 32))
			} else {
				ui.Column(c).Size(px(t, 64), px(t, 64)).Radius(px(t, 32)).Background(tok(t).Hover).Center().Children(func() {
					ui.Text(c, initials(name)).FontSize(px(t, fsTitle)).FontWeight(600).TextColor(tok(t).Text2)
				})
			}
		})
		if bmp != nil && ghostButton(c.Key(key+"-remove"), i18n.T("Remove")).FontSize(px(t, fsMicro)).Tooltip(i18n.T("Remove photo")).Clicked() {
			remove()
		}
	})
}

// initials are up to two letters of a name, for a portrait placeholder.
func initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		r := []rune(w)
		if len(r) == 0 {
			continue
		}
		out = append(out, unicode.ToUpper(r[0]))
		if len(out) == 2 || r[0] > 0x2E80 { // one CJK character is enough
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}
