package main

// Exporting a take: the save panel, the export itself with the files
// beside it, the encoder, and the settings remembered per kind; split
// from export_dialog.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/doc/subtitle"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// exportSaveDir is where tests export (no save panel).
var exportSaveDir string

// exportChoose asks where to save, then exports there.
func (a *app) exportChoose(f *exportForm) {
	name, ext := f.fileName(), f.fmt_().ext
	if a.win == nil {
		if exportSaveDir != "" {
			a.exportTo(f, filepath.Join(exportSaveDir, name))
		}
		return
	}
	a.saveFile(mygo.SaveDialogOptions{Title: i18n.T("Export audio"), DefaultPath: name,
		Filters: fileTypes(strings.ToUpper(ext), ext)}, func(dst string) { a.exportTo(f, dst) })
}

// exportTo exports the form's take to dst in the background, remembers
// the settings for the kind and closes the dialog.
func (a *app) exportTo(f *exportForm, dst string) {
	ef := f.fmt_()
	if filepath.Ext(dst) == "" {
		dst += "." + ef.ext
	}
	o := f.options()
	kind, prefs := f.kind.id, f.prefs()
	a.updateSettings(func(s *settings.Settings) {
		if s.Export.Audio == nil {
			s.Export.Audio = map[string]settings.AudioExport{}
		}
		s.Export.Audio[kind] = prefs
	})
	tk, src := f.take, f.src
	extras := map[string]bool{}
	for _, x := range f.extrasShown() {
		extras[x] = f.extraOn(x)
	}
	enc := f.encode
	if enc == nil {
		enc = a.exportEncoder()
	}
	f.open = false
	if a.exp == f {
		a.exp = nil
	}
	a.toast(i18n.T("Exporting %s…", filepath.Base(dst)))
	a.background(func() {
		ctx := context.Background()
		res, err := services.ExportAudio(ctx, src, dst, o, enc)
		if err == nil {
			err = writeExtras(tk, dst, res, extras)
		}
		if err == nil {
			a.recordTakeExport(tk.ID, dst, ef.ext) // Projects → Exports
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Export failed: %v", err))
			} else {
				a.toast(i18n.T("Saved %s", filepath.Base(dst)))
			}
		})
	})
}

// writeExtras saves the chosen extras beside dst: the script (.txt) and
// subtitles from the take's chunks (.srt), timed to the audio written.
func writeExtras(t core.Take, dst string, res services.ExportedAudio, on map[string]bool) error {
	base := strings.TrimSuffix(dst, filepath.Ext(dst))
	if on["script"] {
		if err := os.WriteFile(base+".txt", []byte(strings.TrimSpace(t.Text)+"\n"), 0o644); err != nil {
			return err
		}
	}
	if on["subtitles"] && t.Render != nil && len(t.Render.Chunks) > 0 {
		var segs []subtitle.Segment
		for _, ch := range t.Render.Chunks {
			start, end := max(ch.Start-res.Offset, 0), min(ch.End-res.Offset, res.Seconds)
			if txt := strings.TrimSpace(markupRE.ReplaceAllString(ch.Text, " ")); txt != "" && end > start {
				segs = append(segs, subtitle.Segment{Start: secs(start), End: secs(end), Text: strings.Join(strings.Fields(txt), " ")})
			}
		}
		cues, _ := subtitle.Split(segs, subtitle.Defaults(subtitleLang(t)))
		var b strings.Builder
		if err := subtitle.WriteSRT(&b, cues); err != nil {
			return err
		}
		if err := os.WriteFile(base+".srt", []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// subtitleLang is the language the subtitles' line lengths follow: the
// take's when it is a BCP-47 tag, else "zh" for a script in Chinese.
func subtitleLang(t core.Take) string {
	if l := strings.ToLower(t.Language); len(l) >= 2 && len(l) <= 7 && !strings.Contains(l, " ") {
		return l
	}
	for _, r := range t.Text {
		if unicode.Is(unicode.Han, r) {
			return "zh"
		}
		if unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return "ja"
		}
	}
	return "en"
}

// exportEncoder writes the formats other than WAV: M4A with the engine's
// media.encode, MP3, FLAC and Opus with ffmpeg.
func (a *app) exportEncoder() services.AudioEncoder {
	return exportEncoderOf(a.lib.encodeExecutor(), a.mediaTool())
}

// exportEncoderOf is the encoder of exports over the engine's
// media.encode (ex, nil = none) and ffmpeg (tool, nil = none); the API's
// export_audio uses it too.
func exportEncoderOf(ex jobs.Executor, tool *ffmpeg.Tool) services.AudioEncoder {
	return func(ctx context.Context, in, dst string, o services.AudioExport) error {
		if o.Format == services.FormatM4A {
			if ex == nil {
				return fmt.Errorf("%s", i18n.T("M4A needs the speech engine; it is not running."))
			}
			p := proto.EncodeParams{Input: in, Output: proto.Output{Path: dst, Format: services.FormatM4A}, Codec: "aac", Bitrate: o.Bitrate,
				Metadata: proto.EncodeMetadata{Title: o.Metadata["title"], Author: o.Metadata["artist"]}}
			raw, err := json.Marshal(p)
			if err != nil {
				return err
			}
			job := jobs.Job{ID: "export-" + core.NewID(time.Now()), Spec: jobs.Spec{Kind: services.KindEncode, Lane: jobs.LaneMedia, Params: raw}}
			_, err = ex.Run(ctx, job, func(jobs.Progress) {}, nil)
			return err
		}
		if tool == nil {
			return ffmpeg.ErrNotFound
		}
		return tool.EncodeAudio(ctx, ffmpeg.EncodeOptions{Input: in, Output: dst, Format: o.Format, Bitrate: o.Bitrate, Metadata: o.Metadata}, nil)
	}
}

// changes names what the form changes from the kind's defaults for the
// chosen format (the 「高级」 settings: bit depth or bitrate, sample rate,
// channels, trimming).
func (f *exportForm) changes() []string {
	ef, i := f.fmt_(), f.format
	def := f.kind.defaults
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(len(ef.bits) > 0 && f.bits[i] != indexOr(ef.bits, def.Bits, 0), i18n.T("Bit depth"))
	add(len(ef.bitrates) > 0 && f.bitrate[i] != indexOr(ef.bitrates, ef.defBitrate, 0), i18n.T("Bitrate"))
	add(ef.rates && f.rate[i] != 0, i18n.T("Sample rate"))
	add(f.channels != 0, i18n.T("Channels"))
	add(f.trim, i18n.T("Trim silence"))
	return out
}

// restore puts the 「高级」 settings back to the kind's defaults.
func (f *exportForm) restore() {
	ef, i := f.fmt_(), f.format
	def := f.kind.defaults
	f.bits[i] = indexOr(ef.bits, def.Bits, 0)
	f.bitrate[i] = indexOr(ef.bitrates, ef.defBitrate, 0)
	f.rate[i], f.channels, f.trim = 0, 0, false
}
