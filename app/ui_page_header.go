package main

// The page header (0.5 H): there is no bar shared by the pages; each
// page's top is a row of its own, which drags the window. On its left
// the title (an item's page: its list's name, muted, before it), ‹ ›
// through the window's history (⌘[ ⌘], a mouse's back and forward
// buttons: MyGo's router), the count of what the page lists and a status;
// on its right the page's tools: a search field, filters, view toggles,
// icon actions and at most one primary button. In a narrow window the
// search is a magnifier that opens the field, and the tools the page
// marks as less needed go into a ⋯ menu.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// pageHead is what a page's header shows.
type pageHead struct {
	title string
	// parent is the list an item's page belongs to ("视频配音"), muted
	// before the title; a click on it, and ‹ when the history has nothing
	// before, open up (the list's path).
	parent, up string
	// upFn, when set, is what the list's name and ‹ do instead (Stories
	// and Audiobook close the open work: longform_entry.go).
	upFn   func()
	count  string // how many items the page lists ("3 项"); "" none
	sub    string // a status or a short line after the count; "" none
	bare   bool   // Home: ‹ › only, no title (the page's question is)
	search *headSearch
	// tools builds the page's tools on the right, after the search;
	// narrow says the window is narrow (pageNarrow): leave out what more
	// lists, give labelled buttons their icon only.
	tools func(narrow bool)
	// more adds the tools left out when narrow to a ⋯ menu; nil none.
	more func(m *ui.Menu)
	// primary is the page's one primary button, last; nil none.
	primary func(narrow bool)
}

// headSearch is a header's search field: its key, the query, the
// placeholder; changed runs when the query changes; focus, when set,
// asks for the keyboard (and is cleared).
type headSearch struct {
	key, placeholder string
	label            string // its name for VoiceOver, "" the placeholder
	value            *string
	changed          func()
	focus            *bool
}

// headerNav is the window's history for the headers' ‹ ›, and what the
// headers need of the app: set by the view each frame.
var headerNav struct {
	canBack, canForward bool
	back, forward       func()
	open                func(path string) // app.navigate
	pageW               func(c *ui.Context) float32
}

// headerInset is the room the headers leave on their left for the window
// controls: set each frame by sidebarFrame, non-zero beside the icon rail.
var headerInset float32

// headSearchOpen holds which narrow headers' search fields are open, by
// key; headSearchFocus those that take the keyboard at their next build.
var headSearchOpen, headSearchFocus = map[string]bool{}, map[string]bool{}

// headerNarrowW is the page width under which headers go narrow, DIPs at
// 100 %.
const headerNarrowW = 640

// pageNarrow reports whether the page is too narrow for its header's
// tools in full.
func pageNarrow(c *ui.Context) bool {
	t := c.Theme()
	w, _ := c.Size()
	if headerNav.pageW != nil {
		w = headerNav.pageW(c)
	}
	return w < px(t, headerNarrowW)
}

// pageHeader is a page's header with its title and tools (trailing,
// shown as they are at any width).
func pageHeader(c *ui.Context, title string, trailing func()) {
	h := pageHead{title: title}
	if trailing != nil {
		h.tools = func(bool) { trailing() }
	}
	pageHeaderBar(c, h)
}

