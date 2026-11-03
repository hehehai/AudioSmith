package services

// Exports of a dub: subtitles and ZIPs are written in Go; WAV converted
// from the mixed track; MP4 (the source's video with the original and
// dubbed tracks, soft subtitles) and M4A go through the engine's
// media.mux / media.encode; MP3 (media.encode to "mp3") and burnt-in
// subtitles (MethodBurn) need the optional ffmpeg, which the app runs
// behind those methods (M7a).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/oplog"
)

// MethodBurn draws subtitles into a video (an app-level method: the app
// runs it on ffmpeg; params as the app's BurnParams).
const MethodBurn = "media.burn"

// DubExportOwner prefixes the owners of export jobs ("dubexport:<id>"),
// apart from the pipeline's so an export does not block a run.
const DubExportOwner = "dubexport:"

// ExportRequest asks for one export file.
type ExportRequest struct {
	Dub    string `json:"dub"`
	Format string `json:"format"` // a dub.Export* format
	// Lang is the language of single-language exports; "" or
	// dub.OrigTrack = the source (subtitles).
	Lang string `json:"lang,omitempty"`
	// Tracks are an MP4's audio tracks (languages, dub.OrigTrack for
	// the original), Default the one players pick.
	Tracks  []string `json:"tracks,omitempty"`
	Default string   `json:"default,omitempty"`
	// Dual subtitles (translated + original); Subtitles adds soft
	// subtitle tracks to an MP4.
	Dual      bool `json:"dual,omitempty"`
	Subtitles bool `json:"subtitles,omitempty"`
	Bits      int  `json:"bits,omitempty"` // WAV
	// Output is the file to write (absolute).
	Output string `json:"output"`
}

// ExportResult is what an export wrote.
type ExportResult struct {
	Output string `json:"output"`
	Bytes  int64  `json:"bytes"`
	Format string `json:"format"`
}

// ExportJob queues an export (cpu lane) and returns its job id.
func (s *Dubs) ExportJob(ctx context.Context, req ExportRequest) (string, error) {
	if !filepath.IsAbs(req.Output) {
		return "", fmt.Errorf("dub export: output must be absolute: %q", req.Output)
	}
	params, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	oplog.Info("dub.export", "id", req.Dub, "file", filepath.Base(req.Output), "by", oplog.Actor(ctx))
	return s.cfg.Scheduler.Submit(ctx, jobs.Spec{Kind: KindDubExport, Lane: jobs.LaneCPU, Priority: jobs.PriorityInteractive,
		Params: params, Title: i18n.T("Export %s", filepath.Base(req.Output)), Owner: DubExportOwner + req.Dub, MaxAttempts: 1})
}

func (s *Dubs) export(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var req ExportRequest
	if err := json.Unmarshal(job.Params, &req); err != nil {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: err.Error()}
	}
	res, err := s.Export(ctx, req, func(f float64) { report(jobs.Progress{Stage: "exporting", Fraction: f}) })
	if err != nil {
		if d := jobs.DiskFull(err); d != nil {
			return nil, d
		}
		var je *jobs.Error
		if errors.As(err, &je) {
			return nil, je
		}
		return nil, &jobs.Error{Code: DubCodeFailed, Message: err.Error()}
	}
	return json.Marshal(res)
}

// ExportNeeds says what an export format needs that may be missing: ""
// (nothing), "engine:<method>" or "ffmpeg".
func ExportNeeds(format string) string {
	switch format {
	case dub.ExportMP4:
		return "engine:" + dub.MethodMux
	case dub.ExportM4A:
		return "engine:" + dub.MethodEncode
	case dub.ExportMP3, dub.ExportBurn:
		return "ffmpeg"
	}
	return ""
}

