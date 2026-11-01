package main

// The Dub page's export panel: MP4 with the original and dubbed tracks
// (and soft subtitles), WAV, M4A, subtitles (SRT, VTT, dual, karaoke
// ASS), stems and per-line ZIPs. MP4 and M4A need the engine's proposed
// media.mux / media.encode, MP3 and burnt-in subtitles ffmpeg: those are
// marked until they are there.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubExportState is the export panel's choices and the export running.
type dubExportState struct {
	format string
	lang   string
	tracks map[string]bool // MP4: languages (dub.OrigTrack = the original)
	def    string          // MP4: the default track
	dual   bool
	subs   bool // MP4: soft subtitle tracks
	bits   int  // WAV: 0 = 16, 1 = 24, 2 = 32 float
	job    string
	output string
	last   string // the last file written
	err    string
}

// dubExportDir is where exports go in tests (no save dialog).
var dubExportDir string

// dubExportFormats are the formats offered for the open project.
func (a *app) dubExportFormats() []string {
	var out []string
	for _, f := range dub.ExportFormats {
		if (f == dub.ExportMP4 || f == dub.ExportBurn) && (a.dub == nil || !a.dub.Source.HasVideo) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// dubExportFormatName names a format.
func dubExportFormatName(f string) string {
	switch f {
	case dub.ExportMP4:
		return i18n.T("MP4 video (original + dubbed tracks)")
	case dub.ExportWAV:
		return i18n.T("WAV (lossless)")
	case dub.ExportM4A:
		return i18n.T("M4A (AAC)")
	case dub.ExportMP3:
		return i18n.T("MP3")
	case dub.ExportSRT:
		return i18n.T("SRT subtitles")
	case dub.ExportVTT:
		return i18n.T("WebVTT subtitles")
	case dub.ExportASS:
		return i18n.T("Karaoke subtitles (ASS, word highlight)")
	case dub.ExportStems:
		return i18n.T("Stems (ZIP: dubbed speech, background, original voices)")
	case dub.ExportSegments:
		return i18n.T("Every line (ZIP of clips)")
	case dub.ExportBurn:
		return i18n.T("MP4 with burnt-in subtitles")
	}
	return f
}

// dubEngineHas reports whether the engine runs a method (unknown until
// its worker started: then yes, and the export says if it cannot).
func (a *app) dubEngineHas(method string) bool {
	role := dub.RoleOf(method)
	for _, w := range a.workers {
		if w.Role == role && w.Hello != nil {
			return slices.Contains(w.Hello.Methods, method)
		}
	}
	return true
}

// dubExportBlocked says why a format cannot be exported now ("" = it can).
func (a *app) dubExportBlocked(f string) string {
	switch services.ExportNeeds(f) {
	case "ffmpeg": // M7a: the optional ffmpeg (ffmpeg_settings.go)
		tool := a.mediaTool()
		if f == dub.ExportMP3 && tool.CanEncode(ffmpeg.MP3) || f == dub.ExportBurn && tool.CanBurn() {
			return ""
		}
		return i18n.T("Needs ffmpeg (Settings → Audio tools)")
	case "engine:" + dub.MethodMux:
		if !a.dubEngineHas(dub.MethodMux) {
			return i18n.T("Needs an engine update")
		}
	case "engine:" + dub.MethodEncode:
		if !a.dubEngineHas(dub.MethodEncode) {
			return i18n.T("Needs an engine update")
		}
	}
	return ""
}

// dubExportPanel is the Export step.
func (a *app) dubExportPanel(c *ui.Context) {
	t := c.Theme()
	p := a.dub
	ex := &a.dubExport
	if ex.format == "" {
		ex.format = p.Settings.Export.Format
		if ex.format == "" || !slices.Contains(a.dubExportFormats(), ex.format) {
			ex.format = dub.ExportWAV
			if p.Source.HasVideo {
				ex.format = dub.ExportMP4
			}
		}
		ex.lang = p.Primary()
		ex.tracks = map[string]bool{dub.OrigTrack: true}
		for _, l := range p.Targets {
			ex.tracks[l] = true
		}
		ex.def, ex.bits, ex.subs = p.Primary(), 1, true
	}
	dubPanelTitle(c, i18n.T("Export"))
	formats := a.dubExportFormats()
	labels := make([]string, len(formats))
	for i, f := range formats {
		labels[i] = dubExportFormatName(f)
		if why := a.dubExportBlocked(f); why != "" {
			labels[i] += " — " + why
		}
	}
	cur := labels[max(slices.Index(formats, ex.format), 0)]
	ui.Field(c, i18n.T("Format"), func() {
		if ui.Select(c.Key("dx-format"), &cur, labels).Label(i18n.T("Format")).Changed() {
			ex.format = formats[slices.Index(labels, cur)]
			f := ex.format
			a.dubSettings(func(s *dub.Settings) { s.Export.Format = f })
		}
	})
	subtitle := ex.format == dub.ExportSRT || ex.format == dub.ExportVTT || ex.format == dub.ExportASS
	switch {
	case ex.format == dub.ExportMP4:
		sectionTitle(c, i18n.T("Tracks"))
		ui.Column(c).Gap(px(t, 4)).Children(func() {
			for _, l := range append([]string{dub.OrigTrack}, p.Targets...) {
				on := ex.tracks[l]
				name := i18n.T("Original")
				if l != dub.OrigTrack {
					name = dubLangName(l)
					if p.Track(l).File == "" {
						name += " · " + i18n.T("not generated")
					}
				}
				if ui.Checkbox(c.Key("dx-track-"+l), &on, name).Changed() {
					ex.tracks[l] = on
				}
			}
		})
		var defs, defIDs []string
		for _, l := range append([]string{dub.OrigTrack}, p.Targets...) {
			if ex.tracks[l] {
				defIDs = append(defIDs, l)
				if l == dub.OrigTrack {
					defs = append(defs, i18n.T("Original"))
				} else {
					defs = append(defs, dubLangName(l))
				}
			}
		}
		if len(defs) > 0 {
			i := max(slices.Index(defIDs, ex.def), 0)
			d := defs[i]
			ui.Field(c, i18n.T("Default audio track"), func() {
				if ui.Select(c.Key("dx-default"), &d, defs).Label(i18n.T("Default audio track")).Changed() {
					ex.def = defIDs[slices.Index(defs, d)]
				}
			})
		}
		ui.Checkbox(c.Key("dx-subs"), &ex.subs, i18n.T("Subtitle tracks (soft)"))
	default:
		langs := slices.Clone(p.Targets)
		names := make([]string, 0, len(langs)+1)
		for _, l := range langs {
			names = append(names, dubLangName(l))
		}
		if subtitle || ex.format == dub.ExportBurn { // M7a: burn the original's subtitles too
			langs = append(langs, dub.OrigTrack)
			names = append(names, i18n.T("Original (%s)", dubLangName(p.SourceLanguage())))
		}
		if !slices.Contains(langs, ex.lang) && len(langs) > 0 {
			ex.lang = langs[0]
		}
		if len(names) > 0 {
			n := names[max(slices.Index(langs, ex.lang), 0)]
			ui.Field(c, i18n.T("Language"), func() {
				if ui.Select(c.Key("dx-lang"), &n, names).Label(i18n.T("Language")).Changed() {
					ex.lang = langs[slices.Index(names, n)]
				}
			})
		}
	}
	if ex.format == dub.ExportSRT || ex.format == dub.ExportVTT {
		ui.Checkbox(c.Key("dx-dual"), &ex.dual, i18n.T("Dual subtitles (translated + original)"))
	}
	if ex.format == dub.ExportWAV {
		a.dubAdvanced(c, dubStepExport, false, func() {
			ui.Field(c, i18n.T("Bit depth"), func() {
				segmentedFit(c.Key("dx-bits"), &ex.bits, i18n.T("16-bit"), i18n.T("24-bit"), i18n.T("32-bit float"))
			})
		})
	}
	k := tok(t)
	if subtitle {
		caption(c, i18n.T("Subtitles are made from the lines as written; long lines are split Netflix-style (42 characters a line, 17 a second).")).TextColor(k.Text3)
	}
	if !p.Settings.KeepBackground && (ex.format == dub.ExportMP4 || ex.format == dub.ExportWAV || ex.format == dub.ExportM4A) {
		caption(c, i18n.T("Keep background is off: the dubbed tracks are speech only.")).TextColor(k.Text3)
	}
	// Export… itself is the transport row's primary button.
	blocked := a.dubExportBlocked(ex.format)
	ready := a.dubExportReady()
	mark := func(ic icons.Icon, col ui.Color, text string) {
		ui.Row(c).Gap(px(t, 6)).Children(func() {
			iconMark(c, ic, col, "")
			caption(c, text).TextColor(col).Grow(1).MinWidth(0)
		})
	}
	if ex.job != "" {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			caption(c, i18n.T("Exporting…"))
		})
	}
	switch {
	case blocked != "":
		mark(icons.DangerTriangle, t.Warning, blocked)
	case ready != "":
		mark(icons.InfoCircle, k.Text2, ready)
	}
	if ex.err != "" {
		mark(icons.DangerCircle, t.Danger, ex.err)
	}
	if ex.last != "" {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			iconMark(c, icons.CheckCircle, t.Success, "")
			truncText(c, i18n.T("Exported %s", filepath.Base(ex.last))).FontSize(px(t, fsCaption)).Grow(1)
			if a.win != nil && iconButton(c.Key("dx-reveal"), icons.FolderOpen, i18n.T("Show in Finder")).Clicked() {
				mygo.Shell.ShowItemInFolder(ex.last)
			}
		})
	}
}

