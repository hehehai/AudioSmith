package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAPIAssetsPaging: list_assets reads each source only as far as the
// page reaches, and render bodies without their manifest (code review
// 1102 perf M4), yet every page is what sorting everything gives: past
// the 2000 takes it used to cut, across ties of time at a page's edge,
// with exports that are no media, and drafts named by their script.
func TestAPIAssetsPaging(t *testing.T) {
	lib := openTestLibrary(t)
	ctx := context.Background()
	st := lib.store
	base := time.Now().Add(-1000 * time.Hour).Truncate(time.Millisecond)
	for i := range 2100 {
		at := base.Add(time.Duration(i) * time.Minute)
		if i >= 1990 && i < 2010 {
			at = base.Add(1990 * time.Minute) // a tie across page edges
		}
		if err := st.Takes().Put(ctx, core.Take{ID: fmt.Sprintf("t%04d", i), File: fmt.Sprintf("t%04d.wav", i), Text: fmt.Sprintf("take %d", i),
			Model: "m", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	// Books with a long manifest, one draft (no project: its title is
	// its script's first words), and a story.
	var chapters []services.ChapterSpec
	for c := range 40 {
		ch := services.ChapterSpec{Title: fmt.Sprintf("Chapter %d", c+1)}
		for range 60 {
			ch.Lines = append(ch.Lines, services.LineSpec{Spans: []services.SpanSpec{{Voice: "n", Text: strings.Repeat("A sentence of the book. ", 4)}}})
		}
		chapters = append(chapters, ch)
	}
	for i := range 12 {
		id, kind, project := fmt.Sprintf("r%02d", i), core.ProjectAudiobook, fmt.Sprintf("p%02d", i)
		title := fmt.Sprintf("Book %d", i)
		switch i {
		case 3:
			project, title = "", "Untitled"
		case 5:
			kind = core.ProjectStory
		}
		if project != "" {
			if err := st.Projects().Put(ctx, core.Project{ID: project, Kind: kind, Title: title, CreatedAt: base, UpdatedAt: base}); err != nil {
				t.Fatal(err)
			}
		}
		at := base.Add(time.Duration(i*200) * time.Minute)
		body, _ := json.Marshal(services.RenderBody{Spec: services.LongformSpec{Project: project, Kind: kind, Chapters: chapters},
			Output: &services.AssembleResult{Book: "/books/" + id + ".wav"}})
		if err := st.Renders().Put(ctx, core.Render{ID: id, ProjectID: project, Kind: kind, Title: title, State: core.RenderDone,
			Body: body, CreatedAt: at, UpdatedAt: at.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		// Exports: a book (media) and its cue sheet (not media).
		for j, ext := range []string{"m4b", "txt"} {
			if err := st.Exports().Put(ctx, core.Export{ID: fmt.Sprintf("e%02d%d", i, j), Kind: "book", SourceID: id, ProjectID: project,
				Title: title, Path: "/out/" + id + "." + ext, CreatedAt: at.Add(time.Duration(j) * time.Second)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	b := &apiBackend{lib: lib}

	// Everything, sorted: what every page must be a slice of.
	all, err := b.Assets(ctx, api.AssetQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if want := 2100 + 12 + 12; len(all) != want {
		t.Fatalf("%d assets, want %d (takes past 2000 dropped?)", len(all), want)
	}
	if i := slices.IndexFunc(all, func(a api.AssetJSON) bool { return a.ID == "render:r03" }); i < 0 || !strings.HasPrefix(all[i].Title, "A sentence") {
		t.Errorf("draft render %+v", all[max(i, 0)])
	}
	ids := func(l []api.AssetJSON) []string {
		out := make([]string, len(l))
		for i, a := range l {
			out[i] = a.ID
		}
		return out
	}
	for _, q := range []api.AssetQuery{
		{Limit: 50}, {Limit: 50, Offset: 60}, {Limit: 10, Offset: 85}, {Limit: 15, Offset: 100}, {Limit: 500, Offset: 1500},
		{Limit: 5, Kind: "audiobook"}, {Limit: 3, Kind: "story"}, {Limit: 20, Kind: "speech", Offset: 95},
	} {
		want := pageAssets(slices.Clone(all), q)
		got, err := b.Assets(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(got), ids(want)) {
			t.Errorf("%+v: got %v\nwant %v", q, ids(got), ids(want))
		}
	}

	// The cost: a first page against reading everything (as before, but
	// with whole bodies).
	t0 := time.Now()
	for range 5 {
		b.Assets(ctx, api.AssetQuery{Limit: 50})
	}
	page := time.Since(t0) / 5
	t0 = time.Now()
	for range 5 {
		b.lib.svc.Takes.List(ctx, core.TakeQuery{Limit: 2000})
		l, _ := b.lib.svc.Longform.List(ctx, store.RenderQuery{States: []core.RenderState{core.RenderDone}, Limit: 1000})
		for _, r := range l {
			services.DecodeRender(r)
		}
	}
	old := time.Since(t0) / 5
	t.Logf("first page of 50: %v; reading 2000 takes and decoding the whole bodies: %v", page, old)
}

// TestPoCNewestOverflowLoops (code review 1103 M2): a need near MaxInt
// (an MCP client's offset) reads the source once instead of for ever
// (need+1 and the doubling overflowed into fetch(0) = all, again and
// again); a table shorter than the limit too.
func TestPoCNewestOverflowLoops(t *testing.T) {
	for _, need := range []int{int(^uint(0) >> 1), int(^uint(0)>>1) - 500, api.MaxOffset, api.MaxOffset - 1, 3} {
		calls := 0
		l, err := newest(need, func(n int) ([]int, error) {
			if calls++; calls > 40 {
				t.Fatalf("need %d: still reading after %d reads (limit %d)", need, calls, n)
			}
			return []int{1, 2, 3}, nil // the whole table, whatever the limit
		}, nil, func(int) time.Time { return time.Time{} })
		if err != nil || len(l) != 3 || calls != 1 {
			t.Errorf("need %d: %v %v after %d reads", need, l, err, calls)
		}
	}
}

// TestAPIAssetsHugeOffset: list_assets over MCP with an offset past
// MaxInt/2 answers an empty page (bounded as REST bounds it).
func TestAPIAssetsHugeOffset(t *testing.T) {
	e := newStudioEnv(t)
	e.must(t, "generate_speech", map[string]any{"text": "One asset.", "output": "file"}, nil)
	for _, off := range []float64{9e18, float64(api.MaxOffset)} {
		var page struct {
			Count  int             `json:"count"`
			Assets []api.AssetJSON `json:"assets"`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		res, err := e.cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_assets", Arguments: map[string]any{"offset": off, "limit": 500}})
		cancel()
		if err != nil || res.IsError {
			t.Fatalf("offset %g: %v %+v", off, err, res)
		}
		e.must(t, "list_assets", map[string]any{"offset": off}, &page)
		if page.Count != 0 || len(page.Assets) != 0 {
			t.Errorf("offset %g: %+v", off, page)
		}
	}
}
