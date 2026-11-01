package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// Red wins over violet; no dot when nothing goes on.
func TestTrayDotOf(t *testing.T) {
	for _, c := range []struct {
		busy, agent bool
		want        trayDot
		color       string
	}{
		{false, false, trayDotNone, ""},
		{false, true, trayDotAgent, trayViolet},
		{true, false, trayDotBusy, trayRed},
		{true, true, trayDotBusy, trayRed},
	} {
		d := trayDotOf(c.busy, c.agent)
		if d != c.want || d.color() != c.color {
			t.Errorf("busy %v agent %v: %v %q, want %v %q", c.busy, c.agent, d, d.color(), c.want, c.color)
		}
	}
}

// Running jobs that make something turn the dot red; downloads, moving
// the models and loading one do not; nor do queued jobs.
func TestTrayDotJobs(t *testing.T) {
	a, _, _ := newTestApp(t)
	clear(a.jobList)
	a.updateTray()
	if a.dictTray.dot != trayDotNone {
		t.Fatalf("dot %v with no jobs", a.dictTray.dot)
	}
	job := func(id, kind string, st jobs.State) {
		a.jobsChanged(jobs.Job{ID: id, Spec: jobs.Spec{Kind: kind}, State: st})
	}
	job("d", hub.KindDownload, jobs.StateRunning)
	job("m", kindMoveModels, jobs.StateRunning)
	job("l", proto.MethodModelsLoad, jobs.StateRunning)
	job("q", services.KindRender, jobs.StateQueued)
	if a.dictTray.dot != trayDotNone {
		t.Errorf("dot %v for downloads, loads and queued work", a.dictTray.dot)
	}
	job("r", services.KindRender, jobs.StateRunning)
	if a.dictTray.dot != trayDotBusy || a.dictTray.tip == "AudioSmith" {
		t.Errorf("rendering: dot %v tip %q", a.dictTray.dot, a.dictTray.tip)
	}
	job("r", services.KindRender, jobs.StateSucceeded)
	if a.dictTray.dot != trayDotNone {
		t.Errorf("dot %v after the render", a.dictTray.dot)
	}
	a.llmCalls.Add(1) // the engine's language model writes
	a.updateTray()
	if a.dictTray.dot != trayDotBusy {
		t.Errorf("dot %v while the language model writes", a.dictTray.dot)
	}
	a.llmCalls.Add(-1)
	a.updateTray()
	if a.dictTray.dot != trayDotNone {
		t.Errorf("dot %v after it", a.dictTray.dot)
	}
}

// An MCP client turns the dot violet until it disconnects.
func TestTrayDotAgent(t *testing.T) {
	useMemKeys(t)
	a, _, _ := newTestApp(t)
	clear(a.jobList)
	q := make(chan func(), 64)
	a.postQueue = func(fn func()) { q <- fn }
	a.startAPI(t.TempDir())
	t.Cleanup(a.stopAPI)
	ts := httptest.NewServer(a.apiSrv.Handler())
	t.Cleanup(ts.Close)
	wait := func(what string, want trayDot) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			select {
			case fn := <-q:
				fn()
			case <-time.After(10 * time.Millisecond):
			}
			if a.dictTray.dot == want {
				return
			}
		}
		t.Fatalf("%s: dot %v, want %v", what, a.dictTray.dot, want)
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "claude-code", Version: "1.0"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wait("connected", trayDotAgent)
	a.jobsChanged(jobs.Job{ID: "r", Spec: jobs.Spec{Kind: services.KindRender}, State: jobs.StateRunning})
	if a.dictTray.dot != trayDotBusy {
		t.Errorf("generating with an agent: dot %v", a.dictTray.dot)
	}
	a.jobsChanged(jobs.Job{ID: "r", Spec: jobs.Spec{Kind: services.KindRender}, State: jobs.StateSucceeded})
	if a.dictTray.dot != trayDotAgent {
		t.Errorf("after generating with an agent: dot %v", a.dictTray.dot)
	}
	cs.Close()
	wait("disconnected", trayDotNone)
}
