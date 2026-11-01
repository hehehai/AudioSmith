package services

// The batch queue (/batch): N videos × M languages dubbed without
// checkpoints, one project per video (batch "queue"), run at background
// priority (they wait behind anything interactive), and a watch folder
// that adds new videos with the batch's current settings. Unlike
// VoiceStudio's, the watch folder persists: it resumes after a restart
// without re-adding files it saw.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// BatchQueue is the batch queue's id (store.DubRecord.Batch).
const BatchQueue = "queue"

// Documents of the batch queue.
const (
	batchSettingsDoc = "dub:batch"
	batchWatchDoc    = "dub:watch"
)

// BatchSettings are the batch queue's defaults for the videos it adds.
type BatchSettings struct {
	Targets []string `json:"targets"`
	// TargetsAuto: Targets are the default (dub.DefaultTarget), switched
	// per video in that language (dub.Project.AutoRetarget).
	TargetsAuto    bool                `json:"targetsAuto,omitempty"`
	SourceLang     string              `json:"sourceLang,omitempty"`
	Voice          string              `json:"voice,omitempty"` // a VoiceRef for every speaker; "" = clone each speaker
	KeepBackground bool                `json:"keepBackground"`
	Model          string              `json:"model,omitempty"`
	Translator     string              `json:"translator,omitempty"`
	Quality        translate.Quality   `json:"quality,omitempty"`
	Timing         dub.Timing          `json:"timing,omitempty"`
	Formality      translate.Formality `json:"formality,omitempty"`
	// Dialogue: dub.DialogueOff dubs every line line by line; "" (and
	// dub.DialogueAuto) speaks conversations with the dialogue model
	// where it can, as a new project does (thread #89).
	Dialogue string `json:"dialogue,omitempty"`
}

// DefaultBatchSettings are the batch queue's first settings.
func DefaultBatchSettings() BatchSettings {
	return BatchSettings{Targets: DefaultDubTargets(), TargetsAuto: true, KeepBackground: true, Translator: "llm", Quality: translate.Fast, Timing: dub.TimingSmart}
}

// WatchState is the watch folder, as persisted.
type WatchState struct {
	Dir    string       `json:"dir,omitempty"`
	Active bool         `json:"active"`
	Paused bool         `json:"paused,omitempty"`
	Added  int          `json:"added"`
	Last   time.Time    `json:"last,omitzero"` // the last scan
	Err    string       `json:"error,omitempty"`
	W      *dub.Watcher `json:"watcher,omitempty"`
}

// WatchChanged is published when the watch folder starts, stops,
// pauses, scans with a result or adds a video. Topic "dub.watch".
type WatchChanged struct{ State WatchState }

func (WatchChanged) Topic() string { return "dub.watch" }

// batch is the batch queue's state in the service.
type batch struct {
	mu    sync.Mutex
	watch WatchState
	stop  chan struct{} // closes the scan loop
	every time.Duration
}

// BatchSettings reads the batch queue's settings.
func (s *Dubs) BatchSettings(ctx context.Context) BatchSettings {
	bs := DefaultBatchSettings()
	if s.cfg.Documents == nil {
		return bs
	}
	if data, ok, err := s.cfg.Documents.Get(ctx, batchSettingsDoc); err == nil && ok {
		bs.TargetsAuto = false // saved targets are a choice unless they say otherwise
		json.Unmarshal(data, &bs)
	}
	return bs
}

// SetBatchSettings saves the batch queue's settings.
func (s *Dubs) SetBatchSettings(ctx context.Context, bs BatchSettings) error {
	if s.cfg.Documents == nil {
		return nil
	}
	data, err := json.Marshal(bs)
	if err != nil {
		return err
	}
	return s.cfg.Documents.Put(ctx, batchSettingsDoc, data)
}