// dubExportReady says what the export lacks ("" = nothing).
func (a *app) dubExportReady() string {
	p, ex := a.dub, &a.dubExport
	switch ex.format {
	case dub.ExportSRT, dub.ExportVTT, dub.ExportASS:
		if len(p.Segments) == 0 {
			return i18n.T("There is no transcript yet.")
		}
		return ""
	case dub.ExportMP4:
		for l, on := range ex.tracks {
			if on && l != dub.OrigTrack && p.Track(l).File == "" {
				return i18n.T("Generate the %s track first.", dubLangName(l))
			}
		}
		return ""
	case dub.ExportSegments:
		if len(p.Track(ex.lang).Lines) == 0 {
			return i18n.T("Generate the %s track first.", dubLangName(ex.lang))
		}
		return ""
	case dub.ExportBurn: // M7a: the original's subtitles need only the transcript
		if len(p.Segments) == 0 {
			return i18n.T("There is no transcript yet.")
		}
		if ex.lang == dub.OrigTrack {
			return ""
		}
	}
	if p.Track(ex.lang).File == "" {
		return i18n.T("Generate the %s track first.", dubLangName(ex.lang))
	}
	return ""
}

// dubDoExport asks where to save, then queues the export.
func (a *app) dubDoExport() {
	p, ex := a.dub, &a.dubExport
	ex.err = ""
	req := services.ExportRequest{Dub: p.ID, Format: ex.format, Lang: ex.lang, Dual: ex.dual, Subtitles: ex.subs, Default: ex.def}
	req.Bits = []int{16, 24, 32}[min(max(ex.bits, 0), 2)]
	if ex.format == dub.ExportMP4 {
		for _, l := range append([]string{dub.OrigTrack}, p.Targets...) {
			if ex.tracks[l] {
				req.Tracks = append(req.Tracks, l)
			}
		}
	}
	lang := ex.lang
	if ex.format == dub.ExportMP4 {
		lang = ""
	} else if lang == dub.OrigTrack {
		lang = "orig"
	}
	name := dub.ExportName(p.Title, lang, ex.format)
	start := func(dst string) {
		req.Output = dst
		svc := a.dubSvc
		a.async(func() {
			id, err := svc.ExportJob(context.Background(), req)
			a.post(func() {
				if err != nil {
					ex.err = err.Error()
					return
				}
				if ex.job == "" {
					ex.job, ex.output = id, dst
				}
			})
		})
		ex.job = "pending"
	}
	if a.win == nil {
		if dubExportDir != "" {
			start(filepath.Join(dubExportDir, name))
		}
		return
	}
	ext := dub.ExportExt(ex.format)
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Export the dub"), DefaultPath: name,
			Filters: []mygo.FileFilter{{Name: strings.ToUpper(ext), Extensions: []string{ext}}}})
		if err != nil || dst == "" {
			return
		}
		a.post(func() { start(dst) })
	}()
}

