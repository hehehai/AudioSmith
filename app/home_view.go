package main

// Home (/home, 0.5 F1, the start page; after ElevenLabs' Home): a big
// question, a composer that takes text, a file or a link and sends it to
// its tool (home_route.go), a row of tool tiles, and two tabs: Recent (the
// latest things made in every tool) and Samples (the sample projects, F6
// fills them). No banners.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/store"
)

// homeState is the Home page's state.
type homeState struct{ homeUI homePage }

type homePage struct {
	text    string // the composer's text
	file    string // a file added or dropped ("" none): it goes before the text
	smart   bool   // 「智能分派」: the writing help picks the tool for text
	routing bool   // the writing help is picking
	focus   bool   // the composer takes the focus (the mic)
	send    bool   // Enter was pressed in the box: send in the next build
	// composing: an input method composes in the box (Enter is its own)
	composing bool
	tab       int  // 0 Recent, 1 Samples
	more      bool // the More tile's menu shows

	recent   []store.LibraryItem // the library's latest, for Recent
	readAt   time.Time           // when they were read
	reading  bool
	readFail bool
}

// homeRecentKinds are the library's categories Recent shows: what the
// tools make (voices and export records stay in their places).
var homeRecentKinds = []string{store.LibTake, store.LibDub, store.LibStory, store.LibAudiobook}

// homeItem is a row of Recent.
type homeItem struct {
	key, title, kind string
	icon             icons.Icon
	at               time.Time
	open             func()
}

// homePlaceholders are the composer's examples, in turn.
func homePlaceholders() []string {
	return []string{
		i18n.T("Narrate a product introduction…"),
		i18n.T("Dub this video into Chinese: drop it here or paste its link…"),
		i18n.T("Paste a chapter to read aloud…"),
		i18n.T("Drop a recording to turn into text…"),
	}
}

func (a *app) homePage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	_, h := c.Size()
	a.homeReadRecent()
	top := px(t, sp8) * 2
	if h < px(t, 640) {
		top = px(t, sp6)
	}
	ui.Scroll(c.Key("home")).Grow(1).Children(func() {
		ui.Column(c).Padding(top, px(t, sp6), px(t, sp8)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Width(px(t, 720)).MaxWidth(a.pageWidth(c) - px(t, 2*sp6)).Gap(px(t, sp5)).Children(func() {
				ui.Text(c, i18n.T("What would you like to create?")).FontSize(px(t, 24)).FontWeight(600).TextAlign(ui.Center).TextColor(k.Text)
				a.homeComposer(c)
				a.homeTiles(c)
				ui.Column(c.Key("home-lists")).Gap(px(t, sp3)).Margin(px(t, sp3), 0, 0).Children(func() {
					underlineTabs(c.Key("home-tab"), &a.homeUI.tab, i18n.T("Recent"), i18n.T("Samples")).Label(i18n.T("Home"))
					if a.homeUI.tab == 0 {
						a.homeRecent(c)
					} else {
						a.homeSamples(c)
					}
				})
			})
		})
	})
}

