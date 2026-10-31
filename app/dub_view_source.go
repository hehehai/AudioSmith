package main

// The Dub page's Source and Transcribe step panels; split from
// dub_view.go.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// dubPanelTitle is a panel's title.
func dubPanelTitle(c *ui.Context, title string) {
	sectionHeading(c, title)
}

// dubSourcePanel: the source, its language and speakers.
func (a *app) dubSourcePanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	locked := a.dubBusy()
	dubPanelTitle(c, i18n.T("Source"))
	if p.Source.Path == "" && p.Source.URL == "" {
		zone := ui.Column(c.Key("src-drop")).Padding(px(t, sp4)).Gap(px(t, sp2)).AlignItems(ui.Center).Radius(px(t, radiusCard)).Border(1, tok(t).Line)
		if files := zone.DroppedFiles(); len(files) > 0 {
			a.dubDropped(files)
		}
		zone.Children(func() {
			caption(c, i18n.T("Drop a video or audio file here")).TextAlign(ui.Center)
			if secondaryButton(c.Key("src-choose"), i18n.T("Choose file…")).Clicked() {
				a.chooseDubSource()
			}
		})
	} else if !a.dubCardAbove {
		fillCard(c.Key("src-info")).Gap(px(t, 4)).Padding(px(t, sp3)).Children(func() {
			name := p.Source.Name
			if name == "" {
				name = p.Source.URL
			}
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ic := icons.Soundwave
				if p.Source.HasVideo || p.Media.Audio == "" && p.Source.Meta != nil && p.Source.Meta.HasVideo() {
					ic = dubIcon
				}
				iconMark(c, ic, tok(t).Text2, "")
				ui.Text(c, name).FontWeight(600).MaxLines(2).Ellipsis("…").Grow(1).MinWidth(0).Tooltip(name)
				a.dubRemoveSourceButton(c, locked)
			})
			var meta []string
			if p.Source.Kind == dub.SourceURL {
				meta = append(meta, p.Source.URL)
			}
			if p.Source.Duration > 0 {
				meta = append(meta, tlClock(p.Source.Duration))
			}
			if v := p.Source.Video; v != nil {
				meta = append(meta, fmt.Sprintf("%d×%d %s", v.Width, v.Height, strings.ToUpper(v.Codec)))
			} else if p.Source.Path != "" && !p.Source.HasVideo && p.Media.Audio != "" {
				meta = append(meta, i18n.T("Audio only"))
			}
			if p.Source.Size > 0 {
				meta = append(meta, fmt.Sprintf("%.1f MB", float64(p.Source.Size)/1e6))
			}
			caption(c, strings.Join(meta, " · ")).MaxLines(2).Ellipsis("…")
			if p.Source.Kind == dub.SourceURL && p.Source.WantCaptions {
				caption(c, i18n.T("The video's captions are used when it has them.")).TextColor(tok(t).Text3)
			}
		})
	}
	// The spoken language.
	ui.Field(c, i18n.T("Spoken language"), func() {
		names := []string{i18n.T("Auto-detect")}
		ids := []string{""}
		for _, l := range dubTargetLangs {
			names = append(names, dubLangName(l))
			ids = append(ids, l)
		}
		if p.SourceLang != "" && !slices.Contains(ids, p.SourceLang) {
			names, ids = append(names, dubLangName(p.SourceLang)), append(ids, p.SourceLang)
		}
		cur := names[max(slices.Index(ids, p.SourceLang), 0)]
		if ui.Select(c.Key("src-lang"), &cur, names).Label(i18n.T("Spoken language")).Disabled(locked).Changed() {
			l := ids[slices.Index(names, cur)]
			a.dubSet(func(p *dub.Project) {
				p.SourceLang = l
				if len(p.Segments) == 0 {
					p.AutoRetarget() // the transcript does it otherwise
				}
			})
			if !slices.Contains(a.dub.Languages(), a.dubLang) {
				a.dubLang = a.dub.Primary()
			}
		}
	})
	if p.Detected != "" && p.SourceLang == "" {
		caption(c, i18n.T("Detected: %s", dubLangName(p.Detected)))
	}
	if note := dubRetargetNote(p); note != "" {
		caption(c, note)
	}
	if len(p.Segments) == 0 {
		a.dubSpeakersHint(c, "src-speakers-model")
	}
	a.dubAdvanced(c, dubStepSource, locked, func() {
		info := i18n.T("How many people speak? Leave it on Auto to detect them; the speaker model tells up to 4 apart.")
		if ready, _ := a.modelOnDisk(dubSpeakersEntry()); ready {
			info = i18n.T("How many people speak? Leave it on Auto to detect them; 1 skips telling speakers apart.")
		}
		fieldInfo(c, i18n.T("Speakers"), info, func() {
			names := []string{i18n.T("Auto-detect")}
			for i := 1; i <= dub.MaxSpeakers; i++ {
				names = append(names, strconv.Itoa(i))
			}
			cur := names[min(max(p.Speakers, 0), dub.MaxSpeakers)]
			if ui.Select(c.Key("src-speakers"), &cur, names).Label(i18n.T("Speakers")).Disabled(locked).Changed() {
				n := slices.Index(names, cur)
				a.dubSet(func(p *dub.Project) { p.Speakers = n })
			}
		})
		review := p.Settings.Review
		if dubSwitch(c, "src-review", i18n.T("Review after each step"), i18n.T("Stops after the transcript and after the translation, so they can be checked first."), &review, false) {
			a.dubSettings(func(s *dub.Settings) { s.Review = review })
		}
		if ghostIconButton(c.Key("src-import"), icons.Import, i18n.T("Import subtitles (SRT/VTT)…")).Label(i18n.T("Import subtitles (SRT/VTT)…")).Disabled(locked || p.Source.Path == "" && p.Source.URL == "").
			Tooltip(i18n.T("Use a subtitle file as the transcript instead of recognising speech")).Clicked() {
			a.dubImportSubtitles()
		}
	})
}

