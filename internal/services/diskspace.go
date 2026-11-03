package services

// Free-space pre-checks (code review 1102 M4): long or large work (a dub
// run, a book's render, an isolation, an export) estimates what it will
// write and is refused up front, with a core.DiskFullError (code
// disk_full: needed and free bytes), rather than failing hours in with a
// write error. The estimates are rough upper bounds, from the length of
// the audio; writes that still hit a full disk are reported with the same
// code (jobs.DiskFull).

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
)

// freeSpace is the space free to the user under dir, -1 when unknown (a
// variable: tests fake a full disk).
var freeSpace = diskFree

// DiskKeepFree is what work keeps free beyond its estimate: the system
// and the other work under way need room too.
const DiskKeepFree int64 = 1 << 30

// Bytes per second of audio the work writes, by kind.
const (
	bpsPCM48s16  = 48000 * 2 * 2 // 48 kHz stereo 16-bit WAV
	bpsPCM16k    = 16000 * 2     // 16 kHz mono 16-bit (speech for the recogniser)
	bpsPCM441s16 = 44100 * 2 * 2 // the isolator's 44.1 kHz stereo 16-bit
	bpsRender    = 48000 * 4     // a rendered line: up to 48 kHz mono float32
	bpsVideo     = 1_000_000     // a downloaded video, about 8 Mb/s (1080p)
)

// checkFree refuses work (what) that writes about need bytes under dir
// when that would leave less than DiskKeepFree free there.
func checkFree(what, dir string, need int64) error {
	free := freeSpace(dir)
	if free < 0 {
		return nil // unknown: the writes say it if it is full
	}
	need = max(need, 0) + DiskKeepFree
	if need <= free {
		return nil
	}
	return &core.DiskFullError{What: what, Path: dir, Need: need, Free: free}
}

// stageBytesPerSecond are what each dub stage writes per second of the
// source: fetch the video; ingest audio.wav and the 16 kHz speech;
// separate two stems and the 16 kHz vocals; synthesize the renders (at
// most the whole length speaks); mix the track and its speech alone.
var stageBytesPerSecond = map[string]int64{
	KindDubFetch:      bpsVideo,
	KindDubIngest:     bpsPCM48s16 + bpsPCM16k,
	KindDubSeparate:   2*bpsPCM48s16 + bpsPCM16k,
	KindDubSynthesize: bpsRender,
	KindDubMix:        2 * bpsPCM48s16,
}

// dubSeconds is the length of p's source (the site's for a URL not yet
// downloaded; 0 when not known).
func dubSeconds(p *dub.Project) float64 {
	if p.Source.Duration > 0 {
		return p.Source.Duration
	}
	if p.Source.Meta != nil {
		return max(p.Source.Meta.Duration, 0)
	}
	return 0
}

// dubRunNeed is about what a run's stages (nodes) write.
func dubRunNeed(p *dub.Project, nodes []jobs.Node) int64 {
	sec := dubSeconds(p)
	var n int64
	for _, node := range nodes {
		n += int64(sec * float64(stageBytesPerSecond[node.Spec.Kind]))
	}
	return n
}

// dubExportNeed is about what export req of p writes: the video copied
// with its tracks, the audio (and the WAV an encoder reads), the stems,
// or nothing to speak of (subtitles).
func dubExportNeed(p *dub.Project, req ExportRequest) int64 {
	sec := dubSeconds(p)
	track := int64(sec * bpsPCM48s16)
	switch req.Format {
	case dub.ExportMP4, dub.ExportBurn:
		video := p.Source.Size
		if fi, err := os.Stat(p.Source.Path); err == nil {
			video = fi.Size()
		}
		return video + track*int64(max(len(req.Tracks), 1)+1)
	case dub.ExportWAV, dub.ExportM4A, dub.ExportMP3:
		return 2 * track
	case dub.ExportStems, dub.ExportSegments:
		return 3 * track
	}
	return 0
}

// isolateNeed is about what isolating seconds of audio writes: the
// decoded input, the voices, the rest and the voices denoised.
func isolateNeed(seconds float64) int64 { return int64(seconds * 4 * bpsPCM441s16) }

// isolateStemsNeed is what is left to write once the input is decoded.
func isolateStemsNeed(seconds float64) int64 { return int64(seconds * 3 * bpsPCM441s16) }

// wavSeconds is the length of a WAV file, 0 when path is not one Go reads.
func wavSeconds(path string) float64 {
	if !strings.EqualFold(filepath.Ext(path), ".wav") {
		return 0
	}
	f, err := wav.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	return f.Seconds()
}

// Speaking rates of the estimate of a book's length: words of spaced
// scripts and characters of the others, per second.
const (
	wordsPerSecond = 2.5
	charsPerSecond = 4.5
)

// bookSeconds is about how long chapters speak.
func bookSeconds(chapters []ChapterSpec) float64 {
	sec := 0.0
	for _, ch := range chapters {
		for _, l := range ch.Lines {
			for _, sp := range l.Spans {
				w, c := text.Count(sp.Text)
				sec += float64(w)/wordsPerSecond + float64(c)/charsPerSecond
			}
		}
	}
	return sec
}

// bookNeed is about what rendering chapters writes: the lines' chunks,
// the chapters and the assembled book (three copies of rendered audio),
// and two more with stems (the voices and the rest).
func bookNeed(chapters []ChapterSpec, stems bool) int64 {
	copies := int64(3)
	if stems {
		copies += 2
	}
	return int64(bookSeconds(chapters) * float64(copies*bpsRender))
}

// exportNeed is about what exporting the WAV src writes: the output at
// up to twice its bytes per second (16-bit to 32-bit float, a higher
// rate), and the WAV an encoder reads for the other formats.
func exportNeed(src string, o AudioExport) int64 {
	fi, err := os.Stat(src)
	if err != nil {
		return 0
	}
	n := 2 * fi.Size()
	if o.Format != "" && o.Format != FormatWAV {
		n += 2 * fi.Size()
	}
	return n
}
