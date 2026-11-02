package services

// The dub stages before the transcript: fetch, ingest, separate; then
// transcribe and references in dub_transcribe.go.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/analysis"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Files of a dub project, in its directory.
const (
	dubAudio      = "audio.wav"     // the source's audio, ≤ 48 kHz, ≤ 2 channels
	dubSpeech     = "speech.wav"    // 16 kHz mono
	dubVocals     = "vocals.wav"    // separated voices
	dubVocals16k  = "vocals16k.wav" // 16 kHz mono of them
	dubBackground = "background.wav"
	dubPeaks      = "audio.peaks"
	dubPoster     = "poster.jpg"
	dubProxy      = "preview.mp4"
)

// cookies are cookies.txt files for URL downloads, kept in memory only
// (never in the project or a job's params), by project id.
var cookies sync.Map

// SetCookies gives project id's next download a cookies.txt ("" = none).
func (s *Dubs) SetCookies(id, path string) {
	if path == "" {
		cookies.Delete(id)
		return
	}
	cookies.Store(id, path)
}

// fetch downloads a URL source with yt-dlp.
func (s *Dubs) fetch(ctx context.Context, sc *stageCtx) error {
	p := sc.p
	var y dub.YtDlp
	if s.cfg.YtDlp != nil {
		y = s.cfg.YtDlp()
	}
	if y.Path == "" {
		return stageErr(dub.CodeNoYtDlp, "URL import is off: turn it on in Settings → Dubbing.")
	}
	if _, err := dub.CheckURL(ctx, p.Source.URL, dub.LookupHost); err != nil {
		return urlErr(err)
	}
	sc.progress("downloading", -1, "")
	info, err := s.metaOrInfo(ctx, y, p) // the probe's, when it read the link
	if err != nil {
		return urlErr(err)
	}
	o := dub.DownloadOptions{}
	if p.Source.WantCaptions {
		o.Captions = p.SourceLang
		if o.Captions == "" {
			o.Captions = dubFirst(info.Language, "en")
		}
	}
	if c, ok := cookies.LoadAndDelete(p.ID); ok {
		o.Cookies = c.(string)
	}
	dir := filepath.Join(sc.dir(), "download")
	var last time.Time
	var lastPhase dub.DownloadPhase
	video, captions, err := y.Download(ctx, p.Source.URL, dir, o, func(d dub.DownloadProgress) {
		// A few reports a second; a new phase at once.
		if now := time.Now(); d.Phase != lastPhase || now.Sub(last) >= 250*time.Millisecond {
			last, lastPhase = now, d.Phase
			if sc.report != nil {
				sc.report(FetchProgress(d))
			}
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			os.RemoveAll(dir) // what was fetched so far
		}
		return urlErr(err)
	}
	fi, _ := os.Stat(video)
	return sc.save(ctx, func(p *dub.Project) {
		p.Source.Path = video
		p.Source.Captions = captions
		if fi != nil {
			p.Source.Size = fi.Size()
		}
		if info.Title != "" {
			p.Source.Name = info.Title
			if p.Title == "" || p.Title == p.Source.URL {
				p.Title = info.Title
			}
		}
		if p.SourceLang == "" && info.Language != "" {
			p.Detected = info.Language
		}
		p.AutoRetarget() // a defaulted target is not the spoken language
	})
}

// Stages of a dub.fetch job's progress.
const (
	FetchDownloading = "downloading"
	FetchMerging     = "merging"
	FetchFinishing   = "finishing"
)

// FetchProgress is a URL download's progress as its job's: Bytes and
// TotalBytes of the whole download, ETA, and the fraction (-1 while not
// known, or merging).
func FetchProgress(d dub.DownloadProgress) jobs.Progress {
	pr := jobs.Progress{Stage: FetchDownloading, Fraction: d.Fraction, Bytes: d.Done, TotalBytes: d.Total, ETA: d.ETA}
	switch d.Phase {
	case dub.DownloadMerging:
		pr = jobs.Progress{Stage: FetchMerging, Fraction: -1}
	case dub.DownloadFinishing:
		pr = jobs.Progress{Stage: FetchFinishing, Fraction: -1}
	}
	return pr
}

func urlErr(err error) error {
	var ue *dub.URLError
	if errors.As(err, &ue) {
		data, _ := json.Marshal(map[string]string{"detail": ue.Detail})
		return &jobs.Error{Code: ue.Code, Message: ue.Error(), Data: data}
	}
	return err
}

// playable reports whether the web view plays a video as it is.
func playable(path string, codec string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "mp4", "mov", "m4v":
	default:
		return false
	}
	switch strings.ToLower(codec) {
	case "", "h264", "avc1", "hevc", "hvc1", "hev1", "h265", "mp4v", "mpeg4":
		return true
	}
	return false
}

