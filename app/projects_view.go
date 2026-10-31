package main

// The Projects page (/projects, 0.5 F5; after ElevenLabs' Studio
// projects list): the samples to start from on top, then every long
// piece of work (projects_list.go) as a table or as cards, with the
// header's search, type filter, view toggle and 「+ 新建」 menu. A row
// opens its tool; its ⋯ opens, renames, duplicates or deletes it.

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/store"
)

// projectsPage is /projects.
func (a *app) projectsPage(c *ui.Context) {
	t := c.Theme()
	s := &a.projects
	s.filter = min(max(s.filter, 0), len(projFilters)-1)
	kind := projFilters[s.filter]
	q := strings.TrimSpace(s.query)
	items := a.projItems(kind, q)
	total := len(a.projItems("", ""))
	count := ""
	if s.loaded {
		count = i18n.N(total, "%d item", "%d items", total)
		if q != "" || kind != "" {
			count = i18n.N(len(items), "%d found", "%d found", len(items))
		}
	}
	filter := func(m *ui.Menu) {
		for i, k := range projFilters {
			if m.Item(projKindLabel(k)).Checked(s.filter == i).Chosen() {
				s.filter = i
			}
		}
	}
	pageHeaderBar(c, pageHead{
		title:  i18n.T("Projects"),
		count:  count,
		search: &headSearch{key: "pj-search", value: &s.query, placeholder: i18n.T("Search projects")},
		tools: func(narrow bool) {
			if !narrow {
				dropText(c.Key("pj-type"), projKindLabel(kind), kind != "").Label(i18n.T("Type")).Menu(filter)
			}
			if iconToggle(c.Key("pj-view-list"), icons.List, s.cards == 0, tip(i18n.T("List"), "⌥1")).Clicked() {
				s.cards = 0
			}
			if iconToggle(c.Key("pj-view-cards"), galleryIcon, s.cards == 1, tip(i18n.T("Cards"), "⌥2")).Clicked() {
				s.cards = 1
			}
		},
		more: func(m *ui.Menu) { m.Submenu(i18n.T("Type"), filter) },
		primary: func(narrow bool) {
			label := i18n.T("New")
			var b ui.Element
			if narrow {
				b = iconButton(c.Key("pj-new"), icons.AddCircle, label)
			} else {
				b = primaryIconButton(c.Key("pj-new"), icons.AddCircle, label).Label(label)
			}
			b.Menu(a.newProjectMenu)
		},
	})
	if c.Shortcut(ui.Alt, ui.Key1) {
		s.cards = 0
	}
	if c.Shortcut(ui.Alt, ui.Key2) {
		s.cards = 1
	}
	a.toolPage(c, "pj-page", func() {
		sectionTitle(c, i18n.T("Samples"))
		a.templateCards(c) // projects_templates.go
		ui.Box(c).Height(px(t, sp2))
		switch {
		case total == 0 && !s.loaded:
			ui.Row(c).Justify(ui.Center).Padding(px(t, sp5)).Children(func() { ui.Spinner(c).Label(i18n.T("Loading…")) })
		case total == 0:
			ghostRows(c.Key("pj-ghost"), 4, projectsIcon, i18n.T("No projects yet"),
				i18n.T("Start from a sample above, or make a story, an audiobook, a dub or a transcript: each shows here."))
		case len(items) == 0:
			ui.Column(c).Padding(px(t, sp5)).AlignItems(ui.Center).Children(func() {
				caption(c, i18n.T("No projects match."))
			})
		case s.cards == 1:
			a.projCards(c, items)
		default:
			a.projTable(c, items, true)
		}
	})
	a.projConfirm(c)
}

// newProjectMenu is 「+ 新建」's menu: a kind of project to make.
func (a *app) newProjectMenu(m *ui.Menu) {
	if m.Item(i18n.T("New story")).Chosen() {
		a.lfEntryNew("/stories")
	}
	if m.Item(i18n.T("New audiobook")).Chosen() {
		a.lfEntryNew("/audiobook")
	}
	if m.Item(i18n.T("New dub")).Chosen() {
		a.navigate("/dub")
	}
	if m.Item(i18n.T("New transcript")).Chosen() {
		a.navigate("/transcripts")
	}
	if m.Item(i18n.T("New isolation")).Chosen() {
		a.navigate("/isolate")
	}
}

// projTable is the rows under their columns' titles; withType shows
// the type column (the Projects page lists every kind).
func (a *app) projTable(c *ui.Context, items []projItem, withType bool) {
	narrow := a.toolNarrow(c)
	ui.Column(c.Key("pj-table")).Shrink(0).Children(func() {
		cols := []listColumn{{title: i18n.T("Name")}}
		if withType {
			cols = append(cols, listColumn{title: i18n.T("Type"), w: 80, wide: true})
		}
		cols = append(cols, listColumn{title: i18n.T("Updated"), w: 110, wide: true}, listColumn{title: i18n.T("Length"), w: 100, wide: true},
			listColumn{title: i18n.T("Status"), w: 130})
		listHead(c, narrow, cols...)
		for _, it := range items {
			a.projRow(c, it, withType, narrow)
		}
	})
}

