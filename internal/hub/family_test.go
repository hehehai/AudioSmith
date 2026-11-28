package hub

// The other ActNow apps sharing the model folder (family.go): deleting
// and cleaning up keep their repos, and the family locks (held here by
// another process, as Dicta would) make deletes refuse and downloads wait.

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
)

// noFamily reads no claims (no family directory).
var noFamily = Family{}

const (
	gemma    = "mlx-community/gemma-4-e4b-it-4bit"  // removed from AudioSmith; Dicta's default LLM
	parakeet = "mlx-community/parakeet-tdt-0.6b-v3" // removed; not in the pins table
	dictaID  = "dev.hehehai.dicta"
	testRev  = "0123456789abcdef0123456789abcdef01234567" // fakeRepo's snapshot
)

// storeDecider is the app's Decider without the transition's legacy
// users (internal/family): a models.Store.
type storeDecider struct{ *models.Store }

func (d storeDecider) DeleteModel(ctx context.Context, ref models.Ref, e catalog.Entry, scope models.Scope) (models.Result, error) {
	return d.Store.DeleteModel(ctx, ref, e.ID, scope)
}

// familyFixture is a family directory and model folder (conformance)
// where AudioSmith decides with its claims, and Dicta is installed (a
// manifest) and claims what claim names.
func familyFixture(t *testing.T, claim ...string) (*conformance.Fixture, Family, *models.Store) {
	t.Helper()
	fx := conformance.New(t)
	fx.Register(dictaID)
	dicta := fx.Peer(dictaID)
	for _, repo := range claim {
		fakeRepo(t, fx.Hub, repo, 100)
		if err := dicta.Claim(context.Background(), models.Claim{Repo: repo, Revision: testRev, Aux: true}); err != nil {
			t.Fatal(err)
		}
	}
	me := fx.Peer(BundleID)
	return fx, Family{Decider: storeDecider{me}}, me
}

func TestAppName(t *testing.T) {
	for bid, want := range map[string]string{dictaID: "Dicta", dictaID + ".dev": "Dicta", "com.example.x": "com.example.x", "": ""} {
		if got := AppName(bid); got != want {
			t.Errorf("AppName(%q) = %q", bid, got)
		}
	}
}

// TestCleanUpKeepsFamilyRepos: Clean up leaves the removed models Dicta
// claims, and deletes the others (unclaimed old downloads included);
// once Dicta released it, the next clean-up deletes it too.
func TestCleanUpKeepsFamilyRepos(t *testing.T) {
	fx, fam, _ := familyFixture(t, gemma)
	cache := fx.Hub
	fakeRepo(t, cache, parakeet, 300)
	repos := []string{gemma, parakeet}
	ls, err := Leftovers(cache, repos, fam)
	if err != nil || len(ls) != 2 || !slices.Equal(ls[0].UsedBy, []string{"Dicta"}) || ls[0].Users[0].BundleID != dictaID || ls[1].Kept() {
		t.Fatalf("leftovers %+v %v", ls, err)
	}
	freed, kept, err := RemoveLeftovers(context.Background(), cache, repos, fam)
	if err != nil || freed != 300 || len(kept) != 1 || kept[0].Repo != gemma || !slices.Equal(kept[0].UsedBy, []string{"Dicta"}) {
		t.Fatalf("freed %d, kept %+v, %v", freed, kept, err)
	}
	if _, err := os.Stat(filepath.Join(RepoDir(cache, gemma), "blobs", "abc123")); err != nil {
		t.Errorf("Dicta's model went: %v", err)
	}
	if _, err := os.Stat(RepoDir(cache, parakeet)); !os.IsNotExist(err) {
		t.Errorf("parakeet left: %v", err)
	}
	if _, err := fx.Peer(dictaID).Release(models.Ref{Repo: gemma, Revision: testRev}, ""); err != nil {
		t.Fatal(err)
	}
	freed, kept, err = RemoveLeftovers(context.Background(), cache, repos, fam)
	if err != nil || freed != 100 || len(kept) != 0 {
		t.Fatalf("after Dicta released it: freed %d, kept %+v, %v", freed, kept, err)
	}
	if _, err := os.Stat(RepoDir(cache, gemma)); !os.IsNotExist(err) {
		t.Errorf("gemma left: %v", err)
	}
}