// dubRemoveSourceButton is the source's 🗑 (it asks first when there is
// a transcript to lose).
func (a *app) dubRemoveSourceButton(c *ui.Context, locked bool) {
	if iconButton(c.Key("src-remove"), icons.TrashBinMinimalistic, i18n.T("Remove source")).Disabled(locked).Clicked() {
		if len(a.dub.Segments) > 0 {
			a.dubConfirm, a.dubConfirmOpen = "remove", true
		} else {
			a.dubRemoveSource()
		}
	}
}

// dubTranscribePanel: the preparation stages, speakers and their names.
func (a *app) dubTranscribePanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	dubPanelTitle(c, i18n.T("Transcribe"))
	for _, st := range []dub.Stage{dub.StageIngest, dub.StageSeparate, dub.StageTranscribe, dub.StageReferences} {
		s := p.Stage(st, "")
		ui.Row(c.Key("stage-" + string(st))).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(px(t, 14), px(t, 14)).Center().Shrink(0).Children(func() {
				switch s.Status {
				case dub.StatusDone:
					iconMark(c, icons.CheckCircle, t.Success, "")
				case dub.StatusSkipped:
					iconMark(c, icons.DangerTriangle, t.Warning, "")
				case dub.StatusFailed:
					iconMark(c, icons.DangerCircle, t.Danger, "")
				case dub.StatusRunning:
					ui.Spinner(c).Size(px(t, 12), px(t, 12))
				default:
					statusDot(c, tok(t).G[5])
				}
			})
			ui.Text(c, dubStageTitle(st)).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0)
		})
		if s.Status == dub.StatusFailed && s.Message != "" {
			caption(c, s.Message).TextColor(t.Danger).Padding(0, 0, 0, px(t, 22))
		}
	}
	a.dubSpeakersHint(c, "tr-speakers-model")
	a.dubRetranscribeHint(c, "tr-speakers-again")
	if len(p.Segments) > 0 {
		sectionTitle(c, i18n.T("Speakers"))
		for _, sp := range p.SpeakerLabels() {
			name := p.SpeakerName(sp)
			n := 0
			for _, s := range p.Segments {
				if s.Speaker == sp {
					n++
				}
			}
			ui.Row(c.Key("spk-" + sp)).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, sp).FontSize(px(t, fsMicro)).TextColor(tok(t).Text3).Width(px(t, 22)).Shrink(0)
				in := ui.TextInput(c.Key("name"), &name).Label(i18n.T("Name of %s", sp)).Grow(1).MinWidth(0)
				if in.Changed() {
					a.dubRenameSpeaker(sp, name)
				}
				tagChip(c, i18n.N(n, "%d line", "%d lines", n))
			})
		}
	}
}

// dubStageTitle names a stage in the Transcribe panel.
func dubStageTitle(s dub.Stage) string {
	switch s {
	case dub.StageIngest:
		return i18n.T("Read the source")
	case dub.StageSeparate:
		return i18n.T("Separate voices and background")
	case dub.StageTranscribe:
		return i18n.T("Recognise speech and speakers")
	case dub.StageReferences:
		return i18n.T("Pick each speaker's voice sample")
	}
	return string(s)
}
