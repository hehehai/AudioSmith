package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/mirror"
)

// fakeFFmpeg writes a script that answers like ffmpeg 8 with MP3, Opus
// and FLAC encoders and the overlay filter, and an ffprobe beside it.
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		script := "@echo off\r\n" +
			"if \"%2\"==\"-version\" (\r\n" +
			"  echo ffmpeg version n8.1.2-fake Copyright\r\n" +
			"  echo configuration: --enable-gpl --enable-libmp3lame\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"if \"%2\"==\"-encoders\" (\r\n" +
			"  echo  A....D = Audio\r\n" +
			"  echo  ------\r\n" +
			"  echo  A....D libmp3lame  MP3\r\n" +
			"  echo  A....D libopus  Opus\r\n" +
			"  echo  A....D flac  FLAC\r\n" +
			"  echo  A....D aac  AAC\r\n" +
			"  exit /b 0\r\n" +
			")\r\n" +
			"if \"%2\"==\"-filters\" (\r\n" +
			"  echo  TSC overlay VV-^>V Overlay\r\n" +
			"  exit /b 0\r\n" +
			")\r\n"
		for _, n := range []string{"ffmpeg.cmd", "ffprobe.cmd"} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "ffmpeg.cmd")
	}
	script := `#!/bin/sh
case "$*" in
*-version*) echo "ffmpeg version n8.1.2-fake Copyright"; echo "configuration: --enable-gpl --enable-libmp3lame";;
*-encoders*) printf ' A....D = Audio\n ------\n A....D libmp3lame  MP3\n A....D libopus  Opus\n A....D flac  FLAC\n A....D aac  AAC\n';;
*-filters*) printf ' TSC overlay VV->V Overlay\n';;
esac
`
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "ffmpeg")
}

// realFFmpeg is the ffmpeg on PATH, or skips.
func realFFmpeg(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("no ffprobe on PATH")
	}
	return p
}

