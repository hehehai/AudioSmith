package main

// Text to speech's 历史 tab (0.5 F3): the takes made on this page, newest
// first — play, the first words, the voice, the length, when — each with
// a ⋯ of what can follow (the hand-offs, shared with the latest take):
// export, reuse the settings, 换个音色再生成, 存为项目 (long scripts) and
// delete. The Assets page still lists every take.

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// studioHistoryMax is how many takes the tab lists; Assets has them all.
const studioHistoryMax = 50

// studioTakes are the takes made on this page (Origin studio or none, in
// no project), newest first.
func (a *app) studioTakes() []*core.Take {
	var out []*core.Take
	for i := range a.takes {
		t := &a.takes[i]
		if (t.Origin == "" || t.Origin == core.TakeStudio) && t.ProjectID == "" {
			out = append(out, t)
		}
	}
	return out
}

// takeVoiceName names the voice a take was spoken with: the saved voice,
// the speaker, else the model's default.
func (a *app) takeVoiceName(t *core.Take) string {
	if v := a.findVoice(t.VoiceID); v != nil {
		return v.Name
	}
	if s := strings.TrimSpace(t.Voice); s != "" {
		if sp, _, ok := strings.Cut(s, ","); ok {
			return strings.TrimSpace(sp)
		}
		return s
	}
	return i18n.T("Model default")
}

// studioHistory is the tab: the rows, or ghost rows while there are none.
func (a *app) studioHistory(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	takes := a.studioTakes()
	if len(takes) == 0 {
		ghostRows(c, 4, icons.History, i18n.T("No takes yet"), i18n.T("What you generate here shows up in this list, newest first."))
		return
	}
	ui.Column(c.Key("studio-history")).Children(func() {
		for _, tk := range takes[:min(len(takes), studioHistoryMax)] {
			a.studioHistoryRow(c, tk)
		}
	})
	ui.Row(c).Children(func() {
		b := ghostBase(c.Key("all-assets")).Padding(px(t, 4), px(t, 6)).Label(i18n.T("All takes in Assets"))
		b.Children(func() {
			ui.Text(c, i18n.T("All takes in Assets")+" ›").FontSize(px(t, fsCaption)).TextColor(k.Text2)
		})
		if b.Clicked() {
			a.navigate("/assets")
		}
	})
}

// studioHistoryRow is a take of the tab (the row kit's, dense: the tab
// is a side panel): ▶, the first words over the voice, length and when,
// and ⋯. A click on it plays it.
func (a *app) studioHistoryRow(c *ui.Context, tk *core.Take) {
	playing := a.pl.Playing(tk.ID)
	text := scriptStart(tk.Text)
	spec := listRowSpec{key: "hist-" + tk.ID, label: text, tip: text, dense: true, state: rowState{playing: playing}, lead: func() {
		if playButton(c.Key("play"), playing, false, 28).Clicked() {
			a.play(tk)
		}
	}}
	if listRow(c, spec, func() {
		meta := a.takeVoiceName(tk) + " · " + i18n.T("%.1f s", tk.AudioSeconds) + " · " + ago(tk.CreatedAt)
		listTitleMarks(c, text, meta, playing, nil)
	}, func() {
		moreMenu(c.Key("hist-more"), "", func(m *ui.Menu) {
			a.takeHandoffs(m, tk, true)
		})
	}) {
		a.play(tk)
	}
}

// scriptStart is a script's start on one line.
func scriptStart(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}

// takeHandoffs fills a take's ⋯ with what can follow it: Export…, Reuse
// settings (history rows: reuse), 换个音色再生成, 存为项目 when the script
// is long, Show in Finder and, in the history, Delete.
func (a *app) takeHandoffs(m *ui.Menu, tk *core.Take, history bool) {
	if m.Item(i18n.T("Export…")).Chosen() {
		a.openExport(tk) // export_dialog.go
	}
	if history && m.Item(i18n.T("Reuse settings")).Chosen() {
		a.reuse(tk)
	}
	if m.Item(i18n.T("Regenerate with another voice")).Chosen() {
		a.revoice(tk)
	}
	if projectSized(tk.Text) {
		m.Submenu(i18n.T("Save as project"), func(m *ui.Menu) {
			if m.Item(i18n.T("Audiobook")).Chosen() {
				a.homeToTool(homeAudiobook, tk.Text)
			}
			if m.Item(i18n.T("Story")).Chosen() {
				a.homeToTool(homeStories, tk.Text)
			}
		})
	}
	m.Separator()
	path := a.takeStore.AudioPath(*tk)
	if m.Item(i18n.T("Show in Finder")).Chosen() {
		mygo.Shell.ShowItemInFolder(path)
	}
	if history && m.Item(i18n.T("Delete")).Chosen() {
		a.deleteClip(tk)
	}
}

// projectSized is a script long enough to be a project: 400 characters, or
// three paragraphs.
func projectSized(s string) bool {
	s = strings.TrimSpace(s)
	paras := 0
	for _, p := range strings.Split(s, "\n") {
		if strings.TrimSpace(p) != "" {
			paras++
		}
	}
	return utf8.RuneCountInString(s) >= 400 || paras >= 3
}
