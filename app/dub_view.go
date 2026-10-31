package main

// The Dub page (/dub): a stepper (Source → Transcribe → Translate → Cast
// → Generate → Export) whose step shows its panel on the left, and the
// workspace on the right: banners (an interrupted run, errors with their
// fix, review checkpoints, stage warnings), the A/B player and video
// preview, the timeline lane, and the transcript editor. The footer says
// what runs and offers the step's next action.

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

var (
	dubIcon   = icons.VideoFramePlayHorizontal
	batchIcon = icons.LayersMinimalistic
)

// dubTargetLangs are the languages the pickers offer first (any BCP-47
// code can be typed too).
var dubTargetLangs = []string{"es", "es-MX", "fr", "fr-CA", "de", "it", "pt", "pt-BR", "zh-Hans", "zh-Hant", "yue", "ja", "ko",
	"en", "en-GB", "ru", "uk", "pl", "nl", "sv", "da", "nb", "fi", "cs", "el", "tr", "ar", "he", "fa", "hi", "bn", "ur", "ta", "te",
	"id", "ms", "vi", "th", "fil", "sw", "ro", "hu"}

// dubDialects are the dialect variants offered for a language.
var dubDialects = map[string][]string{
	"es": {"es-ES", "es-MX", "es-AR", "es-CO", "es-CL"}, "pt": {"pt-BR", "pt-PT"}, "en": {"en-US", "en-GB", "en-AU", "en-IN"},
	"fr": {"fr-FR", "fr-CA", "fr-BE"}, "de": {"de-DE", "de-AT", "de-CH"}, "ar": {"ar-EG", "ar-SA", "ar-MA"}, "nl": {"nl-NL", "nl-BE"},
}

// dubPage is /dub: the stepper, the workspace, and the step's panel on
// the right (a drawer when the window is narrow); the footer says what
// runs and holds the step's one primary action.
func (a *app) dubPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	a.dubShortcuts(c)
	if a.dubPlaying() {
		c.After(100 * time.Millisecond) // the playhead moves
		a.dubVideoSync(false)
	}
	title := i18n.T("Dubbing")
	if a.dub != nil && a.dub.Title != "" {
		title = i18n.T("Dubbing") + " · " + a.dub.Title
	}
	width := px(t, inspectorWidth+20)
	inline, shown := a.dubSide.layout(a, c, width, px(t, 480))
	w := a.pageWidth(c)
	if inline && shown {
		w -= width
	}
	a.dubNarrow = w < px(t, 560)
	a.dubWorkW = w
	// Narrow, before the transcript: the workspace has nothing to show
	// yet, so the step's panel takes its place.
	embed := a.dub != nil && !inline && len(a.dub.Segments) == 0
	if embed {
		shown = false
	}
	pageHeader(c, title, func() { a.dubHeaderActions(c, shown, embed) })
	if a.dub == nil {
		a.dubEmpty(c)
		a.dubConfirmDialog(c)
		return
	}
	a.dubStepper(c)
	ui.Box(c).Height(1).Background(k.Line).Shrink(0)
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		// The workspace fills the page; when the window is too short for
		// its notices, player, timeline and some lines, it scrolls.
		_, winH := c.Size()
		avail := winH - pageTop - px(t, 40) - px(t, 52) - 4
		// With lines, exactly the scroll's height once it was laid out, so
		// the list of lines (which scrolls itself, dub_editor.go) reaches
		// its foot; one more frame after the window's height changes.
		if b := a.dubWork.Bounds(c); b.H > 0 && len(a.dub.Segments) > 0 && !embed {
			avail = b.H
		}
		if winH != a.dubWorkWin {
			a.dubWorkWin = winH
			c.After(10 * time.Millisecond)
		}
		ui.Scroll(c.Key("dub-work")).Bind(&a.dubWork).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).MinHeight(avail).Padding(px(t, sp3), px(t, sp6), px(t, sp2)).Gap(px(t, sp2)).Children(func() {
				a.dubBanners(c)
				if embed {
					ui.Column(c.Key("dub-embed")).Gap(px(t, sp3)).MaxWidth(px(t, 520)).Children(func() {
						// What the source is (#106), above the step's panel, which
						// then leaves out its own card of it.
						a.dubCardAbove = a.dubStep <= dubStepTranscribe && (a.dub.Source.Path != "" || a.dub.Source.URL != "")
						if a.dubCardAbove {
							a.dubSourceCentre(c, true)
						}
						a.dubStepPanel(c)
						a.dubCardAbove = false
					})
					return
				}
				a.dubWorkspace(c)
			})
		}).ClipX()
		a.dubSide.view(c, "dub-side", i18n.T("Step settings"), inline, shown, false, width, func() {
			ui.Column(c).Padding(px(t, sp4), px(t, sp4), px(t, sp5)).Gap(px(t, sp3)).Bind(&a.dubSideFocus).Children(func() { a.dubStepPanel(c) })
		})
	})
	ui.Box(c).Height(1).Background(k.Line).Shrink(0)
	a.dubFooter(c)
	if a.dubGloss.open {
		ui.Modal(c.Key("dub-gloss-modal"), &a.dubGloss.open, func() { a.dubGlossaryPanel(c) })
	}
	if a.dubPaste.open {
		ui.Modal(c.Key("dub-paste-modal"), &a.dubPaste.open, func() { a.dubPastePanel(c) })
	}
	a.dubConfirmDialog(c)
}

