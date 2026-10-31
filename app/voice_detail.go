package main

// A voice's detail (0.5 F4; after ElevenLabs' voice panel): a panel at
// the right of Discover's and Mine's tables, beside them when the window
// is wide and a drawer over them when it is narrow (sidePanel). A panel
// rather than a page of its own (/voices/{id}): the list stays in view,
// so one can step through voices, preview them and compare, as in
// ElevenLabs; the panel scrolls on its own.
//
// From the top: the name, description and tags; a large preview; the
// hand-offs (「用于文字转语音」, 「用于故事角色」, 「为有声书朗读」, and
// for a Discover voice 「保存到我的音色」); 「试一句」, a line said with
// the voice by the model that speaks it. A saved voice then has its
// reference with the checker's grade, its settings, where it is used,
// its lock, ownership and export (voice_editor.go). 「用于配音」 is left
// out: a dub's cast picker offers every saved voice already, and a dub
// needs a video first, so there is nothing to hand over.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// voiceDetailW is the detail panel's width, DIPs at 100 %.
const voiceDetailW = 380

// voiceTry is a detail's 「试一句」: the line typed.
type voiceTry struct {
	text string
	set  bool // the line was given (else the voice's language's default)
}

// tryLine is the default 「试一句」 line in a language.
func tryLine(lang string) string {
	switch {
	case strings.HasPrefix(lang, "zh"):
		return "你好，这是用这个音色说的一句话。听起来怎么样？"
	case strings.HasPrefix(lang, "ja"):
		return "こんにちは。この声で話すと、こんな感じになります。"
	}
	return "Hello — this is a line in this voice. How does it sound?"
}

// detailHead is a detail's top: the avatar (or the portrait picker:
// avatar builds it), the name, a line under it, marks, ✕; then the
// description and the tags.
func detailHead(c *ui.Context, name, sub, desc string, tags []string, avatar, marks func(), close func()) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("detail-head")).Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
		avatar()
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			ui.Text(c, name).FontSize(px(t, fsTitle)).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(name).Role(ui.RoleHeading)
			if sub != "" {
				ui.Text(c, sub).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0)
			}
			if marks != nil {
				ui.Row(c).Gap(px(t, sp2)).Wrap().Children(marks)
			}
		})
		if iconButton(c.Key("detail-close"), icons.Close, i18n.T("Close")).Clicked() {
			close()
		}
	})
	if desc != "" {
		ui.Text(c, desc).TextColor(k.Text2).Selectable()
	}
	if len(tags) > 0 {
		ui.Row(c.Key("detail-tags")).Gap(px(t, 4)).Wrap().Children(func() {
			for _, tg := range tags {
				tagChip(c, tg)
			}
		})
	}
}

// detailPlayer is the large preview: a round ▶ and what plays.
func detailPlayer(c *ui.Context, key string, playing bool, title, line string, busy string, play func()) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key(key)).Gap(px(t, sp3)).AlignItems(ui.Center).Padding(px(t, sp3)).Radius(px(t, radiusCard)).Background(k.Subtle).Children(func() {
		if busy != "" {
			ui.Spinner(c)
			caption(c, busy).Grow(1).MinWidth(0)
			return
		}
		b := playButton(c.Key("detail-play"), playing, true, 44)
		if !playing {
			b.Label(i18n.T("Preview voice")).Tooltip(i18n.T("Preview voice"))
		}
		if b.Clicked() && play != nil {
			play()
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
			tx := ui.Text(c, title).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
			if playing {
				tx.TextColor(k.AccentInk)
			}
			if line != "" {
				ui.Text(c, line).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(2).Ellipsis("…").MinWidth(0).Tooltip(line)
			}
		})
	})
}

