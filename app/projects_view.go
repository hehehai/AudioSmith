package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// projectsPage is /projects: the categories on the left (a select in
// the toolbar when the window is narrow), the toolbar, and the items.
func (a *app) projectsPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	s := &a.projects
	a.refreshLibrary()
	narrow := a.pageWidth(c)-px(t, 190) < px(t, 520)
	// The header: 项目 ‹ › how many (or how many the search found), the
	// search, the category (when the list of them is not beside the items),
	// the views, New.
	total := s.counts[libCategories[min(max(s.category, 0), len(libCategories)-1)]]
	note := i18n.N(total, "%d item", "%d items", total)
	if strings.TrimSpace(s.query) != "" {
		note = i18n.N(len(s.items), "%d found", "%d found", len(s.items))
	}
	pageHeaderBar(c, pageHead{
		title: i18n.T("Projects"),
		count: note,
		search: &headSearch{key: "pj-search", value: &s.query, placeholder: i18n.T("Search the library"),
			changed: func() { clear(s.selected) }},
		tools: func(hn bool) {
			if narrow && !hn {
				a.libraryCategorySelect(c)
			}
			if iconToggle(c.Key("pj-view-list"), icons.List, s.cards == 0, tip(i18n.T("List"), "⌥1")).Clicked() {
				s.cards = 0
			}
			if iconToggle(c.Key("pj-view-cards"), galleryIcon, s.cards == 1, tip(i18n.T("Cards"), "⌥2")).Clicked() {
				s.cards = 1
			}
			iconButton(c.Key("pj-new"), icons.AddCircle, i18n.T("New")).Menu(func(m *ui.Menu) {
				if m.Item(i18n.T("New story")).Chosen() {
					a.navigate("/stories")
				}
				if m.Item(i18n.T("New audiobook")).Chosen() {
					a.navigate("/audiobook")
				}
				if m.Item(i18n.T("New dub")).Chosen() {
					a.navigate("/dub")
				}
			})
		},
		more: func(m *ui.Menu) {
			if !narrow {
				return
			}
			m.Submenu(i18n.T("Category"), func(m *ui.Menu) {
				for i, cat := range libCategories {
					if m.Item(categoryLabel(cat) + " · " + strconv.Itoa(s.counts[cat])).Checked(s.category == i).Chosen() {
						s.category = i
						clear(s.selected)
					}
				}
			})
		},
	})
	if c.Shortcut(ui.Alt, ui.Key1) {
		s.cards = 0
	}
	if c.Shortcut(ui.Alt, ui.Key2) {
		s.cards = 1
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if !narrow {
			ui.Column(c.Key("pj-filters")).Width(px(t, 190)).Shrink(0).Padding(px(t, sp1), px(t, sp3)).Gap(px(t, 2)).Children(func() {
				for i, cat := range libCategories {
					n := s.counts[cat]
					sel := s.category == i
					row := ui.ButtonBase(c.Key("cat-"+strconv.Itoa(i))).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl)).Label(categoryLabel(cat)).Selected(sel)
					if sel {
						row.Background(k.Pressed)
					} else if row.Hovered() {
						row.Background(k.Hover)
					}
					if row.Clicked() {
						s.category = i
						clear(s.selected)
					}
					row.Children(func() {
						txt := ui.Text(c, categoryLabel(cat)).TextColor(k.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
						if sel {
							txt.FontWeight(600)
						}
						ui.Text(c, strconv.Itoa(n)).FontSize(px(t, fsMicro)).TextColor(k.Text3)
					})
				}
			})
			ui.Box(c).Width(1).Background(k.Line)
		}
		ui.Column(c).Grow(1).MinWidth(0).Padding(px(t, sp1), px(t, sp6), px(t, sp3)).Gap(px(t, sp3)).Children(func() {
			a.libraryToolbar(c, narrow)
			a.libraryItems(c)
		})
	})
	a.libraryDialogs(c)
}

