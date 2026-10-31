package main

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// storiesPage is /stories: the review buffer, the lines and the render in
// the main column; setup, cast and the rest in the inspector (beside it,
// or a drawer when the window is narrow).
func (a *app) storiesPage(c *ui.Context) {
	t := c.Theme()
	p := a.lfStories
	a.lfToasts(c)
	locked := a.lfLocked(p)
	inline, shown := a.lfLayout(c, p)
	n := len(p.doc.Lines)
	pageHeaderBar(c, pageHead{title: i18n.T("Stories"), count: i18n.N(n, "%d line", "%d lines", n), tools: func(bool) {
		a.storyHeader(c, p, locked, shown)
	}})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Padding(px(t, sp1), px(t, sp6), px(t, sp4)).Gap(px(t, sp3)).Children(func() {
			a.reviewBuffer(c, p, locked)
			a.storyLines(c, p, locked)
			a.lfRenderPanel(c, p)
		})
		a.lfInspector(c, p, inline, shown, func() {
			a.lfSection(c, p, "setup", i18n.T("Setup"), a.storyModelSummary(p), func() { a.lfSetup(c, p, locked) })
			a.lfSection(c, p, "cast", i18n.T("Cast"), lfCount(len(p.doc.Cast)), func() { a.storyCast(c, p, locked) })
			a.lfSection(c, p, "scene", i18n.X("story", "Performance"), personaSummary(&p.doc), func() { a.storyPerformance(c, p, locked) }) // stories_perf_view.go
			a.lfAdvanced(c, p, locked)                                                                                                      // longform_advanced.go
		})
	})
	a.lfConfirm(c, p)
	a.autosaveLf(p)
}

// storyHeader is the Stories page's header: Import, Add ▾, the inspector
// toggle, and ⋯ (the review buffer, the sample, Clear, Audiobook).
func (a *app) storyHeader(c *ui.Context, p *lfPage, locked, shown bool) {
	imp := i18n.T("Import…")
	var b ui.Element
	if p.narrow {
		b = iconButton(c.Key("st-import"), icons.Import, imp)
	} else {
		b = ghostIconButton(c.Key("st-import"), icons.Import, imp).Label(imp)
	}
	if b.Tooltip(i18n.T("TXT, Markdown, SRT, VTT, EPUB or DOCX, into the review buffer")).Disabled(locked).Clicked() {
		a.importStory(p)
	}
	add := i18n.T("Add")
	if p.narrow {
		b = iconButton(c.Key("st-add"), icons.AddCircle, add)
	} else {
		b = dropButton(c.Key("st-add"), icons.AddCircle, add)
	}
	b.Disabled(locked).Menu(func(m *ui.Menu) {
		if m.Item(i18n.T("Add line")).Chosen() {
			a.addLine(p, false)
		}
		if m.Item(i18n.T("Add chapter")).Chosen() {
			a.addLine(p, true)
		}
	})
	a.lfProjectMenu(c, p, locked)
	p.panel.toggle(c.Key("st-panel"), icons.PanelRight, shown, i18n.T("Show the inspector"), i18n.T("Hide the inspector"))
	moreMenu(c.Key("st-more"), "", func(m *ui.Menu) {
		if m.Item(i18n.T("Paste or import text")).Checked(a.reviewOpen(p)).Disabled(locked).Chosen() {
			p.open["review"] = !a.reviewOpen(p)
		}
		if m.Item(i18n.T("Load sample")).Disabled(locked).Chosen() {
			a.loadSampleStory(p)
		}
		if m.Item(i18n.T("Clear…")).Disabled(locked || p.empty()).Chosen() {
			p.confirm, p.confirmOpen = "clear", true
		}
		m.Separator()
		if m.Item(i18n.T("Go to Audiobook")).Chosen() {
			a.navigate("/audiobook")
		}
	})
}

// reviewOpen says whether the review buffer shows: opened from ⋯, or
// holding text, or the story has no lines yet.
func (a *app) reviewOpen(p *lfPage) bool {
	return p.open["review"] || strings.TrimSpace(p.doc.ImportText) != "" || len(p.doc.Lines) == 0
}

