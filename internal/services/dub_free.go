package services

// Freeing a dub's working files (code review 1102 L4; the user, 11-02:
// 「配音导出 我们自动释放吧 不过要点延迟」): a while after a dub was exported
// (DubFreeAfter without being opened, edited or exported again) its
// largest intermediates go: the decoded audio (audio.wav, 48 kHz), its
// 16 kHz copy, the separated stems, the files of lines and languages no
// longer in it; its line renders are kept at 16 bits instead of 32-bit
// float. What shows and plays the dub stays: the transcript, the
// translations, the cast and references, the waveform, poster and
// preview, the mixed tracks and the line renders.
//
// A run of a freed dub makes again what it needs (plan: ingest when the
// audio is gone, separate when the stems are), and Rebuild decodes the
// audio alone (the original's playback). Project.Freed says the files are
// gone until a stage finds them back.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/store"
)

// DubFreeAfter is how long after its last export (or opening, or edit) a
// dub keeps its working files.
const DubFreeAfter = 7 * 24 * time.Hour

// DubCodeFreed: an export needs working files that were freed (the
// separated stems); a run makes them again.
const DubCodeFreed = "dub.freed"

// dubOpened is the file whose time says when a dub was last opened.
const dubOpened = ".opened"

// audioGone reports a decoded audio (or its 16 kHz copy) that was made
// and is gone.
func (s *Dubs) audioGone(p *dub.Project) bool {
	return p.Media.Audio != "" && !fileExists(s.Path(p.ID, p.Media.Audio)) ||
		p.Media.Speech != "" && !fileExists(s.Path(p.ID, p.Media.Speech))
}

// stemsGone reports separated stems that were made and are gone.
func (s *Dubs) stemsGone(p *dub.Project) bool {
	if p.Media.Vocals == "" {
		return false
	}
	return !fileExists(s.Path(p.ID, p.Media.Vocals)) || !fileExists(s.Path(p.ID, dubVocals16k)) ||
		p.Media.Background != "" && !fileExists(s.Path(p.ID, p.Media.Background))
}

// WorkGone reports whether some of p's working files are gone (freed):
// a run makes them again.
func (s *Dubs) WorkGone(p *dub.Project) bool { return s.audioGone(p) || s.stemsGone(p) }

// voiceSource is the file a line's own clip is cut from: the separated
// voices, else the audio.
func (s *Dubs) voiceSource(p *dub.Project) string {
	if p.Media.Vocals != "" && fileExists(s.Path(p.ID, p.Media.Vocals)) {
		return p.Media.Vocals
	}
	return dubAudio
}

// MixedTrack is lang's mixed track (absolute), "" when there is none.
func (s *Dubs) MixedTrack(p *dub.Project, l string) string {
	if t := p.Track(l); t.File != "" && fileExists(s.Path(p.ID, t.File)) {
		return s.Path(p.ID, t.File)
	}
	return ""
}

// Touch records that dub id was opened (its working files stay
// DubFreeAfter from now).
func (s *Dubs) Touch(id string) {
	if dir := s.Dir(id); fileExists(dir) {
		os.WriteFile(filepath.Join(dir, dubOpened), nil, 0o644)
	}
}

// lastTouched is when p was last edited, exported or opened.
func (s *Dubs) lastTouched(p *dub.Project) time.Time {
	last := p.UpdatedAt
	if p.Exported.After(last) {
		last = p.Exported
	}
	if fi, err := os.Stat(filepath.Join(s.Dir(p.ID), dubOpened)); err == nil && fi.ModTime().After(last) {
		last = fi.ModTime()
	}
	return last
}

// noteExport records an export of dub id (UpdatedAt stays).
func (s *Dubs) noteExport(ctx context.Context, id string) {
	s.update(context.WithoutCancel(ctx), id, func(p *dub.Project) error {
		p.Exported = s.cfg.Now()
		return nil
	}, false)
}

