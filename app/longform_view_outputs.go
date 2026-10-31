package main

// The longform outputs: the finished render's files, the loudness report,
// and saving audio and chapters; split from longform_view.go.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
)

// lfOutputs shows a finished render as a flat card like the Studio's
// take: play, what it is, Save ▾ (the book, chapters, cue sheet, stems)
// and ⋯ (Show in Finder); then the loudness checks and (Audiobook) the
// read-along player.
func (a *app) lfOutputs(c *ui.Context, p *lfPage) {
	t := c.Theme()
	k := tok(t)
	r := a.lfRenders[p.render]
	if r == nil || r.rec.State != core.RenderDone || r.body.Output == nil {
		return
	}
	out := r.body.Output
	id := "book:" + r.rec.ID
	card(c.Key("lf-out")).Padding(px(t, sp3), px(t, sp4)).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			if playButton(c.Key("out-play"), a.pl.Playing(id), false, 36).Clicked() {
				if err := a.pl.Toggle(id, out.Book); err != nil {
					a.toastWarn(i18n.T("Can't play: %v", err))
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				title := i18n.T("Story ready")
				if r.rec.Kind == core.ProjectAudiobook {
					title = i18n.T("Audiobook ready")
				}
				ui.Text(c, title).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
				meta := lfClock(time.Duration(out.Seconds*float64(time.Second))) + " · " + humanBytes(out.Bytes)
				if n := doneCached(&r.body); n > 0 {
					meta += " · " + i18n.N(n, "Reused %d already-rendered chapter.", "Reused %d already-rendered chapters.", n)
				}
				truncText(c, meta).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			})
			dropButton(c.Key("out-save"), icons.Export, i18n.T("Save")).Shrink(0).Menu(func(m *ui.Menu) {
				m.Submenu(i18n.T("Whole book"), func(m *ui.Menu) {
					if out.Encoded != "" && m.Item(i18n.T("M4B audiobook")).Chosen() {
						a.saveOutput(r, "book", out.Encoded, "m4b", 0)
					}
					for _, bits := range bitDepths {
						if m.Item(i18n.T("WAV, %s", bitsLabel(bits))).Chosen() {
							a.saveOutput(r, "book", out.Book, "wav", bits)
						}
					}
					a.ffmpegBookItems(m, r) // M7a: MP3/Opus with chapters (ffmpeg_formats.go)
				})
				if m.Item(i18n.T("One WAV per chapter…")).Chosen() {
					a.saveChapters(r)
				}
				if m.Item(i18n.T("Chapter times (.txt)…")).Chosen() {
					a.saveOutput(r, "cues", out.Cues, "txt", 0)
				}
				if len(out.Stems) > 0 {
					m.Submenu(i18n.T("Stems"), func(m *ui.Menu) {
						for _, s := range out.Stems {
							name := s.Character
							if name == "" {
								name = i18n.T("Narrator")
							}
							if m.Item(name).Chosen() {
								a.saveOutput(r, "stem", s.File, "wav", 0)
							}
							a.ffmpegStemItems(m, r, s, name) // M7a: ffmpeg_formats.go
						}
					})
				}
			})
			moreMenu(c.Key("out-more"), "", func(m *ui.Menu) {
				if m.Item(i18n.T("Show in Finder")).Chosen() {
					mygo.Shell.ShowItemInFolder(out.Book)
				}
			})
		})
		if a.pl.Current() == id {
			prog := float64(a.pl.Progress(id))
			if seekSlider(c.Key("out-seek"), &prog, 0, 1).Label(i18n.T("Position")).Changed() {
				a.pl.Seek(id, float32(prog))
			}
		}
		if out.EncodeError != "" {
			ui.Row(c).Gap(px(t, 6)).Children(func() {
				iconMark(c, icons.DangerTriangle, t.Warning, "")
				caption(c, i18n.T("No M4B: %s", out.EncodeError)).TextColor(t.Warning).Grow(1).MinWidth(0)
			})
		}
		a.loudnessReport(c, p, r)
		if r.rec.Kind == core.ProjectAudiobook {
			a.readAlongView(c, p, r)
		}
	})
}

// doneCached counts the chapters a render reused.
func doneCached(b *services.RenderBody) int {
	n := 0
	for _, c := range b.Chapters {
		if c.State == services.ChapterCached {
			n++
		}
	}
	return n
}