// pageHeaderBar is a page's header (see the file's comment). A page that
// shows another's view inside it (Voices' tabs) names the header through
// headerHost.
func pageHeaderBar(c *ui.Context, h pageHead) {
	t := c.Theme()
	k := tok(t)
	var below func()
	h.title, below = hostedHeader(h.title) // ui_layout.go
	defer func() {
		if below != nil {
			below()
		}
	}()
	narrow := pageNarrow(c)
	ui.Row(c.Key("page-header")).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp4), 0, px(t, sp6)+headerInset).
		Gap(px(t, sp2)).DragWindow().Role(ui.RoleGroup).Label(i18n.T("Page header")).Children(func() {
		ui.Row(c.Key("head-left")).Grow(1).Shrink(1).MinWidth(0).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			if h.parent != "" && h.up == "" {
				ui.Text(c.Key("head-parent"), h.parent+" /").FontSize(px(t, fsPageTitle)).TextColor(k.Text3).SingleLine().Shrink(0)
			} else if h.parent != "" {
				b := ghostBase(c.Key("head-parent")).Padding(px(t, 2), px(t, 4)).Margin(0, -px(t, 4)).Label(i18n.T("Back to %s", h.parent)).Shrink(0)
				b.Children(func() {
					col := k.Text3
					if b.Hovered() {
						col = k.Text2
					}
					ui.Text(c, h.parent+" /").FontSize(px(t, fsPageTitle)).TextColor(col).SingleLine()
				})
				if b.Clicked() {
					switch {
					case h.upFn != nil:
						h.upFn()
					case headerNav.open != nil:
						headerNav.open(h.up)
					}
				}
			}
			if !h.bare {
				// A page's name keeps its room; an item's (a dub's title)
				// gives way, down to a few letters.
				tx := ui.Text(c.Key("head-title"), h.title).FontSize(px(t, fsPageTitle)).FontWeight(600).SingleLine().Ellipsis("…").Role(ui.RoleHeading)
				if h.parent != "" {
					tx.Shrink(1).MinWidth(px(t, 64))
				} else {
					tx.Shrink(0)
				}
			}
			historyButtons(c, h.up, h.upFn)
			if h.count != "" {
				ui.Text(c.Key("head-count"), h.count).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(h.count)
			}
			if h.sub != "" {
				ui.Text(c.Key("head-sub"), h.sub).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			}
		})
		ui.Row(c.Key("head-tools")).Shrink(0).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			if h.search != nil {
				headerSearch(c, h.search, narrow)
			}
			if h.tools != nil {
				h.tools(narrow)
			}
			if narrow && h.more != nil {
				moreMenu(c.Key("head-more"), i18n.T("More tools"), h.more)
			}
			if h.primary != nil {
				h.primary(narrow)
			}
		})
	})
}

// historyButtons are ‹ and ›: back and forward through the window's
// history. On an item's page (up its list's path) ‹ opens the list when
// the history has nothing before; upFn, when set, is what ‹ does.
func historyButtons(c *ui.Context, up string, upFn func()) {
	if headerNav.back == nil {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("head-history")).Gap(px(t, 2)).Shrink(0).Children(func() {
		canBack := headerNav.canBack || up != ""
		if iconButton(c.Key("go-back"), icons.AltArrowLeft, tip(i18n.X("window history", "Back"), "⌘[")).Disabled(!canBack).Clicked() {
			if upFn != nil {
				upFn()
			} else if headerNav.canBack {
				headerNav.back()
			} else if headerNav.open != nil {
				headerNav.open(up)
			}
		}
		if iconButton(c.Key("go-forward"), icons.AltArrowRight, tip(i18n.X("window history", "Forward"), "⌘]")).Disabled(!headerNav.canForward).Clicked() {
			headerNav.forward()
		}
	})
}

// headerSearch is the header's search field; narrow and empty, a
// magnifier that opens it, and that it goes back to once it loses the
// keyboard empty. Escape clears it.
func headerSearch(c *ui.Context, s *headSearch, narrow bool) {
	t := c.Theme()
	label := s.label
	if label == "" {
		label = s.placeholder
	}
	open := headSearchOpen[s.key]
	if s.focus != nil && *s.focus {
		*s.focus = false
		open, headSearchFocus[s.key] = true, true
	}
	if narrow && *s.value == "" && !open {
		if iconButton(c.Key(s.key+"-open"), icons.Magnifer, label).Clicked() {
			headSearchOpen[s.key], headSearchFocus[s.key] = true, true
		}
		return
	}
	w := px(t, 240)
	if narrow {
		w = px(t, 170)
	}
	ui.Row(c.Key(s.key + "-wrap")).Width(w).Shrink(1).MinWidth(px(t, 120)).Children(func() {
		in := searchField(c, s.key, s.value, s.placeholder, 0).Label(label)
		if headSearchFocus[s.key] {
			in.Focus()
			if in.Focused() {
				delete(headSearchFocus, s.key)
			}
		}
		if in.Shortcut(0, ui.KeyEscape) {
			*s.value = ""
			delete(headSearchOpen, s.key)
			if s.changed != nil {
				s.changed()
			}
		}
		if in.Changed() && s.changed != nil {
			s.changed()
		}
		switch {
		case in.Focused():
			headSearchOpen[s.key] = true
		case *s.value == "" && !headSearchFocus[s.key]:
			delete(headSearchOpen, s.key) // it lost the keyboard empty: the magnifier again
		}
	})
}

// listCount is a header's count of n items ("3 项"), "" until they are
// read.
func listCount(n int, loaded bool) string {
	if !loaded {
		return ""
	}
	return i18n.N(n, "%d item", "%d items", n)
}