// reviewBuffer is the text pasted or imported, before it becomes lines.
func (a *app) reviewBuffer(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	if !a.reviewOpen(p) {
		return
	}
	empty := strings.TrimSpace(p.doc.ImportText) == ""
	card(c.Key("st-review")).Padding(px(t, sp3)).Gap(px(t, sp2)).Shrink(0).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("Paste or import text")).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
			infoTip(c.Key("st-review-tip"), i18n.T("Every line is a take of its own. For one narrator, paragraphs or whole chapters flow better than sentences."))
			ui.Spacer(c)
			if len(p.doc.Lines) > 0 && empty && iconButton(c.Key("st-review-x"), icons.Close, i18n.T("Close")).Clicked() {
				p.open["review"] = false
			}
		})
		box := ui.TextArea(c.Key("st-buffer"), &p.doc.ImportText).Placeholder(i18n.T("Paste a story. Auto-cast finds who speaks — [Name] tags, NAME: lines, or quoted dialogue — and casts a voice for each; Split makes one line per sentence, paragraph or chapter.")).
			Label(i18n.T("Text to review")).Lines(3, 10).MinHeight(0).Disabled(locked)
		if files := box.DroppedFiles(); len(files) > 0 && !locked {
			a.importStoryFrom(p, files[0])
		}
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
			labels := []string{i18n.T("Sentences"), i18n.T("Paragraphs"), i18n.T("Chapters")}
			i := slices.Index(splitModes, p.doc.SplitBy)
			if i < 0 {
				i = 1
			}
			cur := labels[i]
			if ui.Select(c.Key("st-splitby"), &cur, labels).Label(i18n.T("Split by")).Tooltip(i18n.T("Split by")).Disabled(locked).Changed() {
				p.doc.SplitBy = splitModes[slices.Index(labels, cur)]
			}
			if p.doc.SplitBy == "sentences" {
				mc := float64(p.doc.MaxChars)
				if mc == 0 {
					mc = 500
				}
				if numberInput(c.Key("st-maxchars"), &mc, 40, 2000, 10, numOpts{Label: i18n.T("Max characters per line"), Tooltip: i18n.T("Max characters per line"), Width: px(t, 150), Disabled: locked}) {
					p.doc.MaxChars = int(mc)
				}
			}
			ui.Spacer(c)
			if ui.Button(c.Key("st-split"), i18n.T("Split into lines")).Disabled(locked || empty).Clicked() {
				a.splitStory(p)
			}
			if secondaryIconButton(c.Key("st-autocast"), icons.MagicStick3, i18n.T("Auto-cast")).Label(i18n.T("Auto-cast")).
				Tooltip(i18n.T("Find who speaks and give each a voice")).Disabled(locked || empty).Clicked() {
				a.autoCastStory(p)
			}
		})
	})
}

// storyLines is the line list.
func (a *app) storyLines(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	lines := p.doc.Lines
	heard := storyContinuity(p.doc.Model) != services.ContinuityLines
	if len(lines) == 0 {
		emptyState(c.Key("st-empty"), icons.Dialog2, i18n.T("New story"), i18n.T("Start your story with dialogue and narration lines, and give each character a voice in the Cast."), func() {
			if ui.Button(c.Key("st-empty-sample"), i18n.T("Load sample")).Disabled(locked).Clicked() {
				a.loadSampleStory(p)
			}
			if secondaryIconButton(c.Key("st-empty-add"), icons.AddCircle, i18n.T("Add first line")).Label(i18n.T("Add first line")).Disabled(locked).Clicked() {
				a.addLine(p, false)
			}
		}).MinHeight(px(t, 160))
		return
	}
	var after func() // a change to the lines, made once they are built
	p.lines.Key = func(i int) any {
		if i < len(p.doc.Lines) {
			return p.doc.Lines[i].ID
		}
		return i
	}
	ui.List(c.Key("st-lines"), &p.lines, len(lines)).Grow(1).MinHeight(px(t, 160)).Gap(px(t, 2)).ItemKey(p.lines.Key).Rows(func(r ui.ListRow) {
		i := r.Index
		if i >= len(p.doc.Lines) {
			return
		}
		l := &p.doc.Lines[i]
		if f := a.storyLine(c, p, i, l, locked, heard); f != nil {
			after = f
		}
	})
	if after != nil {
		after()
	}
}