// ingest probes and decodes the source, and makes what the workspace
// draws: the waveform pyramid, speech onsets, a poster and a preview.
func (s *Dubs) ingest(ctx context.Context, sc *stageCtx) error {
	p := sc.p
	src := p.Source.Path
	if src == "" {
		return stageErr(DubCodeNoSource, "The dub has no source file.")
	}
	fi, err := os.Stat(src)
	if err != nil {
		data, _ := json.Marshal(map[string]string{"path": src})
		return &jobs.Error{Code: proto.CodeFileNotFound, Message: err.Error(), Data: data}
	}
	if err := os.MkdirAll(sc.dir(), 0o755); err != nil {
		return err
	}
	sc.progress("decoding", -1, "")
	isWAV := strings.EqualFold(filepath.Ext(src), ".wav")
	var pr proto.ProbeResult
	if isWAV {
		w, err := dub.OpenWAV(src, 0, 0)
		if err != nil {
			data, _ := json.Marshal(map[string]string{"path": src})
			return &jobs.Error{Code: proto.CodeUnsupportedMedia, Message: err.Error(), Data: data}
		}
		rate, ch := w.Format()
		pr = proto.ProbeResult{Format: "wav", HasAudio: true, AudioTracks: 1, Codec: "lpcm", SampleRate: rate, Channels: ch,
			Duration: float64(w.Frames()) / float64(rate)}
		w.Close()
	} else if err := sc.call(ctx, proto.MethodMediaProbe, "", proto.ProbeParams{Input: src}, nil, &pr, nil); err != nil {
		return err
	}
	if !pr.HasAudio {
		return stageErr("dub.no_audio", "The file has no sound to dub.")
	}
	rate := pr.SampleRate
	if rate <= 0 || rate > 48000 {
		rate = 48000
	}
	ch := min(max(pr.Channels, 1), 2)
	audio := sc.path(dubAudio)
	if isWAV {
		if _, err := convertStream(ctx, src, audio, rate, ch, wav.PCM16, nil); err != nil {
			return err
		}
	} else {
		dp := proto.DecodeParams{Input: src, Output: proto.Output{Path: audio, Format: "wav", Bits: 16}, SampleRate: rate, Channels: ch}
		var dr proto.DecodeResult
		if err := sc.call(ctx, proto.MethodMediaDecode, "", dp, nil, &dr, func(pg jobs.Progress) {
			pg.Stage = "decoding"
			sc.report(pg)
		}); err != nil {
			return err
		}
	}
	// The speech copy, the waveform and the length.
	sc.progress("analysing", -1, "")
	pb := dub.NewPeakBuilder(rate, ch)
	frames, err := convertStream(ctx, audio, sc.path(dubSpeech), 16000, 1, wav.PCM16, nil)
	if err != nil {
		return err
	}
	if err := eachBlock(ctx, audio, func(b []float32) error { pb.Write(b); return nil }); err != nil {
		return err
	}
	if err := dub.WritePeaks(sc.path(dubPeaks), pb.Peaks()); err != nil {
		return err
	}
	onsets, err := speechOnsets(ctx, sc.path(dubSpeech))
	if err != nil {
		return err
	}
	duration := float64(frames) / 16000
	if pr.Duration > 0 && !isWAV {
		duration = pr.Duration
	}
	// Video: a poster and, for codecs the web view cannot play, a proxy.
	poster, proxy := "", ""
	if p.Media.Audio != "" { // a freed dub decoded again (dub_free.go) keeps its poster and preview
		if name := p.Media.Poster; name != "" && fileExists(sc.path(name)) {
			poster = name
		}
		if name := p.Media.Proxy; name != "" && fileExists(sc.path(name)) {
			proxy = name
		}
	}
	if pr.HasVideo && poster == "" {
		tp := dub.ThumbnailParams{Input: src, Times: []float64{min(1, duration*0.1)}, Outputs: []string{sc.path(dubPoster)}, Width: 640}
		var tr dub.ThumbnailResult
		switch err := sc.call(ctx, dub.MethodThumbnail, "", tp, nil, &tr, nil); {
		case err == nil && len(tr.Frames) > 0:
			poster = dubPoster
		case err == nil || missing(err) || code(err) == proto.CodeUnsupportedMedia:
			sc.warn(DubWarnNoPoster)
		default:
			return err
		}
	}
	if pr.HasVideo && proxy == "" {
		codec := ""
		if pr.Video != nil {
			codec = pr.Video.Codec
		}
		if !playable(src, codec) {
			xp := dub.TranscodeParams{Input: src, Output: proto.Output{Path: sc.path(dubProxy), Format: "mp4"}, MaxHeight: 720}
			var xr dub.TranscodeResult
			switch err := sc.call(ctx, dub.MethodTranscode, "", xp, nil, &xr, func(pg jobs.Progress) {
				pg.Stage = "transcoding"
				sc.report(pg)
			}); {
			case err == nil:
				proxy = dubProxy
			case missing(err) || code(err) == proto.CodeUnsupportedMedia:
				sc.warn(DubWarnNoProxy)
			default:
				return err
			}
		}
	}
	return sc.save(ctx, func(p *dub.Project) {
		p.Source.Size, p.Source.Duration, p.Source.HasVideo, p.Source.Audio = fi.Size(), duration, pr.HasVideo, pr.Codec
		if pr.Video != nil {
			p.Source.Video = &dub.VideoInfo{Codec: pr.Video.Codec, Width: pr.Video.Width, Height: pr.Video.Height, FPS: pr.Video.FPS}
		}
		p.Media.Audio, p.Media.Speech, p.Media.Peaks, p.Media.Onsets = dubAudio, dubSpeech, dubPeaks, onsets
		p.Media.Poster, p.Media.Proxy = poster, proxy
	})
}

