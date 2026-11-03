package main

// The toolkit for agents (thread #135): api.Toolkit over the services the
// pages use — the Voice isolator (isolate.go) and Assets (assets.go)
// here; voice management, previews and the reference check in
// api_kit_voices.go; the samples and the story draft in api_kit_more.go.
// Like the rest of apiBackend it runs on the API's goroutines and reads
// only what is goroutine-safe; what needs the UI thread goes through the
// app's hooks (kitParts).

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

var _ api.Toolkit = (*apiBackend)(nil)

// kitParts are what the toolkit uses besides the library: the isolator,
// and the app's hooks for what runs on the UI thread (nil in tests of the
// API alone: those tools answer unsupported).
type kitParts struct {
	iso *services.Isolations
	// samples lists the sample cards; openSample copies one into the
	// library and remembers the copy, as Home's card does.
	samples    func(ctx context.Context) ([]api.SampleJSON, error)
	openSample func(ctx context.Context, id string) (api.SampleOpened, error)
}

// useKit gives the API the isolator and the hooks (newAPIBackend).
func (a *app) useKit(b *apiBackend) {
	b.iso = a.isoSvc
	b.samples = a.apiSamples
	b.openSample = a.apiOpenSample
}

// --- Voice isolator -------------------------------------------------------------------

// isoStemAbout says what each stem is, for agents.
var isoStemAbout = map[string]string{
	"vocals":     "The voices alone, as separated.",
	"background": "Everything but the voices: music and effects.",
	"clean":      "The voices with the remaining noise removed.",
}

