package main

// The Assets page (/assets, 0.5 F5): the header (search, the type and
// source filters — the takes' star and model filters under the type —
// and Open folder), then the rows (assets.go), and a take's details pane.

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// assetsShown are the assets the search and filters let through.
func (a *app) assetsShown(all []asset) []asset {
	h := &a.history
	kind := assetKinds[min(max(h.kind, 0), len(assetKinds)-1)]
	src := assetSources[min(max(h.source, 0), len(assetSources)-1)]
	q := strings.ToLower(strings.TrimSpace(h.query))
	takes := map[string]bool{} // the takes the star, model and script search let through
	for _, tk := range a.historyTakes() {
		takes[tk.ID] = true
	}
	takeFilter := h.filter == 1 || h.model != ""
	var out []asset
	for _, as := range all {
		if kind != "" && as.kind != kind || src != "" && as.tool != src {
			continue
		}
		if as.take != nil {
			if !takes[as.take.ID] {
				continue
			}
		} else if takeFilter || q != "" && !strings.Contains(strings.ToLower(as.title+" "+as.sub), q) {
			continue
		}
		out = append(out, as)
	}
	return out
}

// historyPage is /assets.
func (a *app) historyPage(c *ui.Context) {
	t := c.Theme()
	h := &a.history
	if h.details == nil {
		h.details = map[string]bool{}
	}
	all := a.assets()
	rows := a.assetsShown(all)
	sel := a.findTake(h.selected)
	w := px(t, 360)
	inline := a.panelFits(c, w)
	count := i18n.N(len(all), "%d item", "%d items", len(all))
	if len(rows) != len(all) {
		count = i18n.N(len(rows), "%d found", "%d found", len(rows))
	}
	pageHeaderBar(c, pageHead{
		title:  i18n.T("Assets"),
		count:  count,
		search: &headSearch{key: "history-search", value: &h.query, placeholder: i18n.T("Search assets"), label: i18n.T("Search assets"), changed: a.searchTakes},
		tools: func(narrow bool) {
			if !narrow {
				kind := assetKinds[h.kind]
				on := kind != "" || h.filter == 1 || h.model != ""
				headerSelect(c.Key("assets-type"), assetKindLabel(kind), on).Label(i18n.T("Type")).Menu(a.assetTypeMenu)
				headerSelect(c.Key("assets-source"), assetSourceLabel(assetSources[h.source]), h.source != 0).Label(i18n.T("Source")).Menu(a.assetSourceMenu)
			}
			if iconButton(c.Key("open-folder"), icons.FolderOpen, i18n.T("Open folder")).Tooltip(i18n.T("Open the folder of the takes in Finder")).Clicked() {
				a.openTakesFolder()
			}
		},
		more: func(m *ui.Menu) {
			m.Submenu(i18n.T("Type"), a.assetTypeMenu)
			m.Submenu(i18n.T("Source"), a.assetSourceMenu)
		},
	})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Padding(px(t, sp2), px(t, sp6), 0).Children(func() {
			if len(all) == 0 {
				ghostRows(c.Key("assets-ghost"), 5, assetsIcon, i18n.T("Nothing here yet"),
					i18n.T("What you generate, render, export or isolate shows here, with the files you brought in."))
				return
			}
			narrow := a.toolNarrow(c)
			if len(rows) == 0 {
				listNoMatch(c, i18n.T("Nothing matches."))
				return
			}
			ui.List(c.Key("assets-list"), &a.historyList, len(rows)).Grow(1).MinHeight(0).Padding(0, 0, px(t, sp4)).
				ItemKey(func(i int) any { return rows[i].key }).Rows(func(r ui.ListRow) {
				a.assetRow(c, rows[r.Index], narrow)
			})
		})
		if sel != nil {
			sidePanel(c, "details", w, inline, i18n.T("Take details"), func() { h.selected = "" }, func() {
				a.takeDetails(c, sel)
			})
		}
	})
}

// assetTypeMenu is the type filter, and the takes' star and model.
func (a *app) assetTypeMenu(m *ui.Menu) {
	h := &a.history
	for i, k := range assetKinds {
		if m.Item(assetKindLabel(k)).Checked(h.kind == i).Chosen() {
			h.kind = i
		}
	}
	m.Separator()
	if m.Item(i18n.T("Starred takes only")).Checked(h.filter == 1).Chosen() {
		h.filter = 1 - h.filter
	}
	models := append([]string{i18n.T("All models")}, a.historyModels()...)
	m.Submenu(i18n.T("Model"), func(m *ui.Menu) {
		for i, name := range models {
			on := h.model == name || i == 0 && h.model == ""
			if m.Item(name).Checked(on).Chosen() {
				h.model = name
				if i == 0 {
					h.model = ""
				}
			}
		}
	})
}

