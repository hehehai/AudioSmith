package main

// The Transcribe page (/transcripts) and its toolbar: the model line, the
// language button and the speakers toggle. The history, the open
// transcript and its segments are in transcribe_view_history.go,
// transcribe_view_detail.go and transcribe_view_segments.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/settings"
)

var (
	transcribeIcon = icons.Subtitles
	micIcon        = icons.Microphone3
)

// clock shows seconds as "1:05" or "1:02:05".
func clock(s float64) string {
	n := int(max(s, 0))
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

// ago is how long ago t was, for lists.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T("Just now")
	case d < time.Hour:
		return i18n.T("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.T("%d h ago", int(d.Hours()))
	}
	return i18n.DateTime(t)
}

func (a *app) transcribePage(c *ui.Context) {
	t := c.Theme()
	width := px(t, 280)
	inline, shown := a.trSide.layout(a, c, width, px(t, 480))
	w := a.pageWidth(c)
	if inline && shown {
		w -= width
	}
	a.trNarrow = w < px(t, 560)
	a.trTight = a.pageWidth(c) < px(t, 760)
	page := ui.Column(c.Key("transcribe-page")).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	if files := page.DroppedFiles(); files != nil {
		a.transcribeDropped(files)
	}
	page.Children(func() {
		pageHeader(c, i18n.T("Transcribe"), func() { a.transcribeToolbar(c, shown) })
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.trSide.view(c, "tr-history", i18n.T("History"), inline, shown, true, width, func() {
				ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.transcriptHistory(c, inline) })
			})
			ui.Scroll(c.Key("tr-main")).Grow(1).MinWidth(0).Children(func() {
				ui.Column(c).Padding(px(t, sp2), px(t, sp6), px(t, sp6)).Gap(px(t, sp3)).MaxWidth(px(t, 920)).Children(func() {
					a.transcriptStatus(c)
					if a.trSel == "" {
						a.transcribeSources(c)
					} else {
						a.transcriptDetail(c)
					}
				})
			}).ClipX()
		})
	})
}

// transcribeToolbar: the history toggle, mode, language, and New
// transcript (the page's primary while a transcript shows).
func (a *app) transcribeToolbar(c *ui.Context, shown bool) {
	busy := a.trJob != nil || a.trMic.phase != ""
	e := a.trModel(a.trMode == 0)
	mode := segmented(c.Key("tr-mode"), &a.trMode, i18n.T("Fast"), i18n.T("Accurate")).Label(i18n.T("Transcription quality")).Disabled(busy).Shrink(0).
		Tooltip(i18n.T("Fast gives the text without word timings. Accurate also times every word, for subtitles and editing."))
	if mode.Changed() {
		m := settings.TranscribeAccurate
		if a.trMode == 0 {
			m = settings.TranscribeFast
		}
		a.updateSettings(func(s *settings.Settings) { s.Transcribe.Mode = m })
	}
	a.trSpeakersToggle(c, busy)
	a.trLanguageButton(c, e)
	a.trSide.toggle(c.Key("tr-list-toggle"), icons.PanelLeft, shown, i18n.T("Show the transcripts"), i18n.T("Hide the transcripts"))
	if a.trSel != "" {
		label := i18n.T("New transcript")
		var b ui.Element
		if a.trNarrow || a.trTight {
			b = primaryButton(c.Key("tr-new"), "").Label(label).Tooltip(label).Children(func() {
				ui.Icon(c, icons.AddCircle.Linear).Size(px(c.Theme(), 16), px(c.Theme(), 16))
			})
		} else {
			b = primaryIconButton(c.Key("tr-new"), icons.AddCircle, label).Label(label)
		}
		if b.Clicked() {
			a.selectTranscript("")
		}
	}
}

// trSpeakersToggle is the Speakers option: transcribe with the speakers
// model (thread #48), which labels who says what. Disabled when the
// catalogue has none.
func (a *app) trSpeakersToggle(c *ui.Context, busy bool) {
	t := c.Theme()
	k := tok(t)
	e, ok := catalog.Builtin().SpeakersModel()
	on := a.prefs.Transcribe.Speakers && ok
	label := i18n.X("transcribe option", "Speakers")
	tip := i18n.T("Tell who says what: transcribe with %s (slower than the default).", modelRef(e))
	var b ui.Element
	if a.trNarrow || a.trTight {
		b = iconToggle(c.Key("tr-speakers"), icons.UsersGroupRounded, on, label)
	} else {
		b = ghostBase(c.Key("tr-speakers")).Padding(t.Space(1.5), t.Space(2)).Gap(t.Space(1.5)).Label(label).Checked(on).Shrink(0)
		if on {
			b.Background(k.Pressed)
		}
		b.Children(func() {
			col := k.Text2
			if on {
				col = k.Text
			}
			ui.Icon(c, icons.UsersGroupRounded.Get(on)).Size(labelIcon(t), labelIcon(t)).TextColor(col)
			ui.Text(c, label).SingleLine().TextColor(col)
		})
	}
	if b.Tooltip(tip).Disabled(busy || !ok).Clicked() {
		a.toggleTranscribeSpeakers()
	}
}

