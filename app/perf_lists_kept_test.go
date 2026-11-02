package main

// What the perf fixes of 11-02 keep between frames must follow the data:
// Assets' take rows, ⌘K's results, Projects' counts and Home's Recent.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/store"
)

// assetKeys are the keys of assets, in order.
func assetKeys(as []asset) []string {
	var out []string
	for _, x := range as {
		out = append(out, x.key)
	}
	return out
}

// TestAssetsMergedAsSorted: the take rows kept and the rest, merged, are
// in the order the whole list sorted (stable, takes first on a tie) was.
func TestAssetsMergedAsSorted(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	perfLibrary{takes: 40, transcripts: 3, dubs: 12, isolations: 8}.fill(a)
	// Ties: a dub and an isolation at a take's time.
	a.dubList[0].CreatedAt = a.takes[5].CreatedAt
	a.isoList[0].UpdatedAt = a.takes[7].CreatedAt
	sortTakes(a.takes)
	want := append(append([]asset{}, a.takeAssets()...), a.otherAssets()...)
	slices.SortStableFunc(want, func(x, y asset) int { return y.at.Compare(x.at) })
	if got := assetKeys(a.assets()); !slices.Equal(got, assetKeys(want)) {
		t.Fatalf("merged:\n%q\nwant the sorted list:\n%q", got, assetKeys(want))
	}
}

// TestAssetsTakeRowsFollowTakes: the take rows are kept while no take
// changed, made anew when one did (its text, a new take, one deleted, a
// new snapshot), and their titles are short.
func TestAssetsTakeRowsFollowTakes(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	perfLibrary{takes: 5}.fill(a)
	rows := a.takeAssets()
	if again := a.takeAssets(); &again[0] != &rows[0] {
		t.Fatal("the take rows were made again with nothing changed")
	}
	for _, r := range rows {
		if len(r.title) > 404 || strings.ContainsAny(r.title, "\n") || r.text != r.take.Text {
			t.Fatalf("a title of %d bytes (want ≤ ~400 on one line), or the script lost", len(r.title))
		}
	}
	tk := a.takes[2]
	tk.Text = "A new line."
	a.putTake(tk)
	if r := a.takeAssets(); r[2].title != "A new line." {
		t.Fatalf("a take's text changed, its row says %q", r[2].title)
	}
	a.putTake(core.Take{ID: "new", CreatedAt: time.Now(), Text: "Newest.", Model: kokoro})
	if r := a.takeAssets(); len(r) != 6 || r[0].key != "take:new" || r[0].take != &a.takes[0] {
		t.Fatalf("a new take: %q", assetKeys(r))
	}
	a.takeDeleted("new")
	r := a.takeAssets()
	if len(r) != 5 || slices.Contains(assetKeys(r), "take:new") {
		t.Fatalf("a take deleted: %q", assetKeys(r))
	}
	for i := range r {
		if r[i].take != &a.takes[i] {
			t.Fatal("a row holds a take that is not in the snapshot")
		}
	}
	a.takes = slices.Clone(a.takes) // a new snapshot (another array)
	if r := a.takeAssets(); r[0].take != &a.takes[0] {
		t.Fatal("a new snapshot: the rows hold the old one's takes")
	}
}

// TestAssetsFilterTakes: the star, model and script filters, without a
// copy of the takes.
func TestAssetsFilterTakes(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	perfLibrary{takes: 4}.fill(a)
	a.takes[1].Starred = true
	a.takes[2].Text = "Ferry at ELEVEN."
	takes := func() (out []string) {
		all := a.assets()
		for _, i := range a.assetsShown(all) {
			if all[i].take != nil {
				out = append(out, all[i].take.ID)
			}
		}
		return out
	}
	a.history.filter = 1
	if got := takes(); !slices.Equal(got, []string{a.takes[1].ID}) {
		t.Errorf("starred: %q", got)
	}
	a.history.filter, a.history.query = 0, "eleven" // before the index answers: the script, in any case
	if got := takes(); !slices.Equal(got, []string{a.takes[2].ID}) {
		t.Errorf("searched: %q", got)
	}
	a.history.searchFor, a.history.results = "eleven", map[string]bool{a.takes[3].ID: true} // the index answered
	if got := takes(); !slices.Equal(got, []string{a.takes[3].ID}) {
		t.Errorf("the index's results: %q", got)
	}
}