// convertStream writes src (a WAV) to dst at rate and channels, block by
// block (sources of hours), atomically; also sees each block written.
func convertStream(ctx context.Context, src, dst string, rate, ch int, enc wav.Encoding, also func([]float32)) (int64, error) {
	in, err := dub.OpenWAV(src, rate, ch)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := wav.Create(dst, wav.Format{Rate: rate, Channels: ch, Encoding: enc}, wav.Options{Large: true, Dither: enc == wav.PCM16})
	if err != nil {
		return 0, err
	}
	buf := make([]float32, 16384*ch)
	for {
		if err := ctx.Err(); err != nil {
			out.Abort()
			return 0, err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if also != nil {
				also(buf[:n])
			}
			if err := out.Write(buf[:n]); err != nil {
				out.Abort()
				return 0, err
			}
		}
		if rerr == io.EOF || n == 0 {
			break
		}
		if rerr != nil {
			out.Abort()
			return 0, rerr
		}
	}
	frames := out.Frames()
	return frames, out.Close()
}

// speechOnsets finds speech onsets in a 16 kHz mono WAV, a minute at a time.
func speechOnsets(ctx context.Context, path string) ([]float64, error) {
	in, err := dub.OpenWAV(path, 16000, 1)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	const window = 60 * 16000
	buf := make([]float32, window)
	var out []float64
	offset := 0.0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := 0
		for n < window {
			k, err := in.Read(buf[n:])
			n += k
			if err != nil || k == 0 {
				break
			}
		}
		if n == 0 {
			break
		}
		b := dsp.Buffer{Rate: 16000, Channels: 1, Samples: buf[:n]}
		for _, o := range analysis.Onsets(b, analysis.OnsetOptions{}) {
			if o.Kind == "speech" || o.Strength >= 0.3 {
				out = append(out, offset+o.Time)
			}
		}
		offset += float64(n) / 16000
		if n < window {
			break
		}
	}
	return out, nil
}

// separate splits the voices from the music and effects.
func (s *Dubs) separate(ctx context.Context, sc *stageCtx) error {
	model := s.cfg.Models().Separate
	if model == "" {
		sc.warn(DubWarnNoSeparation)
		sc.skipped = DubWarnNoSeparation
		if s.stemsGone(sc.p) {
			return sc.save(ctx, func(p *dub.Project) { p.Media.Vocals, p.Media.Background, p.Media.Separator = "", "", "" })
		}
		return nil
	}
	sp := dub.SeparateParams{Model: model, Audio: sc.path(dubAudio),
		Vocals:     proto.Output{Path: sc.path(dubVocals), Format: "wav", Bits: 16},
		Background: proto.Output{Path: sc.path(dubBackground), Format: "wav", Bits: 16}}
	var res dub.SeparateResult
	err := sc.call(ctx, dub.MethodSeparate, model, &sp, &sp.ModelPath, &res, func(pg jobs.Progress) {
		if pg.Stage == "" || pg.Stage == "working" {
			pg.Stage = "separating"
		}
		sc.report(pg)
	})
	if missing(err) {
		sc.warn(DubWarnNoSeparation)
		sc.skipped = DubWarnNoSeparation
		if s.stemsGone(sc.p) { // freed, and cannot be made again: the original serves
			return sc.save(ctx, func(p *dub.Project) { p.Media.Vocals, p.Media.Background, p.Media.Separator = "", "", "" })
		}
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := convertStream(ctx, sc.path(dubVocals), sc.path(dubVocals16k), 16000, 1, wav.PCM16, nil); err != nil {
		return err
	}
	return sc.save(ctx, func(p *dub.Project) {
		p.Media.Vocals, p.Media.Background, p.Media.Separator = dubVocals, dubBackground, model
	})
}
