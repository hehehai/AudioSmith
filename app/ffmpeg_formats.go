package main

// Saving in ffmpeg's formats (M7a): the books' Save book… and Stems menus
// offer MP3 and Opus when ffmpeg is set up (Settings → Audio tools), and
// say it is needed when it is not. (Takes export through the Export audio
// dialog, export_dialog.go.)

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// formatLabel names a format in menus.
func formatLabel(format string) string {
	switch format {
	case ffmpeg.MP3:
		return i18n.T("MP3 (192 kb/s)")
	case ffmpeg.FLAC:
		return i18n.T("FLAC (lossless)")
	case ffmpeg.Opus:
		return i18n.T("Opus (96 kb/s)")
	}
	return strings.ToUpper(format)
}

// formatExt is a format's file extension.
func formatExt(format string) string {
	if format == ffmpeg.Opus {
		return "opus"
	}
	return format
}

// ffmpegMenuItems adds an item per format ffmpeg writes to m, calling
// save with the one chosen; without ffmpeg, one disabled item says why.
func (a *app) ffmpegMenuItems(m *ui.Menu, formats []string, save func(format string)) {
	tool := a.mediaTool()
	any := false
	for _, f := range formats {
		if tool.CanEncode(f) {
			if !any {
				m.Separator()
				any = true
			}
			if m.Item(formatLabel(f)).Chosen() {
				save(f)
			}
		}
	}
	if !any {
		m.Separator()
		m.Item(i18n.T("MP3, FLAC, Opus: set up ffmpeg in Settings → Audio tools")).Disabled(true)
	}
}

// saveEncodedAs asks for a destination (named name) and runs write there,
// with a toast for the outcome.
func (a *app) saveEncodedAs(name, format string, write func(dst string) error) {
	ext := formatExt(format)
	run := func(dst string) {
		if filepath.Ext(dst) == "" {
			dst += "." + ext
		}
		a.toast(i18n.T("Saving %s…", filepath.Base(dst)))
		a.background(func() {
			err := write(dst)
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Export failed: %v", err))
				} else {
					a.toast(i18n.T("Saved %s", filepath.Base(dst)))
				}
			})
		})
	}
	if a.win == nil {
		if ffmpegSaveDir != "" {
			run(filepath.Join(ffmpegSaveDir, name))
		}
		return
	}
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Save audio as"), DefaultPath: name,
			Filters: []mygo.FileFilter{{Name: strings.ToUpper(ext), Extensions: []string{ext}}}})
		if err != nil || dst == "" {
			return
		}
		a.post(func() { run(dst) })
	}()
}

// ffmpegSaveDir is where tests save (no dialog).
var ffmpegSaveDir string

// encodeFile runs ffmpeg (the length comes from the WAV for progress).
func (a *app) encodeFile(ctx context.Context, o ffmpeg.EncodeOptions) error {
	tool := a.mediaTool()
	if tool == nil {
		return ffmpeg.ErrNotFound
	}
	return tool.EncodeAudio(ctx, o, nil)
}

// ffmpegBookItems adds the book in ffmpeg's formats, with its chapters,
// tags and cover, to Save book… (longform_view_outputs.go).
func (a *app) ffmpegBookItems(m *ui.Menu, r *lfRender) {
	out := r.body.Output
	if out == nil {
		return
	}
	rec, spec := r.rec, r.body.Spec
	a.ffmpegMenuItems(m, []string{ffmpeg.MP3, ffmpeg.Opus}, func(f string) {
		a.saveRenderEncoded(r, "book", bookEncodeOptions(rec, &spec, out, f), services.SafeFileName(rec.Title, "audiobook"))
	})
}

// bookEncodeOptions encode a render's book as f (MP3, Opus) with its
// chapters, cover and tags (the API's export_longform uses it too).
func bookEncodeOptions(rec core.Render, spec *services.LongformSpec, out *services.AssembleResult, f string) ffmpeg.EncodeOptions {
	o := ffmpeg.EncodeOptions{Input: out.Book, Format: f, Cover: spec.Book.Cover, Duration: out.Seconds,
		Metadata: map[string]string{"title": rec.Title, "album": rec.Title, "artist": spec.Book.Author, "album_artist": spec.Book.Author,
			"composer": spec.Book.Narrator, "date": spec.Book.Year, "genre": spec.Book.Genre, "comment": spec.Book.Description}}
	if f == ffmpeg.MP3 {
		o.Bitrate = 128000 // speech: smaller books, as audiobook stores ask
	}
	for i, c := range out.Chapters {
		title := c.Title
		if title == "" {
			title = services.Untitled(i + 1)
		}
		o.Chapters = append(o.Chapters, ffmpeg.Chapter{Title: title, Start: c.Start, End: c.Start + c.Seconds})
	}
	return o
}

// ffmpegStemItems adds a stem in ffmpeg's formats to the Stems menu.
func (a *app) ffmpegStemItems(m *ui.Menu, r *lfRender, s services.Stem, name string) {
	tool := a.mediaTool()
	if !tool.CanEncode(ffmpeg.MP3) {
		return
	}
	if m.Item(i18n.T("%s (MP3)", name)).Chosen() {
		o := ffmpeg.EncodeOptions{Input: s.File, Format: ffmpeg.MP3, Metadata: map[string]string{"title": r.rec.Title + " · " + name}}
		a.saveRenderEncoded(r, "stem", o, services.SafeFileName(r.rec.Title+" "+name, "stem"))
	}
}

// saveRenderEncoded saves an encoded file of a render and records it
// with the project's exports.
func (a *app) saveRenderEncoded(r *lfRender, kind string, o ffmpeg.EncodeOptions, base string) {
	rec, lib := r.rec, a.lib.svc.Library
	a.saveEncodedAs(base+"."+formatExt(o.Format), o.Format, func(dst string) error {
		o.Output = dst
		ctx := context.Background()
		if err := a.encodeFile(ctx, o); err != nil {
			return err
		}
		_, err := lib.RecordExport(ctx, core.Export{Kind: kind, SourceID: rec.ID, ProjectID: rec.ProjectID, Title: rec.Title, Path: dst, Format: formatExt(o.Format)})
		return err
	})
}

// firstWords is the start of a text, for a title tag.
func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		return strings.Join(f[:n], " ") + "…"
	}
	return strings.Join(f, " ")
}