// libraryToolbar: the selection. The search, the category (narrow) and
// the views are in the header.
func (a *app) libraryToolbar(c *ui.Context, narrow bool) {
	t := c.Theme()
	s := &a.projects
	ui.Row(c.Key("pj-toolbar")).Gap(px(t, sp2)).AlignItems(ui.Center).MinHeight(px(t, 30)).Children(func() {
		ui.Spacer(c)
		n := len(s.selected)
		if n == 0 {
			sel := i18n.T("Select visible")
			var b ui.Element
			if narrow {
				b = iconButton(c.Key("pj-select"), icons.CheckCircle, sel)
			} else {
				b = ghostButton(c.Key("pj-select"), sel).Shrink(0)
			}
			if b.Disabled(len(s.items) == 0).Clicked() {
				for _, it := range s.items {
					s.selected[libKey(it)] = true
				}
			}
			return
		}
		caption(c, i18n.N(n, "%d selected", "%d selected", n)).SingleLine().Shrink(0)
		if iconButton(c.Key("pj-clear"), icons.Close, i18n.T("Clear selection")).Clicked() {
			clear(s.selected)
			clear(s.errors)
		}
		if ghostIconButton(c.Key("pj-delete"), icons.TrashBinMinimalistic, i18n.T("Delete selected (%d)", n)).Label(i18n.T("Delete selected (%d)", n)).Shrink(0).Clicked() {
			s.deleteOpen = true
		}
	})
}

// libraryCategorySelect is the category as a select, in the header
// while the list of them is not beside the items.
func (a *app) libraryCategorySelect(c *ui.Context) {
	t := c.Theme()
	s := &a.projects
	labels := make([]string, len(libCategories))
	for i, cat := range libCategories {
		labels[i] = categoryLabel(cat) + " · " + strconv.Itoa(s.counts[cat])
	}
	cur := labels[min(max(s.category, 0), len(labels)-1)]
	if ui.Select(c.Key("pj-category"), &cur, labels).Label(i18n.T("Category")).Tooltip(i18n.T("Category")).Width(px(t, 160)).Shrink(1).MinWidth(px(t, 90)).Changed() {
		for i, l := range labels {
			if l == cur {
				s.category = i
			}
		}
		clear(s.selected)
	}
}

// libraryItems is the list or the cards.
func (a *app) libraryItems(c *ui.Context) {
	t := c.Theme()
	s := &a.projects
	items := s.items
	if len(items) == 0 {
		switch {
		case s.loading && s.loaded == "":
			ui.Column(c.Key("pj-empty")).Grow(1).Center().Children(func() { ui.Spinner(c).Label(i18n.T("Loading…")) })
		case strings.TrimSpace(s.query) != "":
			emptyState(c.Key("pj-empty"), icons.Magnifer, i18n.T("No matches for “%s”", strings.TrimSpace(s.query)), "", func() {
				if ui.Button(c.Key("pj-empty-clear"), i18n.T("Clear search")).Clicked() {
					s.query = ""
				}
			})
		default:
			emptyState(c.Key("pj-empty"), projectsIcon, i18n.T("Nothing here yet"), i18n.T("Write a story, make an audiobook, design a voice or generate a take, and it shows up here."), nil)
		}
		return
	}
	if s.cards == 1 {
		ui.GridView(c.Key("pj-grid"), &s.grid, len(items), px(t, 260), px(t, 150), func(i int) {
			it := items[i]
			a.libraryCard(c, it)
		}).Grow(1)
	} else {
		s.list.Key = func(i int) any {
			if i < len(items) {
				return libKey(items[i])
			}
			return i
		}
		ui.List(c.Key("pj-list"), &s.list, len(items)).Grow(1).Gap(px(t, 2)).ItemKey(s.list.Key).Rows(func(r ui.ListRow) {
			a.libraryRow(c, items[r.Index])
		})
	}
	if len(items) >= s.limit {
		ui.Row(c).Justify(ui.Center).Children(func() {
			if ghostButton(c.Key("pj-more"), i18n.T("Show more")).Clicked() {
				s.limit += libPageSize
			}
		})
	}
}

