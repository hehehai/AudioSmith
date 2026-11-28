package main

// ffmpeg behind the engine's media methods (M7a): the supervisor every
// area calls is wrapped, so each caller (dubbing, transcription, voice
// references, workflows, books, the API) gets ffmpeg's formats without
// knowing about it:
//
//   - media.probe, media.decode, media.thumbnail, media.transcode and
//     media.mux run on ffmpeg when the engine answers unsupported_media
//     (WebM, MKV, VP9, AV1…) or does not know the method;
//   - media.encode to MP3, Opus, FLAC or Ogg runs on ffmpeg (the engine
//     writes M4A/M4B only);
//   - media.burn (an app-level method: subtitles drawn into a video) runs
//     on ffmpeg only.
//
// Without ffmpeg these answer as before, or unsupported with capability
// "ffmpeg", which the UI turns into "needs ffmpeg (Settings → Audio
// tools)".

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/doc/subtitle"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/lang"
)

// methodBurn draws subtitles into a video (ffmpeg only).
const methodBurn = "media.burn"

// BurnParams are media.burn's params.
type BurnParams struct {
	Video     string       `json:"video"`
	Audio     string       `json:"audio,omitempty"` // replaces the video's sound
	Subtitles string       `json:"subtitles"`       // SRT, WebVTT or ASS
	Output    proto.Output `json:"output"`          // MP4
}

// ffmpegFormats are the media.encode output formats ffmpeg writes.
var ffmpegFormats = map[string]bool{ffmpeg.MP3: true, ffmpeg.Opus: true, ffmpeg.FLAC: true, ffmpeg.OGG: true}

// mediaSup is the engine supervisor with ffmpeg behind its media methods.
type mediaSup struct {
	engine.Supervisor
	tool func() *ffmpeg.Tool // nil: none set up
}

// withMediaTools wraps sup (once).
func withMediaTools(sup engine.Supervisor, tool func() *ffmpeg.Tool) engine.Supervisor {
	if sup == nil {
		return nil
	}
	if _, ok := sup.(*mediaSup); ok {
		return sup
	}
	return &mediaSup{Supervisor: sup, tool: tool}
}

// unwrapSup is the supervisor under the ffmpeg wrapper.
func unwrapSup(sup engine.Supervisor) engine.Supervisor {
	if m, ok := sup.(*mediaSup); ok {
		return m.Supervisor
	}
	return sup
}

func (m *mediaSup) Call(ctx context.Context, role proto.Role, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	switch method {
	case methodBurn:
		var p BurnParams
		if err := remarshal(params, &p); err != nil {
			return nil, &proto.Error{Code: proto.CodeBadRequest, Message: err.Error()}
		}
		return m.burn(ctx, id, p, notify)
	case proto.MethodMediaEncode:
		var p proto.EncodeParams
		if err := remarshal(params, &p); err == nil && ffmpegFormats[strings.ToLower(p.Output.Format)] {
			return m.encode(ctx, id, p, notify)
		}
	}
	res, err := m.Supervisor.Call(ctx, role, id, method, params, notify)
	if err == nil || ctx.Err() != nil || !fallsBack(method, err) {
		return res, err
	}
	tool := m.tool()
	if tool == nil {
		return res, err
	}
	var fres any
	var ferr error
	switch method {
	case proto.MethodMediaProbe:
		var p proto.ProbeParams
		if remarshal(params, &p) == nil {
			fres, ferr = ffProbe(ctx, tool, p.Input)
		}
	case proto.MethodMediaDecode:
		var p proto.DecodeParams
		if remarshal(params, &p) == nil {
			fres, ferr = ffDecode(ctx, tool, id, p, notify)
		}
	case proto.MethodMediaThumbnail:
		var p proto.ThumbnailParams
		if remarshal(params, &p) == nil {
			fres, ferr = ffThumbnail(ctx, tool, p)
		}
	case proto.MethodMediaTranscode:
		var p proto.TranscodeParams
		if remarshal(params, &p) == nil {
			fres, ferr = ffTranscode(ctx, tool, id, p, notify)
		}
	case proto.MethodMediaMux:
		var p proto.MuxParams
		if remarshal(params, &p) == nil {
			fres, ferr = ffMux(ctx, tool, id, p, notify)
		}
	default:
		return res, err
	}
	if fres == nil && ferr == nil {
		return res, err
	}
	if ferr != nil {
		return nil, ffError(ferr, err)
	}
	return json.Marshal(fres)
}

