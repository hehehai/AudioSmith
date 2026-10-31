package main

// The Stories and Audiobook pages' production overrides and the book card
// (details, cover, loudness); split from longform_view.go.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfProduction are the production settings (under Advanced).
func (a *app) lfProduction(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	pr := &p.doc.Production
	e := entryFor(p.doc.Model)
	pre, _ := catalog.PresetFor(e.ID)
	rec := pre.ParamsFor(lfWorkflow(p))
	if modelInfo(e.Repo).Steps {
		cur := float64(pr.Steps)
		if cur == 0 {
			cur = services.StepsBalanced
			if rec.Steps != nil {
				cur = float64(*rec.Steps)
			}
		}
		tier := slices.Index(tierSteps, cur)
		fieldInfo(c, i18n.T("Voice refinement"), i18n.T("More steps sound cleaner and take longer."), func() {
			if segmentedFit(c.Key("lf-steps"), &tier, i18n.T("Fast"), i18n.T("Balanced"), i18n.T("Quality")).Label(i18n.T("Voice refinement")).Disabled(locked).Changed() && tier >= 0 {
				pr.Steps = int(tierSteps[tier])
			}
		})
	}
	if e.Has(catalog.CapGuidance) {
		g := shown(pr.Guidance, rec.Guidance, 2)
		ui.Field(c, i18n.T("Guidance %.1f", g), func() {
			if snapped(slimSlider(c.Key("lf-cfg"), &g, 0, 4).Label(i18n.T("Guidance")).Disabled(locked), &g, 0.1).Changed() {
				pr.Guidance = g
			}
		})
	}
	formRow(c, "lf-vary-row", i18n.T("Vary repeated lines"), i18n.T("Give identical lines takes of their own instead of reusing one recording."), func() {
		inkSwitch(c.Key("lf-vary"), &pr.VaryRepeats).Label(i18n.T("Vary repeated lines")).Disabled(locked)
	})
	if _, paced := services.GapsOf(pr.Pacing); paced && p.kind == core.ProjectStory {
		caption(c.Key("lf-paced"), i18n.T("Gaps between lines follow the pacing: %s (Performance).", pacingLabel(pr.Pacing)))
	} else {
		lg := float64(pr.LineGapMs)
		ui.Field(c, i18n.T("Gap between lines %d ms", pr.LineGapMs), func() {
			if snapped(slimSlider(c.Key("lf-line-gap"), &lg, 0, 2000).Label(i18n.T("Gap between lines")).Disabled(locked), &lg, 50).Changed() {
				pr.LineGapMs = int(lg)
			}
		})
	}
	pg := float64(pr.ParagraphGapMs)
	ui.Field(c, i18n.T("Gap between paragraphs %d ms", pr.ParagraphGapMs), func() {
		if snapped(slimSlider(c.Key("lf-para-gap"), &pg, 0, 3000).Label(i18n.T("Gap between paragraphs")).Disabled(locked), &pg, 50).Changed() {
			pr.ParagraphGapMs = int(pg)
		}
	})
	trim := !pr.KeepEdges
	formRow(c, "lf-trim-row", i18n.T("Trim engine silence"), i18n.T("Cut the silence engines pad onto each chunk before joining, so the gaps above are what you hear."), func() {
		if inkSwitch(c.Key("lf-trim"), &trim).Label(i18n.T("Trim engine silence")).Disabled(locked).Changed() {
			pr.KeepEdges = !trim
		}
	})
	seed := float64(pr.Seed)
	formRow(c, "lf-seed-row", i18n.T("Seed"), i18n.T("0: the project's own seed (%d), so a render can be made again from the cache.", p.doc.Seed), func() {
		if numberInput(c.Key("lf-seed"), &seed, 0, 1<<31-1, 1, numOpts{Label: i18n.T("Seed"), Width: px(t, 160), Disabled: locked}) {
			pr.Seed = int64(seed)
		}
	})
}

// Loudness choices of the book card.
var lfLoudness = []string{services.LoudnessOff, services.LoudnessACX, services.LoudnessPodcast}

