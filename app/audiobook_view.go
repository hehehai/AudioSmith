package main

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/markup"
)

// audiobookPage is /audiobook: the manuscript and its render in the main
// column, the book's settings in the inspector (beside it, or a drawer
// when the window is narrow).
func (a *app) audiobookPage(c *ui.Context) {
	t := c.Theme()
	p := a.lfBook
	a.syncManuscript(p)
	a.lfToasts(c)
	locked := a.lfLocked(p)
	inline, shown := a.lfLayout(c, p)
	n := len(p.spec.Chapters)
	pageHeaderBar(c, pageHead{title: i18n.T("Audiobook"), count: i18n.N(n, "%d chapter", "%d chapters", n), tools: func(bool) {
		a.bookHeader(c, p, locked, shown)
	}})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Padding(px(t, sp1), px(t, sp6), px(t, sp4)).Gap(px(t, sp3)).Children(func() {
			a.manuscriptToolbar(c, p, locked)
			a.manuscriptView(c, p, locked)
			a.manuscriptWarnings(c, p)
			a.lfRenderPanel(c, p)
		})
		a.lfInspector(c, p, inline, shown, func() {
			a.lfSection(c, p, "setup", i18n.T("Setup"), entryFor(p.doc.Model).DisplayName(), func() { a.lfSetup(c, p, locked) })
			a.lfSection(c, p, "cast", i18n.T("Cast"), lfCount(len(bookCast(p))), func() { a.bookCastCard(c, p, locked) })
			if len(p.blocks) > 0 { // audiobook_dialogue_view.go
				a.lfSection(c, p, "dialogue", i18n.T("Dialogue passages"), lfCount(len(p.blocks)), func() { a.bookDialogueCard(c, p, locked) })
			}
			a.lfSection(c, p, "plan", i18n.T("Chapters"), lfCount(len(p.spec.Chapters)), func() { a.chapterPlan(c, p) })
			a.lfSection(c, p, "book", i18n.T("Cover & details"), "", func() { a.lfBookCard(c, p, locked) })
			a.lfAdvanced(c, p, locked) // longform_advanced.go: production, stems, pronunciations
		})
	})
	a.lfConfirm(c, p)
	a.syncManuscript(p)
	a.autosaveLf(p)
}

// bookHeader is the Audiobook page's header: Import, the inspector
// toggle, and ⋯ (the sample, Clear, Stories).
func (a *app) bookHeader(c *ui.Context, p *lfPage, locked, shown bool) {
	imp := i18n.T("Import…")
	var b ui.Element
	if p.narrow {
		b = iconButton(c.Key("ab-import"), icons.Import, imp)
	} else {
		b = ghostIconButton(c.Key("ab-import"), icons.Import, imp).Label(imp)
	}
	if b.Tooltip(i18n.T("TXT, Markdown, EPUB or DOCX, up to 64 MB, split into chapters")).Disabled(locked).Clicked() {
		a.importBook(p)
	}
	a.lfProjectMenu(c, p, locked)
	p.panel.toggle(c.Key("ab-panel"), icons.PanelRight, shown, i18n.T("Show the inspector"), i18n.T("Hide the inspector"))
	moreMenu(c.Key("ab-more"), "", func(m *ui.Menu) {
		if m.Item(i18n.T("Load sample")).Disabled(locked).Chosen() {
			a.loadSample(p)
		}
		if m.Item(i18n.T("Clear…")).Disabled(locked || strings.TrimSpace(p.doc.Script) == "").Chosen() {
			p.confirm, p.confirmOpen = "clear", true
		}
		m.Separator()
		if m.Item(i18n.T("Go to Stories")).Chosen() {
			a.navigate("/stories")
		}
	})
}

// lfLocked reports whether a page's inputs are locked: its render runs,
// or it imports.
func (a *app) lfLocked(p *lfPage) bool {
	if p.importing {
		return true
	}
	r := a.lfRenders[p.render]
	return r != nil && r.rec.State == core.RenderRunning
}

// lfToasts shows the pending toasts with Undo.
func (a *app) lfToasts(c *ui.Context) {
	for _, u := range a.lfUndo {
		c.ToastAction(u.msg, i18n.T("Undo"), u.undo)
	}
	a.lfUndo = nil
}

