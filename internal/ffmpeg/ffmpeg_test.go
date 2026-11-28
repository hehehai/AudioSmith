package ffmpeg

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hehehai/actnowkit/mirror"
)

const versionOut = `ffmpeg version n8.1.2 Copyright (c) 2000-2026 the FFmpeg developers
built with Apple clang version 17.0.0
configuration: --pkg-config-flags=--static --enable-libmp3lame --enable-libopus --enable-gpl --enable-version3 --enable-videotoolbox
libavutil      60.  8.100 / 60.  8.100
`

const encodersOut = `Encoders:
 V..... = Video
 A..... = Audio
 ------
 V....D h264_videotoolbox    VideoToolbox H.264 Encoder (codec h264)
 A....D aac                  AAC (Advanced Audio Coding)
 A....D flac                 FLAC (Free Lossless Audio Codec)
 A....D libmp3lame           libmp3lame MP3 (MPEG audio layer 3) (codec mp3)
 A....D libopus              libopus Opus (codec opus)
`

const filtersOut = `Filters:
  T.. = Timeline support
  | = Source or sink filter
 TSC overlay           VV->V      Overlay a video source on top of the input.
 ..C scale             V->V       Scale the input video size and/or convert the image format.
`

func TestParse(t *testing.T) {
	v, cfg := parseVersion(versionOut)
	if v != "n8.1.2" || !strings.Contains(cfg, "--enable-gpl") || licenceOf(cfg) != "GPL" {
		t.Fatalf("version %q config %q", v, cfg)
	}
	if licenceOf("--enable-shared") != "LGPL" || licenceOf("--enable-gpl --enable-nonfree") != "nonfree" {
		t.Error("licence")
	}
	tool := &Tool{encoders: parseList(encodersOut), filters: parseList(filtersOut)}
	for _, e := range []string{"h264_videotoolbox", "aac", "flac", "libmp3lame", "libopus"} {
		if !tool.HasEncoder(e) {
			t.Errorf("no %s in %v", e, tool.encoders)
		}
	}
	if tool.HasEncoder("Video") || tool.HasEncoder("=") {
		t.Errorf("legend parsed as encoders: %v", tool.encoders)
	}
	if !tool.HasFilter("overlay") || tool.HasFilter("subtitles") || !tool.CanBurn() {
		t.Errorf("filters %v", tool.filters)
	}
	if !tool.CanEncode(MP3) || !tool.CanEncode(Opus) || !tool.CanEncode(FLAC) || tool.CanEncode(OGG) {
		t.Error("CanEncode")
	}
	if got := tool.videoCodec(); got[1] != "h264_videotoolbox" {
		t.Errorf("video codec %v", got)
	}
	var none *Tool
	if none.CanEncode(MP3) || none.HasFilter("overlay") {
		t.Error("nil tool")
	}
}