// homeComposer is the composer: the text (or a file), then a row of "+",
// 「智能分派」 when the writing help is loaded, the mic and the submit.
func (a *app) homeComposer(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	hp := &a.homeUI
	box := ui.Column(c.Key("home-composer")).Gap(px(t, sp2)).Padding(px(t, sp3), px(t, sp3), px(t, sp2)).Radius(px(t, radiusCard)).Border(1, k.Line).Background(k.Bg).
		Shadow(0, 2, 10, 0, ui.RGBA(0, 0, 0, 0.05))
	if files := box.DroppedFiles(); len(files) > 0 {
		hp.file = files[0]
	}
	if box.FileDragOver() {
		box.Border(1, k.Accent)
	}
	box.Children(func() {
		if hp.file != "" {
			a.homeFileChip(c)
		}
		ph := homePlaceholders()
		n := 0
		if a.win != nil { // in turn, every 4 s (tests keep the first)
			n = int(time.Now().Unix()/4) % len(ph)
			c.After(4 * time.Second)
		}
		if hp.file != "" {
			ph[n] = i18n.T("Add a note, or send the file as it is…")
		}
		if hp.send { // Enter in the box (below), before the box takes the text again
			hp.send = false
			a.homeSubmit()
		}
		in := ui.TextAreaBase(c.Key("home-text"), &hp.text).Lines(2, 8).Placeholder(ph[n]).Label(i18n.T("What to create")).Padding(px(t, 4))
		// Enter sends, ⌘↩ too; ⇧Enter breaks the line. While an input
		// method composes (Pinyin), Enter is its own: it only chooses the
		// candidate. macOS gives fn no key then; composing (as of the last
		// build, which a composition change brings) guards it too.
		hp.composing = in.Composing()
		in.OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || (k.Mods != 0 && k.Mods != ui.Cmd) || hp.composing {
				return false
			}
			hp.text, hp.send = k.Text(), true
			return true
		})
		if hp.focus {
			hp.focus = false
			in.Focus()
		}
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			if iconButton(c.Key("home-add"), icons.Add, i18n.T("Add a file: a video, a recording, a text or an ebook")).Clicked() {
				a.homeChooseFile()
			}
			if a.homeSmartReady() {
				on := hp.smart
				b := ghostBase(c.Key("home-smart")).Gap(px(t, 4)).AlignItems(ui.Center).Padding(px(t, 3), px(t, sp2)).Radius(px(t, 12)).Border(1, k.Line).
					Label(i18n.T("Smart routing")).Tooltip(i18n.T("The writing help picks the tool for your text: Text to speech, Stories or Audiobook.")).Checked(on)
				if on {
					b.Background(k.Pressed)
				}
				b.Children(func() {
					ui.Icon(c, icons.Stars.Get(on)).Size(px(t, 13), px(t, 13)).TextColor(k.Text2)
					ui.Text(c, i18n.T("Smart routing")).FontSize(px(t, fsCaption)).SingleLine()
				})
				if b.Clicked() {
					hp.smart = !hp.smart
				}
			}
			ui.Spacer(c)
			if hp.routing {
				ui.Spinner(c).Size(px(t, 16), px(t, 16))
				caption(c, i18n.T("Choosing a tool…")).SingleLine()
			}
			if strings.TrimSpace(hp.text) != "" && !hp.routing {
				ui.Text(c, i18n.T("↩ send · ⇧↩ new line")).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(1).MinWidth(0)
			}
			if iconButton(c.Key("home-mic"), micIcon, i18n.T("Dictate into the box")).Clicked() {
				hp.focus = true // the text goes where the focus is
				a.dictToggle()
			}
			ready := (strings.TrimSpace(hp.text) != "" || hp.file != "") && !hp.routing
			if submitButton(c.Key("home-send"), tip(i18n.T("Start"), "↩"), ready).Tooltip(i18n.T("Send: Enter · New line: Shift-Enter")).Clicked() {
				a.homeSubmit()
			}
		})
	})
}

// homeFileChip is the file the composer holds: its name, the tool it
// goes to, and × to take it out.
func (a *app) homeFileChip(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	hp := &a.homeUI
	dest := homeFileDest(hp.file)
	ui.Row(c.Key("home-file")).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 4), px(t, 4), px(t, 4), px(t, sp2)).Radius(px(t, radiusControl)).Background(k.Subtle).Children(func() {
		ui.Icon(c, icons.FileText.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Text2).Shrink(0)
		ui.Text(c, filepath.Base(hp.file)).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		to := i18n.T("No tool takes this file")
		switch dest {
		case homeDub:
			to = "→ " + pageTitle("/dub")
		case homeTranscribe:
			to = "→ " + pageTitle("/transcripts")
		case homeAudiobook:
			to = "→ " + pageTitle("/audiobook")
		}
		ui.Text(c, to).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
		ui.Spacer(c)
		if iconButton(c.Key("home-file-x"), icons.Close, i18n.T("Remove the file")).Clicked() {
			hp.file = ""
		}
	})
}

