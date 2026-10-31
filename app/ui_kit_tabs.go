package main

// The 0.5 kit's tabs and segments (ui_kit.go): underline tabs, which
// switch between the views of a page or a panel, and the segmented
// control, which picks one of a few values (a grey track, the chosen
// segment white and lifted).

import (
	"github.com/egoist/mygo/ui"
)

// underlineTabs is a row of tabs, of which *selected is the chosen one:
// the chosen tab in the text colour over a 2 px ink underline, the others
// in the secondary grey; a hairline runs under the row. A click, the
// arrows, Home and End choose (MyGo's TabsBase); Changed reports a new
// choice. Give the row Grow or AlignSelf to stretch its hairline.
//
//	underlineTabs(c.Key("panel-tabs"), &a.panel, i18n.T("Settings"), i18n.T("History")).Label(i18n.T("Panel"))
func underlineTabs(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	k := tok(t)
	tabs := ui.TabsBase(c, selected, len(labels))
	list := tabs.List.Gap(px(t, sp4)).AlignItems(ui.Stretch).Shrink(0).
		BorderWidth(0, 0, 1, 0).BorderColor(k.Line)
	list.Children(func() {
		for i, label := range labels {
			on := i == *selected
			tab := tabs.Tab(i).Padding(px(t, 7), px(t, 2), px(t, 9)).Label(label).FocusRing(false)
			col := k.Text2
			if on || tab.Hovered() {
				col = k.Text
			}
			tab.DrawOver(func(p *ui.Painter, r ui.Rect) {
				if on {
					h := px(t, 2)
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, k.Ink, 0)
				}
			})
			tab.Children(func() {
				tx := ui.Text(c, label).SingleLine().TextColor(col)
				if on {
					tx.FontWeight(500)
				}
			})
		}
	})
	return list
}

// segmented is a segmented control (ui.Segmented's replacement): labels
// on a light grey track, the chosen one on a white segment with a faint
// shadow. A click or the arrows choose; Changed reports a new choice;
// name it with Label. It stretches across a column; segmentedFit keeps it
// as wide as its segments.
//
//	segmented(c.Key("theme"), &a.theme, i18n.T("System"), i18n.T("Light"), i18n.T("Dark")).Label(i18n.T("Theme"))
func segmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	return segmentTrack(c, selected, labels, nil, nil)
}

// segmentedFit is segmented as wide as its segments, for a form's
// column: there a plain segmented control stretches across, its segments
// keeping their width and leaving the rest of the track empty.
func segmentedFit(c *ui.Context, selected *int, labels ...string) ui.Element {
	return segmentTrack(c, selected, labels, nil, nil).AlignSelf(ui.Start)
}

// segmentedSome is segmentedFit whose segments may be off: off[i] true
// greys segment i out, and tips[i] ("" for none) says why, as its
// tooltip. Its Changed reports a new choice.
func segmentedSome(c *ui.Context, selected *int, labels []string, off []bool, tips []string) ui.Element {
	return segmentTrack(c, selected, labels, off, tips).AlignSelf(ui.Start)
}

// segmentTrack builds the segmented controls: MyGo's SegmentedBase in the
// kit's look.
func segmentTrack(c *ui.Context, selected *int, labels []string, off []bool, tips []string) ui.Element {
	t := c.Theme()
	k := tok(t)
	parts := ui.SegmentedBase(c, selected, len(labels))
	track, chosen := k.G[2], k.Bg
	if t.Dark {
		track, chosen = k.G[1], k.G[4]
	}
	tr := parts.Track.Shrink(0).AlignItems(ui.Stretch).Padding(px(t, 2)).Gap(px(t, 2)).
		Radius(px(t, radiusControl)).Background(track).Border(1, track)
	tr.Children(func() {
		for i, label := range labels {
			s := parts.Segment(i).Padding(t.Space(1), t.Space(3)).Radius(px(t, radiusControl-2)).Center().Label(label)
			dis := i < len(off) && off[i]
			if dis {
				s.Disabled(true)
			}
			if i < len(tips) && tips[i] != "" {
				s.Tooltip(tips[i])
			}
			col := k.Text2
			switch {
			case i == *selected:
				col = k.Text
				s.Background(chosen).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.1))
			case dis:
				col = k.Text3
			case s.Pressed():
				s.Background(k.Pressed)
			case s.Hovered():
				col = k.Text
			}
			s.Children(func() {
				ui.Text(c, label).SingleLine().TextColor(col)
			})
		}
	})
	return tr
}
