package main

// Meta badges (thread #129; user, 11-01): a row's meta value that a
// filter of its list knows — a voice's language, gender and age, use, a
// project's type, an asset's source, a dub's language — is a small
// rounded chip on a faint fill, its icon (or a flag) and its text; a
// click filters the whole list by it, and a click on the badge of the
// filter already set clears it. It is a button inside the row's body
// (MyGo gives a click to the innermost handler), so it never opens the
// row. Under the pointer it takes the row actions' face (ActHover); while
// its filter is set, the pressed one in full ink.

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// badgeSpec is a meta badge.
type badgeSpec struct {
	key   string
	ic    icons.Icon // before the text (noIcon: none, a language's flag leads)
	value string     // the text, the value filtered by
	on    bool       // the list is filtered by this value now
	small bool       // a card's badge: micro type, tighter
}

// badgeTip is a badge's tooltip and label: 「按『中文』筛选」, or
// 「清除『中文』筛选」 while on.
func badgeTip(value string, on bool) string {
	value = noFlag(value)
	if on {
		return i18n.T("Clear the “%s” filter", value)
	}
	return i18n.T("Filter by “%s”", value)
}

// noFlag is s without the flag a language's name leads with.
func noFlag(s string) string {
	if r, _ := utf8.DecodeRuneInString(s); r >= 0x1F1E6 && r <= 0x1F1FF {
		if _, rest, ok := strings.Cut(s, " "); ok {
			return rest
		}
	}
	return s
}

// metaBadge is a badge; it reports a click. An empty value shows nothing.
func metaBadge(c *ui.Context, s badgeSpec) bool {
	if s.value == "" {
		return false
	}
	t := c.Theme()
	k := tok(t)
	tip := badgeTip(s.value, s.on)
	padY, padX, fs, is := px(t, 2), px(t, 7), px(t, fsCaption), px(t, 12)
	if s.small {
		padY, padX, fs, is = px(t, 1), px(t, 5), px(t, fsMicro), px(t, 11)
	}
	b := ui.ButtonBase(c.Key(s.key)).Padding(padY, padX).Gap(px(t, 4)).AlignItems(ui.Center).Radius(px(t, 6)).
		Label(tip).Tooltip(tip).Checked(s.on).MinWidth(0).Shrink(1)
	switch {
	case b.Pressed() || s.on:
		b.Background(k.ActPressed)
	case b.Hovered():
		b.Background(k.ActHover)
	default:
		b.Background(k.BadgeFill)
	}
	col := k.Text2
	if s.on || b.Hovered() {
		col = k.Text
	}
	b.Children(func() {
		if s.ic.Linear != nil {
			ui.Icon(c, s.ic.Linear).Size(is, is).TextColor(k.Text3).Shrink(0)
		}
		ui.Text(c, s.value).FontSize(fs).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
	})
	return b.Clicked()
}

// badgeCell is a row's meta column w DIPs wide holding badges (and a
// countPill): the room stays when it is empty, so the columns line up.
func badgeCell(c *ui.Context, key string, w float32, children func()) {
	t := c.Theme()
	ui.Row(c.Key(key)).Width(px(t, w)).Shrink(0).Gap(px(t, 4)).AlignItems(ui.Center).MinWidth(0).Children(children)
}

// badgeMore is a badge cell's muted "+n" whose tooltip names all the
// values (tip).
func badgeMore(c *ui.Context, n int, tip string) {
	if n <= 0 {
		return
	}
	ui.Box(c.Key("more")).Tooltip(tip).Label(tip).Shrink(0).Children(func() { countPill(c, n) })
}