// TestPaletteFoundFollows: ⌘K keeps what it found for a query, and finds
// again when the query or the takes change.
func TestPaletteFoundFollows(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	perfLibrary{takes: 3, projects: 4}.fill(a)
	a.takes[0].Text = "The lighthouse keeper."
	titles := func(rows []paletteRow) (out []string) {
		for _, r := range rows {
			out = append(out, r.title)
		}
		return out
	}
	_, tk := a.paletteFound("LIGHTHOUSE")
	if !slices.Equal(titles(tk), []string{"The lighthouse keeper."}) {
		t.Fatalf("found %q", titles(tk))
	}
	pj, _ := a.paletteFound("project 3")
	if !slices.Equal(titles(pj), []string{"Project 3"}) {
		t.Fatalf("projects found %q", titles(pj))
	}
	a.putTake(core.Take{ID: "n", CreatedAt: time.Now(), Text: "Another lighthouse.", Model: kokoro})
	if _, tk := a.paletteFound("lighthouse"); len(tk) != 2 {
		t.Fatalf("a new take: found %q", titles(tk))
	}
	if _, tk := a.paletteFound("米粉"); len(tk) != 2 { // the two scripts perfText made have it
		t.Fatalf("米粉: %d takes found, want 2", len(tk))
	}
}

// TestProjTotal is the count of every row, as projItems makes them.
func TestProjTotal(t *testing.T) {
	i18n.SetLocale("en")
	a, _, _ := newTestApp(t)
	perfLibrary{projects: 7, transcripts: 2, dubs: 3, isolations: 4}.fill(a)
	a.trList = append(a.trList, core.Transcript{ID: "dict", Source: core.SourceDictation})
	if got, want := a.projTotal(), len(a.projItems("", "")); got != want || want != 16 {
		t.Fatalf("projTotal %d, projItems %d (want 16)", got, want)
	}
}

// TestHomeRecentFollowsLibrary: Recent is read once, then when the
// library changes (not every few seconds).
func TestHomeRecentFollowsLibrary(t *testing.T) {
	i18n.SetLocale("en")
	a, _, tt := newTestApp(t)
	a.navigate("/home")
	tt.Frame()
	hp := &a.homeUI
	read := hp.readAt
	if read.IsZero() || hp.stale() {
		t.Fatalf("Home did not read Recent (at %v, stale %v)", read, hp.stale())
	}
	hp.readAt = read.Add(-10 * time.Second) // what read it every 5 s
	read = hp.readAt
	tt.Frame()
	if hp.readAt != read {
		t.Fatal("Recent was read again with nothing changed")
	}
	addTake(t, a, core.Take{ID: "T1", CreatedAt: time.Now(), Text: "The last ferry leaves at eleven.", Model: kokoro}, 0.3)
	if !hp.stale() {
		t.Fatal("a new take did not mark Recent stale")
	}
	tt.Frame()
	if !slices.ContainsFunc(hp.recent, func(it store.LibraryItem) bool { return it.ID == "T1" }) || hp.stale() {
		t.Fatalf("Recent after a new take: %v, stale %v", hp.recent, hp.stale())
	}
	// A project: it marks it too.
	before := hp.changed
	a.lib.bus.Publish(core.ProjectChanged{})
	tt.Frame()
	if hp.changed != before+1 {
		t.Fatalf("a project changed: %d changes, want %d", hp.changed, before+1)
	}
}