// projRow is a row: icon, title over its source, type, updated, length,
// status and ⋯; a click opens it.
func (a *app) projRow(c *ui.Context, it projItem, withType, narrow bool) {
	t := c.Theme()
	k := tok(t)
	s := &a.projects
	if s.renaming == it.key() {
		a.projRename(c, it)
		return
	}
	if it.running {
		c.After(500 * time.Millisecond) // the progress moves
	}
	open := listRow(c, "pj-row-"+it.key(), it.title, func() {
		var bmp *ui.Bitmap
		if it.kind == projDub {
			bmp = a.coverBitmap(a.dubCovers[it.id])
		}
		listThumb(c, projKindIcon(it.kind), bmp)
		listTitle(c, it.title, it.sub)
		if !narrow {
			if withType {
				listCell(c, projKindLabel(it.kind), 80)
			}
			listCell(c, ago(it.updated), 110)
			listCell(c, orDash(it.length), 100)
		}
		col := k.Text2
		if it.failed {
			col = k.Danger
		}
		listStatus(c, it.status, it.running, it.frac, col, 130)
	}, func() { a.projMenu(c, it) })
	if open {
		a.openProj(it)
	}
}

// projMenu is a row's ⋯.
func (a *app) projMenu(c *ui.Context, it projItem) {
	s := &a.projects
	lf := (it.kind == projStory || it.kind == projBook) && !it.draft
	moreMenu(c.Key("pj-more-"+it.key()), i18n.T("Project actions"), func(m *ui.Menu) {
		if m.Item(i18n.T("Open")).Chosen() {
			a.openProj(it)
		}
		if lf && m.Item(i18n.T("Rename")).Chosen() {
			s.renaming, s.renameText = it.key(), it.title
		}
		if lf && m.Item(i18n.T("Duplicate")).Chosen() {
			a.duplicateProj(it)
		}
		m.Separator()
		if m.Item(i18n.T("Delete…")).Chosen() {
			s.confirm, s.confirmOpen = it, true
		}
	})
}

// projRename is a row being renamed: the name's field, Save, Cancel.
func (a *app) projRename(c *ui.Context, it projItem) {
	t := c.Theme()
	k := tok(t)
	s := &a.projects
	ui.Row(c.Key("pj-rename-"+it.key())).MinHeight(px(t, 52)).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(0, px(t, sp2)).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line).Children(func() {
		listThumb(c, projKindIcon(it.kind), nil)
		in := ui.TextInput(c.Key("rename"), &s.renameText).Label(i18n.T("Project name")).Grow(1).MinWidth(0).AutoFocus()
		ok := strings.TrimSpace(s.renameText) != ""
		if (in.Submitted() || ui.Button(c.Key("rename-ok"), i18n.T("Save name")).Disabled(!ok).Clicked()) && ok {
			a.renameProject(it.id, s.renameText)
			s.renaming = ""
		}
		if ghostButton(c.Key("rename-cancel"), i18n.T("Cancel")).Clicked() || in.Shortcut(0, ui.KeyEscape) {
			s.renaming = ""
		}
	})
}

// projCards are the rows as cards that wrap.
func (a *app) projCards(c *ui.Context, items []projItem) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("pj-cards")).Wrap().GapX(px(t, sp3)).GapY(px(t, sp3)).Children(func() {
		for _, it := range items {
			b := ui.ButtonBase(c.Key("pj-card-"+it.key())).Column().Gap(px(t, sp2)).Padding(px(t, sp3)).Radius(px(t, radiusCard)).
				Border(1, k.Line).Background(k.Bg).Grow(1).Basis(px(t, 220)).MinWidth(px(t, 180)).MaxWidth(px(t, 320)).Label(it.title)
			if b.Hovered() {
				b.Background(k.Hover)
			}
			b.Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					listThumb(c, projKindIcon(it.kind), nil)
					ui.Spacer(c)
					a.projMenu(c, it)
				})
				ui.Text(c, orDash(it.title)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
				meta := projKindLabel(it.kind) + " · " + ago(it.updated)
				if it.length != "" {
					meta += " · " + it.length
				}
				ui.Text(c, meta).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
				col := k.Text2
				if it.failed {
					col = k.Danger
				}
				listStatus(c, it.status, it.running, it.frac, col, 160)
			})
			if b.Clicked() {
				a.openProj(it)
			}
		}
	})
}

// projConfirm asks before a row's item is deleted.
func (a *app) projConfirm(c *ui.Context) {
	s := &a.projects
	if !s.confirmOpen {
		return
	}
	it := s.confirm
	body := i18n.T("This cannot be undone.")
	if it.kind == projStory || it.kind == projBook {
		body = i18n.T("Its finished renders and exported files are kept.")
	}
	if ui.AlertDialog(c, &s.confirmOpen, i18n.T("Delete “%s”?", it.title), body, i18n.T("Cancel"), i18n.T("Delete")) == 1 {
		a.deleteProj(it)
	}
}

// renameProject renames a story or a book.
func (a *app) renameProject(id, title string) {
	a.renameItem(store.LibraryItem{Source: "project", ID: id}, title)
}
