// Package media implements the protocol-v2 "media" role for Windows:
// probe, decode/resample, encode (WAV/M4A/M4B), mux (MP4/MOV), thumbnails,
// H.264 preview transcode with Windows hardware encoder detection
// (h264_mf / h264_qsv / h264_amf / h264_nvenc -> libx264/mpeg4 fallback),
// and a clean hook for AudioSeal ONNX watermarking.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
)

// WatermarkBackend is an optional backend (e.g. ONNX Runtime CPU AudioSeal)
// plugged into the media role for watermark.embed / watermark.detect.
type WatermarkBackend interface {
	Embed(ctx context.Context, p proto.WatermarkEmbedParams) (proto.WatermarkEmbedResult, error)
	Detect(ctx context.Context, p proto.WatermarkDetectParams) (proto.WatermarkDetectResult, error)
}

// EncoderSpec describes the chosen H.264 encoder and its ffmpeg arguments.
type EncoderSpec struct {
	Name     string   `json:"name"`
	Hardware bool     `json:"hardware"`
	Args     []string `json:"args"`
}

// Service implements the media role RPC handlers.
type Service struct {
	hw        hw.Info
	watermark WatermarkBackend

	mu         sync.Mutex
	ffmpegPath string
	tool       *ffmpeg.Tool
	inspected  bool
}

// New creates a media Service. ffmpegPath may be empty to auto-discover
// ffmpeg from AUDIOSMITH_FFMPEG, beside the executable, or PATH.
func New(hwInfo hw.Info, ffmpegPath string, wm WatermarkBackend) *Service {
	return &Service{
		hw:         hwInfo,
		ffmpegPath: ffmpegPath,
		watermark:  wm,
	}
}

// Register attaches all media role RPC handlers to s.
func (m *Service) Register(s *server.Server) {
	s.Handle(proto.MethodMediaProbe, m.handleProbe)
	s.Handle(proto.MethodMediaDecode, m.handleDecode)
	s.Handle(proto.MethodMediaEncode, m.handleEncode)
	s.Handle(proto.MethodMediaMux, m.handleMux)
	s.Handle(proto.MethodMediaThumbnail, m.handleThumbnail)
	s.Handle(proto.MethodMediaTranscode, m.handleTranscode)
	s.Handle(proto.MethodWatermarkEmbed, m.handleWatermarkEmbed)
	s.Handle(proto.MethodWatermarkDetect, m.handleWatermarkDetect)
}

// SelectH264Encoder picks the best available Windows H.264 encoder for the
// detected hardware, preferring native iGPU/OS encoders (h264_qsv, h264_amf,
// h264_mf) on everyday PCs and h264_nvenc on CUDA machines, with automatic
// libx264 / mpeg4 software fallback.
func SelectH264Encoder(hasEncoder func(string) bool, hwInfo hw.Info) EncoderSpec {
	vendor := ""
	if hwInfo.PreferredAdapter != nil {
		vendor = hwInfo.PreferredAdapter.Vendor
	}
	var order []EncoderSpec
	switch {
	case hwInfo.GPUTier == hw.TierCUDA || vendor == "NVIDIA":
		order = []EncoderSpec{
			{Name: "h264_nvenc", Hardware: true, Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "22"}},
			{Name: "h264_mf", Hardware: true, Args: []string{"-c:v", "h264_mf", "-rate_control", "quality", "-quality", "75"}},
			{Name: "h264_qsv", Hardware: true, Args: []string{"-c:v", "h264_qsv", "-global_quality", "22"}},
			{Name: "h264_amf", Hardware: true, Args: []string{"-c:v", "h264_amf", "-quality", "balanced"}},
		}
	case vendor == "Intel":
		order = []EncoderSpec{
			{Name: "h264_qsv", Hardware: true, Args: []string{"-c:v", "h264_qsv", "-global_quality", "22"}},
			{Name: "h264_mf", Hardware: true, Args: []string{"-c:v", "h264_mf", "-rate_control", "quality", "-quality", "75"}},
			{Name: "h264_amf", Hardware: true, Args: []string{"-c:v", "h264_amf", "-quality", "balanced"}},
			{Name: "h264_nvenc", Hardware: true, Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "22"}},
		}
	case vendor == "AMD":
		order = []EncoderSpec{
			{Name: "h264_amf", Hardware: true, Args: []string{"-c:v", "h264_amf", "-quality", "balanced"}},
			{Name: "h264_mf", Hardware: true, Args: []string{"-c:v", "h264_mf", "-rate_control", "quality", "-quality", "75"}},
			{Name: "h264_qsv", Hardware: true, Args: []string{"-c:v", "h264_qsv", "-global_quality", "22"}},
			{Name: "h264_nvenc", Hardware: true, Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "22"}},
		}
	default:
		// Everyday PC / ARM64 / VM: prefer Media Foundation, then vendor HW, then software.
		order = []EncoderSpec{
			{Name: "h264_mf", Hardware: true, Args: []string{"-c:v", "h264_mf", "-rate_control", "quality", "-quality", "75"}},
			{Name: "h264_qsv", Hardware: true, Args: []string{"-c:v", "h264_qsv", "-global_quality", "22"}},
			{Name: "h264_amf", Hardware: true, Args: []string{"-c:v", "h264_amf", "-quality", "balanced"}},
			{Name: "h264_nvenc", Hardware: true, Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "22"}},
		}
	}
	for _, spec := range order {
		if hasEncoder != nil && hasEncoder(spec.Name) {
			return spec
		}
	}
	if hasEncoder != nil && hasEncoder("libx264") {
		return EncoderSpec{Name: "libx264", Hardware: false, Args: []string{"-c:v", "libx264", "-crf", "20", "-preset", "veryfast"}}
	}
	return EncoderSpec{Name: "mpeg4", Hardware: false, Args: []string{"-c:v", "mpeg4", "-q:v", "3"}}
}