// dubStepPanel is the panel of the step shown.
func (a *app) dubStepPanel(c *ui.Context) {
	switch a.dubStep {
	case dubStepSource:
		a.dubSourcePanel(c)
	case dubStepTranscribe:
		a.dubTranscribePanel(c)
	case dubStepTranslate:
		a.dubTranslatePanel(c)
	case dubStepCast:
		a.dubCastPanel(c)
	case dubStepGenerate:
		a.dubGeneratePanel(c)
	case dubStepExport:
		a.dubExportPanel(c)
	}
}

// dubHeaderActions: Projects ▾, New dub, the step panel's toggle, and ⋯
// (Create Story, Delete, Batch).
func (a *app) dubHeaderActions(c *ui.Context, shown, embed bool) {
	projects := i18n.T("Projects")
	var b ui.Element
	if a.dubNarrow {
		b = iconButton(c.Key("dub-projects"), icons.FolderWithFiles, projects)
	} else {
		b = dropButton(c.Key("dub-projects"), icons.FolderWithFiles, projects)
	}
	b.Menu(func(m *ui.Menu) {
		if len(a.dubList) == 0 {
			m.Item(i18n.T("No dubs yet")).Disabled(true)
		}
		for _, r := range a.dubList {
			id := r.ID
			label := firstOf(r.Title, i18n.T("Untitled dub")) + " · " + dubPhaseText(dub.Phase(r.Phase))
			if m.Item(label).Checked(a.dub != nil && a.dub.ID == id).Chosen() {
				a.openDub(id)
			}
		}
	})
	if a.dub != nil {
		newDub := i18n.T("New dub")
		if a.dubNarrow {
			b = iconButton(c.Key("dub-new"), icons.AddCircle, newDub)
		} else {
			b = ghostIconButton(c.Key("dub-new"), icons.AddCircle, newDub).Label(newDub)
		}
		if b.Clicked() {
			a.dubStopPlayer()
			a.dub = nil
			a.updateSettings(func(s *settings.Settings) { s.Dubbing.Open = "" })
			return
		}
	}
	if a.dub != nil && !embed {
		a.dubSide.toggle(c.Key("dub-panel"), icons.PanelRight, shown, i18n.T("Show the step settings"), i18n.T("Hide the step settings"))
	}
	moreMenu(c.Key("dub-more-actions"), "", func(m *ui.Menu) {
		if a.dub != nil {
			if m.Item(i18n.T("Create Story")).Disabled(len(a.dub.Segments) == 0).Chosen() {
				a.dubAskStory()
			}
			if m.Item(i18n.T("Delete…")).Chosen() {
				a.dubConfirm, a.dubConfirmOpen = "delete:"+a.dub.ID, true
			}
			m.Separator()
		}
		if m.Item(i18n.T("Batch dubbing")).Chosen() {
			a.navigate("/batch")
		}
	})
}

