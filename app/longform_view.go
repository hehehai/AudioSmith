package main

// Panels the Stories and Audiobook pages share: the layout, the setup
// card, the confirmation before replacing a draft, and clearing the
// script. The production overrides and book card, the render bar and the
// outputs are in longform_view_book.go, longform_view_render.go and
// longform_view_outputs.go.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfLayout places a longform page's inspector: beside the main column
// when both fit, else a drawer; it also notes whether the main column is
// narrow (rows fold their actions into ⋯, the header shows icons).
func (a *app) lfLayout(c *ui.Context, p *lfPage) (inline, shown bool) {
	t := c.Theme()
	inline, shown = p.panel.layout(a, c, px(t, inspectorWidth), px(t, 480))
	w := a.pageWidth(c)
	if inline && shown {
		w -= laneWidth(t, px(t, inspectorWidth))
	}
	p.narrow = w < px(t, 600)
	return inline, shown
}

// lfInspector is a longform page's inspector: its sections, beside the
// page or as a drawer.
func (a *app) lfInspector(c *ui.Context, p *lfPage, inline, shown bool, sections func()) {
	t := c.Theme()
	p.panel.view(a, c, "lf-inspector", i18n.T("Inspector"), inline, shown, px(t, inspectorWidth), func() {
		ui.Column(c).Padding(px(t, sp2), px(t, sp4), px(t, sp5)).Gap(px(t, sp1)).Children(sections)
	})
}

// lfSection is a collapsible section of a page's inspector; summary
// shows on its header (a model, a count).
func (a *app) lfSection(c *ui.Context, p *lfPage, key, title, summary string, fn func()) {
	t := c.Theme()
	open := p.open[key]
	var trailing func()
	if summary != "" {
		trailing = func() {
			caption(c, summary).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).MaxWidth(px(t, 150))
		}
	}
	collapsible(c.Key("sec-"+key), title, &open, trailing, func() {
		ui.Column(c).Gap(px(t, sp3)).Padding(0, 0, px(t, sp2)).Children(fn)
	})
	p.open[key] = open
}

// lfCount is a section's count summary, "" for none.
func lfCount(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// lfSetup is the setup card: title (the project's name), model, default
// voice, speed, and an audiobook's format (the rest is under Advanced,
// longform_advanced.go).
func (a *app) lfSetup(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	d := &p.doc
	placeholder := i18n.T("Untitled story")
	if p.kind == core.ProjectAudiobook {
		placeholder = i18n.T("Untitled audiobook")
	}
	ui.Field(c, i18n.T("Title"), func() {
		ui.TextInput(c.Key("lf-title"), &d.Title).Placeholder(placeholder).Label(i18n.T("Title")).Disabled(locked)
	})
	e := entryFor(d.Model)
	ui.Field(c, i18n.T("Model"), func() {
		a.modelPicker(c, "lf-model", lfWorkflow(p), e, func(ne catalog.Entry) {
			d.Model, p.auto = ne.Repo, false
		}, pickerOpts{Locked: locked, Dialogue: a.storyDialoguePick(p)})
	})
	if m := a.model(e); e.ID != "custom" && m.known && m.local.State != hub.Downloaded {
		ui.Row(c.Key("lf-download")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			iconMark(c, icons.DangerTriangle, t.Warning, "")
			caption(c, i18n.T("%s is not downloaded yet.", modelRef(e))).TextColor(t.Warning).Grow(1).MinWidth(0)
			if !m.downloading && secondaryIconButton(c.Key("lf-dl"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Tooltip(e.Repo).Clicked() {
				a.download(e)
			}
		})
	}
	label := i18n.T("Narrator voice")
	if p.kind == core.ProjectStory {
		label = i18n.T("Default voice")
	}
	ui.Field(c, label, func() {
		before := d.Voice
		a.lfVoiceSelect(c, "lf-voice-"+string(p.kind), &d.Voice, e, i18n.T("None"), locked)
		if d.Voice != before {
			p.auto = false
		}
	})
	if e.NeedsReference {
		caption(c.Key("lf-needs-ref"), i18n.T("%s only clones voices: each line needs a saved cloned voice.", modelRef(e)))
	} else if len(a.lfVoices(e)) == 0 {
		caption(c, i18n.T("No voices for this model yet: clone or design one first."))
	}
	if e.Has(catalog.CapSpeed) {
		if d.Speed <= 0 {
			d.Speed = 1
		}
		ui.Field(c, i18n.T("Speed %.2f×", d.Speed), func() {
			snapped(ui.Slider(c.Key("lf-speed"), &d.Speed, 0.5, 2).Label(i18n.T("Speed")).Disabled(locked), &d.Speed, 0.05)
		})
		if p.kind == core.ProjectStory {
			n := 0
			for _, l := range d.Lines {
				if l.Speed > 0 {
					n++
				}
			}
			if n > 0 {
				ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
					caption(c, i18n.N(n, "%d line has its own speed", "%d lines have their own speed", n)).Grow(1).MinWidth(0)
					if ghostButton(c.Key("lf-speed-all"), i18n.T("Use for all lines")).Disabled(locked).Clicked() {
						for i := range d.Lines {
							d.Lines[i].Speed = 0
						}
					}
				})
			}
		}
	}
	if p.kind == core.ProjectAudiobook {
		a.lfFormat(c, p, locked)
	}
}