// libraryRow is one item in the list: a flat row (hover, chosen on the
// pressed grey) with its tick, icon, title and meta, its main action and
// ⋯.
func (a *app) libraryRow(c *ui.Context, it store.LibraryItem) {
	t := c.Theme()
	k := tok(t)
	s := &a.projects
	key := libKey(it)
	row := ui.Column(c.Key("it-"+key)).Gap(px(t, 4)).Padding(px(t, 6), px(t, sp2)).Radius(px(t, radiusControl))
	switch {
	case s.selected[key]:
		row.Background(k.Pressed)
	case row.Hovered():
		row.Background(k.Hover)
	}
	row.Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			on := s.selected[key]
			if ui.Checkbox(c.Key("sel"), &on, "").Label(i18n.T("Select %s", it.Title)).Changed() {
				if on {
					s.selected[key] = true
				} else {
					delete(s.selected, key)
				}
			}
			a.itemAvatar(c, it)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				a.itemTitle(c, it)
				truncText(c, a.itemMeta(it)).FontSize(px(t, fsMicro)).TextColor(k.Text2)
				if e := s.errors[key]; e != "" {
					caption(c, e).TextColor(t.Danger)
				}
			})
			a.itemActions(c, it)
		})
		if it.Source == "render" {
			ui.Column(c).Padding(0, 0, 0, px(t, 64)).Children(func() { a.renderDetails(c, it) })
		}
	})
}

// libraryCard is one item as a flat card (on the pressed grey when
// chosen).
func (a *app) libraryCard(c *ui.Context, it store.LibraryItem) {
	t := c.Theme()
	k := tok(t)
	s := &a.projects
	key := libKey(it)
	var cd ui.Element
	if s.selected[key] {
		cd = fillCard(c.Key("card-" + key)).Background(k.Pressed)
	} else {
		cd = card(c.Key("card-" + key))
	}
	cd.Grow(1).Gap(px(t, sp2)).Padding(px(t, sp3)).Children(func() {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			a.itemAvatar(c, it)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				a.itemTitle(c, it)
			})
			on := s.selected[key]
			if ui.Checkbox(c.Key("sel"), &on, "").Label(i18n.T("Select %s", it.Title)).Changed() {
				if on {
					s.selected[key] = true
				} else {
					delete(s.selected, key)
				}
			}
		})
		ui.Text(c, a.itemMeta(it)).FontSize(px(t, fsMicro)).TextColor(k.Text2).MaxLines(2).Ellipsis("…")
		ui.Spacer(c)
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Justify(ui.End).Children(func() { a.itemActions(c, it) })
	})
}

// itemAvatar: a voice's portrait, else the kind's icon on a grey circle.
func (a *app) itemAvatar(c *ui.Context, it store.LibraryItem) {
	t := c.Theme()
	k := tok(t)
	if it.Source == "voice" {
		if v := a.findVoice(it.ID); v != nil {
			ui.Avatar(c, v.Name, a.portrait(v))
			return
		}
	}
	ui.Column(c).Width(px(t, 32)).Height(px(t, 32)).Center().Radius(px(t, 16)).Background(k.Hover).Shrink(0).Children(func() {
		ui.Icon(c, itemIcon(it).Linear).Size(px(t, 16), px(t, 16)).TextColor(k.Text2)
	})
}

// itemTitle is the name, an inline editor while renaming.
func (a *app) itemTitle(c *ui.Context, it store.LibraryItem) {
	t := c.Theme()
	s := &a.projects
	key := libKey(it)
	if s.renaming == key {
		ui.Row(c).Gap(px(t, 4)).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("rename"), &s.renameText).Label(i18n.T("Project name")).Grow(1).MinWidth(0).AutoFocus()
			if in.Submitted() || ui.Button(c.Key("rename-ok"), i18n.T("Save name")).Disabled(strings.TrimSpace(s.renameText) == "").Clicked() {
				if strings.TrimSpace(s.renameText) != "" {
					a.renameItem(it, s.renameText)
					s.renaming = ""
				}
			}
			if ghostButton(c.Key("rename-cancel"), i18n.T("Cancel")).Clicked() || c.Shortcut(0, ui.KeyEscape) {
				s.renaming = ""
			}
		})
		return
	}
	title := strings.Join(strings.Fields(it.Title), " ")
	if title == "" {
		title = i18n.T("Untitled")
	}
	b := ui.ButtonBase(c.Key("title")).Label(i18n.T("Open %s", title)).Tooltip(title).MinWidth(0)
	b.Children(func() {
		txt := ui.Text(c, title).FontWeight(600).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if b.Hovered() {
			txt.Underline()
		}
	})
	if b.Clicked() {
		a.openItem(it)
	}
}