func TestAudioToolsPage(t *testing.T) {
	a, _, tt := newTestApp(t)
	fake := fakeFFmpeg(t)
	ffmpegFind = func() []string { return []string{fake} }
	t.Cleanup(func() { ffmpegFind = ffmpeg.FindSystem })
	a.navigate("/settings/audio-tools")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Not set up") || tt.HasText("Download ffmpeg") || !tt.HasText("Use this one") {
		t.Fatalf("page: %q", tt.Texts())
	}
	if a.mediaTool() != nil {
		t.Fatal("a tool before any is set up")
	}
	if err := tt.Click("Use this one"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	tool := a.mediaTool()
	if tool == nil || tool.Version != "n8.1.2-fake" || a.settings.Get().Media.Origin != settings.MediaSystem {
		t.Fatalf("tool %+v err %q settings %+v", tool, a.ffErr, a.settings.Get().Media)
	}
	if !tt.HasText("ffmpeg is ready") || !tt.HasText("MP3 export") || !tt.HasText("Stop using it") {
		t.Fatalf("ready: %q", tt.Texts())
	}
	if err := tt.Click("Stop using it"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.mediaTool() != nil || a.settings.Get().Media.FFmpeg != "" || !tt.HasText("Not set up") {
		t.Fatalf("stopped: %+v", a.settings.Get().Media)
	}
	// A path that does not run says so.
	a.useFFmpeg(filepath.Join(t.TempDir(), "nothing"), settings.MediaCustom)
	tt.Frame()
	if a.mediaTool() != nil || !tt.HasText("ffmpeg does not run") {
		t.Errorf("broken: %q", tt.Texts())
	}
}

func TestFFmpegDownload(t *testing.T) {
	a, _, tt := newTestApp(t)
	fake := fakeFFmpeg(t)
	var gotDir string
	ffmpegInstall = func(ctx context.Context, _ *mirror.Client, dir string, b ffmpeg.Build, progress func(int64, int64)) (string, error) {
		gotDir = dir
		progress(b.Size()/2, b.Size())
		os.MkdirAll(dir, 0o755)
		data, _ := os.ReadFile(fake)
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".cmd"
		}
		for _, n := range []string{"ffmpeg" + ext, "ffprobe" + ext} {
			os.WriteFile(filepath.Join(dir, n), data, 0o755)
		}
		return filepath.Join(dir, "ffmpeg"+ext), nil
	}
	ffmpegFind = func() []string { return nil }
	ffmpegDownloadOffered = true
	t.Cleanup(func() { ffmpegInstall, ffmpegFind, ffmpegDownloadOffered = ffmpeg.Install, ffmpeg.FindSystem, false })
	a.navigate("/settings/audio-tools")
	tt.Frame()
	if !tt.HasText("No ffmpeg found in Homebrew's or MacPorts' folders. With Homebrew: brew install ffmpeg") {
		t.Errorf("no system copy: %q", tt.Texts())
	}
	if err := tt.Click("Download"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if gotDir != filepath.Join(a.lib.store.Dir(), "tools", "ffmpeg") {
		t.Errorf("dir %s", gotDir)
	}
	m := a.settings.Get().Media
	if m.Origin != settings.MediaDownloaded || a.mediaTool() == nil || !strings.Contains(strings.Join(tt.Texts(), " "), "Downloaded by AudioSmith") {
		t.Fatalf("after download %+v %q", m, tt.Texts())
	}
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, err := os.Stat(gotDir); !os.IsNotExist(err) {
		t.Errorf("download not removed: %v", err)
	}
}

// supStub answers media calls like an engine that cannot read the file.
type supStub struct {
	engine.Supervisor
	calls []string
}

func (s *supStub) Call(ctx context.Context, role proto.Role, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error) {
	s.calls = append(s.calls, method)
	if method == proto.MethodMediaEncode {
		return json.Marshal(proto.EncodeResult{Format: "m4a"})
	}
	data, _ := json.Marshal(map[string]string{"path": "x"})
	return nil, &proto.Error{Code: proto.CodeUnsupportedMedia, Message: "Cannot read", Data: data}
}

func TestMediaSupFallback(t *testing.T) {
	stub := &supStub{}
	var tool *ffmpeg.Tool
	sup := withMediaTools(stub, func() *ffmpeg.Tool { return tool })
	if withMediaTools(sup, nil) != sup || unwrapSup(sup) != stub {
		t.Fatal("wrap/unwrap")
	}
	ctx := context.Background()
	dir := t.TempDir()
	// Without ffmpeg: the engine's answers stand; MP3 says it needs ffmpeg.
	var je *jobs.Error
	if _, err := engine.Probe(ctx, sup, filepath.Join(dir, "a.webm")); !errors.As(err, &je) || je.Code != proto.CodeUnsupportedMedia {
		t.Errorf("probe without ffmpeg: %v", err)
	}
	_, err := sup.Call(ctx, proto.RoleMedia, "j1", proto.MethodMediaEncode, proto.EncodeParams{Input: "in.wav", Output: proto.Output{Path: filepath.Join(dir, "o.mp3"), Format: "mp3"}}, nil)
	var pe *proto.Error
	if !errors.As(err, &pe) || !isNeedsFFmpeg(pe.Code, pe.Data) {
		t.Errorf("mp3 without ffmpeg: %v", err)
	}
	if res, err := sup.Call(ctx, proto.RoleMedia, "j2", proto.MethodMediaEncode, proto.EncodeParams{Output: proto.Output{Format: "m4a"}}, nil); err != nil || !strings.Contains(string(res), "m4a") {
		t.Errorf("m4a goes to the engine: %s %v", res, err)
	}
	if _, err := sup.Call(ctx, proto.RoleMedia, "j3", methodBurn, BurnParams{}, nil); !errors.As(err, &pe) || !isNeedsFFmpeg(pe.Code, pe.Data) {
		t.Errorf("burn without ffmpeg: %v", err)
	}

	// With a real ffmpeg: WebM in, MP3 out, a burnt-in video.
	path := realFFmpeg(t)
	tool, err = ffmpeg.Inspect(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !tool.HasEncoder("libvpx-vp9") || !tool.CanEncode(ffmpeg.MP3) {
		t.Skip("this ffmpeg lacks VP9 or MP3")
	}
	webm := filepath.Join(dir, "clip.webm")
	if out, err := exec.Command(path, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=2", "-c:v", "libvpx-vp9", "-c:a", "libopus", webm).CombinedOutput(); err != nil {
		t.Skipf("making a WebM: %v %s", err, out)
	}
	info, err := engine.Probe(ctx, sup, webm)
	if err != nil || info.Format != "webm" || !info.HasVideo || info.Video.Codec != "vp9" || info.Codec != "opus" {
		t.Fatalf("probe %+v %v", info, err)
	}
	wav := filepath.Join(dir, "clip.wav")
	var progress []float64
	raw, err := sup.Call(ctx, proto.RoleMedia, "j4", proto.MethodMediaDecode, proto.DecodeParams{Input: webm, Output: proto.Output{Path: wav}, SampleRate: 16000, Channels: 1},
		func(m proto.Message) {
			var p proto.ProgressParams
			m.DecodeParams(&p)
			if p.Fraction != nil {
				progress = append(progress, *p.Fraction)
			}
		})
	var dr proto.DecodeResult
	json.Unmarshal(raw, &dr)
	if err != nil || dr.SampleRate != 16000 || dr.Channels != 1 || dr.AudioSeconds < 1.9 || dr.Source.Format != "webm" {
		t.Fatalf("decode %+v %v", dr, err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 1 {
		t.Errorf("progress %v", progress)
	}
	mp3 := filepath.Join(dir, "clip.mp3")
	raw, err = sup.Call(ctx, proto.RoleMedia, "j5", proto.MethodMediaEncode, proto.EncodeParams{Input: wav, Output: proto.Output{Path: mp3, Format: "mp3"},
		Metadata: proto.EncodeMetadata{Title: "Clip"}, Chapters: []proto.EncodeChapter{{Title: "All", Start: 0, End: 2}}}, nil)
	var er proto.EncodeResult
	json.Unmarshal(raw, &er)
	if err != nil || er.Format != "mp3" || er.Bytes == 0 {
		t.Fatalf("mp3 %+v %v", er, err)
	}
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,200 --> 00:00:01,500\nHello\n"), 0o644)
	out := filepath.Join(dir, "burnt.mp4")
	if _, err := sup.Call(ctx, proto.RoleMedia, "j6", methodBurn, BurnParams{Video: webm, Audio: wav, Subtitles: srt, Output: proto.Output{Path: out}}, nil); err != nil {
		t.Fatalf("burn %v", err)
	}
	if info, err := engine.Probe(ctx, sup, out); err != nil || info.Video == nil || !info.HasAudio {
		t.Errorf("burnt %+v %v", info, err)
	}
	// Preview proxy and poster.
	prev := filepath.Join(dir, "preview.mp4")
	if _, err := sup.Call(ctx, proto.RoleMedia, "j7", proto.MethodMediaTranscode, proto.TranscodeParams{Input: webm, Output: proto.Output{Path: prev, Format: "mp4"}, MaxHeight: 120}, nil); err != nil {
		t.Errorf("transcode %v", err)
	}
	raw, err = sup.Call(ctx, proto.RoleMedia, "j8", proto.MethodMediaThumbnail, proto.ThumbnailParams{Input: webm, Times: []float64{1}, Outputs: []string{filepath.Join(dir, "p.jpg")}, Width: 64}, nil)
	var tr proto.ThumbnailResult
	json.Unmarshal(raw, &tr)
	if err != nil || len(tr.Frames) != 1 || tr.Frames[0].Width != 64 {
		t.Errorf("thumbnail %+v %v", tr, err)
	}
	_ = core.ErrEngine
}

// The Export audio dialog writes MP3 with ffmpeg.
func TestSaveTakeMP3(t *testing.T) {
	path := realFFmpeg(t)
	a, _, tt := newTestApp(t)
	tool, err := ffmpeg.Inspect(context.Background(), path)
	if err != nil || !tool.CanEncode(ffmpeg.MP3) {
		t.Skip("no MP3 encoder")
	}
	a.ffTool.Store(tool)
	take := core.Take{ID: "01JTAKE0000000000000000001", Text: "Hello there", Model: kokoro, Voice: "af_heart", Speed: 1, SampleRate: 24000}
	addTake(t, a, take, 1)
	take = a.takes[0] // with its file
	exportSaveDir = t.TempDir()
	t.Cleanup(func() { exportSaveDir = "" })
	a.openExport(&take) // the Export audio dialog (export_dialog.go)
	a.exp.format = formatIndex(ffmpeg.MP3)
	a.exportChoose(a.exp)
	toasts := a.toasts
	tt.Frame()
	ents, _ := os.ReadDir(exportSaveDir)
	if len(ents) != 1 || filepath.Ext(ents[0].Name()) != ".mp3" {
		t.Fatalf("saved %v (toasts %q)", ents, toasts)
	}
}