// Export writes one export (on the caller's goroutine).
func (s *Dubs) Export(ctx context.Context, req ExportRequest, progress func(float64)) (ExportResult, error) {
	p, err := s.Get(ctx, req.Dub)
	if err != nil {
		return ExportResult{}, err
	}
	if progress == nil {
		progress = func(float64) {}
	}
	if err := os.MkdirAll(filepath.Dir(req.Output), 0o755); err != nil {
		return ExportResult{}, err
	}
	if err := checkFree("the export", filepath.Dir(req.Output), dubExportNeed(p, req)); err != nil {
		return ExportResult{}, err
	}
	x := &dubExport{s: s, ctx: ctx, p: p, req: req, lang: req.Lang,
		progress: func(pg jobs.Progress) { progress(max(pg.Fraction, 0)) }}
	if x.lang == dub.OrigTrack {
		x.lang = ""
	}
	switch req.Format {
	case dub.ExportSRT, dub.ExportVTT, dub.ExportASS:
		err = x.subtitles()
	case dub.ExportWAV, dub.ExportM4A, dub.ExportMP3:
		err = x.audio()
	case dub.ExportBurn: // M7a: ffmpeg draws the subtitles of lang into the video, with lang's track
		err = x.burn()
	case dub.ExportStems:
		err = x.stems()
	case dub.ExportSegments:
		err = x.segments()
	case dub.ExportMP4:
		err = x.mp4()
	default:
		err = &jobs.Error{Code: proto.CodeBadRequest, Message: "unknown export format " + req.Format}
	}
	if err != nil {
		return ExportResult{}, err
	}
	progress(1)
	s.noteExport(ctx, p.ID) // its working files go a while after (dub_free.go)
	res := ExportResult{Output: req.Output, Format: req.Format}
	if fi, err := os.Stat(req.Output); err == nil {
		res.Bytes = fi.Size()
	}
	return res, nil
}

// dubExport is one export of a dub (Dubs.Export).
type dubExport struct {
	s        *Dubs
	ctx      context.Context
	p        *dub.Project
	req      ExportRequest
	lang     string // the language exported; "" the original
	progress func(jobs.Progress)
}

// track is the mixed track of language l, or why there is none.
func (x *dubExport) track(l string) (dub.Track, error) {
	t := x.p.Track(l)
	if t.File == "" || !fileExists(x.s.Path(x.p.ID, t.File)) {
		data, _ := json.Marshal(map[string]string{"lang": l})
		return t, &jobs.Error{Code: DubCodeNotMixed, Message: fmt.Sprintf("There is no %s track yet: generate it first.", l), Data: data}
	}
	return t, nil
}

func (x *dubExport) subtitles() error {
	req := x.req
	data, err := x.p.Subtitles(dub.SubtitleOptions{Format: req.Format, Lang: x.lang, Dual: req.Dual, Split: !req.Dual && req.Format != dub.ExportASS})
	if err != nil {
		return err
	}
	return writeAtomic(req.Output, data)
}

// audio writes the language's track as WAV, M4A (the engine) or MP3
// (M7a: ffmpeg behind media.encode).
func (x *dubExport) audio() error {
	s, p, req := x.s, x.p, x.req
	t, err := x.track(x.lang)
	if err != nil {
		return err
	}
	in := s.Path(p.ID, t.File)
	var er dub.EncodeResult
	switch req.Format {
	case dub.ExportWAV:
		bits := req.Bits
		if bits == 0 {
			bits = 24
		}
		return convertWAV(x.ctx, in, req.Output, bits)
	case dub.ExportM4A:
		ep := dub.EncodeParams{Input: in, Output: proto.Output{Path: req.Output, Format: "m4a"}, Codec: "aac", Bitrate: 192000}
		if err := s.exportCtx(p, "encode").call(x.ctx, dub.MethodEncode, "", ep, nil, &er, nil); err != nil {
			return needsEngine(err, dub.MethodEncode)
		}
	default: // MP3
		ep := dub.EncodeParams{Input: in, Output: proto.Output{Path: req.Output, Format: "mp3"}, Bitrate: 192000,
			Metadata: proto.EncodeMetadata{Title: p.Title}}
		if err := s.exportCtx(p, "encode").call(x.ctx, dub.MethodEncode, "", ep, nil, &er, x.progress); err != nil {
			return needsFFmpeg(err)
		}
	}
	return nil
}

