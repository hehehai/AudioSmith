package main

// What the pages tell the model picker about their content (thread
// #167), so it recommends models that fit it: the content's languages
// (VoxCPM2, then OmniVoice, where the usual models don't speak them), a
// story directed line by line (VoxCPM2 keeps cloned voices and hears the
// directions) and a Chinese dialect on the Design page (VoxCPM2). Only
// recommended: no page switches its model or changes its default.

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfContentLang is a Stories or Audiobook project's language: the one
// set, else read from the letters of its first lines or script ("" none).
func lfContentLang(d *services.LongformDoc) string {
	if d.Language != "" {
		return d.Language
	}
	const enough = 2000 // bytes: plenty to tell the script, cheap each frame
	var b strings.Builder
	for _, l := range d.Lines {
		if b.Len() >= enough {
			break
		}
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	if b.Len() < enough {
		b.WriteString(d.Script[:min(len(d.Script), enough)])
	}
	return textLang(b.String())
}

// lfUses: a story with a scene, personas, directions or emotions calls
// for a model whose clones follow them.
func lfUses(p *lfPage) []catalog.Use {
	if p.kind == core.ProjectStory && storyDirected(&p.doc) {
		return []catalog.Use{catalog.UseDirectedClone}
	}
	return nil
}

// designUses: a Chinese dialect calls for the dialect designer.
func designUses(d *designState) []catalog.Use {
	if d.attrs.Dialect != "" {
		return []catalog.Use{catalog.UseDialect}
	}
	return nil
}

// useModel is the first model presets recommend for use u in workflow
// w (false when none).
func useModel(u catalog.Use, w catalog.Workflow) (catalog.Entry, bool) {
	for _, e := range catalog.Builtin().ForUse(u, w) {
		return e, true
	}
	return catalog.Entry{}, false
}

// designDialectNote, with a Chinese dialect picked and another model
// chosen: the model recommended for dialects (VoxCPM2), with a link that
// picks it.
func (a *app) designDialectNote(c *ui.Context, cur catalog.Entry, ms []catalog.Entry) {
	d := &a.design
	if d.attrs.Dialect == "" {
		return
	}
	rec, ok := useModel(catalog.UseDialect, catalog.WorkflowDesign)
	if !ok || rec.ID == cur.ID || !slices.ContainsFunc(ms, func(m catalog.Entry) bool { return m.ID == rec.ID }) {
		return
	}
	ui.Row(c.Key("design-dialect-rec")).Gap(px(c.Theme(), sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		caption(c, i18n.T("For Chinese dialects, %s is recommended.", modelRef(rec))).TextColor(tok(c.Theme()).Text3).MinWidth(0).Shrink(1)
		if linkButton(c.Key("design-dialect-use"), i18n.T("Use %s", rec.DisplayName())).Clicked() {
			d.model = rec.Label
		}
	})
}
