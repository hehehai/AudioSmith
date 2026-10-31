package main

// A render's "How it was made" on the Assets page (the Projects page
// showed it before 0.5 F5): its voices, speed, engine, language, format,
// size, production settings and loudness.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// renderDetails is "How it was made".
func (a *app) renderDetails(c *ui.Context, id, key string) {
	t := c.Theme()
	ui.Column(c.Key("how-"+key)).Gap(px(t, sp1)).Children(func() {
		sectionTitle(c, i18n.T("How it was made"))
		r := a.renderFor(id)
		if r == nil {
			caption(c, i18n.T("Loading…"))
			return
		}
		sum := r.body.Summary
		if sum.Model == "" {
			caption(c, i18n.T("Rendered before details were recorded."))
			return
		}
		row := func(k, v string) {
			if v == "" {
				return
			}
			ui.Row(c.Key("how-" + k)).Gap(px(t, sp2)).Children(func() {
				ui.Text(c, k).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Width(px(t, 110)).Shrink(0)
				ui.Text(c, v).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0).Selectable()
			})
		}
		lang := sum.Lang
		if lang == "" {
			lang = i18n.T("Auto-detect")
		} else {
			lang = languageName(lang)
		}
		format := strings.ToUpper(sum.Format)
		if format == "" {
			format = "WAV"
		}
		row(i18n.T("Voices"), a.voiceNames(sum.Voices))
		row(i18n.T("Speed"), speedRange(sum.MinSpeed, sum.MaxSpeed))
		row(i18n.T("Engine"), modelName(sum.Model))
		row(i18n.T("Language"), lang)
		row(i18n.T("Format"), format)
		size := i18n.N(sum.Chapters, "%d chapter", "%d chapters", sum.Chapters) + " · " + i18n.N(sum.Lines, "%d line", "%d lines", sum.Lines)
		if sum.Words > 0 {
			size += " · " + i18n.N(sum.Words, "%d word", "%d words", sum.Words)
		}
		if sum.Chars > 0 {
			size += " · " + i18n.N(sum.Chars, "%d character", "%d characters", sum.Chars)
		}
		row(i18n.T("Size"), size)
		row(i18n.T("Settings"), productionText(sum))
		if out := r.body.Output; out != nil && out.Loudness.Target != "" {
			row(i18n.T("Loudness"), fmt.Sprintf("%s · %.1f LUFS · %.1f dBTP", strings.ToUpper(out.Loudness.Target), out.Loudness.Integrated, out.Loudness.TruePeak))
		}
	})
}

// productionText lists a render's production settings.
func productionText(s services.RenderSummary) string {
	p := s.Prod
	var parts []string
	if p.Steps > 0 {
		parts = append(parts, i18n.T("steps %d", p.Steps))
	}
	if p.Guidance > 0 {
		parts = append(parts, i18n.T("guidance %.1f", p.Guidance))
	}
	if p.Seed != 0 {
		parts = append(parts, i18n.T("seed %d", p.Seed))
	}
	parts = append(parts, i18n.T("line gap %d ms", p.LineGapMs), i18n.T("paragraph gap %d ms", p.ParagraphGapMs))
	if p.VaryRepeats {
		parts = append(parts, i18n.T("vary repeated lines"))
	}
	if p.KeepEdges {
		parts = append(parts, i18n.T("engine silence kept"))
	} else {
		parts = append(parts, i18n.T("engine silence trimmed"))
	}
	if s.Stems {
		parts = append(parts, i18n.T("stems"))
	}
	return strings.Join(parts, " · ")
}