// TestCleanUpPerSnapshot: Clean up deletes snapshot by snapshot, never
// the whole directory: a blob another process is downloading (its lock
// is held) stays, and with it the repo; the next clean-up finishes.
func TestCleanUpPerSnapshot(t *testing.T) {
	cache := t.TempDir()
	fakeRepo(t, cache, parakeet, 300)
	repo := RepoDir(cache, parakeet)
	other := strings.Repeat("e", 40)
	os.MkdirAll(filepath.Join(repo, "snapshots", other), 0o755)
	partial := filepath.Join(repo, "blobs", "bead.incomplete")
	os.WriteFile(partial, make([]byte, 70), 0o644)
	release := holdLock(t, cache, parakeet, "bead", dictaID)
	freed, kept, err := RemoveLeftovers(context.Background(), cache, []string{parakeet}, noFamily)
	if err != nil || freed != 300 || len(kept) != 0 {
		t.Fatalf("freed %d, kept %+v, %v", freed, kept, err)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Errorf("the blob being downloaded went: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(repo, "snapshots")); len(entries) != 0 {
		t.Errorf("snapshots left: %v", entries)
	}
	release()
	if freed, _, err := RemoveLeftovers(context.Background(), cache, []string{parakeet}, noFamily); err != nil || freed != 70 {
		t.Fatalf("second clean-up freed %d, %v", freed, err)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Errorf("repo left: %v", err)
	}
}

// TestRemoveKeepsShared: Models → Delete of a model Dicta claims removes
// it from AudioSmith only: AudioSmith's claim goes, the files stay, Kept
// says who uses them. "Delete for all apps" deletes them and leaves a
// tombstone for Dicta.
func TestRemoveKeepsShared(t *testing.T) {
	e := catalog.Entry{ID: "qwen3-asr", Repo: "mlx-community/Qwen3-ASR-0.6B-4bit", Revision: testRev}
	fx, fam, me := familyFixture(t, e.Repo)
	ctx := context.Background()
	ref := models.Ref{Repo: e.Repo, Revision: testRev}
	if err := me.Claim(ctx, models.Claim{Repo: e.Repo, Revision: testRev, Model: e.ID}); err != nil {
		t.Fatal(err)
	}
	r, err := RemoveFor(ctx, fx.Hub, "darwin", e, fam, models.ThisApp)
	if err != nil || r.Deleted || !slices.Equal(r.Kept, []string{"Dicta"}) || r.Freed != 0 || len(r.Decision.Users) != 1 {
		t.Fatalf("remove %+v %v", r, err)
	}
	if l, _ := StatusFor(fx.Hub, "darwin", e); l.State != Downloaded {
		t.Errorf("after removing from AudioSmith: %+v", l)
	}
	if fx.Claimed(BundleID, ref) {
		t.Error("AudioSmith still claims what it removed")
	}
	r, err = RemoveFor(ctx, fx.Hub, "darwin", e, fam, models.AllApps)
	if err != nil || !r.Deleted || !r.Tombstoned || r.Freed != 100 {
		t.Fatalf("delete for all apps %+v %v", r, err)
	}
	if _, err := os.Stat(RepoDir(fx.Hub, e.Repo)); !os.IsNotExist(err) {
		t.Errorf("repo left: %v", err)
	}
	if hit, err := fx.Peer(dictaID).ApplyTombstones(); err != nil || len(hit) != 1 {
		t.Errorf("Dicta's tombstones %v %v", hit, err)
	}
}

// TestRemoveAux: Delete takes the model's auxiliary repos too, unless
// another of AudioSmith's models or another app uses them.
func TestRemoveAux(t *testing.T) {
	const tok, g2p = "acme/shared-tokenizer", "acme/own-g2p"
	a := catalog.Entry{ID: "a", Repo: "acme/model-a", Revision: testRev, Aux: []catalog.Aux{{Repo: tok, Revision: testRev}, {Repo: g2p, Revision: testRev}}}
	b := catalog.Entry{ID: "b", Repo: "acme/model-b", Revision: testRev, Aux: []catalog.Aux{{Repo: tok, Revision: testRev}}}
	fx, fam, me := familyFixture(t)
	ctx := context.Background()
	for _, r := range []string{a.Repo, b.Repo, tok, g2p} {
		fakeRepo(t, fx.Hub, r, 100)
	}
	for _, e := range []catalog.Entry{a, b} {
		me.Claim(ctx, models.Claim{Repo: e.Repo, Revision: testRev, Model: e.ID})
		for _, x := range e.Aux {
			me.Claim(ctx, models.Claim{Repo: x.Repo, Revision: testRev, Model: e.ID, Aux: true})
		}
	}
	r, err := RemoveFor(ctx, fx.Hub, "darwin", a, fam, models.ThisApp)
	if err != nil || !r.Deleted || r.Freed != 200 {
		t.Fatalf("remove a: %+v %v", r, err)
	}
	for repo, want := range map[string]bool{a.Repo: false, g2p: false, tok: true, b.Repo: true} {
		if _, err := os.Stat(RepoDir(fx.Hub, repo)); (err == nil) != want {
			t.Errorf("%s there: %v, want %v", repo, err == nil, want)
		}
	}
	if c, ok := fx.Claims(BundleID).Find(models.Ref{Repo: tok, Revision: testRev}); !ok || !slices.Equal(c.Users(), []string{"b"}) {
		t.Errorf("the shared tokenizer's claim %+v", c)
	}
}

// TestDeleteRefusedWhileLocked: a repo another process holds (Dicta
// downloads it) is neither deleted nor cleaned up; it says who.
func TestDeleteRefusedWhileLocked(t *testing.T) {
	for name, fam := range map[string]Family{"no family": noFamily, "claims": {}} {
		t.Run(name, func(t *testing.T) {
			fx := conformance.New(t)
			cache := fx.Hub
			if name == "claims" {
				fam = Family{Decider: storeDecider{fx.Peer(BundleID)}}
			}
			e := catalog.Entry{Repo: parakeet, Revision: testRev}
			fakeRepo(t, cache, parakeet, 100)
			release := holdLock(t, cache, parakeet, "", dictaID)
			ctx := context.Background()
			_, err := RemoveFor(ctx, cache, "darwin", e, fam, models.ThisApp)
			if name, busy := Holder(err); !busy || name != "Dicta" {
				t.Fatalf("remove while locked: %v (%q %v)", err, name, busy)
			}
			freed, kept, err := RemoveLeftovers(ctx, cache, []string{parakeet}, fam)
			if err != nil || freed != 0 || len(kept) != 1 || !kept[0].Busy || kept[0].BusyBy != "Dicta" {
				t.Fatalf("clean up while locked: freed %d, kept %+v, %v", freed, kept, err)
			}
			if l, _ := StatusFor(cache, "darwin", e); l.State != Downloaded {
				t.Errorf("files went: %+v", l)
			}
			release()
			if r, err := RemoveFor(ctx, cache, "darwin", e, fam, models.ThisApp); err != nil || r.Freed != 100 {
				t.Fatalf("after release %+v %v", r, err)
			}
		})
	}
}

// TestDownloadWaitsForAnotherApp: a file Dicta is downloading (it holds
// the blob's lock) is not fetched again: the download says it waits for
// Dicta, and takes the blob when Dicta finished it, or resumes Dicta's
// partial file when Dicta stopped. Finishing the snapshot waits for the
// repo's other holders.
func TestDownloadWaitsForAnotherApp(t *testing.T) {
	for _, finish := range []bool{true, false} {
		h := newFakeHub(t)
		c := h.client(t, func(o *Options) { o.Family = noFamily })
		big := h.files["model.safetensors"]
		blob := sha(big)
		blobs := filepath.Join(RepoDir(c.opts.CacheDir, testRepo), "blobs")
		os.MkdirAll(blobs, 0o755)
		os.WriteFile(filepath.Join(blobs, blob+".incomplete"), big[:4096], 0o644) // four chunks
		release := holdLock(t, c.opts.CacheDir, testRepo, blob, dictaID)

		var mu sync.Mutex
		var waitingFor []string
		waiting := make(chan struct{})
		var once sync.Once
		done := make(chan error, 1)
		go func() {
			done <- c.Download(context.Background(), h.entry(), func(p Progress) {
				if p.Waiting {
					mu.Lock()
					waitingFor = append(waitingFor, p.WaitingFor)
					mu.Unlock()
					once.Do(func() { close(waiting) })
				}
			})
		}()
		select {
		case <-waiting:
		case err := <-done:
			t.Fatalf("finish %v: the download did not wait: %v", finish, err)
		case <-time.After(20 * time.Second):
			t.Fatal("no waiting progress")
		}
		if finish {
			os.WriteFile(filepath.Join(blobs, blob), big, 0o644)
			os.Remove(filepath.Join(blobs, blob+".incomplete"))
		}
		release()
		if err := <-done; err != nil {
			t.Fatalf("finish %v: %v", finish, err)
		}
		checkSnapshot(t, c, h)
		mu.Lock()
		if !slices.Contains(waitingFor, "Dicta") {
			t.Errorf("waiting for %q", waitingFor)
		}
		mu.Unlock()
		var others int64
		for path, b := range h.files {
			if path != "model.safetensors" {
				others += int64(len(b))
			}
		}
		want := others
		if !finish {
			want += int64(len(big)) - 4096 // only what Dicta had not written
		}
		if got := h.count("served"); got != want {
			t.Errorf("finish %v: served %d bytes, want %d", finish, got, want)
		}
	}

	// The repo's lock, shared by another app's download: the blobs come,
	// the snapshot waits for it.
	h := newFakeHub(t)
	c := h.client(t, func(o *Options) { o.Family = noFamily })
	release := holdLock(t, c.opts.CacheDir, testRepo, "", dictaID)
	done := make(chan error, 1)
	go func() { done <- c.Download(context.Background(), h.entry(), nil) }()
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("finished while another app held the repo: %v", err)
	default:
	}
	if l, _ := c.Status(context.Background(), h.entry()); l.State == Downloaded {
		t.Error("snapshot linked while another app held the repo")
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, c, h)
}

