package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store"
)

// exportTake adds a take of origin with two chunks.
func exportTake(t *testing.T, a *app, id, origin string) *core.Take {
	t.Helper()
	tk := core.Take{ID: id, CreatedAt: time.Date(2026, 10, 14, 20, 0, 0, 0, time.Local), Origin: origin,
		Text: "The last ferry left the harbour at eleven. [pause 500ms] Fog had settled on the water like wool.", Model: kokoro, Voice: "af_heart",
		SampleRate: 24000, AudioSeconds: 2, Render: &core.TakeRender{Bits: 16, Channels: 1, Bytes: 96044, Chunks: []core.TakeChunk{
			{Text: "The last ferry left the harbour at eleven.", Start: 0, End: 1},
			{Text: "Fog had settled on the water like wool.", Start: 1, End: 2}}}}
	addTake(t, a, tk, 2)
	return a.findTake(id)
}

// fakeFFmpegLog is fakeFFmpeg that also writes its output file (the last
// argument) and logs its arguments, one run per line.
func fakeFFmpegLog(t *testing.T) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "args.log")
	script := `#!/bin/sh
case "$*" in
*-version*) echo "ffmpeg version n8.1.2-fake Copyright"; exit 0;;
*-encoders*) printf ' A....D = Audio\n ------\n A....D libmp3lame  MP3\n A....D libopus  Opus\n A....D flac  FLAC\n'; exit 0;;
*-filters*) exit 0;;
esac
echo "$*" >> ` + log + `
for last; do :; done
echo fake > "$last"
`
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "ffmpeg"), log
}

func useFFmpeg(t *testing.T, a *app, path string) {
	t.Helper()
	tool, err := ffmpeg.Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	a.ffTool.Store(tool)
}