// dubEmpty is the page without a project: drop a file or paste a link.
func (a *app) dubEmpty(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Scroll(c.Key("dub-empty")).Grow(1).Children(func() {
		ui.Column(c).Padding(px(t, sp4), px(t, sp6), px(t, sp6)).Gap(px(t, sp4)).AlignItems(ui.Center).Children(func() {
			zone := ui.Column(c.Key("dub-drop")).FillWidth().MaxWidth(px(t, 560)).Padding(px(t, sp6)).Gap(px(t, sp3)).AlignItems(ui.Center).
				Radius(px(t, radiusCard)).Border(1, k.Line)
			if files := zone.DroppedFiles(); len(files) > 0 {
				a.dubDropped(files)
			}
			if zone.FileDragOver() {
				zone.Border(2, k.Accent)
			}
			zone.Children(func() {
				ui.Icon(c, dubIcon.Linear).Size(px(t, 36), px(t, 36)).TextColor(k.Text3)
				ui.Text(c, i18n.T("Video and audio dubbing")).FontSize(px(t, fsSection)).FontWeight(600).TextAlign(ui.Center)
				caption(c, i18n.T("Drop a video or audio file here")).TextAlign(ui.Center)
				ui.Text(c, "MP4 · MOV · M4V · MKV · WEBM · MP3 · WAV · M4A · FLAC").FontSize(px(t, fsMicro)).TextColor(k.Text3).TextAlign(ui.Center)
				if primaryButton(c.Key("dub-choose"), i18n.T("Choose file…")).Clicked() {
					a.chooseDubSource()
				}
				ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).FillWidth().MaxWidth(px(t, 480)).Children(func() {
					ui.TextInput(c.Key("dub-url"), &a.dubURL).Placeholder(i18n.T("…or paste a YouTube or video link")).Label(i18n.T("Video link")).Grow(1).MinWidth(0)
					if ui.Button(c.Key("dub-url-go"), i18n.T("Import")).Disabled(strings.TrimSpace(a.dubURL) == "").Clicked() {
						a.dubImportURL()
					}
				})
				ui.Column(c).FillWidth().MaxWidth(px(t, 480)).Children(func() {
					collapsible(c.Key("dub-url-more"), i18n.T("Link options"), &a.dubURLMore, nil, func() {
						ui.Checkbox(c.Key("dub-captions"), &a.dubCaptions, i18n.T("Use the video's captions when it has them (written by people)"))
						ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
							label := i18n.T("Sign-in cookies (cookies.txt)…")
							if a.dubCookies != "" {
								label = shortPath(a.dubCookies)
							}
							if ui.Button(c.Key("dub-cookies"), label).Shrink(1).MinWidth(0).Clicked() {
								a.dubChooseCookies()
							}
							if a.dubCookies != "" && iconButton(c.Key("dub-cookies-x"), icons.Close, i18n.T("Remove the cookies")).Clicked() {
								a.dubCookies = ""
							}
							infoTip(c.Key("dub-cookies-tip"), i18n.T("Cookies are used for this download only and never saved."))
						})
					})
				})
				if a.dubErr != "" {
					ui.Column(c).FillWidth().Children(func() {
						notice(c, "dub-url-err", noticeError, a.dubErr, "", func() {
							if a.dubErrCode == dub.CodeNoYtDlp && ui.Button(c.Key("dub-url-settings"), i18n.T("Open Settings → Dubbing")).Clicked() {
								a.navigate("/settings/dubbing")
							}
						}, nil)
					})
				}
			})
			ui.Row(c).Gap(px(t, sp2)).Wrap().Justify(ui.Center).Children(func() {
				for i, s := range []string{i18n.T("Upload & transcribe"), i18n.T("Translate"), i18n.T("Generate the dub")} {
					if i > 0 {
						iconMark(c, icons.AltArrowRight, k.Text3, "")
					}
					ui.Text(c, strconv.Itoa(i+1)+" · "+s).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				}
			})
			if len(a.dubList) > 0 {
				ui.Column(c.Key("dub-recent")).FillWidth().MaxWidth(px(t, 560)).Gap(px(t, 2)).Children(func() {
					sectionTitle(c, i18n.T("Recent dubs"))
					for _, r := range a.dubList[:min(len(a.dubList), 8)] {
						id := r.ID
						row := ui.Row(c.Key("recent-"+id)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl))
						if row.Hovered() {
							row.Background(k.Hover)
						}
						row.Children(func() {
							iconMark(c, dubIcon, k.Text2, "")
							ui.Column(c).Grow(1).MinWidth(0).Children(func() {
								truncText(c, firstOf(r.Title, i18n.T("Untitled dub")))
								meta := []string{dubPhaseText(dub.Phase(r.Phase))}
								if r.Duration > 0 {
									meta = append(meta, tlClock(r.Duration))
								}
								for _, l := range r.Languages {
									meta = append(meta, dubLangName(l))
								}
								truncText(c, strings.Join(meta, " · ")).FontSize(px(t, fsMicro)).TextColor(k.Text2)
							})
							if ghostButton(c.Key("open"), i18n.T("Open")).Clicked() {
								a.openDub(id)
							}
						})
					}
				})
			}
		})
	})
}

