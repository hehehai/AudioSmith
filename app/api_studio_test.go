package main

// Tests of the app's modules for agents (thread #35): every REST route
// and MCP tool of api_studio*.go over a real library, job queue and
// services, with dubtest's fake engine behind speech, dubbing and
// workflows. This file is the harness (studioEnv); the tests are in
// api_studio_longform_test.go, api_studio_dub_test.go and
// api_studio_more_test.go.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/dub/dubtest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	mcpsrv "github.com/hehehai/AudioSmith/internal/mcp"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// studioEnv is the app over a real library and job queue, its API and
// MCP server, and an MCP client.
type studioEnv struct {
	a    *app
	lib  *library
	fe   *dubtest.Engine
	b    *apiBackend
	srv  *api.Server
	ts   *httptest.Server
	cs   *sdk.ClientSession
	out  string // the agents' folder
	gate *gatedSynth
}

// gatedSynth holds speech while closed (progress and cancel tests).
type gatedSynth struct {
	ex   jobs.Executor
	open chan struct{}
	hold bool
}

func (g *gatedSynth) Run(ctx context.Context, job jobs.Job, report func(jobs.Progress), chunk func(json.RawMessage)) (json.RawMessage, error) {
	if g.hold {
		select {
		case <-g.open:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.ex.Run(ctx, job, report, chunk)
}

func newStudioEnv(t *testing.T) *studioEnv {
	t.Helper()
	return newStudioEnvWith(t, libraryOptions{})
}

// newStudioEnvWith: newStudioEnv over a library opened with o.
func newStudioEnvWith(t *testing.T, o libraryOptions) *studioEnv {
	t.Helper()
	saved := llmSecrets
	llmSecrets = llm.StaticSecrets{}
	t.Cleanup(func() { llmSecrets = saved })
	savedTr := dubMakeTranslator
	dubMakeTranslator = func(*llm.Router, dub.Settings) (translate.Translator, error) { return &dubFakeTranslator{}, nil }
	t.Cleanup(func() { dubMakeTranslator = savedTr })

	dir := t.TempDir()
	lib := openTestLibraryWith(t, dir, o)
	useAllModelFiles(t, useLocalModels(t, lib))
	out := t.TempDir()
	if err := lib.settings.Update(context.Background(), func(s *settings.Settings) { s.API.MCPDir = out }); err != nil {
		t.Fatal(err)
	}
	eng := newFakeEngine()
	a := newApp(eng.host, lib)
	eng.a = a
	// The main thread: one goroutine runs what the app posts.
	q := make(chan func(), 1<<14)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case fn := <-q:
				fn()
			case <-stop:
				return
			}
		}
	}()
	a.postQueue = func(fn func()) { q <- fn }
	a.enginePost = a.postQueue
	a.connectEngine(eng.host)
	fe := dubtest.New()
	fe.Script = []string{"Hello there.", "How are you today?", "I am fine, thank you.", "See you tomorrow."}
	a.dubEngine, a.wfEngine = fe.Executor, fe.Executor
	g := &gatedSynth{ex: fe.Executor(proto.MethodTTSSynthesize), open: make(chan struct{})}
	lib.useEngine(func() jobs.Executor { return g })
	if err := lib.sched.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := a.newAPIBackend(dir)
	b.sup = &apiTestSup{}
	srv := api.New(api.Options{Backend: b, Sched: lib.sched, Bus: lib.bus, Version: "test", Logf: t.Logf})
	srv.MountMCP(mcpsrv.New(mcpsrv.Options{API: srv, Backend: b}).Handler())
	ts := httptest.NewServer(srv.Handler())
	c := sdk.NewClient(&sdk.Implementation{Name: "studio-test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: http.DefaultClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ts.Close()
		srv.Close(context.Background())
		if b.unsub != nil {
			b.unsub()
		}
		lib.sched.Close()
		close(stop)
	})
	return &studioEnv{a: a, lib: lib, fe: fe, b: b, srv: srv, ts: ts, cs: cs, out: out, gate: g}
}

// onMain runs fn on the app's main thread (the app's state is its own)
// and waits for it.
func (e *studioEnv) onMain(fn func()) {
	done := make(chan struct{})
	e.a.postQueue(func() { defer close(done); fn() })
	<-done
}

// call calls a tool and decodes its JSON answer into v (nil: ignored);
// it returns the error text, "" on success.
func (e *studioEnv) call(t *testing.T, name string, args map[string]any, v any) string {
	t.Helper()
	res, err := e.cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return b.String()
	}
	if v != nil {
		if err := json.Unmarshal([]byte(b.String()), v); err != nil {
			t.Fatalf("%s: %v in %s", name, err, b.String())
		}
	}
	return ""
}

// must calls a tool that must succeed.
func (e *studioEnv) must(t *testing.T, name string, args map[string]any, v any) {
	t.Helper()
	if msg := e.call(t, name, args, v); msg != "" {
		t.Fatalf("%s %v: %s", name, args, msg)
	}
}

// fails calls a tool that must fail with code.
func (e *studioEnv) fails(t *testing.T, name string, args map[string]any, code string) {
	t.Helper()
	if msg := e.call(t, name, args, nil); !strings.HasPrefix(msg, code+":") {
		t.Fatalf("%s %v: want %s, got %q", name, args, code, msg)
	}
}

// rest calls the REST API: status and body.
func (e *studioEnv) rest(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.HeaderCSRF, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// jobAnswer is get_job's answer.
type jobAnswer struct {
	JobID    string          `json:"job_id"`
	Kind     string          `json:"kind"`
	State    string          `json:"state"`
	Done     bool            `json:"done"`
	Stage    string          `json:"stage"`
	Progress *float64        `json:"progress"`
	Error    *Code           `json:"error"`
	Files    []string        `json:"files"`
	Result   json.RawMessage `json:"result"`
	Jobs     []string        `json:"jobs"`
}

// Code is a job error's code.
type Code struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// wait follows a job to its end with get_job's long poll.
func (e *studioEnv) wait(t *testing.T, id string) jobAnswer {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var j jobAnswer
		e.must(t, "get_job", map[string]any{"job_id": id, "wait_seconds": 5}, &j)
		if j.Done {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not end: %+v", id, j)
		}
	}
}

// writeEPUB writes a small EPUB 2 with two chapters.
func writeEPUB(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	page := func(title, body string) string {
		return `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + title + `</title></head><body>` + body + `</body></html>`
	}
	for _, f := range [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"content.opf", `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>The Lighthouse</dc:title><dc:creator>Ann Writer</dc:creator><dc:language>en</dc:language></metadata>
<manifest><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/><item id="a" href="a.html" media-type="application/xhtml+xml"/><item id="b" href="b.html" media-type="application/xhtml+xml"/></manifest>
<spine toc="ncx"><itemref idref="a"/><itemref idref="b"/></spine></package>`},
		{"toc.ncx", `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap><navPoint id="p1"><navLabel><text>Arrival</text></navLabel><content src="a.html"/></navPoint><navPoint id="p2"><navLabel><text>Night</text></navLabel><content src="b.html"/></navPoint></navMap></ncx>`},
		{"a.html", page("a", `<h1>Arrival</h1><p>The keeper climbed the stairs.</p>`)},
		{"b.html", page("b", `<h1>Night</h1><p>The light turned all night.</p>`)},
	} {
		fw, _ := w.Create(f[0])
		io.WriteString(fw, f[1])
	}
	w.Close()
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