// lfFormat is the render's format: WAV, or M4B with chapters once the
// engine encodes.
func (a *app) lfFormat(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	d := &p.doc
	formats := []string{i18n.T("WAV")}
	if a.lfEncode != nil && a.lfEncode() {
		formats = append(formats, i18n.T("M4B (chapters)"))
	}
	f := 0
	if d.Format == services.FormatM4B && len(formats) > 1 {
		f = 1
	}
	ui.Field(c, i18n.T("Format"), func() {
		if segmentedFit(c.Key("lf-format"), &f, formats...).Label(i18n.T("Format")).Disabled(locked).Changed() {
			d.Format = services.FormatWAV
			if f == 1 {
				d.Format = services.FormatM4B
			}
		}
	})
	if len(formats) == 1 {
		caption(c, i18n.T("M4B with chapters and cover comes once the engine can encode it; books are WAV until then.")).TextColor(tok(t).Text3)
	}
}

// lfVoiceSelect picks a voice reference for model e in the shared voice
// picker (voice_picker.go): none is the label of "" (the default voice);
// key must be unique in the app (the picker's state is kept by it).
func (a *app) lfVoiceSelect(c *ui.Context, key string, ref *string, e catalog.Entry, none string, locked bool) {
	opts := []voiceOption{{value: "", label: none}}
	seen := map[string]int{}
	for _, v := range a.lfVoices(e) {
		l := v.label
		if n := seen[l]; n > 0 {
			l += " (" + strconv.Itoa(n+1) + ")"
		}
		seen[v.label]++
		op := voiceOption{value: v.ref, label: l}
		if id, ok := strings.CutPrefix(v.ref, services.SpeakerRef); ok {
			op.group, op.preview = i18n.T("Model voices"), speakerPreview(e, id)
		} else if sv := a.findVoice(v.ref); sv != nil {
			op.group, op.voice, op.sub, op.preview = i18n.T("My voices"), sv, voiceKindLabel(sv), a.savedVoicePreview(sv)
		}
		opts = append(opts, op)
	}
	if *ref != "" && !slices.ContainsFunc(opts, func(o voiceOption) bool { return o.value == *ref }) {
		opts = append(opts, voiceOption{value: *ref, label: a.lfVoiceLabel(*ref) + " ⚠", warn: true})
	}
	if v, ok := a.voicePicker(c, key, *ref, opts, voicePickerOpts{label: none, locked: locked}); ok {
		*ref = v
	}
}