// The dialog's state: defaults by kind, formats that cannot be written,
// options, the estimate, what shows for each format.
func TestExportDialogState(t *testing.T) {
	a, _, tt := newTestApp(t)
	tk := exportTake(t, a, "T1", core.TakeStudio)
	a.openExport(tk)
	f := a.exp
	if f == nil || f.kind.id != "take" || f.fmt_().id != "wav" {
		t.Fatalf("form %+v", f)
	}
	o := f.options()
	if o.Format != "wav" || o.Bits != 24 || o.SampleRate != 0 || o.Channels != 0 || o.Loudness != "" || o.Trim || o.Metadata != nil {
		t.Errorf("defaults %+v", o)
	}
	if f.info == nil || f.info.Rate != 24000 || f.seconds() != 2 {
		t.Errorf("info %+v", f.info)
	}
	if f.name != "The last ferry left the harbour" || f.title != "The last ferry left the harbour at eleven." || f.artist != "AudioSmith" {
		t.Errorf("name %q title %q", f.name, f.title)
	}
	// 2 s × 24 kHz × 3 bytes + the header.
	if got := f.estimate(); got != 2*24000*3+44 {
		t.Errorf("estimate %d", got)
	}
	// No ffmpeg: MP3, FLAC and Opus are off, with the reason; M4A runs
	// through the (fake) engine.
	if !f.availOK["m4a"] || f.availOK["mp3"] || f.availOK["flac"] || f.availOK["opus"] || !strings.Contains(f.availWhy["mp3"], "Audio tools") {
		t.Errorf("availability %v", f.availOK)
	}
	tt.Frame()
	// The format's details are under Advanced (thread #42).
	if tt.HasText("Bit depth") || !tt.HasText("Recommended settings") {
		t.Errorf("advanced closed: %q", tt.Texts())
	}
	a.setAdvancedOpen("export", true)
	tt.Frame()
	for _, s := range []string{"Export audio", "Studio take", "Bit depth", "Sample rate", "Original (24 kHz)", "Loudness", "Trim silence",
		"File name", ".wav", "Also save", "Subtitles timed to the audio (.srt)", "About 144 KB · 2.0 s", "Export…", "Cancel"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText("Bitrate") || tt.HasText("Artist") {
		t.Error("WAV shows a bitrate or tags")
	}

	// M4A: a bitrate and tags, no bit depth.
	f.format = formatIndex("m4a")
	tt.Frame()
	if !tt.HasText("Bitrate") || !tt.HasText("128 kb/s") || !tt.HasText("Artist") || tt.HasText("Bit depth") || !tt.HasText(".m4a") {
		t.Errorf("m4a: %q", tt.Texts())
	}
	o = f.options()
	if o.Bitrate != 128000 || o.Metadata["artist"] != "AudioSmith" || f.fileName() != "The last ferry left the harbour.m4a" {
		t.Errorf("m4a options %+v %q", o, f.fileName())
	}
	if got := f.estimate(); got != 2*128000/8+1024 {
		t.Errorf("m4a estimate %d", got)
	}
	// Stereo at 48 kHz, trimmed: the length follows the trim.
	f.channels, f.rate[f.format], f.trim = 1, 2, true
	o = f.options()
	if o.Channels != 2 || o.SampleRate != 48000 || !o.Trim {
		t.Errorf("stereo %+v", o)
	}

	// Opus has no sample rate to choose.
	a.ffTool.Store(nil)
	path, _ := fakeFFmpegLog(t)
	useFFmpeg(t, a, path)
	a.openExport(tk)
	f = a.exp
	if !f.availOK["opus"] || !f.availOK["flac"] {
		t.Fatalf("with ffmpeg %v", f.availOK)
	}
	f.format = formatIndex("opus")
	tt.Frame()
	if tt.HasText("Sample rate") || !tt.HasText("96 kb/s") {
		t.Errorf("opus: %q", tt.Texts())
	}
	if o := f.options(); o.SampleRate != 0 || o.Bitrate != 96000 {
		t.Errorf("opus %+v", o)
	}
	// FLAC: 16 or 24 bits.
	f.format = formatIndex("flac")
	tt.Frame()
	if _, ok := tt.Find("24-bit"); !ok || slices.Contains(tt.Texts(), "32-bit float") {
		t.Errorf("flac: %q", tt.Texts())
	}

	// A voice preview: plain audio, no tags or files beside it.
	vp := exportTake(t, a, "V1", core.TakeVoice)
	a.openExport(vp)
	f = a.exp
	f.format = formatIndex("mp3")
	tt.Frame()
	if f.kind.id != "voice" || tt.HasText("Also save") || tt.HasText("Artist") || !tt.HasText("Voice preview") || f.options().Metadata != nil {
		t.Errorf("voice preview: %q", tt.Texts())
	}
	// Cancel closes it.
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, shown := tt.Find("Export audio"); a.exp != nil || shown {
		t.Errorf("still open: %v %v", a.exp != nil, shown)
	}
}

// Each format's path: WAV's bit depth, M4A through the engine's
// media.encode, MP3/FLAC/Opus through ffmpeg with their arguments.
func TestExportFormats(t *testing.T) {
	a, eng, _ := newTestApp(t)
	tk := exportTake(t, a, "T1", core.TakeStudio)
	dir := t.TempDir()
	exportSaveDir = dir
	t.Cleanup(func() { exportSaveDir = "" })
	export := func(set func(f *exportForm)) string {
		t.Helper()
		a.toasts = nil
		a.openExport(tk)
		set(a.exp)
		name := a.exp.fileName()
		a.exportChoose(a.exp)
		if a.exp != nil {
			t.Error("the dialog stays open")
		}
		if len(a.toasts) == 0 || !strings.HasPrefix(a.toasts[len(a.toasts)-1], "Saved ") {
			t.Fatalf("toasts %q", a.toasts)
		}
		return filepath.Join(dir, name)
	}

	for i, enc := range []wav.Encoding{wav.PCM16, wav.PCM24, wav.Float32} {
		path := export(func(f *exportForm) { f.bits[0] = i; f.name = "bits-" + enc.String() })
		_, fm, err := wav.ReadFile(path)
		if err != nil || fm.Encoding != enc || fm.Rate != 24000 {
			t.Errorf("WAV %d: %+v %v", i, fm, err)
		}
	}

	// M4A: the engine's media.encode with the bitrate and tags.
	path := export(func(f *exportForm) {
		f.format = formatIndex("m4a")
		f.bitrate[f.format] = 2 // 256 kb/s
		f.title = "Ferry"
	})
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("FAKE-M4A\n")) {
		t.Fatalf("m4a: %q %v", data, err)
	}
	var p proto.EncodeParams
	json.Unmarshal(eng.lastOf(t, proto.MethodMediaEncode).Params, &p)
	if p.Output.Format != "m4a" || p.Output.Path != path || p.Bitrate != 256000 || p.Codec != "aac" ||
		p.Metadata.Title != "Ferry" || p.Metadata.Author != "AudioSmith" || !strings.HasSuffix(p.Input, ".wav") {
		t.Errorf("media.encode %+v", p)
	}
	if _, err := os.Stat(p.Input); !os.IsNotExist(err) {
		t.Errorf("the intermediate WAV remains: %v", err)
	}

	// ffmpeg: MP3 at 320 kb/s and 44.1 kHz, FLAC 24-bit, Opus 64 kb/s.
	ff, log := fakeFFmpegLog(t)
	useFFmpeg(t, a, ff)
	export(func(f *exportForm) {
		f.format = formatIndex("mp3")
		f.bitrate[f.format] = 2
		// MP3 holds 320 kb/s only from 32 kHz: the take's 24 kHz is
		// not offered.
		if opts := f.rateOptions(f.fmt_()); !slices.Equal(opts, []int{44100, 48000}) || f.options().SampleRate != 44100 {
			t.Errorf("mp3 rates %v", opts)
		}
	})
	export(func(f *exportForm) { f.format = formatIndex("flac"); f.bits[f.format] = 1 })
	export(func(f *exportForm) { f.format = formatIndex("opus"); f.bitrate[f.format] = 0 })
	raw, _ := os.ReadFile(log)
	runs := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(runs) != 3 {
		t.Fatalf("ffmpeg runs %q", runs)
	}
	for i, want := range [][]string{
		{"-c:a libmp3lame", "-b:a 320k", "-f mp3", "-f ffmetadata", "The last ferry left the harbour.part.mp3"},
		{"-c:a flac", "-f flac", "The last ferry left the harbour.part.flac"},
		{"-c:a libopus", "-b:a 64k", "-f ogg", "The last ferry left the harbour.part.opus"},
	} {
		for _, w := range want {
			if !strings.Contains(runs[i], w) {
				t.Errorf("run %d lacks %q: %s", i, w, runs[i])
			}
		}
	}
	// Projects → Exports lists them, in their formats.
	items, err := a.lib.svc.Library.Search(context.Background(), store.LibraryQuery{Category: store.LibExport})
	if err != nil {
		t.Fatal(err)
	}
	var formats []string
	for _, it := range items {
		formats = append(formats, strings.ToLower(filepath.Ext(it.Detail)))
	}
	for _, f := range []string{".wav", ".m4a", ".mp3", ".flac", ".opus"} {
		if !slices.Contains(formats, f) {
			t.Errorf("exports %q lack %s", formats, f)
		}
	}
}

