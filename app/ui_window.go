package main

// The kit's parts of the ActNow family's window layout (the family's
// window spec, research/window-ui/spec.md of the ActNow project): the
// main pages' header with the window's history and a count, tooltips
// with their keys and under the title bar, the sidebar's search field
// with a button in it, and Settings' groups of rows with a line under
// each name.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// Sizes of the window layout, DIPs at 100 %.
const (
	// fsPageTitle is a page's title in the title bar, in the regular
	// weight: under fsTitle, which would crowd the title bar's tools.
	fsPageTitle = 22
	// fwPageTitle is its weight: regular, as the overhaul's titles are.
	fwPageTitle = 400
	// radiusDialog rounds what floats over the window: Settings, ⌘K, the
	// dialogs.
	radiusDialog = 16
	// radiusNotice rounds the activity cards at the sidebar's foot.
	radiusNotice = 14
	// settingControlW is the width of Settings' selects, fields and
	// sliders, so that their left edges line up row after row.
	settingControlW = 220
)

// settingW is settingControlW at the theme's scale.
func settingW(t *ui.Theme) float32 { return px(t, settingControlW) }

// tip is a tooltip with the keys that do the same after it: "Search
// (⌘F)"; keys "" leaves text as it is. Every icon-only button has one.
func tip(text, keys string) string {
	if keys == "" {
		return text
	}
	return text + " (" + keys + ")"
}

// tooltipBelow is Element.Tooltip shown under e: for the controls in the
// title bar, whose tip above would go under the traffic lights.
func tooltipBelow(c *ui.Context, e ui.Element, s string) ui.Element {
	if s == "" {
		return e
	}
	e.Description(s)
	t := c.Theme()
	fill, text := t.Inverse, t.InverseText
	if fill == ui.Transparent {
		fill = t.Text
	}
	if text == ui.Transparent {
		text = t.Background
	}
	ui.TooltipBase(c, e, func(box ui.Element) {
		box.MaxWidth(t.Space(80)).Padding(t.Space(1.25), t.Space(2)).Radius(t.Space(1.25)).
			Background(fill).TextColor(text).FontSize(t.FontSize-1).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.2))
		box.Children(func() { ui.Text(c, s) })
		box.AttachTo(e, ui.AnchorBottom, ui.AnchorTop).Margin(t.Space(1.5), 0, 0, 0)
	})
	return e
}

// iconButtonBelow is iconButton with its tip under it.
func iconButtonBelow(c *ui.Context, ic icons.Icon, label string) ui.Element {
	t := c.Theme()
	b := ghostBase(c).Size(px(t, 28), px(t, 28)).Center().Label(label)
	tooltipBelow(c, b, label)
	b.Children(func() {
		col := tok(t).Text2
		if b.Hovered() {
			col = tok(t).Text
		}
		ui.Icon(c, ic.Linear).Size(px(t, 16), px(t, 16)).TextColor(col)
	})
	return b
}

// headerNav is the window's history for the main pages' ‹ ›: set by the
// view each frame.
var headerNav struct {
	canBack, canForward bool
	back, forward       func()
}

// browseHeader is a main page's header (Projects, History): ‹ › (⌘[ ⌘]),
// the title, note in the secondary grey (a count, "3 found"; "" none),
// then trailing on the right: the views, +, the toggles, ⋯.
func browseHeader(c *ui.Context, title, note string, trailing func()) {
	headerRow(c, title, note, true, trailing)
}

// pageHeaderNote is pageHeader with note after the title (a count).
func pageHeaderNote(c *ui.Context, title, note string, trailing func()) {
	headerRow(c, title, note, false, trailing)
}

func headerRow(c *ui.Context, title, note string, history bool, trailing func()) {
	t := c.Theme()
	k := tok(t)
	left := px(t, sp6)
	if history && headerNav.back != nil {
		left = px(t, sp4)
	}
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, px(t, sp4), 0, left+headerInset).Gap(px(t, sp2)).DragWindow().Children(func() {
		if history && headerNav.back != nil {
			if iconButton(c.Key("go-back"), icons.AltArrowLeft, tip(i18n.X("window history", "Back"), "⌘[")).Disabled(!headerNav.canBack).Clicked() {
				headerNav.back()
			}
			if iconButton(c.Key("go-forward"), icons.AltArrowRight, tip(i18n.X("window history", "Forward"), "⌘]")).Disabled(!headerNav.canForward).Clicked() {
				headerNav.forward()
			}
		}
		ui.Text(c, title).FontSize(px(t, fsPageTitle)).FontWeight(fwPageTitle).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		if note != "" {
			ui.Text(c, note).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
		}
		ui.Box(c).Grow(1).MinWidth(0)
		if trailing != nil {
			trailing()
		}
	})
}

// fieldButtonSize is a fieldButton's side, DIPs at 100 %.
const fieldButtonSize = 22

