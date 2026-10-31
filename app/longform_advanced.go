package main

// Stories and Audiobook, simplified (thread #42, phase D): one 「高级」
// section at the foot of the inspector (language, the production
// settings, a stem per character; a story's M4B book with its cover and
// details; an audiobook's own pronunciations), and the project in a
// 「项目 ▾」 menu of the header, its name being the title.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfDefaultProduction is a new page's production settings.
func lfDefaultProduction(p *lfPage) services.Production {
	if p.kind == core.ProjectStory {
		return services.DefaultStoryProduction()
	}
	return services.DefaultProduction()
}

// lfChanges names what a page's Advanced settings change from the
// recommendation (the pacing is a core choice: not counted).
func (a *app) lfChanges(p *lfPage) []string {
	d := &p.doc
	pr, def := d.Production, lfDefaultProduction(p)
	e := entryFor(d.Model)
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(pr.Steps != 0 && modelInfo(e.Repo).Steps, i18n.T("Voice refinement"))
	add(pr.Guidance != 0 && e.Has(catalog.CapGuidance), i18n.T("Guidance"))
	add(pr.VaryRepeats, i18n.T("Vary repeated lines"))
	_, paced := services.GapsOf(pr.Pacing)
	add(!(paced && p.kind == core.ProjectStory) && pr.LineGapMs != def.LineGapMs, i18n.T("Gap between lines"))
	add(pr.ParagraphGapMs != def.ParagraphGapMs, i18n.T("Gap between paragraphs"))
	add(pr.KeepEdges, i18n.T("Trim engine silence"))
	add(pr.Seed != 0, i18n.T("Seed"))
	add(p.kind == core.ProjectStory && pr.NoMerge, i18n.T("Merge a character's lines"))
	add(p.kind == core.ProjectStory && pr.NoLevel, i18n.T("Even out each voice"))
	add(p.kind == core.ProjectStory && pr.NoEmotionGaps, i18n.T("Pauses follow the emotion"))
	add(p.kind == core.ProjectStory && pr.NoTracks, i18n.T("Each character's part in one go"))
	return out
}

// lfRestore brings the production settings back to the recommended ones,
// keeping the pacing.
func (a *app) lfRestore(p *lfPage) {
	pacing := p.doc.Production.Pacing
	p.doc.Production = lfDefaultProduction(p)
	p.doc.Production.Pacing = pacing
}