// Rebuild decodes a freed dub's audio again (the original's playback,
// a line's own clip); a run makes the rest.
func (s *Dubs) Rebuild(ctx context.Context, id string) (map[string]string, error) {
	return s.Run(ctx, id, RunRequest{Through: dub.StageIngest})
}

// busyWith reports whether a job of dub id (its pipeline or an export)
// is waiting, queued or running.
func (s *Dubs) busyWith(ctx context.Context, id string) (bool, error) {
	if s.cfg.Scheduler == nil {
		return false, nil
	}
	for _, owner := range []string{DubOwner + id, DubExportOwner + id} {
		l, err := s.cfg.Scheduler.List(ctx, jobs.Filter{Owner: owner, States: activeStates})
		if err != nil {
			return false, err
		}
		if len(l) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// FreeWork frees dub id's working files now and returns the bytes freed.
// It refuses (core.ErrBusy) while a job of the dub runs. The audio and
// the stems go only while the source is there to make them again.
func (s *Dubs) FreeWork(ctx context.Context, id string) (int64, error) {
	return s.freeWork(ctx, id, nil)
}

// errInUse: freeWork's still found the dub in use (opened, or edited).
var errInUse = errors.New("dub in use")

// freeWork is FreeWork; idle, when set, says whether the dub is still
// idle, asked again once no run can start (code review 1103 L-4: the
// user may open the dub while the sweep goes through the others).
func (s *Dubs) freeWork(ctx context.Context, id string, idle func(p *dub.Project) bool) (int64, error) {
	s.runMu.Lock() // no run starts meanwhile
	defer s.runMu.Unlock()
	if busy, err := s.busyWith(ctx, id); err != nil || busy {
		return 0, errors.Join(err, fmt.Errorf("dub %s: a job is going on: %w", id, core.ErrBusy))
	}
	p, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if idle != nil && !idle(p) {
		return 0, errInUse
	}
	var freed int64
	rebuildable := p.Source.Path != "" && fileExists(p.Source.Path)
	if rebuildable {
		for _, name := range []string{p.Media.Audio, p.Media.Speech, p.Media.Vocals, p.Media.Background} {
			if name != "" {
				freed += removeSized(s.Path(id, name))
			}
		}
		if p.Media.Vocals != "" {
			freed += removeSized(s.Path(id, dubVocals16k))
		}
	}
	freed += s.pruneStale(p, true)
	freed += removeSized(s.Path(id, filepath.Join("renders", "tmp")))
	freed += s.compactLines(p)
	var gone []string // the tracks of languages removed
	for l, t := range p.Tracks {
		if !speaks(p, l) {
			gone = append(gone, l)
			freed += removeSized(s.Path(id, t.File)) + removeSized(s.Path(id, t.Speech))
		}
	}
	_, err = s.update(ctx, id, func(q *dub.Project) error {
		for _, l := range gone {
			if !speaks(q, l) {
				delete(q.Tracks, l)
			}
		}
		if rebuildable && s.WorkGone(q) {
			q.Freed = s.cfg.Now()
		}
		return nil
	}, false)
	return freed, err
}

// FreeIdle frees the working files of the dubs exported (exported: an
// export the app recorded, besides Project.Exported) and not touched
// for after; skip names dubs to leave (the one open: asked again, with
// the times, just before a dub's files go). It returns how many dubs it
// freed and the bytes.
func (s *Dubs) FreeIdle(ctx context.Context, after time.Duration, exported func(id string) time.Time, skip func(id string) bool) (int, int64) {
	l, err := s.List(ctx, store.DubQuery{})
	if err != nil {
		return 0, 0
	}
	n, total := 0, int64(0)
	for _, r := range l {
		if ctx.Err() != nil {
			break
		}
		idle := func(p *dub.Project) bool {
			if skip != nil && skip(p.ID) {
				return false
			}
			at := p.Exported
			if exported != nil {
				if t := exported(p.ID); t.After(at) {
					at = t
				}
			}
			return !at.IsZero() && s.cfg.Now().Sub(s.lastTouched(p)) >= after
		}
		p, err := s.Get(ctx, r.ID)
		if err != nil || !idle(p) || !s.hasWork(p) {
			continue
		}
		if b, err := s.freeWork(ctx, r.ID, idle); err == nil && b > 0 {
			n, total = n+1, total+b
		}
	}
	if n > 0 {
		s.cfg.Logf("dub: freed the working files of %d dubs (%d MB)", n, total>>20)
	}
	return n, total
}

// hasWork reports whether p has working files to free.
func (s *Dubs) hasWork(p *dub.Project) bool {
	for _, name := range []string{p.Media.Audio, p.Media.Vocals, p.Media.Background} {
		if name != "" && fileExists(s.Path(p.ID, name)) {
			return true
		}
	}
	for l := range p.Tracks {
		if !speaks(p, l) {
			return true
		}
	}
	return false
}

// pruneStale deletes the renders of languages p no longer speaks and,
// with lines, the line renders of lines no longer in p, and returns the
// bytes. Call it with no job of p running (Run, FreeWork). A run leaves
// the renders of removed lines (code review 1103 L-3): the editor's Undo
// may bring a line back, and its render with it; they go when the dub
// is freed.
func (s *Dubs) pruneStale(p *dub.Project, lines bool) int64 {
	root := s.Path(p.ID, "renders")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	langs := map[string]bool{"tmp": true}
	for _, l := range p.Languages() {
		langs[dub.SafeName(l)] = true
	}
	segs := map[string]bool{}
	for _, seg := range p.Segments {
		segs[seg.ID] = true
	}
	var freed int64
	for _, d := range dirs {
		dir := filepath.Join(root, d.Name())
		if !d.IsDir() {
			continue
		}
		if !langs[d.Name()] {
			freed += removeSized(dir)
			continue
		}
		if d.Name() == "tmp" {
			continue
		}
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			name := f.Name()
			if f.IsDir() {
				continue
			}
			id := name
			if i := strings.IndexAny(id, "-."); i > 0 {
				id = id[:i]
			}
			if lines && !segs[id] || strings.HasSuffix(name, ".raw.wav") {
				freed += removeSized(filepath.Join(dir, name))
			}
		}
	}
	return freed
}

// compactLines rewrites p's float line renders at 16 bits (the mix is
// 24-bit, under a limiter) and returns the bytes saved.
func (s *Dubs) compactLines(p *dub.Project) int64 {
	var saved int64
	for _, t := range p.Tracks {
		for _, r := range t.Lines {
			if r.File == "" {
				continue
			}
			saved += compactWAV(s.Path(p.ID, r.File))
		}
	}
	return saved
}

// compactWAV rewrites a 32-bit float WAV at 16 bits (atomically), and
// returns the bytes saved.
func compactWAV(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	rd, err := wav.NewReader(f)
	enc := wav.Encoding(0)
	if err == nil {
		enc = rd.Format().Encoding
	}
	f.Close()
	if enc != wav.Float32 {
		return 0
	}
	b, _, err := wav.ReadFile(path)
	if err != nil {
		return 0
	}
	tmp := path + ".tmp"
	if err := wav.WriteFile(tmp, b, wav.PCM16, wav.Options{Dither: true}); err != nil {
		os.Remove(tmp)
		return 0
	}
	if os.Rename(tmp, path) != nil {
		os.Remove(tmp)
		return 0
	}
	if nfi, err := os.Stat(path); err == nil {
		return fi.Size() - nfi.Size()
	}
	return 0
}

// speaks reports whether p is dubbed into l (a target, or a line's own
// language).
func speaks(p *dub.Project, l string) bool { return slices.Contains(p.Languages(), l) }

// removeSized removes a file or folder and returns the bytes it held.
func removeSized(path string) int64 {
	var n int64
	filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	if os.RemoveAll(path) != nil {
		return 0
	}
	return n
}