// searchFieldWith is searchField with n small buttons inside it on the
// right (fieldButton), which trailing builds; the text stops short of
// them.
func searchFieldWith(c *ui.Context, key string, value *string, placeholder string, maxW float32, n int, trailing func()) ui.Element {
	t := c.Theme()
	k := tok(t)
	var in ui.Element
	box := ui.Row(c.Key(key + "-box")).Grow(1).Shrink(1).Basis(0).MinWidth(px(t, 120)).AlignItems(ui.Stretch)
	if maxW > 0 {
		box.MaxWidth(maxW)
	}
	right := px(t, float32(n)*(fieldButtonSize+2)+4)
	box.Children(func() {
		in = ui.TextInput(c.Key(key), value).Placeholder(placeholder).Label(placeholder).Grow(1).MinWidth(0).
			Padding(t.Space(1.5), right, t.Space(1.5), px(t, 30))
		ui.Column(c).Absolute().Left(0).Top(0).Bottom(0).Width(px(t, 30)).Center().PassThrough().Children(func() {
			ui.Icon(c, icons.Magnifer.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text3)
		})
		if trailing != nil {
			ui.Row(c.Key(key + "-buttons")).Absolute().Right(px(t, 3)).Top(0).Bottom(0).Gap(px(t, 2)).AlignItems(ui.Center).Children(trailing)
		}
	})
	return in
}

// fieldButton is a small icon button inside a field: its tip is its name
// for VoiceOver too.
func fieldButton(c *ui.Context, ic icons.Icon, label string, size float32) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ui.ButtonBase(c).Size(px(t, fieldButtonSize), px(t, fieldButtonSize)).Radius(px(t, 5)).Center().Label(label).Tooltip(label)
	switch {
	case b.Pressed():
		b.Background(k.Pressed)
	case b.Hovered():
		b.Background(k.Hover)
	}
	b.Children(func() {
		col := k.Text3
		if b.Hovered() {
			col = k.Text
		}
		ui.Icon(c, ic.Linear).Size(px(t, size), px(t, size)).TextColor(col)
	})
	return b
}

// Settings' groups: a small title over a rounded box in the sidebar's
// colour, faint hairlines between its rows; each row its name, a line of
// what it does under it, its control at the right edge.
//
// The rhythm (DIPs at 100 %, thread #96): the box's sides groupPadX, its
// top and foot groupPadY, every row and block groupRowPadY above and
// below its content (so the text sits groupPadX from the box's edges all
// round), a hairline between rows and blocks; a group's title
// groupTitleGap above its box; a control groupCtrlGap from the text
// beside it, groupWrapGap under it when it wraps below the name. Notes
// and lists inside a group are blocks of their own (settingNote,
// settingBlock), never bare text against the box or a row.
const (
	groupPadX     = sp4
	groupPadY     = sp1
	groupRowPadY  = sp3
	groupTitleGap = sp2
	groupCtrlGap  = sp4
	groupWrapGap  = sp3
)

// subHeading heads a part of a Settings section that was a section of its
// own (Pronunciation in Studio, Permissions in Dictation): over its groups.
func subHeading(c *ui.Context, title string) ui.Element {
	t := c.Theme()
	return ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600).Margin(px(t, sp3), 0, 0, 0)
}

// settingDepth is how deep the group being built is, and settingCount how
// many rows it has so far (the second on draws a hairline above itself).
// Built on the UI thread only.
var settingDepth, settingCount int

// settingGroup is a group of settings: title ("" none) over a box
// holding fn's rows.
func settingGroup(c *ui.Context, title string, fn func()) {
	t := c.Theme()
	k := tok(t)
	if settingDepth > 0 {
		// Inside a group (a part built by a function of its own): its
		// rows go on in the same box, under a small title of their own,
		// nearer the rows under it than the one above.
		if title != "" {
			settingLine(c)
			sectionTitleText(c, title).Margin(px(t, sp4), 0, -px(t, sp1), 0)
			settingCount = 0
		}
		fn()
		return
	}
	ui.Column(c).Gap(px(t, groupTitleGap)).Shrink(0).Children(func() {
		if title != "" {
			sectionTitleText(c, title).Margin(0, 0, 0, px(t, sp1))
		}
		ui.Column(c).Padding(px(t, groupPadY), px(t, groupPadX)).Radius(px(t, radiusCard)).Background(k.Sidebar).Children(func() {
			outer := settingCount
			settingDepth++
			settingCount = 0
			defer func() { settingDepth--; settingCount = outer }()
			fn()
		})
	})
}

// settingLine draws the hairline above a group's second row on.
func settingLine(c *ui.Context) {
	if settingDepth == 0 {
		return
	}
	settingCount++
	if settingCount > 1 {
		ui.Box(c).Height(1).Background(tok(c.Theme()).Line.Alpha(0.6)).Shrink(0)
	}
}