func (x *dubExport) burn() error {
	s, p, lang := x.s, x.p, x.lang
	if !p.Source.HasVideo {
		return &jobs.Error{Code: "dub.no_video", Message: "The source has no video: export subtitles instead."}
	}
	audio := ""
	if lang != "" {
		t, err := x.track(lang)
		if err != nil {
			return err
		}
		audio = s.Path(p.ID, t.File)
	}
	data, err := p.Subtitles(dub.SubtitleOptions{Format: dub.SubSRT, Lang: lang, Split: true})
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir(p.ID), "exports")
	os.MkdirAll(tmp, 0o755)
	subs := filepath.Join(tmp, "burn-"+dubFirst(dub.SafeName(lang), "orig")+".srt")
	if err := writeAtomic(subs, data); err != nil {
		return err
	}
	params := map[string]any{"video": p.Source.Path, "audio": audio, "subtitles": subs, "output": proto.Output{Path: x.req.Output, Format: "mp4"}}
	err = s.exportCtx(p, "burn").call(x.ctx, MethodBurn, "", params, nil, nil, x.progress)
	os.RemoveAll(tmp)
	if err != nil {
		return needsFFmpeg(err)
	}
	return nil
}

func (x *dubExport) stems() error {
	s, p, lang := x.s, x.p, x.lang
	t, err := x.track(lang)
	if err != nil {
		return err
	}
	if s.stemsGone(p) {
		return &jobs.Error{Code: DubCodeFreed, Message: "The separated stems were freed to save space: generate the dub again to make them."}
	}
	entries := []dub.ZipEntry{{Name: dub.SafeName(lang) + "_speech.wav", Path: s.Path(p.ID, t.Speech)}}
	if p.Media.Background != "" {
		entries = append(entries, dub.ZipEntry{Name: "background.wav", Path: s.Path(p.ID, p.Media.Background)})
	}
	if p.Media.Vocals != "" {
		entries = append(entries, dub.ZipEntry{Name: "original_voices.wav", Path: s.Path(p.ID, p.Media.Vocals)})
	}
	entries = append(entries, dub.ZipEntry{Name: dub.SafeName(lang) + "_mix.wav", Path: s.Path(p.ID, t.File)})
	return writeZip(x.req.Output, entries)
}

func (x *dubExport) segments() error {
	p := x.p
	entries := p.SegmentEntries(x.lang, x.s.Dir(p.ID))
	if len(entries) == 0 {
		return &jobs.Error{Code: DubCodeNotMixed, Message: "No line is rendered in this language yet."}
	}
	if data, err := p.Subtitles(dub.SubtitleOptions{Format: dub.SubSRT, Lang: x.lang}); err == nil {
		entries = append(entries, dub.ZipEntry{Name: "lines.srt", Data: data})
	}
	return writeZip(x.req.Output, entries)
}

