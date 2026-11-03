package services

// The dub service's cache of decoded projects (code review 1102 perf M6,
// old #102 item 12): every Update used to read the whole document back
// and decode it (13 ms and 16 MB for 2000 lines), and wrote it back
// even when nothing changed. The service is the only writer of its
// table, so it keeps the last projects it read or saved, decoded, with
// the document as saved: Get and Update clone the cached project
// instead of decoding, and an Update whose document comes out the same
// writes and publishes nothing. The document format is unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/dub"
)

// dubCacheSize is how many projects the cache keeps: the open one, the
// ones the pipeline runs, a few the list showed.
const dubCacheSize = 4

// dubCache keeps decoded projects, most recently used last.
// Goroutine-safe; writers (Dubs.update, Create, Delete) hold Dubs.mu
// as well, so a project's entry only changes with its row.
type dubCache struct {
	mu      sync.Mutex
	entries []dubCached
	// gen counts the writes, so that a read which missed does not cache
	// what a write replaced meanwhile.
	gen uint64
}

type dubCached struct {
	id  string
	p   *dub.Project // normalized; never handed out (clones are)
	doc []byte       // the document as saved
}

// get is the cached project id (shared: clone it) and its document.
func (c *dubCache) get(id string) (*dub.Project, []byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, e := range c.entries {
		if e.id == id {
			copy(c.entries[i:], c.entries[i+1:])
			c.entries[len(c.entries)-1] = e
			return e.p, e.doc, true
		}
	}
	return nil, nil, false
}

// generation is the count of writes so far, for keepRead.
func (c *dubCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// keepRead caches what a read found, unless a write happened since gen.
func (c *dubCache) keepRead(gen uint64, id string, p *dub.Project, doc []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen == gen {
		c.putLocked(id, p, doc)
	}
}

// saved caches what a write saved (p is the cache's own from now on).
func (c *dubCache) saved(id string, p *dub.Project, doc []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.putLocked(id, p, doc)
}

// drop forgets project id (deleted, or its write failed).
func (c *dubCache) drop(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.removeLocked(id)
}

func (c *dubCache) putLocked(id string, p *dub.Project, doc []byte) {
	c.removeLocked(id)
	if len(c.entries) >= dubCacheSize {
		c.entries = append(c.entries[:0], c.entries[1:]...)
	}
	c.entries = append(c.entries, dubCached{id: id, p: p, doc: doc})
}

func (c *dubCache) removeLocked(id string) {
	for i, e := range c.entries {
		if e.id == id {
			c.entries = append(c.entries[:i], c.entries[i+1:]...)
			return
		}
	}
}

// cached is project id from the cache, or read and cached: the shared
// copy (clone it) and its document as saved.
func (s *Dubs) cached(ctx context.Context, id string) (*dub.Project, []byte, error) {
	if p, doc, ok := s.cache.get(id); ok {
		return p, doc, nil
	}
	gen := s.cache.generation()
	p, doc, err := s.load(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	s.cache.keepRead(gen, id, p, doc)
	return p, doc, nil
}

// load reads and decodes project id from the store, past the cache.
func (s *Dubs) load(ctx context.Context, id string) (*dub.Project, []byte, error) {
	r, err := s.cfg.Store.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	p := &dub.Project{}
	if err := json.Unmarshal(r.Doc, p); err != nil {
		return nil, nil, fmt.Errorf("dub %s: %w", id, err)
	}
	p.Normalize()
	return p, r.Doc, nil
}

// restamp is doc, the encoding of a project, with its updatedAt changed
// from old to now, without encoding it again; ok false when doc does
// not have it where expected. The first `,"updatedAt":` of an encoded
// Project is its own field (the earlier ones are numbers, ids, a title
// escaped as a JSON string, and a time).
func restamp(doc []byte, old, now time.Time) ([]byte, bool) {
	from, err1 := json.Marshal(old)
	to, err2 := json.Marshal(now)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	key := []byte(`,"updatedAt":`)
	i := bytes.Index(doc, key)
	if i < 0 || !bytes.HasPrefix(doc[i+len(key):], from) {
		return nil, false
	}
	at := i + len(key)
	out := make([]byte, 0, len(doc)-len(from)+len(to))
	out = append(append(append(out, doc[:at]...), to...), doc[at+len(from):]...)
	return out, true
}