// fallsBack reports engine errors ffmpeg may do better on.
func fallsBack(method string, err error) bool {
	var pe *proto.Error
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Code {
	case proto.CodeUnsupportedMedia:
		return true
	case proto.CodeUnsupported:
		// Not for a mux the engine refuses for a feature (retime): ffmpeg's
		// would drop it silently.
		return method != proto.MethodMediaMux
	case proto.CodeMethodNotFound: // an engine without the method
		return method != proto.MethodMediaProbe && method != proto.MethodMediaDecode
	}
	return false
}

// ffError is a failed ffmpeg fallback: ffmpeg could not read the file
// either (the engine's answer stands), or another failure.
func ffError(ferr, engineErr error) error {
	var re *ffmpeg.RunError
	if errors.As(ferr, &re) && engineErr != nil {
		var pe *proto.Error
		if errors.As(engineErr, &pe) {
			return &proto.Error{Code: pe.Code, Message: pe.Message + " (ffmpeg: " + re.Tail + ")", Data: pe.Data}
		}
	}
	if errors.Is(ferr, context.Canceled) || errors.Is(ferr, context.DeadlineExceeded) {
		return ferr
	}
	return &proto.Error{Code: proto.CodeInternal, Message: ferr.Error()}
}

// needsFFmpeg is the error of a method ffmpeg alone can do, without it.
func needsFFmpeg(what string) error {
	data, _ := json.Marshal(map[string]string{"capability": "ffmpeg", "format": what})
	return &proto.Error{Code: proto.CodeUnsupported, Message: what + " needs ffmpeg: set it up in Settings → Audio tools.", Data: data}
}

// isNeedsFFmpeg reports needsFFmpeg's error (also as a jobs.Error).
func isNeedsFFmpeg(code string, data json.RawMessage) bool {
	if code != proto.CodeUnsupported {
		return false
	}
	var d struct{ Capability string }
	json.Unmarshal(data, &d)
	return d.Capability == "ffmpeg"
}