// Enqueue adds videos to the batch queue with settings bs and starts
// their runs. It returns the projects made; a file that could not be
// added is in errs (by path).
func (s *Dubs) Enqueue(ctx context.Context, paths []string, bs BatchSettings) ([]string, map[string]error) {
	errs := map[string]error{}
	var ids []string
	if len(bs.Targets) == 0 {
		for _, p := range paths {
			errs[p] = fmt.Errorf("batch: no target language: %w", core.ErrInvalid)
		}
		return nil, errs
	}
	for _, path := range paths {
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			if err == nil {
				err = fmt.Errorf("%s is a folder: %w", path, core.ErrInvalid)
			}
			errs[path] = err
			continue
		}
		if !dub.IsMedia(path, dub.MediaExts) {
			errs[path] = fmt.Errorf("%s: %w", filepath.Base(path), core.ErrUnsupported)
			continue
		}
		p := dub.New("", s.cfg.Now())
		p.Batch = BatchQueue
		p.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		p.Source = dub.Source{Kind: dub.SourceFile, Path: path, Name: filepath.Base(path)}
		p.SourceLang = bs.SourceLang
		p.Targets = append([]string(nil), bs.Targets...)
		p.TargetsAuto = bs.TargetsAuto && slices.Equal(bs.Targets, []string{dub.DefaultTarget})
		p.Settings.Review = false
		p.Settings.KeepBackground = bs.KeepBackground
		p.Settings.Model = bs.Model
		if bs.Translator != "" {
			p.Settings.Translator = bs.Translator
		}
		if bs.Quality != "" {
			p.Settings.Quality = bs.Quality
		}
		if bs.Timing != "" {
			p.Settings.Timing = bs.Timing
		}
		p.Settings.Formality = bs.Formality
		if bs.Dialogue == dub.DialogueOff {
			p.Settings.Dialogue = dub.DialogueOff
		}
		if bs.Voice != "" {
			// One voice for every speaker: cast once the speakers are known
			// (the synthesize stage reads the cast); "*" stands for them.
			p.Cast = map[string]string{dub.CastAll: bs.Voice}
		}
		created, err := s.Create(ctx, p)
		if err != nil {
			errs[path] = err
			continue
		}
		if _, err := s.Run(ctx, created.ID, RunRequest{Through: dub.StageMix, Batch: true}); err != nil {
			errs[path] = err
		}
		ids = append(ids, created.ID)
	}
	return ids, errs
}

// Retry runs a batch project again from where it stopped.
func (s *Dubs) Retry(ctx context.Context, id string) error {
	_, err := s.Run(ctx, id, RunRequest{Through: dub.StageMix, Batch: true})
	return err
}

// Queue lists the batch queue's projects, newest change first.
func (s *Dubs) Queue(ctx context.Context) ([]store.DubRecord, error) {
	return s.List(ctx, store.DubQuery{Batch: BatchQueue, Limit: 500})
}

// --- the watch folder -----------------------------------------------------------------

// Watch is the watch folder's state now.
func (s *Dubs) Watch() WatchState {
	s.batch.mu.Lock()
	defer s.batch.mu.Unlock()
	w := s.batch.watch
	w.W = nil
	return w
}

// StartWatch watches dir: files there now are ignored, new ones join the
// queue with the batch settings of the moment.
func (s *Dubs) StartWatch(ctx context.Context, dir string) error {
	listing, err := dub.List(dir)
	if err != nil {
		return err
	}
	s.batch.mu.Lock()
	s.batch.watch = WatchState{Dir: dir, Active: true, W: dub.NewWatcher(dir, nil, listing), Last: s.cfg.Now()}
	s.batch.mu.Unlock()
	s.saveWatch(ctx)
	s.loop()
	return nil
}

// PauseWatch pauses (or resumes) the watch folder.
func (s *Dubs) PauseWatch(ctx context.Context, paused bool) {
	s.batch.mu.Lock()
	if !s.batch.watch.Active {
		s.batch.mu.Unlock()
		return
	}
	s.batch.watch.Paused = paused
	s.batch.mu.Unlock()
	s.saveWatch(ctx)
}

// StopWatch stops watching.
func (s *Dubs) StopWatch(ctx context.Context) {
	s.batch.mu.Lock()
	if s.batch.stop != nil {
		close(s.batch.stop)
		s.batch.stop = nil
	}
	s.batch.watch = WatchState{}
	s.batch.mu.Unlock()
	s.saveWatch(ctx)
}