func (m *Service) resolveTool(ctx context.Context) (*ffmpeg.Tool, error) {
	m.mu.Lock()
	if m.inspected {
		t := m.tool
		m.mu.Unlock()
		if t == nil {
			return nil, ffmpeg.ErrNotFound
		}
		return t, nil
	}
	path := m.ffmpegPath
	m.mu.Unlock()

	if path == "" {
		path = findFFmpeg()
	}
	var tool *ffmpeg.Tool
	if path != "" {
		if t, err := ffmpeg.Inspect(ctx, path); err == nil {
			tool = t
		}
	}
	m.mu.Lock()
	m.tool = tool
	m.inspected = true
	m.mu.Unlock()
	if tool == nil {
		return nil, ffmpeg.ErrNotFound
	}
	return tool, nil
}

func findFFmpeg() string {
	if p := os.Getenv("AUDIOSMITH_FFMPEG"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range []string{"ffmpeg.exe", "ffmpeg"} {
			cand := filepath.Join(dir, name)
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	return ""
}

func checkInputExists(path string) error {
	if path == "" {
		return proto.Errorf(proto.CodeBadRequest, "input path is required")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data, _ := json.Marshal(map[string]string{"path": path})
			return &proto.Error{Code: proto.CodeFileNotFound, Message: fmt.Sprintf("file not found: %s", path), Data: data}
		}
		return proto.Errorf(proto.CodeInternal, "stat %s: %v", path, err)
	}
	return nil
}

func (m *Service) handleProbe(ctx context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.ProbeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid media.probe params: %v", err)
	}
	if err := checkInputExists(p.Input); err != nil {
		return nil, err
	}
	if isWAVPath(p.Input) {
		if res, err := probeWAV(p.Input); err == nil {
			return res, nil
		}
	}
	tool, err := m.resolveTool(ctx)
	if err != nil {
		return nil, unsupportedMediaError(p.Input, "ffmpeg/ffprobe not installed and file is not a standard WAV")
	}
	pr, err := tool.Probe(ctx, p.Input)
	if err != nil {
		return nil, unsupportedMediaError(p.Input, err.Error())
	}
	return toProtoProbe(pr), nil
}

func (m *Service) handleDecode(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.DecodeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid media.decode params: %v", err)
	}
	if err := checkInputExists(p.Input); err != nil {
		return nil, err
	}
	if p.Output.Path == "" {
		return nil, proto.Errorf(proto.CodeBadRequest, "output.path is required")
	}

	start := time.Now()
	if isWAVPath(p.Input) && p.Track == 0 {
		out, src, err := decodeWAVNative(p)
		if err == nil {
			return proto.DecodeResult{
				AudioOut: out,
				Source:   src,
				Seconds:  time.Since(start).Seconds(),
			}, nil
		}
	}

	tool, err := m.resolveTool(ctx)
	if err != nil {
		return nil, unsupportedMediaError(p.Input, "ffmpeg is required to decode non-WAV media")
	}
	srcProbe, _ := tool.Probe(ctx, p.Input)
	err = tool.DecodeAudio(ctx, ffmpeg.DecodeOptions{
		Input:      p.Input,
		Output:     p.Output.Path,
		SampleRate: p.SampleRate,
		Channels:   p.Channels,
		Bits:       p.Output.Bits,
		Start:      p.Start,
		Duration:   p.Duration,
		Track:      p.Track,
		Total:      srcProbe.Duration,
	}, progressNotifier(jobID, "decoding", n))
	if err != nil {
		return nil, err
	}
	outProbe, err := probeWAV(p.Output.Path)
	if err != nil {
		return nil, err
	}
	frames := int64(outProbe.Duration * float64(outProbe.SampleRate))
	return proto.DecodeResult{
		AudioOut: proto.AudioOut{
			Output:       p.Output.Path,
			SampleRate:   outProbe.SampleRate,
			Channels:     outProbe.Channels,
			Frames:       frames,
			AudioSeconds: outProbe.Duration,
		},
		Source:  toProtoProbe(srcProbe),
		Seconds: time.Since(start).Seconds(),
	}, nil
}

