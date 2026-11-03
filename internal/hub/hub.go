// Package hub owns model files on disk: it finds the Hugging Face cache,
// reports which catalogue entries are downloaded, and (thread C, M0)
// downloads, verifies and deletes them, so engines only ever load local
// files (engine.hello offline:true).
//
// Files go in the huggingface_hub cache layout, shared with the Python
// tools and swift-huggingface:
//
//	<cache>/models--<owner>--<name>/blobs/<sha256 or etag>
//	<cache>/models--<owner>--<name>/snapshots/<revision>/<path> → ../../blobs/…
//	<cache>/models--<owner>--<name>/refs/main  (the revision "main" points at)
//
// Client (client.go) implements Downloader over the HF HTTP API:
//   - Plan asks /api/models/<repo>/revision/<rev>?blobs=true for the commit
//     and its files, racing the endpoints (huggingface.co, hf-mirror.com)
//     and reusing the winner for a day; gated repos go to the official
//     endpoint only, and only it ever sees the token (token.go: $HF_TOKEN,
//     the Keychain, huggingface-cli's file). It refuses when the disk
//     would keep less than ReserveBytes free, counting what the other
//     downloads under way still write (client_reserve.go).
//   - Download fetches files Parallel at a time, big ones in ChunkSize
//     ranges, into blobs/<name>.incomplete (+ .parts recording finished
//     ranges), resuming what is there (also huggingface_hub's sequential
//     partials), retrying dropped connections from where they stopped;
//     checks sha256 (LFS) or the git blob id (others); links the snapshot
//     beside its place and renames it in; writes refs/<branch> last.
//
// Imports: catalog (events satisfy core.Event structurally). Downloads run
// as jobs.LaneNet jobs of kind "model.download"; the Downloader does the
// work for their executor.
package hub