// itemActions are an item's actions, by kind: play where there is audio,
// its main action as a ghost button, and ⋯ (Rename, Show in Finder,
// Delete).
func (a *app) itemActions(c *ui.Context, it store.LibraryItem) {
	s := &a.projects
	key := libKey(it)
	reveal := ""
	switch it.Source {
	case "dub", "workflow", "call": // M4 (thread #19), M6 (thread #21), M6b (thread #23)
		if ghostButton(c.Key("open"), i18n.T("Open")).Clicked() {
			a.openItem(it)
		}
	case "project":
		if ghostButton(c.Key("open"), i18n.T("Open")).Clicked() {
			a.openItem(it)
		}
	case "render":
		if r := a.renderFor(it.ID); r != nil && r.body.Output != nil {
			id := "book:" + r.rec.ID
			book := r.body.Output.Book
			reveal = book
			if playToggle(c.Key("play"), a.pl.Playing(id), i18n.T("Play"), i18n.T("Pause")).Clicked() {
				if err := a.pl.Toggle(id, book); err != nil {
					a.toastWarn(i18n.T("Can't play: %v", err))
				}
			}
		}
	case "voice":
		if ghostButton(c.Key("use"), i18n.T("Use voice")).Clicked() {
			if v := a.findVoice(it.ID); v != nil {
				a.useVoiceInStudio(v)
			}
		}
	case "take":
		if tk := a.findTake(it.ID); tk != nil {
			if playToggle(c.Key("play"), a.pl.Playing(tk.ID), i18n.T("Play"), i18n.T("Pause")).Clicked() {
				a.play(tk)
			}
			if ghostButton(c.Key("reuse"), i18n.T("Reuse script")).Clicked() {
				a.reuse(tk)
			}
		}
	case "export":
		reveal = it.Detail
		if iconButton(c.Key("reveal"), icons.FolderOpen, i18n.T("Show in Finder")).Clicked() {
			mygo.Shell.ShowItemInFolder(reveal)
		}
		reveal = ""
	}
	moreMenu(c.Key("more"), i18n.T("Item actions"), func(m *ui.Menu) {
		if it.Source == "project" && m.Item(i18n.T("Rename")).Chosen() {
			s.renaming, s.renameText = key, it.Title
		}
		if it.Source == "take" && a.findTake(it.ID) != nil && m.Item(i18n.T("Export…")).Chosen() {
			a.openExport(a.findTake(it.ID)) // export_dialog.go (thread #31)
		}
		if reveal != "" && m.Item(i18n.T("Show in Finder")).Chosen() {
			mygo.Shell.ShowItemInFolder(reveal)
		}
		if it.Source == "project" || reveal != "" {
			m.Separator()
		}
		if m.Item(i18n.T("Delete…")).Chosen() {
			clear(s.selected)
			s.selected[key] = true
			s.deleteOpen = true
		}
	})
}

// renderDetails is "How it was made".
func (a *app) renderDetails(c *ui.Context, it store.LibraryItem) {
	t := c.Theme()
	s := &a.projects
	key := libKey(it)
	open := s.details[key]
	collapsible(c.Key("how-"+key), i18n.T("How it was made"), &open, nil, func() {
		r := a.renderFor(it.ID)
		if r == nil {
			caption(c, i18n.T("Loading…"))
			return
		}
		sum := r.body.Summary
		if sum.Model == "" {
			caption(c, i18n.T("Rendered before details were recorded."))
			return
		}
		row := func(k, v string) {
			if v == "" {
				return
			}
			ui.Row(c.Key("how-" + k)).Gap(px(t, sp2)).Children(func() {
				ui.Text(c, k).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Width(px(t, 110)).Shrink(0)
				ui.Text(c, v).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0).Selectable()
			})
		}
		lang := sum.Lang
		if lang == "" {
			lang = i18n.T("Auto-detect")
		} else {
			lang = languageName(lang)
		}
		format := strings.ToUpper(sum.Format)
		if format == "" {
			format = "WAV"
		}
		row(i18n.T("Voices"), a.voiceNames(sum.Voices))
		row(i18n.T("Speed"), speedRange(sum.MinSpeed, sum.MaxSpeed))
		row(i18n.T("Engine"), modelName(sum.Model))
		row(i18n.T("Language"), lang)
		row(i18n.T("Format"), format)
		size := i18n.N(sum.Chapters, "%d chapter", "%d chapters", sum.Chapters) + " · " + i18n.N(sum.Lines, "%d line", "%d lines", sum.Lines)
		if sum.Words > 0 {
			size += " · " + i18n.N(sum.Words, "%d word", "%d words", sum.Words)
		}
		if sum.Chars > 0 {
			size += " · " + i18n.N(sum.Chars, "%d character", "%d characters", sum.Chars)
		}
		row(i18n.T("Size"), size)
		row(i18n.T("Settings"), productionText(sum))
		if out := r.body.Output; out != nil && out.Loudness.Target != "" {
			row(i18n.T("Loudness"), fmt.Sprintf("%s · %.1f LUFS · %.1f dBTP", strings.ToUpper(out.Loudness.Target), out.Loudness.Integrated, out.Loudness.TruePeak))
		}
	})
	s.details[key] = open
}

