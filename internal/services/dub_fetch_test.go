//go:build unix

package services

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// slowYtDlp is a yt-dlp stand-in shaped like the macOS build: asked for
// the info it prints a title; asked to download, a launcher runs a child
// that reports progress for ever (ignoring SIGTERM and broken pipes,
// writing a partial file) and writes its pid to pidFile.
func slowYtDlp(t *testing.T) (path, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "child.pid")
	path = filepath.Join(dir, "yt-dlp")
	script := `#!/bin/sh
for a in "$@"; do
  if [ "$a" = "--dump-single-json" ]; then echo '{"id":"x","title":"A talk about whales"}'; exit 0; fi
done
out=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
d=$(dirname "$out"); mkdir -p "$d"
export d
sh -c 'trap "" TERM PIPE; echo $$ > "` + pidFile + `"; echo ASI 137+140 200000000 "\"A talk about whales\""; n=0; while :; do n=$((n+1000000)); echo partial >> "$d/source.f137.mp4"; echo "ASP downloading $n 150000000 NA 2000000 NA $d/source.f137.mp4"; sleep 0.05; done' &
wait
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, pidFile
}

// A URL download reports its size and progress to its job, names the dub
// after the video before it is fetched, and every way to stop it (the
// dub's Stop, which the card, the footer, the Tasks page and the API's
// group cancel use; or cancelling the job itself, as the API's job cancel
// does) ends the job at once as cancelled, kills yt-dlp with its children
// and removes what was fetched.
func TestDubFetchProgressAndCancel(t *testing.T) {
	for _, how := range []string{"dub", "job"} {
		t.Run(how, func(t *testing.T) {
			e := newDubEnv(t, 2)
			bin, pidFile := slowYtDlp(t)
			e.ytdlp = dub.YtDlp{Path: bin}
			url := "https://203.0.113.9/watch?v=abc" // a literal address: no lookup
			p := e.create(t, func(p *dub.Project) {
				p.Source = dub.Source{Kind: dub.SourceURL, URL: url, Name: url}
				p.Title = url
			})
			ids, err := e.dubs.Run(ctx, p.ID, RunRequest{Through: dub.StageReferences})
			if err != nil {
				t.Fatal(err)
			}
			id := ids["fetch"]
			var j jobs.Job
			deadline := time.Now().Add(10 * time.Second)
			for {
				j, _ = e.sched.Get(ctx, id)
				if j.State == jobs.StateRunning && j.Progress.Fraction > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("no progress: %+v", j)
				}
				time.Sleep(20 * time.Millisecond)
			}
			pr := j.Progress
			if pr.Stage != FetchDownloading || pr.TotalBytes != 200000000 || pr.Bytes <= 0 || pr.ETA <= 0 || pr.Fraction >= 1 {
				t.Errorf("progress %+v", pr)
			}
			if got, _ := e.dubs.Get(ctx, p.ID); got.Title != "A talk about whales" {
				t.Errorf("title %q while downloading", got.Title)
			}
			at := time.Now()
			if how == "dub" {
				err = e.dubs.Cancel(ctx, p.ID)
			} else {
				err = e.sched.Cancel(ctx, id)
			}
			if err != nil {
				t.Fatal(err)
			}
			j = e.wait(t, id)
			if j.State != jobs.StateCancelled {
				t.Errorf("state %s", j.State)
			}
			if d := time.Since(at); d > 1500*time.Millisecond {
				t.Errorf("the job ended %v after the cancel", d)
			}
			b, _ := os.ReadFile(pidFile)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid <= 0 {
				t.Fatal("no child pid")
			}
			for end := time.Now().Add(time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(end) {
					syscall.Kill(pid, syscall.SIGKILL)
					t.Fatal("yt-dlp's child still runs")
				}
			}
			if _, err := os.Stat(filepath.Join(e.dubs.Dir(p.ID), "download")); !os.IsNotExist(err) {
				t.Errorf("the partial download is still there (%v)", err)
			}
			got, _ := e.dubs.Get(ctx, p.ID)
			if st := got.Stage(dub.StageFetch, ""); st.Status == dub.StatusRunning {
				t.Errorf("fetch stage %+v", st)
			}
			for _, other := range ids {
				if o, _ := e.sched.Get(ctx, other); !o.State.Terminal() {
					t.Errorf("%s still %s", o.Kind, o.State)
				}
			}
		})
	}
}