import (
	"context"

	"net/http"
	"path/filepath"
	"strings"

	"github.com/hehehai/actnowkit/models"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

// CacheDir is where models live by default: the ActNow family's model
// library, ~/Library/Application Support/ActNow/models/hub (family spec
// v1.0.9, actnowkit models.DefaultHub; it follows ACTNOW_FAMILY_DIR).
// $HF_HUB_CACHE, $HF_HOME and ~/.cache/huggingface/hub no longer choose
// it: models there are imported (internal/family Import). Settings may
// override it (settings.Models.CacheDir).
func CacheDir() string { return models.DefaultHub() }

// RepoDir is the cache directory of a repo id ("owner/name").
func RepoDir(cacheDir, repo string) string {
	return filepath.Join(cacheDir, "models--"+strings.ReplaceAll(repo, "/", "--"))
}

// LocalState is how much of an entry is on disk.
type LocalState string

const (
	Missing    LocalState = "missing"    // nothing
	Partial    LocalState = "partial"    // some files, or interrupted downloads (resumable)
	Downloaded LocalState = "downloaded" // every file present (and verified, when hashes are known)
)

// Local describes an entry on disk.
type Local struct {
	State    LocalState
	Snapshot string // snapshots/<revision> directory, for proto.LoadParams.Path; "" when Missing
	Bytes    int64  // on disk (blobs this entry references)
	Missing  []string
}

// Progress of a download. Published as DownloadProgress and reported as
// jobs.Progress by the download job.
type Progress struct {
	Repo       string
	File       string // current file
	Bytes      int64  // done, all files
	TotalBytes int64
	Mirror     string // endpoint in use (after racing)
	// WaitingFor is the app (a name; "" when unknown) downloading a
	// file this download needs, when it waits for it (Waiting).
	WaitingFor string
	Waiting    bool
}

// DownloadProgress is published on the core.Bus while downloading (at
// most ~4×/s per entry). Topic "model.download".
type DownloadProgress struct{ Progress Progress }

func (DownloadProgress) Topic() string { return "model.download" }

// Options configure a Downloader.
type Options struct {
	CacheDir string
	// AlsoIn are folders Status also finds models in, after CacheDir (an
	// unfinished move's target, #101 M2). Downloads go to CacheDir.
	AlsoIn []string
	// Endpoints are raced on the first request; the fastest healthy one
	// wins ("https://huggingface.co", "https://hf-mirror.com"). Empty =
	// $HF_ENDPOINT, else DefaultEndpoints.
	Endpoints []string
	// Official is the endpoint trusted with the token: gated repos
	// download only from it, and mirrors never see the token. "" =
	// $HF_ENDPOINT, else "https://huggingface.co".
	Official string
	// Token returns the HF token (from the Keychain; $HF_TOKEN in dev) for
	// gated repos; nil or "" = anonymous. See Token.
	Token func() string
	// Parallel is the number of files (and ranges of big files) fetched at
	// once. 0 = 4.
	Parallel int
	// ChunkSize is the size of the ranges big files are fetched in, in
	// parallel; files under two chunks download whole. 0 = 16 MiB.
	ChunkSize int64
	// ReserveBytes is the free space a download must leave on the cache
	// volume, after it and the other downloads under way are written.
	// 0 = DefaultReserve; negative = no check.
	ReserveBytes int64
	// Proxy is "" (automatic: environment, then the macOS system proxy),
	// "direct", or a proxy URL; see proxy.go.
	Proxy string
	// HTTPClient makes the requests; nil = a client with timeouts and
	// Proxy.
	HTTPClient *http.Client
	// Family is how downloads and deletes deal with the other ActNow apps
	// sharing the folder (family.go): the lock owner files name this app,
	// and deletes keep what other apps' claims say they use (Decider).
	// The zero value reads no claims.
	Family Family
	// Claimed, when set, is called for each repo a download finished
	// (the entry's auxiliary repos, then its own), holding the repo's
	// lock exclusively, right after its ref is written: the app claims it
	// for the ActNow family (spec §7.3, models.Store.ClaimLocked). Its
	// error is logged; the download stands.
	Claimed func(ctx context.Context, cacheDir string, d Finished) error
	// Backup reports the family's models.backup when a download finishes:
	// false (and nil) leaves the repo out of Time Machine (spec §7.12).
	Backup func() bool
}

// Finished is a repo a download finished, for Options.Claimed.
type Finished struct {
	// Entry is the entry downloaded (for an auxiliary repo, the entry
	// it belongs to: Aux).
	Entry  catalog.Entry
	Repo   string
	Commit string
	// Files are the files the entry lists for the repo; nil = the whole
	// snapshot.
	Files []string
	Aux   bool
}

// Downloader manages model files. Goroutine-safe; methods may block on I/O.
type Downloader interface {
	// Status reports what of e is on disk. Cheap enough for a page load,
	// but still I/O: not from a build pass.
	Status(ctx context.Context, e catalog.Entry) (Local, error)
	// Plan lists the files to fetch (from the HF API when e.Files is
	// empty) and checks free disk space; it errors before downloading
	// anything when space is short or the repo is gated without a token.
	Plan(ctx context.Context, e catalog.Entry) (Plan, error)
	// Download fetches what Plan lists, resuming partial files, verifying
	// sha256 of LFS files, and writing refs/ last so a snapshot is never
	// half there. progress is called often; ctx cancels (partial files
	// stay for resuming).
	Download(ctx context.Context, e catalog.Entry, progress func(Progress)) error
	// Verify re-hashes the files and returns the bad or missing ones,
	// removing bad blobs so that a later Download repairs them. It needs
	// the HF API when e lists no files with hashes.
	Verify(ctx context.Context, e catalog.Entry) ([]string, error)
	// Delete removes the entry from AudioSmith: its snapshot and the
	// blobs no other snapshot uses go unless another family app uses
	// them (Client.Remove).
	Delete(ctx context.Context, e catalog.Entry) error
}

// Plan is what a download will do.
type Plan struct {
	Revision   string // resolved commit
	Files      []catalog.File
	TotalBytes int64 // every file
	Bytes      int64 // still to download
	FreeBytes  int64 // on the cache volume; -1 when unknown
	Endpoint   string
	Gated      bool
}