// homeChooseFile asks for a file for the composer.
func (a *app) homeChooseFile() {
	exts := slices.Concat(homeVideoExts, homeAudioExts, homeBookExts)
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Add a file"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Video, audio, text or ebook"), Extensions: exts}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.homeUI.file = paths[0] })
	}()
}

// homeTile is a tile of the tool row.
type homeTile struct {
	key, label string
	icon       icons.Icon
	open       func()
}

// homeTiles is the row of tools: one word each; More is a menu.
func (a *app) homeTiles(c *ui.Context) {
	t := c.Theme()
	tiles := []homeTile{
		{"speech", i18n.X("tile", "Speech"), studioIcon, func() { a.navigate("/studio") }},
		{"clone", i18n.X("tile", "Clone"), cloneIcon, func() { a.startClone("") }},
		{"design", i18n.X("tile", "Design"), designIcon, func() { a.navigate("/voices/new/design") }},
		{"stories", i18n.X("tile", "Stories"), storiesIcon, func() { a.navigate("/stories") }},
		{"audiobook", i18n.X("tile", "Audiobook"), audiobookIcon, func() { a.navigate("/audiobook") }},
		{"dub", i18n.X("tile", "Dub"), dubIcon, func() { a.navigate("/dub") }},
		{"transcribe", i18n.X("tile", "Transcribe"), transcribeIcon, func() { a.navigate("/transcripts") }},
		{"isolate", i18n.X("tile", "Isolate"), isolateIcon, func() { a.navigate("/isolate") }},
	}
	ui.Row(c.Key("home-tiles")).Wrap().Justify(ui.Center).GapX(px(t, sp3)).GapY(px(t, sp3)).Children(func() {
		for _, tl := range tiles {
			if toolTile(c.Key("tile-"+tl.key), tl.icon, tl.label).Clicked() {
				tl.open()
			}
		}
		more := toolTile(c.Key("tile-more"), icons.MenuDots, i18n.X("tile", "More"))
		if more.Clicked() {
			a.homeUI.more = !a.homeUI.more
		}
		ui.Popover(c, more, &a.homeUI.more, func() { a.moreMenu(c, &a.homeUI.more) })
	})
}

// homeReadRecent reads the library's latest items in the background, at
// most every few seconds while Home shows.
func (a *app) homeReadRecent() {
	hp := &a.homeUI
	if hp.reading || time.Since(hp.readAt) < 5*time.Second || a.lib == nil || a.lib.svc.Library == nil {
		return
	}
	hp.reading = true
	lib := a.lib.svc.Library
	a.background(func() {
		items, err := lib.Search(context.Background(), store.LibraryQuery{Limit: 60})
		a.post(func() {
			hp.reading, hp.readAt, hp.readFail = false, time.Now(), err != nil
			if err == nil {
				hp.recent = items
			}
		})
	})
}

// homeItems are Recent's rows: the library's latest (takes, dubs, stories,
// books and their renders) and the transcripts, newest first, at most n.
func (a *app) homeItems(n int) []homeItem {
	var out []homeItem
	for _, it := range a.homeUI.recent {
		if !slices.Contains(homeRecentKinds, it.Category) || it.Source == "export" || it.Source == "voice" {
			continue
		}
		it := it
		out = append(out, homeItem{key: libKey(it), title: it.Title, kind: itemKind(it), icon: itemIcon(it), at: it.Updated, open: func() {
			if it.Source == "take" {
				a.navigate("/studio")
			}
			a.openItem(it)
		}})
	}
	for _, tr := range a.trList {
		if tr.Source == core.SourceDictation {
			continue
		}
		id := tr.ID
		out = append(out, homeItem{key: "tr:" + id, title: transcriptTitle(tr), kind: i18n.T("Transcript"), icon: transcribeIcon, at: tr.CreatedAt, open: func() {
			a.selectTranscript(id)
		}})
	}
	for _, x := range a.isoList { // the Voice isolator's (F2)
		id := x.ID
		out = append(out, homeItem{key: "iso:" + id, title: x.Title, kind: i18n.T("Voice isolator"), icon: isolateIcon, at: x.CreatedAt, open: func() {
			a.navigate("/isolate/" + id)
		}})
	}
	slices.SortStableFunc(out, func(x, y homeItem) int { return y.at.Compare(x.at) })
	return out[:min(n, len(out))]
}