// isolationJSON is an isolation for agents.
func (b *apiBackend) isolationJSON(x services.Isolation) api.IsolationJSON {
	out := api.IsolationJSON{ID: x.ID, Title: x.Title, Input: x.Input, Video: x.Video, Duration: round2(x.Duration), State: x.State,
		JobID: x.Job, Error: x.Error, ErrorCode: x.ErrorCode, Warnings: x.Warnings, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
	for _, s := range []struct{ name, file string }{{"vocals", x.Vocals}, {"background", x.Background}, {"clean", x.Clean}} {
		if s.file != "" {
			out.Stems = append(out.Stems, api.StemJSON{Name: s.name, File: b.iso.Path(x.ID, s.file), About: isoStemAbout[s.name]})
		}
	}
	return out
}

// StartIsolation queues an isolation of a media file.
func (b *apiBackend) StartIsolation(ctx context.Context, path string) (api.IsolationJSON, error) {
	if b.iso == nil {
		return api.IsolationJSON{}, errNoModule("The Voice isolator")
	}
	if !dub.IsMedia(path, isolateExts) {
		return api.IsolationJSON{}, apiErr(400, api.CodeUnsupportedFormat, "path", "%s is not an audio or video file the Voice isolator takes (%s).",
			filepath.Base(path), strings.Join(isolateExts, ", "))
	}
	x, err := b.iso.Start(ctx, path)
	if err != nil {
		return api.IsolationJSON{}, err
	}
	return b.isolationJSON(x), nil
}

// Isolations lists the isolations, newest first.
func (b *apiBackend) Isolations(ctx context.Context) ([]api.IsolationJSON, error) {
	if b.iso == nil {
		return nil, errNoModule("The Voice isolator")
	}
	l, err := b.iso.List(ctx)
	if err != nil {
		return nil, err
	}
	sortIsolations(l)
	out := make([]api.IsolationJSON, 0, len(l))
	for _, x := range l {
		out = append(out, b.isolationJSON(x))
	}
	return out, nil
}

// Isolation reads one isolation.
func (b *apiBackend) Isolation(ctx context.Context, id string) (api.IsolationJSON, error) {
	if b.iso == nil {
		return api.IsolationJSON{}, errNoModule("The Voice isolator")
	}
	x, err := b.iso.Get(ctx, id)
	if err != nil {
		return api.IsolationJSON{}, notFound(err, "isolation", id)
	}
	return b.isolationJSON(x), nil
}

// DeleteIsolation deletes an isolation and its stems.
func (b *apiBackend) DeleteIsolation(ctx context.Context, id string) error {
	if b.iso == nil {
		return errNoModule("The Voice isolator")
	}
	if _, err := b.iso.Get(ctx, id); err != nil {
		return notFound(err, "isolation", id)
	}
	return b.iso.Delete(ctx, id)
}

// --- Assets ------------------------------------------------------------------------------

// assetMediaExts are the files Assets lists from export records (audio
// and video; not captions, scripts or voice files).
var assetMediaExts = append(slices.Clone(assetAudioExts), "mp4", "mov", "m4v", "mkv", "webm", "zip")

// Assets lists every output and imported file, as the Assets page does,
// from the services rather than the page's state. Without a search, each
// source is read only as far as the page can reach (code review 1102
// perf M4: it read 2000 takes and decoded up to 1000 whole render
// bodies a call, and silently dropped what lay past those); sources of
// other kinds than q.Kind are not read.
func (b *apiBackend) Assets(ctx context.Context, q api.AssetQuery) ([]api.AssetJSON, error) {
	var out []api.AssetJSON
	add := func(a api.AssetJSON) {
		if a.File != "" {
			out = append(out, a)
		}
	}
	need := 0 // per source; 0 = all (a search looks at every title)
	if strings.TrimSpace(q.Search) == "" && q.Limit > 0 {
		need = min(max(q.Offset, 0), api.MaxOffset) + q.Limit
	}
	wants := func(kinds ...string) bool { return q.Kind == "" || slices.Contains(kinds, q.Kind) }
	if wants("speech") {
		takes, err := newest(need, func(n int) ([]core.Take, error) { return b.lib.svc.Takes.List(ctx, core.TakeQuery{Limit: n}) },
			nil, func(t core.Take) time.Time { return t.CreatedAt })
		if err != nil {
			return nil, err
		}
		for _, t := range takes {
			add(api.AssetJSON{ID: "take:" + t.ID, Kind: "speech", Title: firstWords(oneLine(t.Text), 12), Detail: modelName(t.Model),
				File: b.TakeAudio(t), Seconds: round2(t.AudioSeconds), At: t.CreatedAt, Page: "/studio", Tool: "list_takes", Source: t.ID})
		}
	}
	storyRender := map[string]bool{}
	if wants("speech", "dub", "story", "audiobook") { // the exports need storyRender too
		// Light: the output, not the manifest. All of them: the list is
		// by creation, assets by update.
		renders, err := b.lib.svc.Longform.List(ctx, store.RenderQuery{States: []core.RenderState{core.RenderDone}, Light: true})
		if err != nil {
			return nil, err
		}
		for _, r := range renders {
			if r.Audition {
				continue
			}
			kind, page := "story", "/stories"
			if r.Kind == core.ProjectAudiobook {
				kind, page = "audiobook", "/audiobook"
			} else {
				storyRender[r.ID] = true
			}
			body, err := services.DecodeRender(r)
			if err != nil || body.Output == nil || !wants(kind) {
				continue
			}
			add(api.AssetJSON{ID: "render:" + r.ID, Kind: kind, Title: firstOf(renderName(r, &body.Spec), "Untitled"), File: body.Output.Book,
				Seconds: round2(r.AudioSeconds), At: r.UpdatedAt, Page: page, Tool: "get_longform", Source: r.ProjectID})
		}
	}
	if lib := b.lib.svc.Library; lib != nil && wants("speech", "dub", "story", "audiobook") {
		keep := func(e core.Export) bool {
			a, ok := exportAsset(e, storyRender)
			return ok && wants(a.Kind)
		}
		exports, err := newest(need, func(n int) ([]core.Export, error) { return lib.Exports(ctx, "", n) },
			keep, func(e core.Export) time.Time { return e.CreatedAt })
		if err != nil {
			return nil, err
		}
		for _, e := range exports {
			if a, ok := exportAsset(e, storyRender); ok {
				add(a)
			}
		}
	}
	if b.iso != nil && wants("stems", "import") {
		isos, err := b.iso.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, x := range isos {
			for _, s := range b.isolationJSON(x).Stems {
				add(api.AssetJSON{ID: "stem:" + x.ID + ":" + s.Name, Kind: "stems", Title: x.Title + " · " + s.Name, Detail: s.About,
					File: s.File, Seconds: round2(x.Duration), At: x.UpdatedAt, Page: "/isolate", Tool: "get_isolation", Source: x.ID})
			}
			add(api.AssetJSON{ID: "in:iso:" + x.ID, Kind: "import", Title: filepath.Base(x.Input), Detail: "isolated", File: x.Input,
				Seconds: round2(x.Duration), At: x.CreatedAt, Page: "/isolate", Tool: "get_isolation", Source: x.ID})
		}
	}
	if b.tr != nil && wants("import") {
		notDictation := func(t core.Transcript) bool { return t.Source != core.SourceDictation }
		trs, err := newest(need, func(n int) ([]core.Transcript, error) { return b.tr.List(ctx, core.TranscriptQuery{Limit: n}) },
			notDictation, func(t core.Transcript) time.Time { return t.CreatedAt })
		if err != nil {
			return nil, err
		}
		for _, t := range trs {
			if !notDictation(t) {
				continue
			}
			path := t.SourcePath
			if t.Audio != "" {
				path = b.tr.AudioPath(t)
			}
			add(api.AssetJSON{ID: "in:tr:" + t.ID, Kind: "import", Title: transcriptTitle(t), Detail: "transcribed", File: path,
				Seconds: round2(t.Duration), At: t.CreatedAt, Page: "/transcripts", Tool: "export_transcript", Source: t.ID})
		}
	}
	if b.dubs != nil && wants("import") {
		dubs, err := b.dubs.List(ctx, store.DubQuery{AllBatches: true}) // by update; assets by creation: all (no documents)
		if err != nil {
			return nil, err
		}
		for _, r := range dubs {
			if r.Source == "" || !filepath.IsAbs(r.Source) { // a link: no file here
				continue
			}
			add(api.AssetJSON{ID: "in:dub:" + r.ID, Kind: "import", Title: filepath.Base(r.Source), Detail: "dubbed", File: r.Source,
				Seconds: round2(r.Duration), At: r.CreatedAt, Page: "/dub", Tool: "get_dub", Source: r.ID})
		}
	}
	return pageAssets(out, q), nil
}

// exportAsset is an export record as an asset; false for files that are
// no audio or video (captions, scripts, voice files).
func exportAsset(e core.Export, storyRender map[string]bool) (api.AssetJSON, bool) {
	if !slices.Contains(assetMediaExts, strings.ToLower(strings.TrimPrefix(filepath.Ext(e.Path), "."))) {
		return api.AssetJSON{}, false
	}
	a := api.AssetJSON{ID: "export:" + e.ID, Kind: "speech", Title: e.Title, Detail: "exported " + e.Kind, File: e.Path, At: e.CreatedAt,
		Page: "/studio", Tool: "list_takes", Source: e.SourceID}
	switch e.Kind {
	case "take", "voice", "transcript", "cues":
	case "dub":
		a.Kind, a.Page, a.Tool = "dub", "/dub", "get_dub"
	case "story":
		a.Kind, a.Page, a.Tool, a.Source = "story", "/stories", "get_longform", e.ProjectID
	default: // book, chapter, stem
		a.Kind, a.Page, a.Tool, a.Source = "audiobook", "/audiobook", "get_longform", e.ProjectID
		if storyRender[e.SourceID] {
			a.Kind, a.Page = "story", "/stories"
		}
	}
	return a, true
}

// newest reads a source listed newest first (by at): all of it when need
// is 0, else at least as far as its need newest items that keep accepts
// (nil: all) and any as new as the last of those, which the page's order
// may put before it. fetch(0) reads all. Past api.MaxOffset it reads
// all at once (code review 1103 M2: a need near MaxInt overflowed into
// fetch(0) for ever).
func newest[T any](need int, fetch func(limit int) ([]T, error), keep func(T) bool, at func(T) time.Time) ([]T, error) {
	if need <= 0 || need >= api.MaxOffset {
		return fetch(0)
	}
	for limit := need + 1; ; limit *= 2 {
		if limit > 2*api.MaxOffset {
			return fetch(0)
		}
		l, err := fetch(limit)
		if err != nil || len(l) < limit {
			return l, err // all of it
		}
		kept := 0
		for i, x := range l {
			if keep != nil && !keep(x) {
				continue
			}
			if kept++; kept == need {
				if i+1 < len(l) && at(l[i+1]).Before(at(x)) {
					return l[:i+1], nil
				}
				break
			}
		}
	}
}

// pageAssets filters, sorts (newest first) and pages assets.
func pageAssets(l []api.AssetJSON, q api.AssetQuery) []api.AssetJSON {
	words := strings.Fields(strings.ToLower(q.Search))
	l = slices.DeleteFunc(l, func(a api.AssetJSON) bool {
		if q.Kind != "" && a.Kind != q.Kind {
			return true
		}
		hay := strings.ToLower(a.Title + " " + a.Detail + " " + a.File)
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return true
			}
		}
		return false
	})
	slices.SortStableFunc(l, func(x, y api.AssetJSON) int {
		if c := y.At.Compare(x.At); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	if q.Offset >= len(l) {
		return []api.AssetJSON{}
	}
	l = l[q.Offset:]
	if q.Limit > 0 && len(l) > q.Limit {
		l = l[:q.Limit]
	}
	return l
}