// manuscriptToolbar: Insert ▾ (pauses, voices, delivery), Reactions ▾,
// and the sample while the manuscript is empty.
func (a *app) manuscriptToolbar(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		drop := func(c *ui.Context, ic icons.Icon, label string) ui.Element {
			if p.narrow {
				return iconButton(c, ic, label)
			}
			return dropButton(c, ic, label).Shrink(0)
		}
		drop(c.Key("mk-insert"), icons.AddCircle, i18n.T("Insert")).Tooltip(i18n.T("Insert a pause, a voice switch or a delivery change")).Disabled(locked).Menu(func(m *ui.Menu) {
			if m.Item(i18n.X("markup", "Pause")).Chosen() {
				a.insertMarkup(p, "[pause 500ms]")
			}
			if m.Item(i18n.T("Voice")).Chosen() {
				a.insertVoice(p)
			}
			if m.Item(i18n.T("Dialogue passage")).Chosen() {
				a.insertDialogue(p) // audiobook_dialogue.go
			}
			m.Separator()
			for _, s := range []struct{ label, tag string }{
				{i18n.T("Slow"), markup.Slow}, {i18n.T("Fast"), markup.Fast},
				{i18n.T("Emphasis"), markup.Emphasis}, {i18n.T("Spell"), markup.Spell},
			} {
				if m.Item(s.label).Chosen() {
					a.wrapMarkup(p, "["+s.tag+"]", "[/"+s.tag+"]")
				}
			}
		})
		drop(c.Key("mk-reactions"), icons.SmileCircle, i18n.T("Reactions")).Disabled(locked).Menu(func(m *ui.Menu) {
			for _, tag := range markup.DefaultNonVerbal {
				if m.Item(nonVerbalLabel(tag) + "  [" + tag + "]").Chosen() {
					a.insertMarkup(p, "["+tag+"]")
				}
			}
		})
		ui.Spacer(c)
		if strings.TrimSpace(p.doc.Script) == "" {
			if ghostButton(c.Key("ab-sample"), i18n.T("Load sample")).Tooltip(i18n.T("A short book that shows chapters, voices, pauses and reactions")).Disabled(locked).Clicked() {
				a.loadSample(p)
			}
			return
		}
		a.manuscriptStats(c, p)
	})
}

// manuscriptView is the editor.
func (a *app) manuscriptView(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	st := p.ed
	st.Font = ui.Font{Family: "system-ui", Size: px(t, 15)}
	st.Placeholder = i18n.T("# Chapter One\n\nOnce upon a time… [pause 500ms] the end.\n\n# Chapter Two\n\n[voice:Mara] Hello!")
	st.Label = i18n.T("Manuscript")
	st.ReadOnly = locked
	st.Theme = manuscriptTheme(t)
	box := ui.Column(c.Key("ms-box")).Grow(1).MinHeight(px(t, 140)).Radius(px(t, radiusCard)).Border(1, tok(t).Line).Background(editor.DefaultTheme(t).Background).
		Padding(px(t, sp2), px(t, sp1))
	if box.FocusWithin() {
		box.Border(1, tok(t).G[5])
	}
	if files := box.DroppedFiles(); len(files) > 0 && !locked {
		a.importBookFrom(p, files[0])
	}
	box.Children(func() {
		if p.importing {
			ui.Row(c).Gap(px(t, 6)).Padding(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				caption(c, i18n.T("Importing…"))
			})
		}
		a.manuscriptDeco(c, p) // audiobook_dialogue_editor.go
		editor.View(c.Key("ms"), st).Grow(1).MinHeight(0)
	})
}

// manuscriptTheme colours the manuscript like the Studio's script
// (scriptTheme), with chapter headings in the text colour, semibold.
func manuscriptTheme(t *ui.Theme) *editor.Theme {
	th := scriptTheme(t)
	th.Tokens[editor.TokenHeading].Color = tok(t).Text
	th.Tokens[editor.TokenHeading].Bold = true
	// Dialogue passages (audiobook_dialogue_editor.go).
	th.Tokens[tokenMarker] = editor.TokenStyle{Color: tok(t).AccentInk, Bold: true}
	th.Tokens[tokenDirection] = editor.TokenStyle{Color: tok(t).Text3, Italic: true}
	for i, col := range speakerColors(t) {
		th.Tokens[speakerKinds[i]] = editor.TokenStyle{Color: col, Bold: true}
	}
	return th
}