// lfAdvanced is a page's 「高级」 section.
func (a *app) lfAdvanced(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	d := &p.doc
	e := entryFor(d.Model)
	page := "story"
	if p.kind == core.ProjectAudiobook {
		page = "audiobook"
	}
	ui.Column(c.Key("lf-advanced")).Padding(px(t, sp1), 0, 0).Children(func() {
		a.advancedSection(c, page, a.lfChanges(p), locked, func() { a.lfRestore(p) }, func() {
			if e.Has(catalog.CapLanguage) || e.ID == "custom" {
				ui.Field(c, i18n.T("Language"), func() { a.lfLanguage(c, d, e, locked) })
			}
			if p.kind == core.ProjectStory && d.Production.Pacing != services.PacingFixed {
				merge := !d.Production.NoMerge
				formRow(c, "perf-merge-row", i18n.T("Merge a character's lines"), i18n.T("Consecutive lines of one character are said in one go, so the voice flows from one into the next; they are split back into lines for the player and stems (or made one by one when they cannot be)."), func() {
					if inkSwitch(c.Key("perf-merge"), &merge).Label(i18n.T("Merge a character's lines")).Disabled(locked).Changed() {
						d.Production.NoMerge = !merge
					}
				})
				level := !d.Production.NoLevel
				formRow(c, "perf-level-row", i18n.T("Even out each voice"), i18n.T("Brings each character's lines toward one loudness, so a voice does not jump in level from line to line; louder or softer directions keep part of their difference."), func() {
					if inkSwitch(c.Key("perf-level-switch"), &level).Label(i18n.T("Even out each voice")).Disabled(locked).Changed() {
						d.Production.NoLevel = !level
					}
				})
				emoGaps := !d.Production.NoEmotionGaps
				formRow(c, "perf-emotion-gaps-row", i18n.T("Pauses follow the emotion"), i18n.T("Lines with an emotion change the pauses around them: excited or angry lines come quicker, hesitant or sad ones after a longer pause."), func() {
					if inkSwitch(c.Key("perf-emotion-gaps"), &emoGaps).Label(i18n.T("Pauses follow the emotion")).Disabled(locked).Changed() {
						d.Production.NoEmotionGaps = !emoGaps
					}
				})
				tracks := !d.Production.NoTracks
				formRow(c, "perf-tracks-row", i18n.T("Each character's part in one go"), i18n.T("A character's lines with emotions are said in one synthesis per stretch of one feeling, so the voice carries on from line to line, and cut apart again with the forced aligner (%s). Narration, and a stretch that cannot be cut cleanly, are made line by line. Off: every line on its own.", modelRefID(catalog.DefaultAlign)), func() {
					if inkSwitch(c.Key("perf-tracks"), &tracks).Label(i18n.T("Each character's part in one go")).Disabled(locked).Changed() {
						d.Production.NoTracks = !tracks
					}
				})
			}
			a.lfProduction(c, p, locked)
			formRow(c, "book-stems-row", i18n.T("A stem per character"), i18n.T("Each character's lines on the book's timeline, silent elsewhere, for mixing. As large as the book per character."), func() {
				inkSwitch(c.Key("book-stems"), &d.Stems).Label(i18n.T("A stem per character")).Disabled(locked)
			})
			if p.kind == core.ProjectStory {
				sectionTitle(c, i18n.T("As an audiobook"))
				a.lfFormat(c, p, locked)
				if d.Format == services.FormatM4B {
					a.lfBookCard(c, p, locked)
				}
				return
			}
			sectionTitle(c, i18n.T("Pronunciation"))
			a.lexiconCard(c, p, locked)
		})
	})
}

// lfProjectMenu is the header's 「项目 ▾」: save (the title is the
// project's name), save a copy, new, the recent projects of the page's
// kind, and the Projects page.
func (a *app) lfProjectMenu(c *ui.Context, p *lfPage, locked bool) {
	label := i18n.T("Project")
	var b ui.Element
	if p.narrow {
		b = iconButton(c.Key("lf-proj"), icons.FolderWithFiles, label)
	} else {
		b = dropButton(c.Key("lf-proj"), icons.FolderWithFiles, label)
	}
	tip := i18n.T("Save, open or start a project")
	if p.project != "" && p.unsaved() {
		tip = i18n.T("Unsaved changes")
	}
	b.Tooltip(tip).Menu(func(m *ui.Menu) {
		if p.project != "" && p.unsaved() {
			m.Item(i18n.T("Unsaved changes")).Disabled(true)
		}
		if m.Item(i18n.T("Save")).Chosen() {
			a.saveLf(p, false)
		}
		if p.project != "" && m.Item(i18n.T("Save a copy")).Chosen() {
			a.saveLf(p, true)
		}
		if m.Item(i18n.T("New")).Disabled(locked).Chosen() {
			if p.unsaved() {
				p.confirm, p.confirmOpen = "new", true
			} else {
				a.newLf(p)
			}
		}
		if len(p.projects) > 0 {
			m.Separator()
			for i, pr := range p.projects {
				if i == 8 {
					break
				}
				id := pr.ID
				title := strings.TrimSpace(pr.Title)
				if title == "" {
					title = i18n.T("Untitled")
				}
				if m.Item(title).Checked(id == p.project).Disabled(locked || id == p.project).Chosen() {
					if p.unsaved() {
						p.confirm, p.confirmOpen = id, true
					} else {
						a.openLfProject(id)
					}
				}
			}
		}
		m.Separator()
		if m.Item(i18n.T("All projects…")).Chosen() {
			a.navigate("/projects")
		}
	})
}

// lfProjectName is the name a page is saved under: its title, else
// "Untitled story" / "Untitled audiobook".
func lfProjectName(p *lfPage) string {
	if t := strings.TrimSpace(p.doc.Title); t != "" {
		return t
	}
	if p.kind == core.ProjectAudiobook {
		return i18n.T("Untitled audiobook")
	}
	return i18n.T("Untitled story")
}