// homeRecent is the Recent tab: rows that open the item, or ghost rows.
func (a *app) homeRecent(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	items := a.homeItems(8)
	if len(items) == 0 {
		ghostRows(c, 4, icons.History, i18n.T("Nothing made yet"), i18n.T("What you make in any tool shows here: speech, dubs, transcripts, stories and books."))
		return
	}
	ui.Column(c.Key("home-recent")).Children(func() {
		for _, it := range items {
			row := ui.ButtonBase(c.Key("recent-" + it.key)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, sp2)).Radius(px(t, radiusControl)).Label(it.title)
			if row.Hovered() {
				row.Background(k.Hover)
			}
			row.Children(func() {
				ui.Box(c).Size(px(t, 30), px(t, 30)).Radius(px(t, radiusControl)).Background(k.Subtle).Center().Shrink(0).Children(func() {
					ui.Icon(c, it.icon.Linear).Size(px(t, 15), px(t, 15)).TextColor(k.Text2)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
					ui.Text(c, orDash(it.title)).SingleLine().Ellipsis("…").MinWidth(0)
					ui.Text(c, it.kind).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine()
				})
				ui.Text(c, ago(it.at)).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Shrink(0)
			})
			if row.Clicked() {
				it.open()
			}
		}
	})
}

// homeSample is a sample project of the Samples tab.
type homeSample struct {
	key, title, about string
	icon              icons.Icon
}

// homeSamples is the Samples tab: the five sample projects, coming with
// F6 (a click says so).
func (a *app) homeSamples(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	samples := []homeSample{
		{"story", i18n.T("A story for two voices"), i18n.T("A short scene, cast and performed"), storiesIcon},
		{"chapter", i18n.T("An audiobook chapter"), i18n.T("A chapter narrated with its headings"), audiobookIcon},
		{"narration", i18n.T("A product narration"), i18n.T("Thirty seconds of voice-over"), studioIcon},
		{"dub", i18n.T("A dubbed clip"), i18n.T("A short video dubbed into Chinese"), dubIcon},
		{"meeting", i18n.T("A meeting transcript"), i18n.T("Who said what, with timings"), transcribeIcon},
	}
	ui.Row(c.Key("home-samples")).Wrap().GapX(px(t, sp3)).GapY(px(t, sp3)).Children(func() {
		for _, s := range samples {
			b := ui.ButtonBase(c.Key("sample-" + s.key)).Column().Gap(px(t, sp2)).Padding(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).
				Grow(1).Basis(px(t, 200)).MinWidth(px(t, 170)).Label(s.title)
			if b.Hovered() {
				b.Background(k.Hover)
			}
			b.Children(func() {
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, s.icon.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text2)
					ui.Spacer(c)
					ui.Text(c, i18n.T("Coming soon")).FontSize(px(t, fsMicro)).TextColor(k.Text3).Padding(px(t, 1), px(t, 6)).Radius(px(t, 8)).Border(1, k.Line).SingleLine()
				})
				ui.Text(c, s.title).FontWeight(600)
				ui.Text(c, s.about).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			})
			if b.Clicked() {
				a.toast(i18n.T("The sample projects come with the next update."))
			}
		}
	})
}