// The settings are remembered per kind and come back when the dialog
// opens again; a format that cannot be written now falls back.
func TestExportRemembered(t *testing.T) {
	a, _, _ := newTestApp(t)
	tk := exportTake(t, a, "T1", core.TakeStudio)
	api := exportTake(t, a, "A1", core.TakeAPI)
	exportSaveDir = t.TempDir()
	t.Cleanup(func() { exportSaveDir = "" })
	a.openExport(tk)
	f := a.exp
	f.format = formatIndex("m4a")
	f.bitrate[f.format] = 1
	f.rate[f.format], f.channels, f.trim = 2, 1, true
	f.loud = loudnessLabel("podcast")
	*f.extras["script"] = true
	a.exportChoose(f)
	want := settings.AudioExport{Format: "m4a", Bitrate: 192, SampleRate: 48000, Stereo: true, Loudness: "podcast", Trim: true, Extras: []string{"script"}}
	got := a.lib.settings.Get().Export.Audio["take"]
	if got.Format != want.Format || got.Bitrate != want.Bitrate || got.SampleRate != want.SampleRate || !got.Stereo || got.Loudness != "podcast" ||
		!got.Trim || !slices.Equal(got.Extras, want.Extras) {
		t.Errorf("saved %+v", got)
	}
	a.openExport(tk)
	o := a.exp.options()
	if o.Format != "m4a" || o.Bitrate != 192000 || o.SampleRate != 48000 || o.Channels != 2 || o.Loudness != "podcast" || !o.Trim || !a.exp.extraOn("script") {
		t.Errorf("reopened %+v", o)
	}
	// Another kind keeps its own defaults.
	a.openExport(api)
	if o := a.exp.options(); o.Format != "wav" || o.Bits != 16 || o.Trim {
		t.Errorf("api take %+v", o)
	}
	// M4A remembered, the engine gone: WAV.
	a.lib.useEncoder(nil)
	a.openExport(tk)
	if o := a.exp.options(); o.Format != "wav" || o.Bits != 24 || o.Loudness != "podcast" {
		t.Errorf("without the engine %+v", o)
	}
}

