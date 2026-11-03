package main

// ⌘K, search everything (0.5 F5, after ElevenLabs' palette): empty, the
// quick actions and the latest work; typing, grouped results — pages,
// actions (the command registry, shell_commands.go), voices (the curated
// ones and mine: Enter uses one in Text to speech), projects
// (projects_list.go) and takes by their script — then always 「用文字转语音
// 朗读 "…"」, and 「让写作助手处理 "…"」 while the writing help's model is
// loaded. ↑↓ choose, Enter runs, Esc closes; the sidebar's search opens
// it (shell_search.go).

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// paletteRow is a row of the palette.
type paletteRow struct {
	key, group string // group: translated
	icon       icons.Icon
	title, sub string
	keys       string // a shortcut, "" none
	run        func()
}

// paletteJump asks the chosen row to scroll into view (the arrows moved it).
var paletteJump bool

// paletteGroupMax is how many rows a group of results shows.
const paletteGroupMax = 5

// paletteRows are the palette's rows for its query.
func (a *app) paletteRows() []paletteRow {
	q := strings.TrimSpace(a.paletteQuery)
	if q == "" {
		return append(a.paletteQuick(), a.paletteRecent()...)
	}
	var out []paletteRow
	pages, actions := i18n.T("Pages"), i18n.T("Actions")
	nPages := 0
	for _, cmd := range a.paletteItems() {
		group := actions
		if strings.HasPrefix(cmd.id, "go/") {
			if nPages == paletteGroupMax+3 {
				continue
			}
			group = pages
			nPages++
		}
		ic := icons.Command
		if p := commandIcon(cmd.id); p != nil {
			ic = *p
		}
		cmd := cmd
		out = append(out, paletteRow{key: "cmd-" + cmd.id, group: group, icon: ic, title: cmd.title, sub: cmd.group, keys: cmd.keys,
			run: func() { a.runCommand(cmd) }})
	}
	// Pages first, then actions (the registry interleaves them).
	var sorted []paletteRow
	for _, g := range []string{pages, actions} {
		for _, r := range out {
			if r.group == g {
				sorted = append(sorted, r)
			}
		}
	}
	out = append(sorted, a.paletteVoices(q)...)
	projects, takes := a.paletteFound(q)
	out = append(out, projects...)
	out = append(out, a.paletteSamples(q)...)
	out = append(out, takes...)
	return append(out, a.paletteTail(q)...)
}

// paletteQuick are the empty palette's quick actions.
func (a *app) paletteQuick() []paletteRow {
	g := i18n.T("Quick actions")
	return []paletteRow{
		{key: "q-speech", group: g, icon: studioIcon, title: i18n.T("Generate speech"), sub: pageTitle("/studio"), run: func() { a.navigate("/studio") }},
		{key: "q-dub", group: g, icon: dubIcon, title: i18n.T("Dub a video"), sub: pageTitle("/dub"), run: func() {
			a.navigate("/dub")
			a.chooseDubSource()
		}},
		{key: "q-transcribe", group: g, icon: transcribeIcon, title: i18n.T("Transcribe a recording"), sub: pageTitle("/transcripts"), run: func() {
			a.navigate("/transcripts")
			a.chooseTranscribeFile()
		}},
		{key: "q-voice", group: g, icon: cloneIcon, title: i18n.T("New voice"), sub: i18n.T("Quick clone or design"), run: func() { a.navigate("/voices/new") }},
		{key: "q-story", group: g, icon: storiesIcon, title: i18n.X("create", "New story"), sub: pageTitle("/stories"), run: func() { a.lfEntryNew("/stories") }},
		{key: "q-book", group: g, icon: audiobookIcon, title: i18n.X("create", "New audiobook"), sub: pageTitle("/audiobook"), run: func() { a.lfEntryNew("/audiobook") }},
	}
}

// paletteRecent are the five latest things made (as Home's Recent).
func (a *app) paletteRecent() []paletteRow {
	var out []paletteRow
	for _, it := range a.homeItems(5) {
		it := it
		out = append(out, paletteRow{key: "r-" + it.key, group: i18n.T("Recent"), icon: it.icon, title: orDash(oneLine(it.title)),
			sub: it.kind + " · " + ago(it.at), run: it.open})
	}
	return out
}