// groupRow is a row of a group: name and its line (note, its colour
// col: the zero colour for the secondary grey), the controls at the right
// edge, or under the name when both do not fit; selectable notes are
// states to copy (a path, a version).
//
// The row of controls may shrink below its widest control (MinWidth(0)):
// a picker with a long choice (Models & network's mirror) then shrinks
// and ends in "…" instead of running off the window. It never reaches
// into the text: the text takes the room the controls leave and wraps in
// it (TestSettingRowsControlsBesideText).
func groupRow(c *ui.Context, key, title, note string, col ui.Color, selectable bool, control func()) {
	t := c.Theme()
	settingLine(c)
	var text, ctrl ui.Element
	var texts [2]ui.Element
	ui.Row(c.Key(key)).Wrap().GapX(px(t, groupCtrlGap)).GapY(px(t, groupWrapGap)).AlignItems(ui.Center).MinHeight(px(t, 44)).Padding(px(t, groupRowPadY), 0).Children(func() {
		text = ui.Column(c).Grow(1).Basis(px(t, 160)).MinWidth(px(t, 140)).Gap(px(t, 2)).Children(func() {
			texts[0] = ui.Text(c, title).MinWidth(0)
			if note != "" {
				n := caption(c, note).MinWidth(0)
				texts[1] = n
				if selectable {
					n.Selectable()
				}
				if col != (ui.Color{}) {
					n.TextColor(col)
				}
			}
		})
		ctrl = ui.Row(c).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Justify(ui.End).Shrink(1).MinWidth(0).Children(control)
	})
	if groupRowLaid != nil {
		groupRowLaid(key, text, ctrl, texts)
	}
}

// groupRowLaid, when set (tests), sees each group row's text column, row
// of controls and texts (title, note: the zero Element for none) as they
// are built: their Bounds are the last frame's.
var groupRowLaid func(key string, text, ctrl ui.Element, texts [2]ui.Element)

// settingRowState is settingRowNote whose line is a state in its colour
// (a permission missing, in Warning).
func settingRowState(c *ui.Context, key, title, state string, col ui.Color, control func()) {
	groupRow(c, key, title, state, col, true, control)
}

// settingBlock is a part of a group that is no row (a list, a note, a
// table): fn under a hairline like a row, as far from it as a row's text.
func settingBlock(c *ui.Context, key string, fn func()) {
	t := c.Theme()
	settingLine(c)
	pad := px(t, sp2)
	if settingDepth > 0 {
		pad = px(t, groupRowPadY)
	}
	ui.Column(c.Key(key)).Gap(px(t, sp2)).Padding(pad, 0).Children(fn)
}

// settingGroupBlock is a group holding one block: a list, a table or a
// form that is no row.
func settingGroupBlock(c *ui.Context, title, key string, fn func()) {
	settingGroup(c, title, func() { settingBlock(c, key, fn) })
}

// settingNote is a line of explanation or state in a group (an intro, "No
// … yet", an error) as a block of its own; outside a group, the caption
// alone. It returns the caption (TextColor for an error).
func settingNote(c *ui.Context, key, text string) ui.Element {
	if settingDepth == 0 {
		return caption(c.Key(key), text)
	}
	var n ui.Element
	settingBlock(c, key, func() { n = caption(c, text) })
	return n
}

// settingFoot is a line that belongs to the row above it, without a
// hairline of its own (a field that goes with a setting, a note on it):
// nearer that row's text than the next row's.
func settingFoot(c *ui.Context, key string, fn func()) {
	t := c.Theme()
	ui.Column(c.Key(key)).Gap(px(t, sp1)).Margin(-px(t, sp1), 0, 0, 0).Padding(0, 0, px(t, groupRowPadY), 0).Children(fn)
}

// settingMore is a fold for an explanation too long for a line ("About
// …"), in a group's block.
func settingMore(c *ui.Context, key, title string, open *bool, text string) {
	t := c.Theme()
	k := tok(t)
	settingBlock(c, key, func() {
		parts := ui.CollapsibleBase(c, open)
		tr := parts.Trigger.AlignSelf(ui.Start).Gap(px(t, 2)).AlignItems(ui.Center).Padding(px(t, 2), px(t, 4), px(t, 2), 0).Radius(px(t, radiusControl)).Label(title)
		col := k.Text3
		if tr.Hovered() {
			col = k.Text2
		}
		tr.Children(func() {
			ui.Text(c, title).FontSize(px(t, fsCaption)).TextColor(col).SingleLine()
			ui.Icon(c, icons.AltArrowRight.Linear).Size(px(t, 12), px(t, 12)).TextColor(col).Rotate(90 * parts.Progress())
		})
		tr.Changed()
		if p := parts.Panel(func() { caption(c, text).Selectable() }); p.Valid() {
			p.Padding(px(t, sp1), 0, 0)
		}
	})
}

// linkButton is a small action in the accent colour in a line under a
// setting ("Use a different setting for this app").
func linkButton(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	b := ghostBase(c).Padding(px(t, 2), px(t, 6)).Margin(0, -px(t, 6)).Label(label).Shrink(0)
	b.Children(func() { ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(tok(t).Accent).SingleLine() })
	return b
}