func TestFFmetadata(t *testing.T) {
	got := ffmetadata(map[string]string{"title": "A=B; #1", "artist": "Me\\You", "genre": " "},
		[]Chapter{{Title: "One", Start: 0, End: 1.5}, {Title: "Two\nlines", Start: 1.5, End: 3}})
	want := ";FFMETADATA1\ntitle=A\\=B\\; \\#1\nartist=Me\\\\You\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1500\ntitle=One\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1500\nEND=3000\ntitle=Two\\\nlines\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestParseProbe(t *testing.T) {
	p, err := parseProbe([]byte(`{"streams":[
		{"codec_type":"video","codec_name":"vp9","width":1280,"height":720,"avg_frame_rate":"30000/1001"},
		{"codec_type":"audio","codec_name":"opus","sample_rate":"48000","channels":2,"bit_rate":"N/A"},
		{"codec_type":"video","codec_name":"mjpeg","width":300,"height":300,"disposition":{"attached_pic":1}},
		{"codec_type":"audio","codec_name":"pcm_s24le","sample_rate":"44100","channels":1,"bits_per_raw_sample":"24"}],
		"format":{"format_name":"matroska,webm","duration":"12.500000"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != "matroska,webm" || p.Duration != 12.5 || len(p.Video) != 1 || len(p.Audio) != 2 {
		t.Fatalf("%+v", p)
	}
	if v := p.Video[0]; v.Codec != "vp9" || v.Width != 1280 || math.Abs(v.FPS-29.97) > 0.01 {
		t.Errorf("video %+v", v)
	}
	if a := p.Audio[1]; a.Bits != 24 || a.SampleRate != 44100 || p.Audio[0].Bits != 0 {
		t.Errorf("audio %+v", p.Audio)
	}
}

func TestInstall(t *testing.T) {
	files := map[string][]byte{"/ffmpeg": []byte("ffmpeg binary"), "/ffprobe": []byte("ffprobe binary!")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	b := Build{Version: "test",
		FFmpeg:  Asset{Path: "/ffmpeg", SHA256: sum(files["/ffmpeg"]), Size: int64(len(files["/ffmpeg"]))},
		FFprobe: Asset{Path: "/ffprobe", SHA256: sum(files["/ffprobe"]), Size: int64(len(files["/ffprobe"]))}}
	src := &mirror.Client{Sources: []string{srv.URL}, HTTP: srv.Client}
	dir := filepath.Join(t.TempDir(), "tools", "ffmpeg")
	var last, total int64
	path, err := Install(context.Background(), src, dir, b, func(d, tot int64) { last, total = d, tot })
	if err != nil {
		t.Fatal(err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	if path != filepath.Join(dir, "ffmpeg"+ext) || last != total || total != b.Size() {
		t.Errorf("path %s progress %d/%d", path, last, total)
	}
	if fi, err := os.Stat(filepath.Join(dir, "ffprobe"+ext)); err != nil || (runtime.GOOS != "windows" && fi.Mode()&0o111 == 0) {
		t.Errorf("ffprobe %v %v", fi, err)
	}
	if err := Verify(dir, b); err != nil {
		t.Error(err)
	}
	// A wrong digest leaves what was there and no temporary files.
	bad := b
	bad.FFprobe.SHA256 = strings.Repeat("0", 64)
	os.WriteFile(filepath.Join(dir, "ffmpeg"+ext), []byte("old"), 0o755)
	_, err = Install(context.Background(), src, dir, bad, nil)
	var ce *ChecksumError
	if !errors.As(err, &ce) {
		t.Fatalf("checksum: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "ffmpeg"+ext)); string(data) != "old" {
		t.Errorf("a failed install replaced ffmpeg: %q", data)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Errorf("left %d files", len(ents))
	}
	if err := Verify(dir, b); err == nil {
		t.Error("Verify passed a changed file")
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Remove left %s", dir)
	}
}

// source is a fake source of GitHub release files (github.com or the
// mirror): files by path, served after delay; hits counts requests.
type source struct {
	*httptest.Server
	hits atomic.Int32
}

func newSource(t *testing.T, files map[string][]byte, delay time.Duration) *source {
	t.Helper()
	s := &source{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Write(data)
	}))
	t.Cleanup(s.Close)
	return s
}

// TestInstallSources: the build comes from the mirror when GitHub does
// not answer, from GitHub when the mirror serves other files, and fails
// as a checksum error when every source serves other files.
func TestInstallSources(t *testing.T) {
	const ffPath = "/shaka-project/static-ffmpeg-binaries/releases/download/n8.1.2-1/ffmpeg-osx-arm64"
	const fpPath = "/shaka-project/static-ffmpeg-binaries/releases/download/n8.1.2-1/ffprobe-osx-arm64"
	good := map[string][]byte{ffPath: []byte("ffmpeg binary"), fpPath: []byte("ffprobe binary!")}
	bad := map[string][]byte{ffPath: []byte("tampered ffmpeg"), fpPath: []byte("tampered ffprobe")}
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	b := Build{Version: "test",
		FFmpeg:  Asset{Path: ffPath, SHA256: sum(good[ffPath]), Size: int64(len(good[ffPath]))},
		FFprobe: Asset{Path: fpPath, SHA256: sum(good[fpPath]), Size: int64(len(good[fpPath]))}}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close() // refuses connections

	t.Run("GitHub down", func(t *testing.T) {
		m := newSource(t, good, 0)
		dir := t.TempDir()
		var last int64
		if _, err := Install(context.Background(), &mirror.Client{Sources: []string{down.URL, m.URL}}, dir, b, func(d, _ int64) { last = d }); err != nil {
			t.Fatal(err)
		}
		if err := Verify(dir, b); err != nil {
			t.Error(err)
		}
		if last != b.Size() {
			t.Errorf("progress ends at %d of %d", last, b.Size())
		}
	})
	t.Run("tampered mirror", func(t *testing.T) {
		m := newSource(t, bad, 0)                      // answers first
		gh := newSource(t, good, 300*time.Millisecond) // slower
		dir := t.TempDir()
		if _, err := Install(context.Background(), &mirror.Client{Sources: []string{gh.URL, m.URL}}, dir, b, nil); err != nil {
			t.Fatal(err)
		}
		if err := Verify(dir, b); err != nil {
			t.Error(err)
		}
		if m.hits.Load() < 2 {
			t.Errorf("the mirror was asked %d times", m.hits.Load())
		}
	})
	t.Run("only tampered", func(t *testing.T) {
		m1, m2 := newSource(t, bad, 0), newSource(t, bad, 0)
		dir := t.TempDir()
		_, err := Install(context.Background(), &mirror.Client{Sources: []string{m1.URL, m2.URL}}, dir, b, nil)
		var ce *ChecksumError
		if !errors.As(err, &ce) || ce.Got != sum(bad[ffPath]) {
			t.Fatalf("want a checksum error, got %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "ffmpeg")); !os.IsNotExist(err) {
			t.Errorf("installed anyway: %v", err)
		}
	})
}

// --- with a real ffmpeg (skipped when there is none) --------------------------

func realTool(t *testing.T) *Tool {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	tool, err := Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// writeSine writes a mono 16-bit WAV.
func writeSine(t *testing.T, path string, rate int, seconds float64) {
	t.Helper()
	n := int(float64(rate) * seconds)
	b := make([]byte, 44+2*n)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*n))
	for i := 0; i < n; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRealAudio(t *testing.T) {
	tool := realTool(t)
	if tool.FFprobe == "" {
		t.Skip("no ffprobe")
	}
	ctx := context.Background()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.wav")
	writeSine(t, in, 24000, 3)
	cover := filepath.Join(dir, "cover.png")
	writePNG(cover, stripTestImage())
	for _, f := range []string{MP3, Opus, FLAC, M4A} {
		if !tool.CanEncode(f) {
			continue
		}
		out := filepath.Join(dir, "out."+f)
		var fracs []float64
		err := tool.EncodeAudio(ctx, EncodeOptions{Input: in, Output: out, Format: f, Duration: 3, Cover: cover,
			Metadata: map[string]string{"title": "Book: one", "artist": "AudioSmith"},
			Chapters: []Chapter{{Title: "One", Start: 0, End: 1}, {Title: "Two", Start: 1, End: 3}}},
			func(x float64) { fracs = append(fracs, x) })
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		p, err := tool.Probe(ctx, out)
		if err != nil || len(p.Audio) != 1 || math.Abs(p.Duration-3) > 0.1 {
			t.Errorf("%s: %+v %v", f, p, err)
		}
		if len(fracs) == 0 || fracs[len(fracs)-1] != 1 {
			t.Errorf("%s: progress %v", f, fracs)
		}
		// And back to WAV, a window of it, at 16 kHz.
		back := filepath.Join(dir, "back-"+f+".wav")
		if err := tool.DecodeAudio(ctx, DecodeOptions{Input: out, Output: back, SampleRate: 16000, Channels: 1, Start: 1, Duration: 1}, nil); err != nil {
			t.Fatalf("%s decode: %v", f, err)
		}
		if p, err := tool.Probe(ctx, back); err != nil || p.Audio[0].SampleRate != 16000 || math.Abs(p.Duration-1) > 0.05 {
			t.Errorf("%s back: %+v %v", f, p, err)
		}
	}
	// No temporary files left, and an unknown encoder is an error.
	if _, err := os.Stat(filepath.Join(dir, ".out.part.mp3")); !os.IsNotExist(err) {
		t.Error("temporary file left")
	}
	var ne *NoEncoderError
	if err := (&Tool{FFmpeg: tool.FFmpeg}).EncodeAudio(ctx, EncodeOptions{Input: in, Output: filepath.Join(dir, "x.mp3"), Format: MP3}, nil); !errors.As(err, &ne) {
		t.Errorf("no encoder: %v", err)
	}
	// A failing run reports ffmpeg's last line.
	var re *RunError
	err := tool.DecodeAudio(ctx, DecodeOptions{Input: filepath.Join(dir, "missing.webm"), Output: filepath.Join(dir, "m.wav")}, nil)
	if !errors.As(err, &re) || re.Tail == "" {
		t.Errorf("failure: %v", err)
	}
}

func TestRealVideo(t *testing.T) {
	tool := realTool(t)
	if tool.FFprobe == "" || !tool.HasEncoder("libvpx-vp9") && !tool.HasEncoder("libvpx") {
		t.Skip("no ffprobe or VP9 encoder")
	}
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.webm")
	enc := "libvpx-vp9"
	if !tool.HasEncoder(enc) {
		enc = "libvpx"
	}
	cmd := exec.Command(tool.FFmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=3", "-c:v", enc, "-b:v", "200k", "-c:a", "libopus", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("making a WebM: %v %s", err, out)
	}
	p, err := tool.Probe(ctx, src)
	if err != nil || len(p.Video) != 1 || p.Video[0].Width != 320 || len(p.Audio) != 1 {
		t.Fatalf("probe %+v %v", p, err)
	}
	prev := filepath.Join(dir, "preview.mp4")
	if err := tool.Transcode(ctx, TranscodeOptions{Input: src, Output: prev, MaxHeight: 120, Duration: p.Duration}, nil); err != nil {
		t.Fatal(err)
	}
	if q, err := tool.Probe(ctx, prev); err != nil || q.Video[0].Height != 120 || q.Video[0].Codec == "vp9" || q.Audio[0].Codec != "aac" {
		t.Errorf("preview %+v %v", q, err)
	}
	frames, err := tool.Thumbnails(ctx, src, []float64{0.5, 2}, []string{filepath.Join(dir, "a.jpg"), filepath.Join(dir, "b.png")}, 160)
	if err != nil || len(frames) != 2 || frames[0].Width != 160 || frames[1].Height != 120 {
		t.Errorf("thumbnails %+v %v", frames, err)
	}
	dubWAV := filepath.Join(dir, "dub.wav")
	writeSine(t, dubWAV, 24000, 3)
	srt := filepath.Join(dir, "subs 'es'.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,000\nHola, ¿qué tal?\n\n2\n00:00:02,000 --> 00:00:02,900\n你好\n"), 0o644)
	muxed := filepath.Join(dir, "muxed.mp4")
	err = tool.Mux(ctx, MuxOptions{Video: src, KeepAudio: true, Audio: []Track{{Path: dubWAV, Language: "spa", Title: "Español", Default: true}},
		Subtitles: []SubtitleTrack{{Path: srt, Language: "spa"}}, Output: muxed, Duration: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q, err := tool.Probe(ctx, muxed); err != nil || len(q.Audio) != 2 || q.Video[0].Codec != "h264" && q.Video[0].Codec != "mpeg4" {
		t.Errorf("muxed %+v %v", q, err)
	}
	cues := []Cue{{Start: 0.5, End: 2, Text: "Hola, ¿qué tal?"}, {Start: 2, End: 2.9, Text: "你好"}}
	for _, draw := range []bool{false, true} {
		if !draw && !tool.HasFilter("subtitles") {
			continue
		}
		forceDraw = draw
		out := filepath.Join(dir, "burnt.mp4")
		err := tool.Burn(ctx, BurnOptions{Video: src, Audio: dubWAV, Subtitles: srt, Cues: cues, Output: out, Duration: 3, Width: 320, Height: 240}, nil)
		forceDraw = false
		if err != nil {
			t.Fatalf("burn (draw %v): %v", draw, err)
		}
		if q, err := tool.Probe(ctx, out); err != nil || len(q.Video) != 1 || len(q.Audio) != 1 || math.Abs(q.Duration-3) > 0.2 {
			t.Errorf("burnt (draw %v): %+v %v", draw, q, err)
		}
		// The caption is in the picture at 1 s, not at 0.2 s.
		if !draw {
			continue
		}
		f, err := tool.Thumbnails(ctx, out, []float64{1, 0.2}, []string{filepath.Join(dir, "with.png"), filepath.Join(dir, "without.png")}, 0)
		if err != nil || len(f) != 2 {
			t.Fatalf("frames %v", err)
		}
		if w, wo := whiteBottom(t, f[0].Path), whiteBottom(t, f[1].Path); w <= wo {
			t.Errorf("caption not drawn: %d white pixels with, %d without", w, wo)
		}
	}
}

func TestRenderCaptions(t *testing.T) {
	dir := t.TempDir()
	list, h, err := RenderCaptions([]Cue{{Start: 1, End: 2, Text: "A long first line that has to wrap somewhere on a narrow video"}, {Start: 1.5, End: 3, Text: "重叠的第二行"}, {Start: 4, End: 4.01, Text: "too short"}}, 320, 240, 5, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if h <= 0 || h%2 != 0 || h > 240 {
		t.Errorf("strip height %d", h)
	}
	data, _ := os.ReadFile(list)
	s := string(data)
	// blank 1 s, cue 1, cue 2 clipped to start at 2, blank to 5, blank.
	if strings.Count(s, "file '") != 5 || !strings.Contains(s, "duration 1.000\nfile '"+filepath.Join(dir, "c00002.png")) && !strings.Contains(s, "c00002.png'\nduration 1.000") {
		t.Errorf("list:\n%s", s)
	}
	fs := loadFonts(nil, 20)
	if lines := fs.wrap("A long first line that has to wrap somewhere", fixed20(120)); len(lines) < 2 {
		t.Errorf("wrap %q", lines)
	}
	if lines := fs.wrap("一二三四五六七八九十一二三四五六七八九十", fixed20(100)); len(lines) < 2 {
		t.Errorf("CJK wrap %q", lines)
	}
}
