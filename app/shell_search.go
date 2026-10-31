package main

// The sidebar's search (0.5 H): a field over Home, 「搜索 ⌘F」 with a ⌘K
// key in it. It searches everything through the command palette
// (shell_commands.go: pages, actions, voices, projects): a click on it or
// on its ⌘K opens the palette, and what is typed in it goes on in the
// palette's field. ⌘F puts the keyboard in it; with the icon rail, a
// magnifier opens the palette, as ⌘F does then.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// sideSearchState is the sidebar search's view state.
type sideSearchState struct {
	sideQuery string // what is typed before the palette takes it
	sideFocus bool   // ⌘F: the field takes the keyboard
	sideKeyed bool   // the field has the keyboard from ⌘F (a click opens the palette)
}

// focusSearch is ⌘F: the keyboard goes to the sidebar's search (with the
// icon rail, which has no field, the palette opens).
func (a *app) focusSearch() {
	a.settingsOpen = false
	if a.railMode() {
		a.openPalette()
		return
	}
	a.sideFocus = true
}

// openPaletteWith opens the palette with q in its field.
func (a *app) openPaletteWith(q string) {
	a.openPalette()
	a.paletteQuery, a.paletteSel = q, 0
}

// sideSearch is the field at the top of the full sidebar.
func (a *app) sideSearch(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	label := i18n.T("Search everything")
	ui.Row(c.Key("side-search")).Padding(0, px(t, 10), px(t, sp2)).Shrink(0).Children(func() {
		box := ui.Row(c.Key("side-search-box")).Grow(1).MinWidth(0).AlignItems(ui.Stretch)
		box.Children(func() {
			in := ui.TextInput(c.Key("side-search-in"), &a.sideQuery).Placeholder(i18n.T("Search")+"  ⌘F").Label(label).Grow(1).MinWidth(0).
				Padding(t.Space(1.5), px(t, 34), t.Space(1.5), px(t, 28))
			if a.sideFocus {
				in.Focus()
				if in.Focused() {
					a.sideFocus, a.sideKeyed = false, true
				}
			}
			focused := in.Focused()
			if !focused && !a.sideFocus {
				a.sideKeyed = false
			}
			if in.Changed() && a.sideQuery != "" {
				q := a.sideQuery
				a.sideQuery, a.sideKeyed = "", false
				a.openPaletteWith(q)
			}
			if in.Shortcut(0, ui.KeyEscape) {
				a.sideQuery, a.sideKeyed = "", false
			}
			ui.Column(c).Absolute().Left(0).Top(0).Bottom(0).Width(px(t, 28)).Center().PassThrough().Children(func() {
				ui.Icon(c, searchIcon.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3)
			})
			if !a.sideKeyed && !a.sideFocus {
				// A click anywhere on the field opens the palette: the
				// field takes the keyboard only from ⌘F.
				over := ui.ButtonBase(c.Key("side-search-open")).Absolute().Left(0).Top(0).Bottom(0).Right(px(t, 34)).Label(label).Tooltip(tip(label, "⌘K"))
				if over.Clicked() {
					a.openPalette()
				}
			}
			kb := ui.ButtonBase(c.Key("side-search-k")).Absolute().Right(px(t, 4)).Top(0).Bottom(0).AlignItems(ui.Center).Padding(0, px(t, 2)).
				Label(i18n.T("Command palette")).Tooltip(tip(i18n.T("Command palette"), "⌘K"))
			kb.Children(func() { kbd(c, "⌘K") })
			if kb.Clicked() {
				a.openPalette()
			}
		})
	})
}

// railSearch is the rail's magnifier: it opens the palette.
func (a *app) railSearch(c *ui.Context) {
	label := i18n.T("Search everything")
	a.railButton(c, "rail/search", icons.Magnifer, label, tip(label, "⌘K"), a.paletteOpen, "", a.openPalette)
}