// assetSourceMenu is the source filter.
func (a *app) assetSourceMenu(m *ui.Menu) {
	h := &a.history
	for i, s := range assetSources {
		if m.Item(assetSourceLabel(s)).Checked(h.source == i).Chosen() {
			h.source = i
		}
	}
}

// assetRow is an asset (the row kit's): ▶ before it when it plays, its
// title over what it is, the tool it came from, length, date, ★ for a
// take, ⋯ (its first item opens it in its tool). A click on it shows a
// take's details, or plays the asset.
func (a *app) assetRow(c *ui.Context, as asset, narrow bool) {
	t := c.Theme()
	k := tok(t)
	h := &a.history
	playing := a.assetPlaying(as)
	ui.Column(c.Key("as-" + as.key)).Shrink(0).Children(func() {
		spec := listRowSpec{key: "row", label: as.title, tip: as.title, state: rowState{selected: as.take != nil && h.selected == as.take.ID, playing: playing}}
		if assetPlayable(as.path) {
			spec.lead = func() {
				ui.Box(c).Size(px(t, listThumbD), px(t, listThumbD)).Radius(px(t, radiusControl)).Background(k.Subtle).Center().Shrink(0).Children(func() {
					if playToggle(c.Key("play"), playing, i18n.T("Play"), i18n.T("Pause")).Clicked() {
						a.playAsset(as)
					}
				})
			}
		}
		open := listRow(c, spec, func() {
			if spec.lead == nil {
				listThumb(c, assetIcon(as.kind), nil)
			}
			listTitleMarks(c, as.title, as.sub, false, nil)
			if !narrow {
				metaCell(c, assetIcon(as.kind), i18n.T("Source"), pageTitle(as.tool), 120)
				metaCell(c, icons.ClockCircle, i18n.T("Length"), durText(as.dur), 72)
				metaCell(c, icons.CalendarMinimalistic, i18n.T("Date"), ago(as.at), 120)
			}
		}, func() {
			if as.take != nil {
				a.starButton(c, as.take)
			} else {
				ui.Box(c).Width(px(t, 28)).Shrink(0)
			}
			moreMenu(c.Key("more"), i18n.T("Asset actions"), func(m *ui.Menu) { a.assetMenu(m, as) })
		})
		if open {
			if as.take != nil {
				h.selected = as.take.ID
			} else {
				a.playAsset(as)
			}
		}
		if as.render != "" && h.details[as.key] {
			ui.Column(c).Padding(0, px(t, sp2), px(t, sp2), px(t, listThumbD+sp3+sp2)).Children(func() { a.renderDetails(c, as.render, as.key) })
		}
	})
}

// assetMenu is a row's ⋯: open, export, Finder, send to…, delete.
func (a *app) assetMenu(m *ui.Menu, as asset) {
	h := &a.history
	if as.open != nil && m.Item(i18n.T("Open in %s", pageTitle(as.tool))).Chosen() {
		as.open()
	}
	if as.take != nil && m.Item(i18n.T("Details")).Chosen() {
		h.selected = as.take.ID
	}
	if as.render != "" && m.Item(i18n.T("How it was made")).Checked(h.details[as.key]).Chosen() {
		h.details[as.key] = !h.details[as.key]
	}
	m.Separator()
	if m.Item(i18n.T("Export…")).Disabled(as.path == "").Chosen() {
		a.saveAssetCopy(as)
	}
	if m.Item(i18n.T("Show in Finder")).Disabled(as.path == "").Chosen() {
		mygo.Shell.ShowItemInFolder(as.path)
	}
	m.Submenu(i18n.T("Send to"), func(m *ui.Menu) {
		media := as.path != ""
		if m.Item(i18n.T("Speech to text")).Disabled(!media).Chosen() {
			a.sendAsset(as, "/transcripts")
		}
		if m.Item(i18n.T("Dub")).Disabled(!media).Chosen() {
			a.sendAsset(as, "/dub")
		}
		if m.Item(i18n.T("Voice isolator")).Disabled(!media || as.kind == assetIsolate).Chosen() {
			a.sendAsset(as, "/isolate")
		}
		if m.Item(i18n.T("Text to speech (the text)")).Disabled(strings.TrimSpace(as.text) == "" && as.studio == nil).Chosen() {
			a.sendAsset(as, "/studio")
		}
	})
	if as.del != nil {
		m.Separator()
		label := i18n.T("Delete")
		if as.delRecord {
			label = i18n.T("Remove from Assets (the file stays)")
		}
		if m.Item(label).Chosen() {
			as.del()
		}
	}
}