// detailSection is a titled part of a detail, after a hairline.
func detailSection(c *ui.Context, key, title string, fn func()) {
	t := c.Theme()
	ui.Column(c.Key(key)).Gap(px(t, sp2)).Padding(px(t, sp3), 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(tok(t).Line).Children(func() {
		ui.Text(c, title).FontWeight(600).Role(ui.RoleHeading)
		fn()
	})
}

// handoffRow is a detail's hand-offs: the primary first (nil none), then
// the others as plain buttons; they wrap.
func handoffRow(c *ui.Context, key string, primary func(), others func()) {
	t := c.Theme()
	ui.Row(c.Key(key)).Gap(px(t, sp2)).Wrap().AlignItems(ui.Center).Children(func() {
		if primary != nil {
			primary()
		}
		if others != nil {
			others()
		}
	})
}

// tryResult is a 「试一句」 job's state: progress, an error with Download,
// or the take with ▶ (and extra, beside it).
func (a *app) tryResult(c *ui.Context, key string, j *voiceJob, extra func(take *core.Take)) {
	t := c.Theme()
	switch {
	case j == nil:
	case j.running():
		ui.Progress(c, j.fraction).Label(j.status)
		caption(c, j.status)
	case j.err != "":
		ui.Text(c, j.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).Selectable()
		if j.needDownload != nil && secondaryIconButton(c.Key(key+"-download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Clicked() {
			a.download(*j.needDownload)
		}
	default:
		take := j.take
		ui.Row(c.Key(key + "-take")).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			if playButton(c.Key(key+"-play"), a.pl.Playing(take.ID), true, 28).Clicked() {
				a.play(take)
			}
			caption(c, i18n.T("%.1f s · seed %d", take.AudioSeconds, take.Seed)).SingleLine().Grow(1)
			if extra != nil {
				extra(take)
			}
		})
	}
}

// --- a Discover voice ---------------------------------------------------

// archetypeDetail is a Discover voice's detail.
func (a *app) archetypeDetail(c *ui.Context, arch gallery.Archetype) {
	t := c.Theme()
	g := &a.gallery
	loc := i18n.Locale()
	name := arch.Name.In(loc)
	p := archetypePreview(arch)
	playing := p.ok() && a.pl.Playing(p.id)
	saved := a.savedFrom(arch.ID)
	ui.Column(c.Key("arch-detail-"+arch.ID)).Padding(px(t, sp4), px(t, sp5), px(t, sp5)).Gap(px(t, sp3)).Children(func() {
		kind := i18n.T("Designed voice")
		if !arch.Designed() {
			kind = i18n.T("Built-in voice")
		}
		detailHead(c, name, kind, arch.Description.In(loc), archetypeTags(arch, loc),
			func() { ui.Avatar(c, name, nil).Size(px(t, 56), px(t, 56)).Shrink(0) },
			func() {
				fav := g.favs.Has(arch.ID)
				tip := i18n.T("Add to favorites")
				if fav {
					tip = i18n.T("Remove from favorites")
				}
				if iconToggle(c.Key("detail-fav"), icons.Star, fav, tip).Clicked() {
					a.toggleFavorite(arch.ID)
				}
				if saved != nil {
					statusMark(c, icons.CheckCircle, t.Success, i18n.T("In My voices"))
				}
			},
			func() { g.detail = "" })
		line, _ := arch.PreviewLine()
		if p.ok() {
			detailPlayer(c, "detail-player", playing, i18n.T("Preview"), line, "", func() { a.previewArchetype(arch) })
		}
		handoffRow(c, "detail-handoffs", func() {
			if primaryButton(c.Key("detail-tts"), i18n.T("Use in Text to speech")).Disabled(g.using).Clicked() {
				a.useArchetype(arch, true)
			}
		}, func() {
			if ui.Button(c.Key("detail-stories"), i18n.T("Use as a story character")).Disabled(g.using).Clicked() {
				a.useArchetypeThen(arch, a.useVoiceInStories)
			}
			if ui.Button(c.Key("detail-book"), i18n.T("Narrate an audiobook")).Disabled(g.using).Clicked() {
				a.useArchetypeThen(arch, a.useVoiceInAudiobook)
			}
			if saved == nil {
				if ui.Button(c.Key("detail-save"), i18n.T("Save to My voices")).Disabled(g.using).Clicked() {
					a.saveArchetype(arch)
				}
			} else if ghostButton(c.Key("detail-mine"), i18n.T("Open in My voices")+" ›").Clicked() {
				a.openVoiceEditor(saved.ID)
			}
		})
		a.archetypeTry(c, arch)
		if arch.Designed() && arch.Persona != "" {
			detailSection(c, "detail-persona", i18n.T("How it was designed"), func() {
				ui.Text(c, arch.Persona).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Selectable()
				if ghostButton(c.Key("detail-design"), i18n.T("Open in Design without saving")).Clicked() {
					a.designFromArchetype(arch)
				}
			})
		}
	})
}