// RestoreWatch resumes the watch folder saved by an earlier run.
func (s *Dubs) RestoreWatch(ctx context.Context) {
	if s.cfg.Documents == nil {
		return
	}
	data, ok, err := s.cfg.Documents.Get(ctx, batchWatchDoc)
	if err != nil || !ok {
		return
	}
	var w WatchState
	if json.Unmarshal(data, &w) != nil || !w.Active || w.W == nil {
		return
	}
	s.batch.mu.Lock()
	s.batch.watch = w
	s.batch.mu.Unlock()
	s.loop()
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(WatchChanged{State: s.Watch()})
	}
}

// CloseWatch stops the scan loop (the app quits); the folder stays
// watched for the next start.
func (s *Dubs) CloseWatch() {
	s.batch.mu.Lock()
	if s.batch.stop != nil {
		close(s.batch.stop)
		s.batch.stop = nil
	}
	s.batch.mu.Unlock()
}

func (s *Dubs) saveWatch(ctx context.Context) {
	s.batch.mu.Lock()
	w := s.batch.watch
	data, _ := json.Marshal(w)
	s.batch.mu.Unlock()
	if s.cfg.Documents != nil {
		if err := s.cfg.Documents.Put(ctx, batchWatchDoc, data); err != nil {
			s.cfg.Logf("watch folder: %v", err)
		}
	}
	if s.cfg.Bus != nil {
		w.W = nil
		s.cfg.Bus.Publish(WatchChanged{State: w})
	}
}

// loop starts the scan loop (once).
func (s *Dubs) loop() {
	s.batch.mu.Lock()
	defer s.batch.mu.Unlock()
	if s.batch.stop != nil {
		return
	}
	stop := make(chan struct{})
	s.batch.stop = stop
	every := s.batch.every
	if every <= 0 {
		every = dub.WatchInterval
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.ScanWatch(context.Background())
			}
		}
	}()
}

// ScanWatch scans the watched folder once (the loop calls it every 5 s)
// and enqueues the files that became stable. It returns them.
func (s *Dubs) ScanWatch(ctx context.Context) []string {
	s.batch.mu.Lock()
	w := s.batch.watch
	if !w.Active || w.Paused || w.W == nil {
		s.batch.mu.Unlock()
		return nil
	}
	dir := w.Dir
	s.batch.mu.Unlock()
	listing, err := dub.List(dir)
	s.batch.mu.Lock()
	if !s.batch.watch.Active || s.batch.watch.W == nil {
		s.batch.mu.Unlock()
		return nil
	}
	s.batch.watch.Last = s.cfg.Now()
	if err != nil {
		changed := s.batch.watch.Err != err.Error()
		s.batch.watch.Err = err.Error()
		s.batch.mu.Unlock()
		if changed {
			s.saveWatch(ctx)
		}
		return nil
	}
	hadErr := s.batch.watch.Err != ""
	s.batch.watch.Err = ""
	pending := len(s.batch.watch.W.Pending)
	ready := s.batch.watch.W.Scan(listing)
	changed := len(ready) > 0 || pending != len(s.batch.watch.W.Pending) || hadErr
	s.batch.mu.Unlock()
	if len(ready) > 0 {
		paths := make([]string, len(ready))
		for i, name := range ready {
			paths[i] = filepath.Join(dir, name)
		}
		ids, errs := s.Enqueue(ctx, paths, s.BatchSettings(ctx))
		s.batch.mu.Lock()
		s.batch.watch.Added += len(ids)
		for p, err := range errs {
			s.batch.watch.Err = filepath.Base(p) + ": " + err.Error()
		}
		s.batch.mu.Unlock()
	}
	if changed {
		s.saveWatch(ctx)
	}
	return ready
}

// DefaultDubTargets are the languages of a dub nobody chose any for.
func DefaultDubTargets() []string { return []string{dub.DefaultTarget} }