// paletteVoices are the voices whose name or description has q: mine,
// then the curated ones; Enter uses one in Text to speech.
func (a *app) paletteVoices(q string) []paletteRow {
	g := i18n.T("Voices")
	var out []paletteRow
	lq := strings.ToLower(q)
	for i := range a.voices {
		v := &a.voices[i]
		if !strings.Contains(strings.ToLower(v.Name+" "+v.Design), lq) {
			continue
		}
		out = append(out, paletteRow{key: "v-" + v.ID, group: g, icon: voicesIcon, title: v.Name,
			sub: i18n.T("My voice · %s", voiceKindText(v.Kind)), run: func() { a.useVoiceInStudio(v) }})
		if len(out) == paletteGroupMax {
			return out
		}
	}
	loc := i18n.Locale()
	for _, arch := range gallery.Default().Search(gallery.Query{Text: q, Limit: paletteGroupMax}).Items {
		arch := arch
		out = append(out, paletteRow{key: "g-" + arch.ID, group: g, icon: voicesIcon, title: arch.Name.In(loc),
			sub: arch.Description.In(loc), run: func() { a.useArchetype(arch, true) }})
		if len(out) == paletteGroupMax {
			break
		}
	}
	return out
}

// paletteSamples are the samples whose title or line has q, or all of
// them for "sample" / 示例.
func (a *app) paletteSamples(q string) []paletteRow {
	lq := strings.ToLower(q)
	all := strings.Contains(strings.ToLower(i18n.T("Samples")), lq) || strings.Contains("samples", lq)
	var out []paletteRow
	for _, s := range a.sampleCards() {
		if s.soon || !all && !strings.Contains(strings.ToLower(s.title+" "+s.about), lq) {
			continue
		}
		key := s.key
		out = append(out, paletteRow{key: "s-" + key, group: i18n.T("Samples"), icon: s.icon, title: s.title, sub: s.about,
			run: func() { a.openSample(key) }})
	}
	return out
}

// paletteProjects are the projects whose title has q.
func (a *app) paletteProjects(q string) []paletteRow {
	var out []paletteRow
	for _, it := range a.projItems("", q) {
		it := it
		sub := projKindLabel(it.kind) + " · " + ago(it.updated)
		out = append(out, paletteRow{key: "p-" + it.key(), group: i18n.T("Projects"), icon: projKindIcon(it.kind), title: it.title, sub: sub,
			run: func() { a.openProj(it) }})
		if len(out) == paletteGroupMax {
			break
		}
	}
	return out
}

// paletteMemo keeps the projects and takes found for a query while
// nothing they come from changed: each frame (a caret's blink, an arrow)
// searched every script before (perf review 11-02 M1: 21 ms a frame for
// 2 000 takes).
type paletteMemo struct {
	q               string
	sig             paletteSig
	projects, takes []paletteRow
}

// paletteSig is what the projects and takes found come from: the lists
// (where they are and how long) and the scripts read for the search.
type paletteSig struct {
	takes   *core.Take
	nTakes  int
	lf      *store.LibraryItem
	nLf     int
	dubs    *store.DubRecord
	nDubs   int
	trs     *core.Transcript
	nTrs    int
	isos    *services.Isolation
	nIsos   int
	textGen int
	locale  string
}

// paletteFound are the projects and takes found for q, as paletteMemo
// keeps them.
func (a *app) paletteFound(q string) (projects, takes []paletteRow) {
	a.refreshProjects() // when they changed
	sig := paletteSig{takes: first(a.takes), nTakes: len(a.takes), lf: first(a.projects.lf), nLf: len(a.projects.lf),
		dubs: first(a.dubList), nDubs: len(a.dubList), trs: first(a.trList), nTrs: len(a.trList), isos: first(a.isoList), nIsos: len(a.isoList),
		textGen: a.projects.textGen, locale: i18n.Locale()}
	m := &a.palMemo
	if m.q != q || m.sig != sig {
		m.q, m.sig, m.projects, m.takes = q, sig, a.paletteProjects(q), a.paletteTakes(q)
	}
	return m.projects, m.takes
}

// paletteStale drops paletteMemo: a list changed in place (a rename, a
// state, a time), which its signature does not see (code review 1103 L1).
func (a *app) paletteStale() { a.palMemo = paletteMemo{} }

// first is a pointer to s's first element, nil when s is empty: where a
// slice is, to tell that it was replaced.
func first[T any](s []T) *T {
	if len(s) == 0 {
		return nil
	}
	return &s[0]
}

// paletteTakes are the takes whose script has q (Assets).
func (a *app) paletteTakes(q string) []paletteRow {
	var out []paletteRow
	lq := strings.ToLower(q)
	for i := range a.takes {
		tk := &a.takes[i]
		if !containsFold(tk.Text, lq) {
			continue
		}
		out = append(out, paletteRow{key: "t-" + tk.ID, group: i18n.T("Assets"), icon: studioIcon, title: oneLine(textHead(tk.Text, 400)),
			sub: modelName(tk.Model) + " · " + ago(tk.CreatedAt), run: func() {
				a.history.selected = tk.ID
				a.navigate("/assets")
			}})
		if len(out) == paletteGroupMax {
			break
		}
	}
	return out
}