// mp4 muxes the video with the tracks asked (all by default) and, when
// asked, their subtitles.
func (x *dubExport) mp4() error {
	s, p, req := x.s, x.p, x.req
	if !p.Source.HasVideo {
		return &jobs.Error{Code: "dub.no_video", Message: "The source has no video: export WAV or M4A."}
	}
	mp := dub.MuxParams{Video: p.Source.Path, Output: proto.Output{Path: req.Output, Format: "mp4"}}
	tracks := req.Tracks
	if len(tracks) == 0 {
		tracks = append([]string{dub.OrigTrack}, p.Targets...)
	}
	def := req.Default
	if def == "" {
		for _, l := range tracks {
			if l != dub.OrigTrack {
				def = l
				break
			}
		}
	}
	for _, l := range tracks {
		if l == dub.OrigTrack {
			mp.KeepAudio = true
			continue
		}
		t, err := x.track(l)
		if err != nil {
			return err
		}
		mp.Audio = append(mp.Audio, dub.MuxTrack{Path: s.Path(p.ID, t.File), Language: l, Title: languageTitle(l), Default: l == def})
	}
	tmp := filepath.Join(s.Dir(p.ID), "exports")
	if req.Subtitles {
		os.MkdirAll(tmp, 0o755)
		subs := slices.DeleteFunc(slices.Clone(tracks), func(l string) bool { return l == dub.OrigTrack })
		subs = append(subs, "")
		for _, l := range subs {
			data, err := p.Subtitles(dub.SubtitleOptions{Format: dub.SubSRT, Lang: l, Split: true})
			if err != nil || len(data) == 0 {
				continue
			}
			name := filepath.Join(tmp, "subs-"+dubFirst(dub.SafeName(l), "orig")+".srt")
			if err := writeAtomic(name, data); err != nil {
				return err
			}
			sl := l
			if sl == "" {
				sl = p.SourceLanguage()
			}
			mp.Subtitles = append(mp.Subtitles, dub.MuxSubtitle{Path: name, Language: sl, Title: languageTitle(sl), Default: l == def})
		}
	}
	var mr dub.MuxResult
	err := s.exportCtx(p, "mux").call(x.ctx, dub.MethodMux, "", mp, nil, &mr, x.progress)
	if req.Subtitles {
		os.RemoveAll(tmp)
	}
	if err != nil {
		return needsEngine(err, dub.MethodMux)
	}
	return nil
}

// exportCtx is a stage context for engine calls of an export.
func (s *Dubs) exportCtx(p *dub.Project, what string) *stageCtx {
	return &stageCtx{s: s, p: p, job: jobs.Job{ID: "export-" + p.ID + "-" + what, Spec: jobs.Spec{Owner: DubExportOwner + p.ID}}}
}

// needsEngine turns an engine that lacks method into DubCodeNeedsEngine.
func needsEngine(err error, method string) error {
	if code(err) == proto.CodeMethodNotFound {
		data, _ := json.Marshal(map[string]string{"method": method})
		return &jobs.Error{Code: DubCodeNeedsEngine, Message: "The engine cannot do this yet (" + method + ").", Data: data}
	}
	return err
}

// needsFFmpeg turns the app's "needs ffmpeg" answer (unsupported,
// capability "ffmpeg") into DubCodeNeedsFFmpeg.
func needsFFmpeg(err error) error {
	var pe *proto.Error
	var je *jobs.Error
	var c string
	var data json.RawMessage
	switch {
	case errors.As(err, &pe):
		c, data = pe.Code, pe.Data
	case errors.As(err, &je):
		c, data = je.Code, je.Data
	}
	if c == proto.CodeUnsupported || c == proto.CodeMethodNotFound {
		var d struct{ Capability string }
		json.Unmarshal(data, &d)
		if d.Capability == "ffmpeg" || c == proto.CodeMethodNotFound {
			return &jobs.Error{Code: DubCodeNeedsFFmpeg, Message: "This export needs ffmpeg: set it up in Settings → Audio tools."}
		}
	}
	return err
}

// languageTitle is a track's title: the language's own name.
func languageTitle(tag string) string {
	if l, ok := lang.Lookup(tag); ok {
		if l.Native != "" {
			return l.Native
		}
		return l.Name
	}
	return tag
}

// writeAtomic writes data to path through a temporary file of a random
// name beside it, created exclusively (code review 11-02, N2: a
// predictable "<path>.tmp" in a shared folder could be a symlink planted
// to overwrite another file).
func writeAtomic(path string, data []byte) error {
	return wav.WriteFileAtomic(path, 0o644, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

// writeZip writes the ZIP of entries to path, like writeAtomic.
func writeZip(path string, entries []dub.ZipEntry) error {
	return wav.WriteFileAtomic(path, 0o644, func(f *os.File) error {
		return dub.WriteZip(f, entries)
	})
}