func remarshal(in, out any) error {
	if raw, ok := in.(json.RawMessage); ok {
		return json.Unmarshal(raw, out)
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// reporter turns ffmpeg's fractions into job.progress notifications (at
// most 5 a second).
func reporter(id, stage string, notify func(proto.Message)) func(float64) {
	if notify == nil {
		return nil
	}
	var mu sync.Mutex
	var last time.Time
	return func(f float64) {
		mu.Lock()
		defer mu.Unlock()
		if f < 1 && time.Since(last) < 200*time.Millisecond {
			return
		}
		last = time.Now()
		params, _ := json.Marshal(proto.ProgressParams{Job: id, Stage: stage, Fraction: &f})
		notify(proto.Message{Method: proto.MethodJobProgress, Params: params})
	}
}

// ---- the methods -------------------------------------------------------------

func ffProbe(ctx context.Context, tool *ffmpeg.Tool, path string) (proto.ProbeResult, error) {
	p, err := tool.Probe(ctx, path)
	if err != nil {
		return proto.ProbeResult{}, err
	}
	return probeResult(path, p), nil
}

// probeResult is ffprobe's answer as media.probe's.
func probeResult(path string, p ffmpeg.Probe) proto.ProbeResult {
	r := proto.ProbeResult{Format: containerName(path, p.Format), Duration: p.Duration, HasAudio: len(p.Audio) > 0,
		HasVideo: len(p.Video) > 0, AudioTracks: len(p.Audio)}
	if len(p.Audio) > 0 {
		a := p.Audio[0]
		r.Codec, r.SampleRate, r.Channels, r.Bits, r.BitRate = a.Codec, a.SampleRate, a.Channels, a.Bits, a.BitRate
		if strings.HasPrefix(a.Codec, "pcm_") {
			r.Codec = "lpcm"
		}
	}
	if len(p.Video) > 0 {
		v := p.Video[0]
		r.Video = &proto.VideoInfo{Codec: v.Codec, Width: v.Width, Height: v.Height, FPS: v.FPS}
	}
	return r
}

// containerName picks the name of ffprobe's list ("matroska,webm") the
// file's extension uses.
func containerName(path, names string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	list := strings.Split(names, ",")
	for _, n := range list {
		if n == ext {
			return n
		}
	}
	switch {
	case ext == "mkv" && strings.Contains(names, "matroska"):
		return "mkv"
	case ext != "":
		return ext
	}
	return list[0]
}

func ffDecode(ctx context.Context, tool *ffmpeg.Tool, id string, p proto.DecodeParams, notify func(proto.Message)) (proto.DecodeResult, error) {
	start := time.Now()
	src, err := tool.Probe(ctx, p.Input)
	if err != nil {
		return proto.DecodeResult{}, err
	}
	o := ffmpeg.DecodeOptions{Input: p.Input, Output: p.Output.Path, SampleRate: p.SampleRate, Channels: p.Channels,
		Bits: p.Output.Bits, Start: p.Start, Duration: p.Duration, Track: p.Track, Total: src.Duration}
	if err := tool.DecodeAudio(ctx, o, reporter(id, "decoding", notify)); err != nil {
		return proto.DecodeResult{}, err
	}
	res := proto.DecodeResult{Source: probeResult(p.Input, src), Seconds: time.Since(start).Seconds()}
	res.Output = p.Output.Path
	if f, err := os.Open(p.Output.Path); err == nil {
		if r, err := wav.NewReader(f); err == nil {
			fm := r.Format()
			res.SampleRate, res.Channels, res.Frames = fm.Rate, fm.Channels, r.Frames()
			if fm.Rate > 0 {
				res.AudioSeconds = float64(res.Frames) / float64(fm.Rate)
			}
		}
		f.Close()
	}
	return res, nil
}

func ffThumbnail(ctx context.Context, tool *ffmpeg.Tool, p proto.ThumbnailParams) (proto.ThumbnailResult, error) {
	frames, err := tool.Thumbnails(ctx, p.Input, p.Times, p.Outputs, p.Width)
	if err != nil {
		return proto.ThumbnailResult{}, err
	}
	var r proto.ThumbnailResult
	for _, f := range frames {
		r.Frames = append(r.Frames, proto.Frame{Time: f.Time, Path: f.Path, Width: f.Width, Height: f.Height})
	}
	return r, nil
}

func ffTranscode(ctx context.Context, tool *ffmpeg.Tool, id string, p proto.TranscodeParams, notify func(proto.Message)) (proto.TranscodeResult, error) {
	src, _ := tool.Probe(ctx, p.Input)
	err := tool.Transcode(ctx, ffmpeg.TranscodeOptions{Input: p.Input, Output: p.Output.Path, MaxHeight: p.MaxHeight, NoAudio: p.NoAudio,
		Duration: src.Duration}, reporter(id, "transcoding", notify))
	if err != nil {
		return proto.TranscodeResult{}, err
	}
	r := proto.TranscodeResult{Output: p.Output.Path, Duration: src.Duration}
	if fi, err := os.Stat(p.Output.Path); err == nil {
		r.Bytes = fi.Size()
	}
	if out, err := tool.Probe(ctx, p.Output.Path); err == nil && len(out.Video) > 0 {
		r.Width, r.Height, r.Duration = out.Video[0].Width, out.Video[0].Height, out.Duration
	}
	return r, nil
}

// iso6392 is the three-letter code MP4 wants for a BCP-47 tag.
func iso6392(tag string) string {
	if tag == "" {
		return ""
	}
	if l, ok := lang.Lookup(tag); ok && l.ISO639_3 != "" {
		switch l.ISO639_3 { // ISO 639-3 macrolanguage members MP4 players know by their 639-2 code
		case "cmn", "yue", "wuu":
			return "chi"
		case "arb":
			return "ara"
		}
		return l.ISO639_3
	}
	return ""
}

func ffMux(ctx context.Context, tool *ffmpeg.Tool, id string, p proto.MuxParams, notify func(proto.Message)) (proto.MuxResult, error) {
	src, err := tool.Probe(ctx, p.Video)
	if err != nil {
		return proto.MuxResult{}, err
	}
	o := ffmpeg.MuxOptions{Video: p.Video, KeepAudio: p.KeepAudio && len(src.Audio) > 0, Output: p.Output.Path, Duration: src.Duration}
	if len(src.Video) > 0 {
		switch src.Video[0].Codec {
		case "h264", "hevc":
			o.CopyVideo = true
		}
	}
	for _, t := range p.Audio {
		o.Audio = append(o.Audio, ffmpeg.Track{Path: t.Path, Language: iso6392(t.Language), Title: t.Title, Default: t.Default, Bitrate: t.Bitrate})
	}
	for _, s := range p.Subtitles {
		o.Subtitles = append(o.Subtitles, ffmpeg.SubtitleTrack{Path: s.Path, Language: iso6392(s.Language), Title: s.Title, Default: s.Default})
	}
	if err := tool.Mux(ctx, o, reporter(id, "muxing", notify)); err != nil {
		return proto.MuxResult{}, err
	}
	r := proto.MuxResult{Output: p.Output.Path, Duration: src.Duration, Tracks: len(o.Audio), Reencoded: !o.CopyVideo}
	if o.KeepAudio {
		r.Tracks++
	}
	if fi, err := os.Stat(p.Output.Path); err == nil {
		r.Bytes = fi.Size()
	}
	return r, nil
}

func (m *mediaSup) encode(ctx context.Context, id string, p proto.EncodeParams, notify func(proto.Message)) (json.RawMessage, error) {
	format := strings.ToLower(p.Output.Format)
	tool := m.tool()
	if tool == nil || !tool.CanEncode(format) {
		return nil, needsFFmpeg(strings.ToUpper(format))
	}
	dur := 0.0
	if f, err := os.Open(p.Input); err == nil {
		if r, err := wav.NewReader(f); err == nil && r.Format().Rate > 0 {
			dur = float64(r.Frames()) / float64(r.Format().Rate)
		}
		f.Close()
	}
	o := ffmpeg.EncodeOptions{Input: p.Input, Output: p.Output.Path, Format: format, Bitrate: p.Bitrate, SampleRate: p.SampleRate,
		Cover: p.Cover, Duration: dur, Metadata: map[string]string{
			"title": p.Metadata.Title, "artist": p.Metadata.Author, "album": p.Metadata.Title, "album_artist": p.Metadata.Author,
			"composer": p.Metadata.Narrator, "date": p.Metadata.Year, "genre": p.Metadata.Genre, "comment": p.Metadata.Description}}
	for _, c := range p.Chapters {
		o.Chapters = append(o.Chapters, ffmpeg.Chapter{Title: c.Title, Start: c.Start, End: c.End})
	}
	if err := tool.EncodeAudio(ctx, o, reporter(id, "encoding", notify)); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &proto.Error{Code: proto.CodeInternal, Message: err.Error()}
	}
	r := proto.EncodeResult{Output: p.Output.Path, Format: format, Duration: dur, Bitrate: p.Bitrate}
	if fi, err := os.Stat(p.Output.Path); err == nil {
		r.Bytes = fi.Size()
		if dur > 0 && r.Bitrate == 0 {
			r.Bitrate = int(float64(fi.Size()*8) / dur)
		}
	}
	return json.Marshal(r)
}

func (m *mediaSup) burn(ctx context.Context, id string, p BurnParams, notify func(proto.Message)) (json.RawMessage, error) {
	tool := m.tool()
	if tool == nil || !tool.CanBurn() {
		return nil, needsFFmpeg("Burnt-in subtitles")
	}
	src, err := tool.Probe(ctx, p.Video)
	if err != nil {
		return nil, ffError(err, nil)
	}
	if len(src.Video) == 0 {
		return nil, &proto.Error{Code: proto.CodeUnsupportedMedia, Message: p.Video + " has no video"}
	}
	data, err := os.ReadFile(p.Subtitles)
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeFileNotFound, Message: err.Error()}
	}
	_, cues, _ := subtitle.Parse(string(data))
	o := ffmpeg.BurnOptions{Video: p.Video, Audio: p.Audio, Subtitles: p.Subtitles, Output: p.Output.Path, Duration: src.Duration,
		Width: src.Video[0].Width, Height: src.Video[0].Height, Fonts: ffmpeg.DefaultFonts()}
	for _, c := range cues {
		o.Cues = append(o.Cues, ffmpeg.Cue{Start: c.Start.Seconds(), End: c.End.Seconds(), Text: c.Text})
	}
	if err := tool.Burn(ctx, o, reporter(id, "encoding", notify)); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ffError(err, nil)
	}
	r := proto.MuxResult{Output: p.Output.Path, Duration: src.Duration, Tracks: 1, Reencoded: true}
	if fi, err := os.Stat(p.Output.Path); err == nil {
		r.Bytes = fi.Size()
	}
	return json.Marshal(r)
}
