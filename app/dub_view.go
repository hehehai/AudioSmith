package main

// The Dub page (/dub): the steps in its header (dub_steps.go: Source → Transcribe → Translate → Cast
// → Generate → Export) whose step shows its panel on the left, and the
// workspace on the right: banners (an interrupted run, errors with their
// fix, review checkpoints, stage warnings), the A/B player and video
// preview, the timeline lane, and the transcript editor. The footer says
// what runs and offers the step's next action.

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
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

// dubPage is /dub: the steps in the header, the workspace, and the step's panel on
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
	width := px(t, inspectorWidth+20)
	inline, shown := a.dubSide.layout(a, c, width, px(t, 480))
	w := a.pageWidth(c)
	if inline && shown {
		w -= laneWidth(t, width)
	}
	a.dubNarrow = w < px(t, 560)
	a.dubWorkW = w
	// Narrow, before the transcript: the workspace has nothing to show
	// yet, so the step's panel takes its place.
	embed := a.dub != nil && !inline && len(a.dub.Segments) == 0
	if embed {
		shown = false
	}
	// The header names the dub under its list (视频配音 / title; ‹ and the
	// list's name lead back to it) and holds the editor's actions.
	pageHeaderBar(c, pageHead{title: a.dubTitleOf(a.dub.ID), parent: pageTitle("/dub"), up: "/dub",
		inline: func() { a.dubSteps(c) }, tools: func(bool) { a.dubHeaderActions(c, shown, embed) }})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		// The workspace fills the page; when the window is too short for
		// its notices, player, timeline and some lines, it scrolls.
		_, winH := c.Size()
		avail := winH - pageTop - px(t, 52) - 4
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
			ui.Column(c).MinHeight(avail).Padding(bodyTop(t), gutterRBeside(t, width, inline && shown), px(t, sp2), gutterL(t)).Gap(px(t, sp2)).Children(func() {
				a.dubBanners(c)
				if embed {
					ui.Column(c.Key("dub-embed")).Gap(px(t, sp3)).Children(func() {
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
		a.dubSide.view(a, c, "dub-side", i18n.T("Step settings"), inline, shown, width, func() {
			ui.Column(c).Padding(px(t, panelPad)).Gap(px(t, sp3)).Bind(&a.dubSideFocus).Children(func() { a.dubStepPanel(c) })
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

// dubHeaderActions: the step panel's toggle, and ⋯ (Create Story,
// Delete, Batch).
func (a *app) dubHeaderActions(c *ui.Context, shown, embed bool) {
	if a.dub != nil && !embed {
		a.dubSide.toggle(c.Key("dub-panel"), icons.PanelRight, shown, i18n.T("Show the step settings"), i18n.T("Hide the step settings"))
	}
	moreMenu(c.Key("dub-more-actions"), "", func(m *ui.Menu) {
		if a.dub != nil {
			if m.Item(i18n.T("Create Story")).Disabled(len(a.dub.Segments) == 0).Chosen() {
				a.dubAskStory()
			}
			if m.Item(i18n.T("Delete")).Chosen() {
				a.dubConfirm, a.dubConfirmOpen = "delete:"+a.dub.ID, true
			}
			m.Separator()
		}
		if m.Item(i18n.T("Batch dubbing")).Chosen() {
			a.navigate("/batch")
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
		a.dubErrorNotice(c) // dub_list.go
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