// storyLine is one line's row; it returns a change to make after the
// list. A flat row: number, character, what the line overrides, then
// preview, options and ⋯ (moving and removing; icons when there is room).
func (a *app) storyLine(c *ui.Context, p *lfPage, i int, l *services.StoryLine, locked, heard bool) (after func()) {
	t := c.Theme()
	k := tok(t)
	sel := p.sel == i && !l.Chapter
	row := ui.Column(c.Key("line-"+l.ID)).Gap(px(t, 6)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusCard))
	if sel {
		row.Background(k.Subtle)
	}
	if l.Chapter {
		row.Margin(px(t, sp2), 0, 0, 0)
	}
	row.Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			if l.Chapter {
				iconMark(c, icons.NotebookMinimalistic, k.Text3, "")
				ui.TextInput(c.Key("title"), &l.Text).Label(i18n.T("Chapter title")).Placeholder(i18n.T("Chapter")).
					FontSize(px(t, fsSection)).FontWeight(600).Grow(1).MinWidth(0).Disabled(locked)
			} else {
				ui.Text(c, strconv.Itoa(a.lineNumber(p, i))).FontSize(px(t, fsMicro)).TextColor(k.Text3).Width(px(t, 20)).Shrink(0)
				a.characterSelect(c, p, l, locked)
				ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Grow(1).MinWidth(0).ClipX().Children(func() {
					if l.Direction != "" || l.Emotion != "" {
						directionChip(c, l, heard)
					}
					if l.Voice != "" {
						tagChip(c, i18n.T("voice · %s", a.lfVoiceLabel(l.Voice))).Shrink(1).MinWidth(0).Ellipsis("…")
					}
					if l.Speed > 0 {
						tagChip(c, strconv.FormatFloat(l.Speed, 'f', 2, 64)+"×")
					}
					if l.PauseAfter > 0 {
						tagChip(c, i18n.T("then %d ms", l.PauseAfter))
					}
				})
				a.auditionButton(c, p, "line:"+l.ID, i18n.T("Preview this line"))
				if iconToggle(c.Key("more"), icons.Tuning2, sel, i18n.T("Line options")).Clicked() {
					if sel {
						p.sel = -1
					} else {
						p.sel = i
					}
				}
			}
			up := func() { after = func() { a.moveLine(p, i, -1) } }
			down := func() { after = func() { a.moveLine(p, i, 1) } }
			del := func() { after = func() { a.deleteLine(p, i) } }
			if p.narrow {
				moreMenu(c.Key("line-more"), i18n.T("More line actions"), func(m *ui.Menu) {
					if m.Item(i18n.T("Move up")).Disabled(locked || i == 0).Chosen() {
						up()
					}
					if m.Item(i18n.T("Move down")).Disabled(locked || i == len(p.doc.Lines)-1).Chosen() {
						down()
					}
					if m.Item(i18n.T("Remove line")).Disabled(locked).Chosen() {
						del()
					}
				})
				return
			}
			if iconButton(c.Key("up"), icons.AltArrowUp, i18n.T("Move up")).Disabled(locked || i == 0).Clicked() {
				up()
			}
			if iconButton(c.Key("down"), icons.AltArrowDown, i18n.T("Move down")).Disabled(locked || i == len(p.doc.Lines)-1).Clicked() {
				down()
			}
			if iconButton(c.Key("del"), icons.Close, i18n.T("Remove line")).Disabled(locked).Clicked() {
				del()
			}
		})
		if l.Chapter {
			return
		}
		ui.TextArea(c.Key("text"), &l.Text).Placeholder(i18n.T("Dialogue or narration… [pause 500ms] for a silent break")).
			Label(i18n.T("Line %d", a.lineNumber(p, i))).Lines(1, 10).MinHeight(0).Disabled(locked)
		if sel {
			a.lineOptions(c, p, l, locked)
		}
	})
	return after
}

// lineNumber is a line's number among the spoken lines (1-based).
func (a *app) lineNumber(p *lfPage, i int) int {
	n := 0
	for j := 0; j <= i && j < len(p.doc.Lines); j++ {
		if !p.doc.Lines[j].Chapter {
			n++
		}
	}
	return n
}

// characterSelect picks a line's character.
func (a *app) characterSelect(c *ui.Context, p *lfPage, l *services.StoryLine, locked bool) {
	t := c.Theme()
	labels, ids := []string{i18n.T("Narrator")}, []string{""}
	for _, m := range p.doc.Cast {
		name := m.Name
		if strings.TrimSpace(name) == "" {
			name = i18n.T("Character")
		}
		labels, ids = append(labels, name), append(ids, m.ID)
	}
	cur := labels[0]
	if i := slices.Index(ids, l.Character); i >= 0 {
		cur = labels[i]
	}
	if ui.Select(c.Key("who"), &cur, labels).Label(i18n.T("Character")).Width(px(t, 150)).Shrink(1).MinWidth(px(t, 90)).Disabled(locked).Changed() {
		if i := slices.Index(labels, cur); i >= 0 {
			l.Character = ids[i]
		}
	}
}