func (m *Service) handleEncode(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.EncodeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid media.encode params: %v", err)
	}
	if err := checkInputExists(p.Input); err != nil {
		return nil, err
	}
	if p.Output.Path == "" {
		return nil, proto.Errorf(proto.CodeBadRequest, "output.path is required")
	}
	format := strings.ToLower(p.Output.Format)
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(p.Output.Path)), ".")
	}
	if format == "wav" && isWAVPath(p.Input) {
		out, _, err := decodeWAVNative(proto.DecodeParams{
			Input:      p.Input,
			Output:     p.Output,
			SampleRate: p.SampleRate,
		})
		if err != nil {
			return nil, err
		}
		fi, _ := os.Stat(p.Output.Path)
		var size int64
		if fi != nil {
			size = fi.Size()
		}
		return proto.EncodeResult{
			Output:   p.Output.Path,
			Format:   "wav",
			Bytes:    size,
			Duration: out.AudioSeconds,
		}, nil
	}

	tool, err := m.resolveTool(ctx)
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupported, "ffmpeg is required to encode %s", format)
	}
	ffFormat := format
	if ffFormat == "m4b" {
		ffFormat = ffmpeg.M4A
	}
	chapters := make([]ffmpeg.Chapter, 0, len(p.Chapters))
	for _, c := range p.Chapters {
		chapters = append(chapters, ffmpeg.Chapter{Title: c.Title, Start: c.Start, End: c.End})
	}
	meta := map[string]string{
		"title":        p.Metadata.Title,
		"artist":       p.Metadata.Author,
		"album":        p.Metadata.Title,
		"album_artist": p.Metadata.Author,
		"composer":     p.Metadata.Narrator,
		"date":         p.Metadata.Year,
		"genre":        p.Metadata.Genre,
		"comment":      p.Metadata.Description,
	}
	var dur float64
	if pr, err := probeWAV(p.Input); err == nil {
		dur = pr.Duration
	}
	err = tool.EncodeAudio(ctx, ffmpeg.EncodeOptions{
		Input:      p.Input,
		Output:     p.Output.Path,
		Format:     ffFormat,
		Bitrate:    p.Bitrate,
		SampleRate: p.SampleRate,
		Metadata:   meta,
		Chapters:   chapters,
		Cover:      p.Cover,
		Duration:   dur,
	}, progressNotifier(jobID, "encoding", n))
	if err != nil {
		return nil, err
	}
	fi, _ := os.Stat(p.Output.Path)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	return proto.EncodeResult{
		Output:   p.Output.Path,
		Format:   format,
		Bytes:    size,
		Duration: dur,
		Bitrate:  p.Bitrate,
	}, nil
}

func (m *Service) handleMux(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.MuxParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid media.mux params: %v", err)
	}
	if err := checkInputExists(p.Video); err != nil {
		return nil, err
	}
	if len(p.Retime) > 0 {
		data, _ := json.Marshal(map[string]string{"capability": "retime"})
		return nil, &proto.Error{Code: proto.CodeUnsupported, Message: "video retiming is not supported yet", Data: data}
	}
	tool, err := m.resolveTool(ctx)
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupported, "ffmpeg is required for media.mux")
	}
	pr, _ := tool.Probe(ctx, p.Video)
	audioTracks := make([]ffmpeg.Track, 0, len(p.Audio))
	for _, a := range p.Audio {
		audioTracks = append(audioTracks, ffmpeg.Track{
			Path: a.Path, Language: a.Language, Title: a.Title, Default: a.Default, Bitrate: a.Bitrate,
		})
	}
	subTracks := make([]ffmpeg.SubtitleTrack, 0, len(p.Subtitles))
	for _, s := range p.Subtitles {
		subTracks = append(subTracks, ffmpeg.SubtitleTrack{
			Path: s.Path, Language: s.Language, Title: s.Title, Default: s.Default,
		})
	}
	err = tool.Mux(ctx, ffmpeg.MuxOptions{
		Video:     p.Video,
		KeepAudio: p.KeepAudio,
		CopyVideo: true,
		Audio:     audioTracks,
		Subtitles: subTracks,
		Output:    p.Output.Path,
		Duration:  pr.Duration,
	}, progressNotifier(jobID, "muxing", n))
	if err != nil {
		return nil, err
	}
	fi, _ := os.Stat(p.Output.Path)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	tracks := len(p.Audio)
	if p.KeepAudio {
		tracks++
	}
	return proto.MuxResult{
		Output:   p.Output.Path,
		Bytes:    size,
		Duration: pr.Duration,
		Tracks:   tracks,
	}, nil
}