// manuscriptStats: chapters · words · estimated runtime.
func (a *app) manuscriptStats(c *ui.Context, p *lfPage) {
	st := p.stats
	parts := []string{i18n.N(max(st.Chapters, 1), "%d chapter", "%d chapters", max(st.Chapters, 1))}
	if st.Words > 0 {
		parts = append(parts, i18n.N(st.Words, "%d word", "%d words", st.Words))
	}
	if st.Chars > 0 {
		parts = append(parts, i18n.N(st.Chars, "%d character", "%d characters", st.Chars))
	}
	if n := len(p.blocks); n > 0 {
		parts = append(parts, i18n.N(n, "%d dialogue passage", "%d dialogue passages", n))
	}
	parts = append(parts, i18n.T("%s est. runtime", clockRuntime(st.Seconds)))
	s := strings.Join(parts, " · ")
	caption(c.Key("ab-stats"), s).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(s)
}

// clockRuntime is M:SS, or H:MM from an hour.
func clockRuntime(seconds float64) string {
	s := int(seconds + 0.5)
	if s >= 3600 {
		return strconv.Itoa(s/3600) + ":" + twoDigits(s/60%60)
	}
	return strconv.Itoa(s/60) + ":" + twoDigits(s%60)
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// manuscriptWarnings is "Before you create": unknown voices, empty
// chapters, tags read aloud. Dismissed until the next edit.
func (a *app) manuscriptWarnings(c *ui.Context, p *lfPage) {
	t := c.Theme()
	var warn []services.Problem
	for _, pr := range p.problems {
		switch pr.Code {
		case services.ProblemUnknownVoice, services.ProblemEmptyChapter, services.ProblemUnknownTag, services.ProblemMissingVoice,
			services.ProblemDialogueUnclosed, services.ProblemDialogueNested, services.ProblemDialogueStrayClose, services.ProblemDialogueEmpty,
			services.ProblemDialogueUnknown:
			warn = append(warn, pr)
		}
	}
	if len(warn) == 0 || p.dismissed == p.fp || a.lfLocked(p) {
		return
	}
	k := tok(t)
	card(c.Key("ab-warn")).Padding(px(t, sp2), px(t, sp3)).Gap(px(t, 2)).Shrink(0).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			iconMark(c, icons.DangerTriangle, t.Warning, "")
			ui.Text(c, i18n.T("Before you create")).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if iconButton(c.Key("ab-warn-x"), icons.Close, i18n.T("Dismiss")).Clicked() {
				p.dismissed = p.fp
			}
		})
		ui.Scroll(c.Key("ab-warn-list")).MaxHeight(px(t, 96)).Children(func() {
			ui.Column(c).Gap(0).Padding(0, 0, 0, px(t, 22)).Children(func() {
				for i, pr := range warn {
					msg := problemText(pr)
					if pr.Span.End > pr.Span.Start {
						start, end := pr.Span.Start, pr.Span.End
						b := ui.ButtonBase(c.Key("w-"+strconv.Itoa(i))).Label(msg).Tooltip(i18n.T("Show in the manuscript")).Padding(px(t, 2), px(t, 6)).Radius(px(t, 6))
						if b.Hovered() {
							b.Background(k.Hover)
						}
						if b.Children(func() {
							caption(c, msg).Grow(1).MinWidth(0)
						}).Clicked() {
							p.ed.Select(start, end)
						}
						continue
					}
					caption(c, msg).Padding(px(t, 2), px(t, 6))
				}
			})
		})
	})
}

// bookCastCard maps every [voice:NAME] of the manuscript to a voice.
func (a *app) bookCastCard(c *ui.Context, p *lfPage, locked bool) {
	names := bookCast(p)
	if len(names) == 0 {
		caption(c, i18n.T("Add [voice:NAME] tags to the manuscript to cast more voices; the narrator reads the rest."))
		return
	}
	if p.doc.VoiceCast == nil {
		p.doc.VoiceCast = map[string]string{}
	}
	e := entryFor(p.doc.Model)
	inPassage := services.DialogueNames(p.doc.Script)
	for _, n := range names {
		ref := p.doc.VoiceCast[n]
		label, none := n, i18n.T("Narrator voice")
		if slices.Contains(inPassage, n) { // a speaker of a dialogue passage
			label, none = n+" · "+i18n.X("cast mark", "dialogue"), i18n.T("Its own voice, given at generation")
		}
		ui.Field(c, label, func() {
			a.lfVoiceSelect(c, "ab-cast-"+n, &ref, e, none, locked)
		})
		if ref != p.doc.VoiceCast[n] {
			p.doc.VoiceCast[n] = ref
		}
	}
}