// --- the stepper ------------------------------------------------------------------------

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

// dubStepper is the row of steps: a quiet row where done steps show a
// check, the current one is on the pressed face, and the rest are grey.
// When the workspace is narrow only the current step shows its name.
func (a *app) dubStepper(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	names := []string{i18n.T("Source"), i18n.T("Transcribe"), i18n.T("Translate"), i18n.T("Cast"), i18n.T("Generate"), i18n.T("Export")}
	compact := a.dubNarrow || a.pageWidth(c) < px(t, 760)
	ui.Row(c.Key("dub-steps")).Gap(px(t, 2)).Padding(px(t, 2), px(t, sp5), px(t, sp2)).AlignItems(ui.Center).FocusGroup(ui.Horizontal).Shrink(0).ClipX().Children(func() {
		for i, name := range names {
			done, running, failed := a.dubStepState(i)
			cur := a.dubStep == i
			label := i18n.T("Step %d: %s", i+1, name)
			b := ghostBase(c.Key("step-"+strconv.Itoa(i))).Padding(px(t, 5), px(t, sp2)).Gap(px(t, 6)).AlignItems(ui.Center).Label(label).Tooltip(name).Shrink(0)
			if cur {
				b.Background(k.Pressed)
			}
			b.Children(func() {
				col := k.Text3
				switch {
				case cur:
					col = k.Text
				case done:
					col = k.Text2
				}
				ui.Box(c).Size(px(t, 16), px(t, 16)).Center().Shrink(0).Children(func() {
					switch {
					case running:
						ui.Spinner(c).Size(px(t, 14), px(t, 14))
					case failed:
						iconMark(c, icons.DangerCircle, k.Danger, "")
					case done:
						iconMark(c, icons.CheckCircle, k.AccentInk, "")
					default:
						ui.Text(c, strconv.Itoa(i+1)).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(col)
					}
				})
				if cur || !compact {
					txt := ui.Text(c, name).SingleLine().TextColor(col)
					if cur {
						txt.FontWeight(600)
					}
				}
			})
			if b.Clicked() {
				a.dubCommitEdit()
				a.dubStep = i
			}
			if i < len(names)-1 {
				ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text3).Shrink(0)
			}
		}
	})
}

// --- banners --------------------------------------------------------------------------------