// loudnessReport shows the book's loudness against its target and each
// chapter's checks.
func (a *app) loudnessReport(c *ui.Context, p *lfPage, r *lfRender) {
	t := c.Theme()
	out := r.body.Output
	rep := out.Loudness
	line := i18n.T("%.1f LUFS · peak %.1f dBTP · LRA %.1f LU", rep.Integrated, rep.TruePeak, rep.Range)
	if rep.Target == "" {
		caption(c, line)
		return
	}
	failed := 0
	for _, ch := range out.Chapters {
		if !ch.Report.Compliant {
			failed++
		}
	}
	open := p.open["loudness"]
	head := i18n.T("%s: every chapter passes", strings.ToUpper(rep.Target))
	if failed > 0 {
		head = i18n.N(failed, "%[2]s: %[1]d chapter fails a check", "%[2]s: %[1]d chapters fail a check", failed, strings.ToUpper(rep.Target))
	}
	var summary func()
	if !p.narrow {
		summary = func() { caption(c, line).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(line) }
	}
	collapsible(c.Key("loud"), head, &open, summary, func() {
		ui.Column(c).Gap(px(t, 4)).Children(func() {
			if p.narrow {
				caption(c, line)
			}
			for i, ch := range out.Chapters {
				title := ch.Title
				if title == "" {
					title = i18n.T("Chapter %d", i+1)
				}
				var bad []string
				for _, k := range ch.Report.Checks {
					if !k.OK {
						bad = append(bad, checkText(k.Code))
					}
				}
				floor := fmt.Sprintf("%.0f dB", ch.Report.NoiseFloor)
				if ch.Report.NoiseFloor <= -119 {
					floor = i18n.T("digital silence")
				}
				text := fmt.Sprintf("%s · %.1f LUFS · %.1f dBTP · RMS %.1f dB · %s %s", title, ch.Report.Integrated, ch.Report.TruePeak, ch.Report.RMS, i18n.T("floor"), floor)
				if len(bad) > 0 {
					text += " · " + strings.Join(bad, ", ")
				}
				ui.Row(c.Key("loud-" + strconv.Itoa(i))).Gap(px(t, sp2)).Children(func() {
					if ch.Report.Compliant {
						iconMark(c, icons.CheckCircle, t.Success, "")
					} else {
						iconMark(c, icons.CloseCircle, t.Danger, "")
					}
					ui.Text(c, text).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Grow(1).MinWidth(0)
				})
			}
		})
	})
	p.open["loudness"] = open
}

// checkText names a loudness check.
func checkText(code string) string {
	switch code {
	case "integrated":
		return i18n.T("loudness out of range")
	case "true_peak":
		return i18n.T("peaks too high")
	case "rms":
		return i18n.T("RMS out of range")
	case "noise_floor":
		return i18n.T("noise floor too high")
	case "silent":
		return i18n.T("silent")
	}
	return code
}

// saveOutput asks where to save one of a render's files, writes it (a WAV
// at bits, 0 = as it is) and records the export.
func (a *app) saveOutput(r *lfRender, kind, src, ext string, bits int) {
	name := services.SafeFileName(r.rec.Title, "audiobook")
	switch kind {
	case "cues":
		name += " cues"
	case "stem":
		name += " " + strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	rec, lib := r.rec, a.lib.svc.Library
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Save as"), DefaultPath: name + "." + ext,
			Filters: []mygo.FileFilter{{Name: strings.ToUpper(ext), Extensions: []string{ext}}}})
		if err != nil || dst == "" {
			return
		}
		if filepath.Ext(dst) == "" {
			dst += "." + ext
		}
		a.writeOutput(lib, rec, kind, src, dst, ext, bits)
	}()
}

// writeOutput copies (or converts) src to dst and records the export.
func (a *app) writeOutput(lib *services.Library, rec core.Render, kind, src, dst, ext string, bits int) {
	err := writeRenderFile(context.Background(), lib, rec, kind, src, dst, ext, bits)
	a.post(func() {
		if err != nil {
			a.toastWarn(i18n.T("Export failed: %v", err))
		} else {
			a.toast(i18n.T("Saved %s", filepath.Base(dst)))
		}
	})
}

// saveChapters copies every chapter's WAV into a folder.
func (a *app) saveChapters(r *lfRender) {
	rec, chs, lib := r.rec, slices.Clone(r.body.Output.Chapters), a.lib.svc.Library
	go func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Save chapters…"), Directory: true,
			ButtonLabel: i18n.T("Save here")})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.writeChapters(lib, rec, chs, dirs[0])
	}()
}

func (a *app) writeChapters(lib *services.Library, rec core.Render, chs []services.BookChapter, dir string) {
	_, err := writeRenderChapters(context.Background(), lib, rec, chs, dir)
	a.post(func() {
		if err != nil {
			a.toastWarn(i18n.T("Export failed: %v", err))
		} else {
			a.toast(i18n.N(len(chs), "Saved %d chapter", "Saved %d chapters", len(chs)))
		}
	})
}

// writeRenderFile copies (or converts to a WAV of bits; 0 = as it is) a
// render's file src to dst and records the export (Save and the API's
// export_longform).
func writeRenderFile(ctx context.Context, lib *services.Library, rec core.Render, kind, src, dst, ext string, bits int) error {
	var err error
	if bits > 0 {
		err = services.ConvertWAV(ctx, src, dst, bits)
	} else {
		err = services.CopyFile(ctx, src, dst)
	}
	if err == nil {
		_, err = lib.RecordExport(ctx, core.Export{Kind: kind, SourceID: rec.ID, ProjectID: rec.ProjectID, Title: rec.Title, Path: dst, Format: ext})
	}
	return err
}

// writeRenderChapters copies every chapter's WAV into dir as "NN Title.wav"
// and records each export; it returns the files written.
func writeRenderChapters(ctx context.Context, lib *services.Library, rec core.Render, chs []services.BookChapter, dir string) ([]string, error) {
	var files []string
	for i, ch := range chs {
		title := ch.Title
		if title == "" {
			title = services.Untitled(i + 1)
		}
		dst := filepath.Join(dir, fmt.Sprintf("%02d %s.wav", i+1, services.SafeFileName(title, "chapter")))
		if err := services.CopyFile(ctx, ch.File, dst); err != nil {
			return files, err
		}
		files = append(files, dst)
		lib.RecordExport(ctx, core.Export{Kind: "chapter", SourceID: rec.ID, ProjectID: rec.ProjectID, Title: rec.Title + " · " + title, Path: dst, Format: "wav"})
	}
	return files, nil
}
