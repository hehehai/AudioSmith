package sqlitestore

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/store"
)

// TestLibrarySearchTopPerPart: a page of the library (Home's recent) reads
// each part's newest rows only (perf review 11-02 H1: 31 ms for 10 000
// takes before, 0.23 ms after), and lists what the whole union, sorted,
// lists: the same rows in the same order, pages and categories too.
func TestLibrarySearchTopPerPart(t *testing.T) {
	if testing.Short() {
		t.Skip("10 000 takes")
	}
	s := openTest(t, t.TempDir())
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	txt := strings.Repeat("他也说过，这个城市里有很多米粉店，西边那家最好吃。", 60)
	if err := s.Takes().Put(ctx, take("t00000", t0, txt)); err != nil {
		t.Fatal(err)
	}
	// 9 999 more, a second apart, in one statement (10 000 Puts take seconds).
	if _, err := s.db.Execute(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 9999)
		INSERT INTO takes (id, created_at, file, text, model, recipe) SELECT printf('t%05d', i), b.created_at + i * 1000, b.file, b.text, b.model, b.recipe
		FROM n, takes b WHERE b.id = 't00000'`); err != nil {
		t.Fatal(err)
	}
	for i := range 300 {
		at := t0.Add(time.Duration(i*37) * time.Second) // among the takes; some on a take's second
		kind := core.ProjectStory
		if i%2 == 1 {
			kind = core.ProjectAudiobook
		}
		if err := s.Projects().Put(ctx, core.Project{ID: fmt.Sprintf("p%04d", i), Kind: kind, Title: fmt.Sprintf("Project %d", i), CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		st := core.RenderDone
		if i%3 == 0 {
			st = core.RenderFailed
		}
		if err := s.Renders().Put(ctx, core.Render{ID: fmt.Sprintf("r%04d", i), ProjectID: fmt.Sprintf("p%04d", i), Kind: kind, Title: "R", State: st, CreatedAt: at, UpdatedAt: at.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	lib := s.Library()
	// whole is the page as the union, sorted whole, lists it.
	whole := func(q store.LibraryQuery) []store.LibraryItem {
		sql, args := libQuery(q.Category, q.Search, 0)
		sql, args = page(`SELECT src, id, cat, title, detail, at FROM (`+sql+`) ORDER BY at DESC, id DESC`, args, q.Limit, q.Offset)
		rows, err := s.db.Query(ctx, sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		var out []store.LibraryItem
		for _, row := range rows.Values {
			src := asString(row[0])
			out = append(out, store.LibraryItem{Source: src, ID: asString(row[1]), Category: libCategory(src, asString(row[2])),
				Title: asString(row[3]), Detail: asString(row[4]), Updated: fromMS(row[5])})
		}
		return out
	}
	for _, q := range []store.LibraryQuery{{Limit: 60}, {Limit: 60, Offset: 120}, {Category: store.LibStory, Limit: 50}, {Category: store.LibTake, Limit: 20, Offset: 9990},
		{Search: "Project", Limit: 30}, {Search: "米粉", Limit: 10}} {
		got, err := lib.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		want := whole(q)
		if len(want) == 0 || !slices.EqualFunc(got, want, func(a, b store.LibraryItem) bool {
			return a.Source == b.Source && a.ID == b.ID && a.Category == b.Category && a.Title == b.Title && a.Detail == b.Detail && a.Updated.Equal(b.Updated)
		}) {
			t.Fatalf("%+v: %d items, want %d (the whole union's page)", q, len(got), len(want))
		}
	}
	best := time.Hour
	for range 5 {
		t1 := time.Now()
		if _, err := lib.Search(ctx, store.LibraryQuery{Limit: 60}); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(t1))
	}
	t1 := time.Now()
	whole(store.LibraryQuery{Limit: 60})
	t.Logf("a page of 60: %v (the whole union sorted: %v)", best, time.Since(t1))
	if best > 8*time.Millisecond { // 0.23 ms measured; 31 ms before (shared host: a wide margin)
		t.Errorf("Home's recent page took %v for 10 000 takes, want < 8 ms", best)
	}
}