// trModelLine names the mode's model: "Multilingual transcription +
// word timings".
func (a *app) trModelLine() string {
	e := a.trModel(a.trMode == 0)
	s := e.DisplayName()
	if a.trMode == 1 {
		if al := trAligner(e); al.Repo != "" {
			s += " + " + i18n.T("word timings")
		}
	}
	return s
}

// trLanguageButton picks the language spoken, or detection.
func (a *app) trLanguageButton(c *ui.Context, e catalog.Entry) {
	t := c.Theme()
	k := tok(t)
	p := &a.trLang
	name := langName(a.prefs.Transcribe.Language)
	var btn ui.Element
	if a.trNarrow || a.trTight {
		btn = iconButton(c.Key("tr-lang"), icons.Global, name)
	} else {
		btn = dropButton(c.Key("tr-lang"), icons.Global, name).MaxWidth(px(t, 180))
	}
	btn.Tooltip(i18n.T("Language spoken"))
	if btn.Clicked() {
		p.open = !p.open
	}
	ui.Popover(c, btn, &p.open, func() {
		langs := lang.All()
		if q := strings.TrimSpace(p.query); q != "" {
			langs = lang.Search(q)
		}
		supported := a.trLanguages(a.trMode == 0)
		opts := lang.Annotate(langs, supported)
		if len(supported) == 0 {
			for i := range opts {
				opts[i].Supported = true
			}
		}
		opts = sortSupported(opts)
		choose := func(code string) {
			p.open = false
			a.updateSettings(func(s *settings.Settings) { s.Transcribe.Language = code })
		}
		_, h := c.Size()
		ui.Column(c.Key("tr-lang-picker")).Width(px(t, 300)).Gap(px(t, sp1)).Padding(px(t, sp1)).Children(func() {
			ui.TextInput(c.Key("tr-lang-search"), &p.query).Placeholder(i18n.T("Search 650 languages")).Label(i18n.T("Search languages"))
			auto := ui.ButtonBase(c.Key("tr-lang-auto")).Padding(px(t, 5), px(t, sp2)).Radius(px(t, radiusControl)).Label(i18n.T("Auto-detect")).Gap(px(t, sp2)).AlignItems(ui.Center)
			if auto.Hovered() {
				auto.Background(k.Hover)
			}
			if auto.Clicked() {
				choose("")
			}
			auto.Children(func() {
				ui.Text(c, i18n.T("Auto-detect")).Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				if a.prefs.Transcribe.Language == "" {
					iconMark(c, icons.Check, k.AccentInk, "")
				}
			})
			ui.List(c.Key("tr-lang-list"), &p.list, len(opts)).Height(min(px(t, 280), h*0.45)).ItemKey(func(i int) any { return opts[i].Code }).Rows(func(r ui.ListRow) {
				o := opts[r.Index]
				row := ui.ButtonBase(c.Key("tr-lang-"+o.Code)).Padding(px(t, 4), px(t, sp2)).Radius(px(t, radiusControl)).Disabled(!o.Supported).Label(o.NameIn(i18n.Locale())).Gap(px(t, 6)).AlignItems(ui.Center)
				if o.Supported && row.Hovered() {
					row.Background(k.Hover)
				}
				if o.Supported && row.Clicked() {
					choose(o.Code)
				}
				row.Children(func() {
					color := k.Text
					if !o.Supported {
						color = k.Text3
					}
					ui.Text(c, o.Flag()).Width(px(t, 22))
					ui.Text(c, o.NameIn(i18n.Locale())).TextColor(color).Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
					if o.Code == a.prefs.Transcribe.Language {
						iconMark(c, icons.Check, k.AccentInk, "")
					}
					ui.Text(c, o.Code).FontSize(px(t, fsMicro)).TextColor(k.Text3)
				})
			})
		})
	})
}

// sortSupported puts the languages a model speaks first.
func sortSupported(opts []lang.Option) []lang.Option {
	out := make([]lang.Option, 0, len(opts))
	for _, o := range opts {
		if o.Supported {
			out = append(out, o)
		}
	}
	for _, o := range opts {
		if !o.Supported {
			out = append(out, o)
		}
	}
	return out
}