// lfBookCard is the book's details, cover and loudness.
func (a *app) lfBookCard(c *ui.Context, p *lfPage, locked bool) {
	t := c.Theme()
	b := &p.doc.Book
	for _, f := range []struct {
		key, label string
		v          *string
	}{
		{"author", i18n.T("Author"), &b.Author}, {"narrator", i18n.T("Narrator"), &b.Narrator},
		{"year", i18n.T("Year"), &b.Year}, {"genre", i18n.T("Genre"), &b.Genre},
	} {
		ui.Field(c, f.label, func() {
			ui.TextInput(c.Key("book-"+f.key), f.v).Label(f.label).Disabled(locked)
		})
	}
	ui.Field(c, i18n.T("Description"), func() {
		ui.TextArea(c.Key("book-desc"), &b.Description).Label(i18n.T("Description")).Lines(2, 6).MinHeight(0).Disabled(locked)
	})
	ui.Row(c.Key("book-cover")).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if bmp := a.coverBitmap(b.Cover); bmp != nil {
			ui.Image(c, bmp).Width(px(t, 36)).Height(px(t, 48)).Radius(px(t, 4))
		}
		name := i18n.T("No cover")
		if b.Cover != "" {
			name = filepath.Base(b.Cover)
		}
		truncText(c, name).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Grow(1)
		if ghostButton(c.Key("cover-add"), i18n.T("Choose cover…")).Tooltip(i18n.T("PNG or JPEG, at most 8 MB; embedded in M4B books")).Disabled(locked).Clicked() {
			a.chooseCover(p)
		}
		if b.Cover != "" && iconButton(c.Key("cover-remove"), icons.TrashBinMinimalistic, i18n.T("Remove")).Disabled(locked).Clicked() {
			b.Cover = ""
		}
	})
	li := max(slices.Index(lfLoudness, b.Loudness), 0)
	note := i18n.T("Chapters as rendered, not normalised.")
	switch b.Loudness {
	case services.LoudnessACX:
		note = i18n.T("Audible's ACX: −19 LUFS, peaks under −3 dBTP; each chapter is checked.")
	case services.LoudnessPodcast:
		note = i18n.T("Podcast: −16 LUFS, peaks under −1.5 dBTP.")
	}
	fieldInfo(c, i18n.T("Loudness"), note, func() {
		if segmentedFit(c.Key("book-loud"), &li, i18n.T("Off"), i18n.T("ACX"), i18n.T("Podcast")).Label(i18n.T("Loudness")).Disabled(locked).Changed() {
			b.Loudness = lfLoudness[li]
		}
	})
}

// coverCache holds decoded covers by path (main thread only).
var coverCache = map[string]*ui.Bitmap{}

// coverBitmap is a cover's thumbnail, decoded in the background.
func (a *app) coverBitmap(path string) *ui.Bitmap {
	if path == "" {
		return nil
	}
	if bmp, ok := coverCache[path]; ok {
		return bmp
	}
	coverCache[path] = nil
	a.background(func() {
		data, err := os.ReadFile(path)
		var bmp *ui.Bitmap
		if err == nil {
			bmp, _ = ui.DecodeBitmap(data)
		}
		a.post(func() { coverCache[path] = bmp })
	})
	return nil
}

// chooseCover asks for a cover image and copies it into the library.
func (a *app) chooseCover(p *lfPage) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Choose cover…"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Images"), Extensions: []string{"png", "jpg", "jpeg"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.setCover(p, paths[0]) })
	}()
}

// setCover copies an image into <data>/covers and makes it the cover.
func (a *app) setCover(p *lfPage, src string) {
	dir := filepath.Join(a.lib.store.Dir(), "covers")
	a.background(func() {
		fi, err := os.Stat(src)
		switch {
		case err != nil:
		case fi.Size() > services.MaxCoverBytes:
			err = fmt.Errorf("%s", i18n.T("%s is larger than 8 MB.", filepath.Base(src)))
		default:
			ext := strings.ToLower(filepath.Ext(src))
			if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
				err = fmt.Errorf("%s", i18n.T("A cover is a PNG or JPEG image."))
				break
			}
			dst := filepath.Join(dir, core.NewID(time.Now())+ext)
			if err = os.MkdirAll(dir, 0o755); err == nil {
				err = services.CopyFile(context.Background(), src, dst)
			}
			if err == nil {
				a.post(func() { p.doc.Book.Cover = dst })
				return
			}
		}
		a.post(func() { a.toastWarn(i18n.T("Can't use this cover: %v", err)) })
	})
}
