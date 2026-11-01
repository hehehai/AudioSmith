// Package historyjson is the MVP's take library: WAVs in <dir>/clips and
// an index, <dir>/history.json, rewritten whole on every change. It
// implements store.Takes so the app runs on the store contract today;
// thread A replaces it with SQLite and keeps this package to import old
// histories (Read).
package historyjson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store"
)

// Library stores takes under a directory. Goroutine-safe.
type Library struct {
	dir   string
	mu    sync.Mutex
	takes []core.Take // newest first
}

var _ store.Takes = (*Library)(nil)

// Open loads dir/history.json, dropping entries whose audio is gone. A
// missing index is an empty library. On error the library is still usable
// (empty), as the MVP did.
func Open(dir string) (*Library, error) {
	l := &Library{dir: dir}
	if err := os.MkdirAll(l.AudioDir(), 0o755); err != nil {
		return l, err
	}
	takes, err := Read(dir)
	if err != nil {
		return l, err
	}
	l.takes = takes
	return l, nil
}

// Read returns the takes of dir/history.json whose audio still exists,
// newest first; none when there is no index. The SQLite importer uses it.
func Read(dir string) ([]core.Take, error) {
	data, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var takes []core.Take
	if err := json.Unmarshal(data, &takes); err != nil {
		return nil, err
	}
	kept := takes[:0]
	for _, t := range takes {
		if _, err := os.Stat(filepath.Join(dir, "clips", t.File)); err == nil {
			kept = append(kept, t)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].CreatedAt.After(kept[j].CreatedAt) })
	return kept, nil
}

func (l *Library) AudioDir() string { return filepath.Join(l.dir, "clips") }

func (l *Library) AudioPath(t core.Take) string { return filepath.Join(l.AudioDir(), t.File) }

func (l *Library) indexPath() string { return filepath.Join(l.dir, "history.json") }

// save writes the index atomically; l.mu is held.
func (l *Library) save() error {
	data, err := json.MarshalIndent(l.takes, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.indexPath())
}

// Put adds a new take first, or replaces the take with its ID in place.
func (l *Library) Put(_ context.Context, t core.Take) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.takes {
		if l.takes[i].ID == t.ID {
			l.takes[i] = t
			return l.save()
		}
	}
	l.takes = append([]core.Take{t}, l.takes...)
	return l.save()
}

func (l *Library) Get(_ context.Context, id string) (core.Take, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range l.takes {
		if t.ID == id {
			return t, nil
		}
	}
	return core.Take{}, fmt.Errorf("take %s: %w", id, core.ErrNotFound)
}

// List filters in memory: Search is a case-insensitive substring match.
func (l *Library) List(_ context.Context, q core.TakeQuery) ([]core.Take, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []core.Take{}
	search := strings.ToLower(q.Search)
	skipped := 0
	for _, t := range l.takes {
		if (q.Model != "" && t.Model != q.Model) || (q.VoiceID != "" && t.VoiceID != q.VoiceID) ||
			(q.ProjectID != "" && t.ProjectID != q.ProjectID) || (q.Starred && !t.Starred) ||
			(search != "" && !strings.Contains(strings.ToLower(t.Text), search)) {
			continue
		}
		if skipped < q.Offset {
			skipped++
			continue
		}
		out = append(out, t)
		if q.Limit > 0 && len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

// Delete removes the take and its audio; deleting a missing take is a no-op.
func (l *Library) Delete(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, t := range l.takes {
		if t.ID == id {
			os.Remove(l.AudioPath(t))
			l.takes = append(l.takes[:i:i], l.takes[i+1:]...)
			return l.save()
		}
	}
	return nil
}

// NewTakeID is the MVP's take id: sortable, and the audio's file name.
// New code should prefer core.NewID; the two coexist (ids are opaque).
func NewTakeID(now time.Time) string {
	return now.Format("20060102-150405.000")
}