// dubExportUpdated follows export jobs.
func (a *app) dubExportUpdated(j jobs.Job) {
	ex := &a.dubExport
	if ex.job == "pending" && a.dub != nil && j.Owner == services.DubExportOwner+a.dub.ID {
		ex.job = j.ID
	}
	if j.ID != ex.job {
		return
	}
	switch j.State {
	case jobs.StateSucceeded:
		ex.job = ""
		var res services.ExportResult
		json.Unmarshal(j.Result, &res)
		ex.last = res.Output
		a.toast(i18n.T("Exported %s", filepath.Base(res.Output)))
		a.dubRecordExport(res)
	case jobs.StateFailed, jobs.StateCancelled, jobs.StateInterrupted:
		ex.job = ""
		if j.Err != nil && j.Err.Code != proto.CodeCancelled {
			ex.err = dubErrorText(j.Err)
		}
	}
}

// dubRecordExport lists an export on the Projects page.
func (a *app) dubRecordExport(res services.ExportResult) {
	if a.dub == nil || res.Output == "" {
		return
	}
	lib := a.lib.svc.Library
	e := core.Export{Kind: "dub", SourceID: a.dub.ID, Title: a.dub.Title, Path: res.Output, Format: res.Format, Bytes: res.Bytes}
	a.background(func() {
		if _, err := lib.RecordExport(context.Background(), e); err != nil {
			oplog.Warn("export.record", "error", err)
		}
	})
}
