package licence

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
)

// testCatalog has one free and two non-commercial models.
func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse([]byte(`{"version":1,"entries":[
	 {"id":"free","label":"Free","name":"Free","family":"f","repo":"o/free","tasks":["tts"],"licence":{"id":"apache-2.0","name":"Apache 2.0","commercial":true}},
	 {"id":"nc","label":"NC","name":"NC","family":"f","repo":"o/nc","tasks":["tts"],"licence":{"id":"cc-by-nc-4.0","name":"CC BY-NC 4.0","url":"https://example.com/nc","commercial":false,"requiresAcceptance":true}},
	 {"id":"nc2","label":"NC2","name":"NC2","family":"f","repo":"o/nc2","tasks":["tts"],"licence":{"id":"cc-by-nc-4.0","name":"CC BY-NC 4.0","commercial":false,"requiresAcceptance":true}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGate(t *testing.T) {
	ctx := context.Background()
	bus := core.NewBus()
	var changes []core.LicenceChanged
	core.On(bus, func(e core.LicenceChanged) { changes = append(changes, e) })
	repo := &memRepo{}
	g := New(repo, testCatalog(t), bus)

	for _, m := range []string{"o/free", "free", "someone/custom"} {
		if err := g.Check(ctx, m); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
	err := g.Check(ctx, "o/nc")
	var req *core.LicenceRequiredError
	if !errors.As(err, &req) || !errors.Is(err, core.ErrLicenceRequired) || req.Licence != "cc-by-nc-4.0" || req.URL != "https://example.com/nc" || req.Model != "o/nc" {
		t.Fatalf("nc: %v", err)
	}
	if err := g.Check(ctx, "nc"); err == nil {
		t.Error("by id: accepted")
	}

	// Accepted for one model: that one passes, the other still asks.
	if err := g.Accept(ctx, core.LicenceAcceptance{Licence: "cc-by-nc-4.0", Model: "o/nc"}); err != nil {
		t.Fatal(err)
	}
	if err := g.Check(ctx, "o/nc"); err != nil {
		t.Error(err)
	}
	if err := g.Check(ctx, "o/nc2"); err == nil {
		t.Error("nc2 passes after accepting for nc")
	}
	// For every model under the licence.
	g.Accept(ctx, core.LicenceAcceptance{Licence: "cc-by-nc-4.0"})
	if err := g.Check(ctx, "o/nc2"); err != nil {
		t.Error(err)
	}

	// Persisted: a new gate over the same repo agrees.
	g2 := New(repo, testCatalog(t), nil)
	list, err := g2.List(ctx)
	if err != nil || len(list) != 2 || list[0].AcceptedAt.IsZero() {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := g2.Revoke(ctx, "cc-by-nc-4.0", ""); err != nil {
		t.Fatal(err)
	}
	if err := g2.Check(ctx, "o/nc2"); err == nil {
		t.Error("nc2 passes after revoking")
	}
	if err := g2.Revoke(ctx, "cc-by-nc-4.0", ""); !errors.Is(err, core.ErrNotFound) {
		t.Error(err)
	}
	if err := g.Accept(ctx, core.LicenceAcceptance{}); !errors.Is(err, core.ErrInvalid) {
		t.Error(err)
	}
	if len(changes) != 2 || !changes[0].Accepted {
		t.Errorf("changes %+v", changes)
	}
}

func TestBuiltinNeedsNoAcceptance(t *testing.T) {
	g := New(&memRepo{}, nil, nil)
	for _, e := range catalog.Builtin().Entries {
		if err := g.Check(context.Background(), e.Repo); err != nil && !e.Licence.RequiresAcceptance {
			t.Errorf("%s: %v", e.ID, err)
		}
	}
}

// memRepo is a Repo in memory.
type memRepo struct {
	mu   sync.Mutex
	list []core.LicenceAcceptance
}

func (r *memRepo) Put(_ context.Context, a core.LicenceAcceptance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = slices.DeleteFunc(r.list, func(x core.LicenceAcceptance) bool { return x.Licence == a.Licence && x.Model == a.Model })
	r.list = append(r.list, a)
	return nil
}

func (r *memRepo) Delete(_ context.Context, licence, model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.list)
	r.list = slices.DeleteFunc(r.list, func(x core.LicenceAcceptance) bool { return x.Licence == licence && x.Model == model })
	if len(r.list) == n {
		return fmt.Errorf("acceptance of %s for %q: %w", licence, model, core.ErrNotFound)
	}
	return nil
}

func (r *memRepo) List(context.Context) ([]core.LicenceAcceptance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list), nil
}