// History's Export button opens the dialog; Export… writes the file, the
// script and the subtitles into the folder, trimmed and at a loudness.
func TestHistoryExportButton(t *testing.T) {
	a, _, tt := newTestApp(t)
	exportTake(t, a, "T1", core.TakeStudio)
	dir := t.TempDir()
	exportSaveDir = dir
	t.Cleanup(func() { exportSaveDir = "" })
	a.showPage("history")
	tt.Frame()
	chooseAway(t, tt, "Asset actions", "Export…") // Assets' ⋯ (0.5 F5)
	if a.exp == nil || !tt.HasText("Export audio") {
		t.Fatalf("no dialog: %q", tt.Texts())
	}
	a.exp.trim = true
	a.exp.loud = loudnessLabel("broadcast")
	*a.exp.extras["script"], *a.exp.extras["subtitles"] = true, true
	tt.Frame()
	if err := tt.Click("Export…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	base := filepath.Join(dir, "The last ferry left the harbour")
	if _, fm, err := wav.ReadFile(base + ".wav"); err != nil || fm.Encoding != wav.PCM24 {
		t.Fatalf("wav %+v %v (toasts %q)", fm, err, a.toasts)
	}
	script, _ := os.ReadFile(base + ".txt")
	srt, _ := os.ReadFile(base + ".srt")
	if !strings.Contains(string(script), "[pause 500ms]") || !strings.Contains(string(srt), "00:00:00,000 --> ") ||
		!strings.Contains(string(srt), "Fog had settled on the water like wool.") {
		t.Errorf("script %q, srt %q", script, srt)
	}
	if a.exp != nil || !tt.HasText("Saved The last ferry left the harbour.wav") {
		t.Errorf("after: %q", tt.Texts())
	}
	// The Studio's latest take and Projects open the same dialog.
	a.showPage("studio")
	a.lastClipID = "T1"
	tt.Frame()
	if err := tt.Click("Export audio…"); err != nil {
		t.Fatal(err)
	}
	if a.exp == nil || a.exp.take.ID != "T1" {
		t.Fatal("studio: no dialog")
	}
	a.exp.open = false
	tt.Frame()
}

// A take's origin is kept: the Studio's, a voice page's, Tools' and the
// API's.
func TestTakeOrigins(t *testing.T) {
	ch := toolChoice{model: entryFor(kokoro)}
	a, _, _ := newTestApp(t)
	if r := a.toolRecipe(ch, "Hello"); r.Origin != core.TakeConvert {
		t.Errorf("tools %q", r.Origin)
	}
	if k := exportKindOf(&core.Take{}); k.id != "take" {
		t.Errorf("no origin: %s", k.id)
	}
	if got := kindLabel(&core.Take{Origin: core.TakeAPI, Client: "Claude"}); got != "Local API take (Claude)" {
		t.Error(got)
	}
	_ = services.FormatM4A
}

// TestExportAdvanced: the dialog counts what its Advanced changes from
// the kind's defaults, and Restore recommended puts them back.
func TestExportAdvanced(t *testing.T) {
	a, _, _ := newTestApp(t)
	tk := exportTake(t, a, "T1", core.TakeStudio)
	a.openExport(tk)
	f := a.exp
	if n := len(f.changes()); n != 0 {
		t.Fatalf("fresh: %v", f.changes())
	}
	f.bits[f.format], f.trim, f.channels = 0, true, 1
	if n := len(f.changes()); n != 3 {
		t.Errorf("changes %v", f.changes())
	}
	f.restore()
	if len(f.changes()) != 0 || f.bits[f.format] != 1 {
		t.Errorf("restored %v bits %d", f.changes(), f.bits[f.format])
	}
}
