package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Key is the document the settings are stored under (store.Documents).
const Key = "settings:app"

// Documents is where a Store keeps its document; store.Documents
// (SQLite) satisfies it.
type Documents interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, doc []byte) error
}

// Scales are the UI scales Appearance offers (90, 100, 110 and 125 %).
var Scales = []float64{0.9, 1, 1.1, 1.25}

// Themes are the values of Appearance.Theme.
var Themes = []string{"system", "light", "dark"}

// Store implements Service over Documents.
type Store struct {
	docs    Documents
	publish func(Changed)

	update sync.Mutex // serializes Update: read, save, swap
	mu     sync.RWMutex
	cur    Settings

	migrated []Migration // what Open's migration changed (Migrations)
}

var _ Service = (*Store)(nil)

// Open loads the settings document from docs, migrating it, and returns a
// Store that saves there. publish (may be nil) receives Changed after each
// saved update; main wires it to the core.Bus. A document that does not
// decode is replaced by the defaults at the next update: Open returns the
// Store with defaults and the error, for the log. A migration that
// changed stored values is saved at once, so it runs once: a later
// choice (say "never" again) is the user's (Migrations lists it).
func Open(ctx context.Context, docs Documents, publish func(Changed)) (*Store, error) {
	s := &Store{docs: docs, publish: publish, cur: Defaults()}
	data, ok, err := docs.Get(ctx, Key)
	if err != nil {
		return s, fmt.Errorf("settings: %w", err)
	}
	if !ok {
		return s, nil
	}
	cur, done, err := migrate(data)
	s.cur = normalize(cur)
	if err != nil {
		return s, fmt.Errorf("settings: %w", err)
	}
	if len(done) > 0 {
		out, err := json.MarshalIndent(s.cur, "", "  ")
		if err == nil {
			err = docs.Put(ctx, Key, out)
		}
		if err != nil {
			return s, fmt.Errorf("settings: saving the migrated settings: %w", err)
		}
		s.migrated = done
	}
	return s, nil
}

// Migrations lists the stored values Open migrated (and saved); nil when
// none. The app logs them.
func (s *Store) Migrations() []Migration {
	return slices.Clone(s.migrated)
}

// NewMemory returns a Store with the defaults that keeps them in memory:
// for tests, and the UI before the app wires the real one.
func NewMemory() *Store {
	s, _ := Open(context.Background(), &memDocs{}, nil)
	return s
}

// Get returns a copy of the current settings, from memory.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cur)
}

// Update applies fn to a copy, normalizes it (unknown themes become
// "system", scales snap to Scales), saves it, swaps it in and publishes
// Changed. Nothing is saved or published when fn changes nothing.
func (s *Store) Update(ctx context.Context, fn func(*Settings)) error {
	s.update.Lock()
	old := s.Get()
	next := clone(old)
	fn(&next)
	next = normalize(next)
	stampDictation(old, &next) // family.go
	if reflect.DeepEqual(old, next) {
		s.update.Unlock()
		return nil
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err == nil {
		err = s.docs.Put(ctx, Key, data)
	}
	if err != nil {
		s.update.Unlock()
		return fmt.Errorf("settings: %w", err)
	}
	s.mu.Lock()
	s.cur = next
	s.mu.Unlock()
	s.update.Unlock()
	if s.publish != nil {
		s.publish(Changed{Old: old, New: clone(next)})
	}
	return nil
}

func clone(s Settings) Settings {
	s.Models.Endpoints = slices.Clone(s.Models.Endpoints)
	cloneM2(&s)
	cloneM7(&s)
	cloneExport(&s)
	cloneFamily(&s)
	return s
}

// normalize keeps values in their ranges.
func normalize(s Settings) Settings {
	s.Version = Version
	if !slices.Contains(Themes, s.Appearance.Theme) {
		s.Appearance.Theme = "system"
	}
	// Snap the scale to the nearest offered one.
	best := 1.0
	for _, v := range Scales {
		if abs(v-s.Appearance.Scale) < abs(best-s.Appearance.Scale) {
			best = v
		}
	}
	s.Appearance.Scale = best
	s.Models.CacheDir = strings.TrimSpace(s.Models.CacheDir)
	s.Models.Proxy = strings.TrimSpace(s.Models.Proxy)
	var eps []string
	for _, e := range s.Models.Endpoints {
		e = strings.TrimRight(strings.TrimSpace(e), "/")
		if e != "" && !slices.Contains(eps, e) {
			eps = append(eps, e)
		}
	}
	s.Models.Endpoints = eps
	s.Models.AutoUnload = max(s.Models.AutoUnload, 0)
	s.Engine.MemoryBudgetGB = max(s.Engine.MemoryBudgetGB, 0)
	normalizeM2(&s)
	normalizeM7(&s)
	normalizeExport(&s)
	normalizeFamily(&s)
	return s
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

type memDocs struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (d *memDocs) Get(_ context.Context, key string) ([]byte, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.m[key]
	return slices.Clone(v), ok, nil
}

func (d *memDocs) Put(_ context.Context, key string, doc []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string][]byte{}
	}
	d.m[key] = slices.Clone(doc)
	return nil
}