// lineOptions: direction, voice override, speed, pause after, and
// Insert ▾ (tones, a pause).
func (a *app) lineOptions(c *ui.Context, p *lfPage, l *services.StoryLine, locked bool) {
	t := c.Theme()
	e := entryFor(p.doc.Model)
	ui.Column(c.Key("opts")).Gap(px(t, sp1)).Padding(px(t, sp1), 0, 0, px(t, 26)).Children(func() {
		if storyContinuity(p.doc.Model) != services.ContinuityLines {
			a.lineDirection(c, p, l, locked) // stories_perf_view.go: heard as an instruction or an intensity
		}
		formRow(c, "opt-voice", i18n.T("Voice of this line"), "", func() {
			ui.Box(c).Width(px(t, 220)).Shrink(1).MinWidth(0).Children(func() {
				a.lfVoiceSelect(c, "line-voice-"+l.ID, &l.Voice, e, i18n.T("The character's voice"), locked)
			})
		})
		sp := l.Speed
		if sp == 0 {
			sp = p.doc.Speed
			if sp <= 0 {
				sp = 1
			}
		}
		if e.Has(catalog.CapSpeed) {
			formRow(c, "opt-speed", i18n.T("Speed %.2f×", sp), "", func() {
				if snapped(ui.Slider(c.Key("line-speed"), &sp, 0.5, 2).Label(i18n.T("Speed")).Width(px(t, 160)).Shrink(1).Disabled(locked), &sp, 0.05).Changed() {
					l.Speed = sp
				}
				if l.Speed > 0 && iconButton(c.Key("speed-reset"), icons.Restart, i18n.T("Reset")).Disabled(locked).Clicked() {
					l.Speed = 0
				}
			})
		}
		formRow(c, "opt-pause", i18n.T("Pause after (ms)"), "", func() {
			ms := float64(l.PauseAfter)
			if numberInput(c.Key("pause-after"), &ms, 0, 10000, 50, numOpts{Label: i18n.T("Pause after (ms)"), Width: px(t, 150), Disabled: locked}) {
				l.PauseAfter = int(ms)
			}
		})
		formRow(c, "opt-insert", i18n.T("Tone"), i18n.T("Switch voice inside a line with [voice:NAME] … [voice:default]; NAME is a character or a voice."), func() {
			dropButton(c.Key("line-insert"), icons.AddCircle, i18n.T("Insert")).Disabled(locked).Menu(func(m *ui.Menu) {
				for _, tag := range toneTags {
					if m.Item(nonVerbalLabel(tag)).Chosen() {
						appendTag(l, "["+tag+"]")
					}
				}
				m.Separator()
				if m.Item(i18n.T("Insert pause")).Chosen() {
					appendTag(l, "[pause 500ms]")
				}
			})
		})
	})
}

// storyCast is the cast panel.
func (a *app) storyCast(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	e := entryFor(p.doc.Model)
	if len(p.doc.Cast) == 0 {
		caption(c, i18n.T("No characters yet: Auto-cast finds them, or add one."))
	}
	remove := ""
	for i := range p.doc.Cast {
		m := &p.doc.Cast[i]
		fillCard(c.Key("cast-" + m.ID)).Padding(px(t, sp2)).Gap(px(t, 6)).Children(func() {
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("name"), &m.Name).Placeholder(i18n.T("Character name")).Label(i18n.T("Character name")).Grow(1).MinWidth(0).Disabled(locked)
				n := 0
				for _, l := range p.doc.Lines {
					if l.Character == m.ID {
						n++
					}
				}
				tagChip(c, i18n.N(n, "%d line", "%d lines", n))
				if iconButton(c.Key("x"), icons.Close, i18n.T("Remove character")).Tooltip(i18n.T("Remove character: its lines go to the narrator")).Disabled(locked).Clicked() {
					remove = m.ID
				}
			})
			a.lfVoiceSelect(c, "cast-voice-"+m.ID, &m.Voice, e, i18n.T("Default voice"), locked)
			level := storyContinuity(p.doc.Model)
			if level != services.ContinuityLines && (m.Baseline != "" || storyHasEmotions(&p.doc)) {
				baselineSelect(c, m, locked) // stories_perf_view.go
			}
			if level != services.ContinuityInstruct {
				return // a persona is an instruction: only models that take one hear it
			}
			ui.TextInput(c.Key("persona"), &m.Persona).Placeholder(i18n.T("Persona: age, temperament, way of speaking")).
				Label(i18n.T("Persona of %s", m.Name)).Tooltip(i18n.T("How this character speaks, for models that take instructions: “seven, lively, coaxing”. Never spoken.")).Disabled(locked)
		})
	}
	if remove != "" {
		a.removeCast(p, remove)
	}
	ui.Row(c).Gap(px(t, sp1)).Wrap().Children(func() {
		if ghostIconButton(c.Key("cast-add"), icons.AddCircle, i18n.T("Add character")).Label(i18n.T("Add character")).Disabled(locked).Clicked() {
			p.doc.Cast = append(p.doc.Cast, services.CastMember{ID: core.NewID(time.Now()), Name: i18n.T("Character")})
		}
		if ghostButton(c.Key("cast-voices"), i18n.T("Voices…")).Tooltip(i18n.T("Clone, design or pick voices")).Clicked() {
			a.navigate("/voices")
		}
	})
}