// holdLock has another process (this test binary) hold a family lock in
// cache as bundleID would: repo's blob lock when blob is set, else the
// repo's lock, shared (a download). release ends it.
func holdLock(t *testing.T, cache, repo, blob, bundleID string) (release func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldLock$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HUB_HOLD_LOCK="+strings.Join([]string{cache, repo, blob, bundleID}, "|"))
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "held" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("lock helper: %q", line)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			stdin.Close()
			cmd.Wait()
		})
	}
	t.Cleanup(release)
	return release
}

// TestHelperHoldLock is holdLock's other process.
func TestHelperHoldLock(t *testing.T) {
	spec := os.Getenv("HUB_HOLD_LOCK")
	if spec == "" {
		t.Skip("the lock helper of holdLock")
	}
	f := strings.Split(spec, "|")
	cache, repo, blob, bid := f[0], f[1], f[2], f[3]
	who := models.Owner{BundleID: bid, Op: models.OpDownload}
	var l *models.HeldLock
	var err error
	if blob != "" {
		l, err = models.TryLockBlob(cache, repo, blob, who)
	} else {
		l, err = models.TryLockRepo(cache, repo, family.Shared, who)
	}
	if err != nil {
		os.Stdout.WriteString("error " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("held\n")
	io.Copy(io.Discard, os.Stdin)
	l.Release()
	os.Exit(0)
}