// lfLanguage is a select of the model's languages (codes), Auto first.
func (a *app) lfLanguage(c *ui.Context, d *services.LongformDoc, e catalog.Entry, locked bool) {
	codes, _, all := languageSupport(e)
	if all {
		codes = []string{"en", "zh", "ja", "ko", "es", "fr", "de", "it", "pt", "ru"}
	}
	labels, values := []string{i18n.T("Auto-detect")}, []string{""}
	for _, code := range codes {
		name := code
		if l, ok := lang.Lookup(code); ok {
			name = strings.TrimSpace(l.Flag() + " " + l.NameIn(i18n.Locale()))
		}
		labels, values = append(labels, name), append(values, code)
	}
	cur := labels[0]
	if i := slices.Index(values, d.Language); i >= 0 {
		cur = labels[i]
	}
	if ui.Select(c.Key("lf-lang"), &cur, labels).Label(i18n.T("Language")).Disabled(locked).Changed() {
		if i := slices.Index(labels, cur); i >= 0 {
			d.Language = values[i]
		}
	}
}

// lfConfirm asks before replacing or deleting.
func (a *app) lfConfirm(c *ui.Context, p *lfPage) {
	if !p.confirmOpen {
		return
	}
	switch {
	case p.confirm == "new":
		if ui.AlertDialog(c, &p.confirmOpen, i18n.T("Start a new project?"), i18n.T("The current draft has unsaved changes. Save your current work first."),
			i18n.T("Cancel"), i18n.T("Discard and start new")) == 1 {
			a.newLf(p)
		}
	case p.confirm == "clear":
		msg := i18n.T("Remove the whole manuscript? This cannot be undone.")
		if p.kind == core.ProjectStory {
			msg = i18n.N(len(p.doc.Lines), "Clear %d line and the text being reviewed? The cast stays. This cannot be undone.",
				"Clear all %d lines and the text being reviewed? The cast stays. This cannot be undone.", len(p.doc.Lines))
		}
		if ui.AlertDialog(c, &p.confirmOpen, i18n.T("Clear the script?"), msg, i18n.T("Cancel"), i18n.T("Clear")) == 1 {
			a.clearLf(p)
		}
	case p.confirm == "changed":
		// Saving found the project saved elsewhere (#101 M7).
		title := strings.TrimSpace(p.saveName)
		switch ui.AlertDialog(c, &p.confirmOpen, i18n.T("“%s” was changed elsewhere", title),
			i18n.T("It was saved from another place, such as an agent through the Local API, after you opened it here. Your changes on this page are not saved yet."),
			i18n.T("Cancel"), i18n.T("Save as a copy"), i18n.T("Replace it with mine")) {
		case 1:
			a.saveLf(p, true)
		case 2:
			a.saveLfAs(p, false, true)
		}
	case strings.HasPrefix(p.confirm, "delete:"):
		id := strings.TrimPrefix(p.confirm, "delete:")
		if ui.AlertDialog(c, &p.confirmOpen, i18n.T("Delete this project permanently?"), i18n.T("Its finished renders and exported files are kept."),
			i18n.T("Cancel"), i18n.T("Delete")) == 1 {
			a.deleteLfProject(id)
		}
	default:
		title := p.confirm
		for _, pr := range p.projects {
			if pr.ID == p.confirm {
				title = pr.Title
			}
		}
		if ui.AlertDialog(c, &p.confirmOpen, i18n.T("Open “%s”?", title), i18n.T("It replaces the current draft. Save your current work first."),
			i18n.T("Cancel"), i18n.T("Open")) == 1 {
			a.openLfProject(p.confirm)
		}
	}
}

// clearLf empties the script (the cast stays).
func (a *app) clearLf(p *lfPage) {
	p.doc.Lines, p.doc.ImportText, p.sel = nil, "", -1
	if p.kind == core.ProjectAudiobook {
		a.setManuscript(p, "")
	}
}

// clearLfUndo empties a story's script at once, with Undo.
func (a *app) clearLfUndo(p *lfPage) {
	lines, review := slices.Clone(p.doc.Lines), p.doc.ImportText
	a.clearLf(p)
	a.toastUndo(i18n.N(len(lines), "Cleared %d line", "Cleared %d lines", len(lines)), func() {
		p.doc.Lines, p.doc.ImportText = lines, review
	})
}