// productionText lists a render's production settings.
func productionText(s services.RenderSummary) string {
	p := s.Prod
	var parts []string
	if p.Steps > 0 {
		parts = append(parts, i18n.T("steps %d", p.Steps))
	}
	if p.Guidance > 0 {
		parts = append(parts, i18n.T("guidance %.1f", p.Guidance))
	}
	if p.Seed != 0 {
		parts = append(parts, i18n.T("seed %d", p.Seed))
	}
	parts = append(parts, i18n.T("line gap %d ms", p.LineGapMs), i18n.T("paragraph gap %d ms", p.ParagraphGapMs))
	if p.VaryRepeats {
		parts = append(parts, i18n.T("vary repeated lines"))
	}
	if p.KeepEdges {
		parts = append(parts, i18n.T("engine silence kept"))
	} else {
		parts = append(parts, i18n.T("engine silence trimmed"))
	}
	if s.Stems {
		parts = append(parts, i18n.T("stems"))
	}
	return strings.Join(parts, " · ")
}

// libraryDialogs: delete the selection.
func (a *app) libraryDialogs(c *ui.Context) {
	t := c.Theme()
	s := &a.projects
	if s.deleteOpen {
		var items []store.LibraryItem
		for _, it := range s.items {
			if s.selected[libKey(it)] {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			s.deleteOpen = false
		}
		ui.Modal(c, &s.deleteOpen, func() {
			w, _ := c.Size()
			ui.Column(c.Key("pj-del")).Width(min(px(t, 440), w-px(t, 64))).Gap(px(t, sp3)).Padding(px(t, sp5)).Children(func() {
				sectionHeading(c, i18n.N(len(items), "Delete %d item?", "Delete %d items?", len(items)))
				ui.Scroll(c).MaxHeight(px(t, 200)).Children(func() {
					ui.Column(c).Gap(px(t, 2)).Children(func() {
						for _, it := range items {
							line := strings.Join(strings.Fields(it.Title), " ")
							e := s.errors[libKey(it)]
							ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
								iconMark(c, itemIcon(it), tok(t).Text2, "")
								truncText(c, line).FontSize(px(t, fsCaption)).Grow(1)
								if e != "" {
									caption(c, e).TextColor(t.Danger).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(e)
								}
							})
						}
					})
				})
				caption(c, i18n.T("This cannot be undone. Projects and export records are removed; deleting voices, takes or renders removes their audio too. Exported files are kept."))
				ui.Row(c).Gap(px(t, sp2)).Children(func() {
					ui.Spacer(c)
					if ui.Button(c.Key("del-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
						s.deleteOpen = false
					}
					label := i18n.N(len(items), "Delete %d item", "Delete %d items", len(items))
					if len(s.errors) > 0 {
						label = i18n.T("Retry")
					}
					if recordButton(c.Key("del-ok"), label).Clicked() {
						a.deleteItems(items)
					}
				})
			})
		})
	}
}

// projectKindOf is the kind of a project item.
func projectKindOf(it store.LibraryItem) core.ProjectKind { return core.ProjectKind(it.Category) }