// dubBanners: an interrupted run, the last error, review checkpoints and
// stage warnings, each a one-line notice.
func (a *app) dubBanners(c *ui.Context) {
	p := a.dub
	if a.dubInterrupted() {
		notice(c, "b-recover", noticeWarn, i18n.T("The last run stopped before it finished (the app quit, or a step failed)."), "", func() {
			if ui.Button(c.Key("b-resume"), i18n.T("Continue the run")).Clicked() {
				a.dubResume()
			}
		}, func() { a.dubSet(func(p *dub.Project) { p.Run = nil }) })
	}
	if a.dubErr != "" {
		notice(c, "b-error", noticeError, a.dubErr, "", func() {
			if a.dubNeed != nil {
				e := *a.dubNeed
				if secondaryIconButton(c.Key("b-download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Clicked() {
					a.download(e)
					a.dubErr, a.dubNeed = "", nil
				}
			}
			switch a.dubErrCode {
			case services.DubCodeNoTranslator:
				if ui.Button(c.Key("b-llm"), i18n.T("Set up writing help")).Clicked() {
					a.navigate("/settings/writing")
				}
			case dub.CodeNoYtDlp:
				if ui.Button(c.Key("b-ytdlp"), i18n.T("Open Settings → Dubbing")).Clicked() {
					a.navigate("/settings/dubbing")
				}
			}
		}, func() { a.dubErr, a.dubErrCode, a.dubNeed = "", "", nil })
	}
	busy := a.dubBusy()
	if p.Settings.Review && !busy {
		switch {
		case len(p.Segments) > 0 && len(p.Tracks) == 0 && !dubAnyTranslated(p) && !a.dubDismissed["transcript"]:
			notice(c, "b-transcript", noticeInfo, i18n.T("Transcript ready — fix recognition errors now: every translation starts from it."), "", func() {
				if ui.Button(c.Key("b-go-translate"), i18n.T("Translate →")).Clicked() {
					a.dubStep = dubStepTranslate
					a.dubTranslate(false)
				}
			}, func() { a.dubDismissed["transcript"] = true })
		case p.AllTranslated() && !p.TracksReady() && !a.dubDismissed["translated"]:
			notice(c, "b-translated", noticeInfo, i18n.T("Translations ready — skim them before generating."), "", func() {
				if ui.Button(c.Key("b-go-generate"), i18n.T("Generate dub →")).Clicked() {
					a.dubStep = dubStepGenerate
					a.dubGenerate()
				}
			}, func() { a.dubDismissed["translated"] = true })
		}
	}
	if p.TracksReady() && !busy && !a.dubDismissed["done"] {
		n := a.dubStaleCount(a.dubLang)
		text := i18n.T("Dub complete — review the timing; changed lines regenerate alone.")
		kind := noticeOK
		if n > 0 {
			text = i18n.N(n, "%d line changed since it was generated.", "%d lines changed since they were generated.", n)
			kind = noticeInfo
		}
		notice(c, "b-done", kind, text, "", nil, func() { a.dubDismissed["done"] = true })
	}
	// Stage warnings: the first, with all of them in its ⓘ; × dismisses
	// them all.
	var codes []string
	for _, k := range sortedKeys(p.Stages) {
		for _, w := range p.Stages[k].Warnings {
			if !slices.Contains(codes, w) && !a.dubDismissed["w:"+w] {
				codes = append(codes, w)
			}
		}
	}
	if len(codes) > 0 {
		text := dubWarningText(codes[0])
		more := ""
		if len(codes) > 1 {
			text = i18n.N(len(codes)-1, "%[2]s (+%[1]d more note)", "%[2]s (+%[1]d more notes)", len(codes)-1, text)
			var all []string
			for _, w := range codes {
				all = append(all, "• "+dubWarningText(w))
			}
			more = strings.Join(all, "\n")
		}
		notice(c, "b-warn", noticeWarn, text, more, nil, func() {
			for _, w := range codes {
				a.dubDismissed["w:"+w] = true
			}
		})
	}
}

// --- the workspace ---------------------------------------------------------------------------

// dubWorkspace: the player and video, the timeline, the editor.
func (a *app) dubWorkspace(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	if len(p.Segments) == 0 {
		// What the source is, then its video (#106); the footer says what runs.
		ui.Column(c.Key("dub-wait")).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(px(t, sp3)).Padding(px(t, sp2), 0).Children(func() {
			a.dubSourceCentre(c, false)
		})
		return
	}
	// The editor takes the keyboard where nothing in it does (a click
	// on a line, on empty room; once when it appears): on macOS the Edit
	// menu takes ⌘Z and ⇧⌘Z before the window sees them and sends its
	// undo and redo to the focused element (dubCommand). A text input in
	// it, as a line's text, undoes its own typing. The toolbar's and the
	// lines' buttons leave the focus where it is (KeepFocus).
	ed := ui.Column(c.Key("dub-ed")).Grow(1).Gap(px(t, sp2)).Focusable().FocusRing(false).Bind(&a.dubEdFocus).Label(i18n.T("Dub editor"))
	if !c.FocusWithin(a.dubSideFocus) {
		ed.AutoFocus()
	}
	ed.HandleInput(func(ev ui.InputEvent) bool {
		return ev.Kind == ui.InputCommand && a.dubCommand(ev.Text)
	}).Children(func() {
		a.dubPlayerBar(c)
		if p.Source.Duration > 0 {
			a.dubTimeline(c)
		}
		a.dubToolbar(c)
		a.dubLines(c)
	})
}