// chapterPlan lists the chapters as they will render (title · characters)
// with an audition each.
func (a *app) chapterPlan(c *ui.Context, p *lfPage) {
	t := c.Theme()
	k := tok(t)
	if len(p.spec.Chapters) == 0 {
		caption(c, i18n.T("Write or import a manuscript to see its chapters."))
		return
	}
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		caption(c, i18n.T("Preview a chapter")).Grow(1).MinWidth(0)
		infoTip(c.Key("plan-tip"), i18n.T("Listen to one chapter with every setting before making the whole book; it warms the cache."))
	})
	ui.Scroll(c.Key("plan")).MaxHeight(px(t, 260)).Children(func() {
		ui.Column(c).Gap(px(t, 2)).Children(func() {
			for i, ch := range p.spec.Chapters {
				title := ch.Title
				if title == "" {
					title = i18n.T("Chapter %d", i+1)
				}
				chars := 0
				for _, l := range ch.Lines {
					for _, s := range l.Spans {
						chars += text.RuneLen(s.Text)
					}
				}
				what := "chapter:" + strconv.Itoa(i)
				ui.Row(c.Key("plan-" + strconv.Itoa(i))).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						truncText(c, title)
						ui.Text(c, i18n.N(chars, "%d character", "%d characters", chars)).FontSize(px(t, fsMicro)).TextColor(k.Text3)
					})
					a.auditionButton(c, p, what, i18n.T("Preview chapter: %s", title))
				})
			}
		})
	})
}

// auditionButton renders, plays or stops an audition: headphones to
// render one, then play / pause.
func (a *app) auditionButton(c *ui.Context, p *lfPage, what, label string) {
	t := c.Theme()
	st, r := a.auditionState(p, what)
	switch st {
	case "running":
		ui.Box(c).Size(px(t, 28), px(t, 28)).Center().Shrink(0).Children(func() {
			ui.Spinner(c).Label(i18n.T("Rendering the preview…"))
		})
	case "done":
		if playToggle(c.Key("aud-"+what), a.pl.Playing("audition:"+r.rec.ID), label, label).Clicked() {
			a.playAudition(r)
		}
	default:
		if iconButton(c.Key("aud-"+what), icons.HeadphonesRound, label).Disabled(a.engState != engineReady).Clicked() {
			a.startLf(p, what)
		}
	}
}

// lexiconCard is the book's pronunciations: word → say it as.
func (a *app) lexiconCard(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	dups := services.DuplicateWords(p.doc.Lexicon)
	remove := -1
	for i := range p.doc.Lexicon {
		e := &p.doc.Lexicon[i]
		ui.Row(c.Key("lex-" + strconv.Itoa(i))).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("w"), &e.Word).Placeholder(i18n.T("Word")).Label(i18n.T("Word")).Grow(1).MinWidth(0).Disabled(locked)
			iconMark(c, icons.AltArrowRight, tok(t).Text3, "")
			ui.TextInput(c.Key("s"), &e.Say).Placeholder(i18n.T("Say it as…")).Label(i18n.T("Say it as…")).Grow(1).MinWidth(0).Disabled(locked)
			if iconButton(c.Key("x"), icons.Close, i18n.T("Remove")).Disabled(locked).Clicked() {
				remove = i
			}
		})
	}
	if remove >= 0 {
		p.doc.Lexicon = append(p.doc.Lexicon[:remove:remove], p.doc.Lexicon[remove+1:]...)
	}
	if len(dups) > 0 {
		ui.Row(c).Gap(px(t, 6)).Children(func() {
			iconMark(c, icons.DangerCircle, t.Danger, "")
			caption(c, i18n.T("Keep one pronunciation per word: %s", strings.Join(dups, ", "))).TextColor(t.Danger).Grow(1).MinWidth(0)
		})
	}
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		if ghostIconButton(c.Key("lex-add"), icons.AddCircle, i18n.T("Add word")).Label(i18n.T("Add word")).Disabled(locked).Clicked() {
			p.doc.Lexicon = append(p.doc.Lexicon, services.LexiconEntry{})
		}
		infoTip(c.Key("lex-tip"), i18n.T("Words of this book and how to say them, on top of Settings → Studio → Pronunciation."))
	})
}