// paletteTail is what the query itself can become: read aloud in Text to
// speech, and handed to the writing help when its model is loaded.
func (a *app) paletteTail(q string) []paletteRow {
	g := i18n.T("Use the text")
	short := q
	if r := []rune(short); len(r) > 40 {
		short = string(r[:40]) + "…"
	}
	out := []paletteRow{{key: "x-read", group: g, icon: studioIcon, title: i18n.T("Read “%s” in Text to speech", short), sub: pageTitle("/studio"),
		run: func() { a.homeToStudio(q) }}}
	if a.homeSmartReady() {
		out = append(out, paletteRow{key: "x-smart", group: g, icon: icons.MagicStick3, title: i18n.T("Let the writing help handle “%s”", short),
			sub: i18n.T("It picks the tool"), run: func() {
				a.homeUI.smart = true
				a.homeSmartRoute(q)
			}})
	}
	return out
}

// runPaletteRow closes the overlays and runs a row.
func (a *app) runPaletteRow(r paletteRow) {
	a.paletteOpen, a.sheetOpen = false, false
	if r.run != nil {
		r.run()
	}
}

// palette is the ⌘K palette.
func (a *app) palette(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	w, h := c.Size()
	ui.DialogBase(c, &a.paletteOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.2)).Justify(ui.Start).Padding(min(px(t, 90), h*0.12), px(t, sp6), 0)
		pw := min(px(t, 620), w-px(t, 48))
		panel.Width(pw).MaxWidth(pw).Radius(px(t, radiusDialog)).Background(k.Bg).Border(1, k.Line).Clip().
			Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.22)).Label(i18n.T("Command palette"))
		rows := a.paletteRows()
		a.paletteSel = max(0, min(a.paletteSel, len(rows)-1))
		ui.Row(c).Padding(px(t, 10), px(t, 14)).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, searchIcon.Linear).Size(px(t, 18), px(t, 18)).TextColor(k.Text3)
			// A text input rather than ui.SearchField, whose placeholder
			// is not translated; Escape closes the palette.
			search := ui.TextInputBase(c.Key("palette-search"), &a.paletteQuery).AutoFocus().Grow(1).FontSize(px(t, 15)).
				Placeholder(i18n.T("Search pages, actions, voices, projects…")).Label(i18n.T("Search commands"))
			if search.Changed() {
				a.paletteSel = 0
			}
			if search.Shortcut(0, ui.KeyDown) && len(rows) > 0 {
				a.paletteSel = (a.paletteSel + 1) % len(rows)
				paletteJump = true
			}
			if search.Shortcut(0, ui.KeyUp) && len(rows) > 0 {
				a.paletteSel = (a.paletteSel - 1 + len(rows)) % len(rows)
				paletteJump = true
			}
			if search.Submitted() && len(rows) > 0 {
				a.runPaletteRow(rows[a.paletteSel])
			}
			kbd(c, "Esc")
		})
		ui.Divider(c)
		ui.Scroll(c.Key("palette-rows")).MaxHeight(min(px(t, 420), h*0.6)).ClipY().Children(func() {
			ui.Column(c).Padding(px(t, 6)).Children(func() {
				group := ""
				for i, r := range rows {
					if r.group != group {
						group = r.group
						ui.Text(c, group).FontSize(px(t, fsMicro)).FontWeight(600).TextColor(k.Text3).Padding(px(t, sp2), px(t, 10), px(t, 4))
					}
					if a.paletteRowView(c, r, i == a.paletteSel) {
						a.runPaletteRow(r)
					}
				}
			})
		})
	})
}

// paletteRowView is a row: its icon in a square, title over a muted
// line, its shortcut, and 「打开 ↵」 on the chosen one; it reports a click.
func (a *app) paletteRowView(c *ui.Context, r paletteRow, chosen bool) bool {
	t := c.Theme()
	k := tok(t)
	row := ui.ButtonBase(c.Key(r.key)).Padding(px(t, 6), px(t, 10)).Radius(px(t, radiusControl)).Label(r.title)
	if chosen {
		row.Background(k.Pressed)
		if paletteJump {
			paletteJump = false
			row.ScrollIntoView()
		}
	} else if row.Hovered() {
		row.Background(k.Hover)
	}
	row.Children(func() {
		ui.Row(c).Grow(1).Gap(px(t, 10)).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(px(t, 28), px(t, 28)).Radius(px(t, radiusControl)).Background(k.Subtle).Border(1, k.Line).Center().Shrink(0).Children(func() {
				ui.Icon(c, r.icon.Linear).Size(px(t, 15), px(t, 15)).TextColor(k.Text2)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 1)).Children(func() {
				ui.Text(c, r.title).SingleLine().Ellipsis("…").MinWidth(0)
				if r.sub != "" {
					ui.Text(c, r.sub).FontSize(px(t, fsCaption)).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0)
				}
			})
			if r.keys != "" {
				kbd(c, r.keys)
			}
			if chosen {
				ui.Text(c, i18n.T("Open ↵")).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Shrink(0)
			}
		})
	})
	return row.Clicked()
}