func (m *Service) handleThumbnail(ctx context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.ThumbnailParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid media.thumbnail params: %v", err)
	}
	if err := checkInputExists(p.Input); err != nil {
		return nil, err
	}
	tool, err := m.resolveTool(ctx)
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupported, "ffmpeg is required for media.thumbnail")
	}
	frames, err := tool.Thumbnails(ctx, p.Input, p.Times, p.Outputs, p.Width)
	if err != nil {
		return nil, err
	}
	out := make([]proto.Frame, 0, len(frames))
	for _, f := range frames {
		out = append(out, proto.Frame{Time: f.Time, Path: f.Path, Width: f.Width, Height: f.Height})
	}
	return proto.ThumbnailResult{Frames: out}, nil
}

func (m *Service) handleTranscode(ctx context.Context, jobID string, raw json.RawMessage, n server.Notifier) (any, error) {
	var p proto.TranscodeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid media.transcode params: %v", err)
	}
	if err := checkInputExists(p.Input); err != nil {
		return nil, err
	}
	tool, err := m.resolveTool(ctx)
	if err != nil {
		return nil, proto.Errorf(proto.CodeUnsupported, "ffmpeg is required for media.transcode")
	}
	pr, _ := tool.Probe(ctx, p.Input)
	err = tool.Transcode(ctx, ffmpeg.TranscodeOptions{
		Input:     p.Input,
		Output:    p.Output.Path,
		MaxHeight: p.MaxHeight,
		NoAudio:   p.NoAudio,
		Duration:  pr.Duration,
	}, progressNotifier(jobID, "transcoding", n))
	if err != nil {
		return nil, err
	}
	fi, _ := os.Stat(p.Output.Path)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	outProbe, _ := tool.Probe(ctx, p.Output.Path)
	var w, h int
	if len(outProbe.Video) > 0 {
		w, h = outProbe.Video[0].Width, outProbe.Video[0].Height
	}
	return proto.TranscodeResult{
		Output:   p.Output.Path,
		Bytes:    size,
		Duration: outProbe.Duration,
		Width:    w,
		Height:   h,
	}, nil
}

func (m *Service) handleWatermarkEmbed(ctx context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.WatermarkEmbedParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid watermark.embed params: %v", err)
	}
	if m.watermark == nil {
		data, _ := json.Marshal(map[string]string{"capability": "watermark"})
		return nil, &proto.Error{Code: proto.CodeUnsupported, Message: "AudioSeal ONNX runtime is not installed", Data: data}
	}
	return m.watermark.Embed(ctx, p)
}

func (m *Service) handleWatermarkDetect(ctx context.Context, _ string, raw json.RawMessage, _ server.Notifier) (any, error) {
	var p proto.WatermarkDetectParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeBadRequest, "invalid watermark.detect params: %v", err)
	}
	if m.watermark == nil {
		data, _ := json.Marshal(map[string]string{"capability": "watermark"})
		return nil, &proto.Error{Code: proto.CodeUnsupported, Message: "AudioSeal ONNX runtime is not installed", Data: data}
	}
	return m.watermark.Detect(ctx, p)
}

func toProtoProbe(pr ffmpeg.Probe) proto.ProbeResult {
	res := proto.ProbeResult{
		Format:      pr.Format,
		Duration:    pr.Duration,
		HasAudio:    len(pr.Audio) > 0,
		HasVideo:    len(pr.Video) > 0,
		AudioTracks: len(pr.Audio),
	}
	if len(pr.Audio) > 0 {
		a := pr.Audio[0]
		res.Codec = a.Codec
		res.SampleRate = a.SampleRate
		res.Channels = a.Channels
		res.Bits = a.Bits
		res.BitRate = a.BitRate
	}
	if len(pr.Video) > 0 {
		v := pr.Video[0]
		res.Video = &proto.VideoInfo{Codec: v.Codec, Width: v.Width, Height: v.Height, FPS: v.FPS}
	}
	return res
}

func unsupportedMediaError(path, detail string) *proto.Error {
	data, _ := json.Marshal(map[string]string{"path": path})
	return &proto.Error{Code: proto.CodeUnsupportedMedia, Message: detail, Data: data}
}

func progressNotifier(jobID, stage string, n server.Notifier) func(float64) {
	if n == nil {
		return nil
	}
	return func(frac float64) {
		f := frac
		_ = n.Notify(proto.MethodJobProgress, proto.ProgressParams{
			Job:      jobID,
			Stage:    stage,
			Fraction: &f,
		})
	}
}