// archetypeTry is a Discover voice's 「试一句」: a built-in speaker says
// it with its model; a designed voice with the model that designed it.
func (a *app) archetypeTry(c *ui.Context, arch gallery.Archetype) {
	t := c.Theme()
	g := &a.gallery
	_, lang := arch.PreviewLine()
	if !g.try.set {
		g.try.text, g.try.set = tryLine(lang), true
	}
	e, ok := archetypeModel(arch)
	detailSection(c, "detail-try", i18n.T("Try a line"), func() {
		if !ok {
			caption(c, i18n.T("No model can speak with this voice."))
			return
		}
		ui.TextArea(c.Key("try-text"), &g.try.text).Lines(2, 4).Label(i18n.T("Test phrase"))
		key := "arch:" + arch.ID
		j := a.voiceJob("try", key)
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			caption(c, i18n.T("Said by %s", e.DisplayName())).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			if ui.Button(c.Key("try-go"), i18n.T("Generate")).Disabled(j.running() || strings.TrimSpace(g.try.text) == "").Clicked() {
				a.tryArchetype(arch)
			}
		})
		a.tryResult(c, "try", j, nil)
	})
}

// archetypeModel is the model that speaks a Discover voice: a built-in
// speaker's, or the one that designed it.
func archetypeModel(arch gallery.Archetype) (catalog.Entry, bool) {
	if arch.Kind == gallery.KindBuiltin {
		return catalog.Builtin().ByID(arch.Model)
	}
	return catalog.Builtin().ByID(galleryDesignModel)
}

// tryArchetype says 「试一句」's line with a Discover voice, unsaved.
func (a *app) tryArchetype(arch gallery.Archetype) {
	g := &a.gallery
	e, ok := archetypeModel(arch)
	text := strings.TrimSpace(g.try.text)
	if !ok || text == "" {
		return
	}
	_, lang := arch.PreviewLine()
	tk := core.Take{Text: text, Model: e.Repo, Language: modelLanguage(e, lang)}
	if arch.Kind == gallery.KindBuiltin {
		tk.Voice = arch.Speaker
	} else {
		low, err := vd.Lower(designTarget(e), arch.Recipe(), vd.Options{Lang: designTextLang(lang)})
		if err != nil {
			a.toastWarn(err.Error())
			return
		}
		tk.Voice, tk.Seed = low.Voice, low.Seed
	}
	a.voiceGenerate("try", "arch:"+arch.ID, tk, func() { a.tryArchetype(arch) })
}

// --- playing --------------------------------------------------------------

// togglePreview plays (or stops) a preview; one plays at a time.
func (a *app) togglePreview(p voicePreview) {
	if !p.ok() {
		return
	}
	if a.pl.Playing(p.id) {
		a.pl.Stop()
		return
	}
	if err := a.pl.PlayPreview(p.id, p.data, p.path, 0); err != nil {
		a.toastWarn(err.Error())
	}
}

// previewSaved plays (or stops) a saved voice: its sample or reference,
// a built-in speaker's clip; a designed voice without a sample renders
// one first.
func (a *app) previewSaved(v *core.Voice) {
	if p := a.savedVoicePreview(v); p.ok() {
		a.togglePreview(p)
		return
	}
	a.playVoice(v)
}

// savedPlaying reports whether a saved voice's preview plays.
func (a *app) savedPlaying(v *core.Voice) bool {
	if p := a.savedVoicePreview(v); p.ok() && a.pl.Playing(p.id) {
		return true
	}
	return a.voicePlaying(v)
}
