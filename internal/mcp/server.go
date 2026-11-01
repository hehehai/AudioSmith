// Package mcp is AudioSmith's Model Context Protocol server (M5): agents
// (Claude Code, Cursor, Codex…) speak, clone and design voices, and
// transcribe through it. It runs inside the Local API (internal/api),
// mounted at /mcp over Streamable HTTP, and reaches stdio-only clients
// through the shim of stdio.go (AudioSmith --mcp-stdio).
//
// Tools: generate_speech, clone_voice, design_voice, describe_voice,
// transcribe, list_voices, list_models, list_languages, check_health;
// and over the app's modules (studio.go, thread #35) jobs, the project
// library and History, Stories and Audiobook, dubbing and batch, voice
// discovery, exports and workflows.
// Resources: voice://{id}, history://recent.
//
// Every tool goes through api.Server's operations, so the queue, the
// licence gate and the engine checks apply as for HTTP clients. Each
// agent may have its own voice (settings.API.Bindings), found by the
// X-AudioSmith-Client header or, failing that, the client's MCP name.
// Audio comes back inline (base64 audio content), as a file in the
// sandbox folder, or both (settings.API.MCPOutput, or the call's output
// argument). Files are read by path only for clients on this Mac (or with
// the API key); LAN clients with the PIN send base64.
//
// Imports: api, core, settings, voicedesign, and the official MCP Go SDK.
package mcp

// The server: options, clients and callers, the sandbox and input files,
// tool results, and the resources. Files: server.go, server_tools.go (the
// core tools), studio.go (the app's modules), stdio.go (the shim).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/oplog"
)

// Options configure a Server.
type Options struct {
	API     *api.Server
	Backend api.Backend
	Version string
}

// Server is the MCP server.
type Server struct {
	o       Options
	srv     *sdk.Server
	handler http.Handler

	mu      sync.Mutex
	clients map[string]*Client

	sessions *sessions // connected agents (sessions.go)
}

// Client is an agent that called the server, for Settings → Local API's
// voice bindings.
type Client struct {
	ID       string    // its client id (header) or MCP name
	Name     string    // its MCP name and version, as it introduced itself
	LastSeen time.Time // its last call
	Calls    int
}

// Instructions tell agents how to use the server: the initialize
// result carries them, and clients like Claude Code show them to the
// model in every new chat. They are a compact form of the agent skill
// the Integrations page makes (integrations_skill.go), so that an agent
// without the skill knows the basics; TestInstructions keeps the tools
// and arguments they name real.
const Instructions = "AudioSmith is a local speech app on this Mac (MLX; nothing leaves the Mac): text to speech, voices, " +
	"transcription, audiobooks, stories, dubbing, voice isolation, exports, workflows.\n" +
	"- generate_speech: text to a WAV; voice = a saved voice (list_voices), a built-in speaker (list_models) or empty; " +
	"output \"file\" (+ filename) writes to the agents' folder, \"inline\" returns base64; instructions = delivery.\n" +
	"- transcribe: path (absolute), timestamps: true for segments, speakers: true for who says what; export_transcript.\n" +
	"- Voices: design_voice_candidates then design_voice (description), describe_voice, check_reference then clone_voice " +
	"(name, path; consent: true only after the user confirms the speaker agreed), search_voices, preview_voice, use_gallery_voice, " +
	"update_voice, duplicate_voice, export_voice, import_voice, convert_voice.\n" +
	"- Stories/books: write_story, create_story (text), create_audiobook (path), get_longform, update_longform (voice, cast, format, " +
	"scene, directions, direct), render_longform, export_longform.\n" +
	"- Dubbing: create_dub (path or url, target_languages), get_dub, edit_dub, run_dub, export_dub, batch_dub, list_batch.\n" +
	"- isolate_voice then get_isolation (stems vocals, background, clean).\n" +
	"- Library: list_projects, list_assets, list_takes, export_audio (format, loudness), list_samples, open_sample, " +
	"list_workflows, run_workflow.\n" +
	"- Tools: probe_media, detect_watermark, direct_performance, check_line_fit, get_settings, list_models, list_languages, check_health.\n" +
	"- Long work returns job_id: get_job with wait_seconds (≤ 60) until done, then report its files; cancel_job stops it.\n" +
	"- delete_item and download_model need confirm: true, only after you asked and the user said yes (\"delete X\" is not yet a yes: say what goes or the size, wait).\n" +
	"Error codes: licence_required or model_not_downloaded → the user fixes it in AudioSmith → Models (or download_model " +
	"with their OK); engine_unavailable, queue_timeout, busy → wait, retry; needs_reference → a cloned voice or another model; " +
	"confirm_required → ask the user. Never change settings or accept licences. Use only 127.0.0.1; report the paths of files you make. " +
	"Resources: voice://{id}, history://recent."

// New makes the MCP server.
func New(o Options) *Server {
	s := &Server{o: o, clients: map[string]*Client{}, sessions: newSessions()}
	s.srv = sdk.NewServer(&sdk.Implementation{Name: "audiosmith", Title: "AudioSmith", Version: o.Version},
		&sdk.ServerOptions{Instructions: Instructions, InitializedHandler: s.sessionInitialized})
	s.addTools()
	s.addResources()
	s.srv.AddReceivingMiddleware(logTools, annotateTools)
	s.handler = s.sessions.wrap(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s.srv },
		&sdk.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute, MaxRequestBodyBytes: 64 << 20}))
	return s
}

// sessionInitialized opens a session for AgentConnected, until the SDK
// closes it.
func (s *Server) sessionInitialized(_ context.Context, req *sdk.InitializedRequest) {
	ss := req.Session
	if ss == nil || ss.ID() == "" {
		return
	}
	id := ss.ID()
	s.sessions.opened(id)
	go func() {
		ss.Wait()
		s.sessions.closed(id)
	}()
}

// Handler serves MCP's Streamable HTTP (mounted at /mcp by the API).
func (s *Server) Handler() http.Handler { return s.handler }

// Clients lists the agents seen since the app started, most recent first.
func (s *Server) Clients() []Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b Client) int { return b.LastSeen.Compare(a.LastSeen) })
	return out
}

// caller is who calls a tool.
type caller struct {
	id  string // binding key: the header, else the MCP client name
	via string // api.Via*
}

func (s *Server) caller(req *sdk.CallToolRequest) caller {
	var c caller
	var h http.Header
	if req.Extra != nil {
		h = req.Extra.Header
	}
	c.via = h.Get(api.HeaderVia)
	c.id = strings.TrimSpace(h.Get(api.HeaderClient))
	name := ""
	if req.Session != nil {
		if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
			name = strings.TrimSpace(p.ClientInfo.Name)
			if p.ClientInfo.Version != "" {
				name += " " + p.ClientInfo.Version
			}
			if c.id == "" {
				c.id = strings.TrimSpace(p.ClientInfo.Name)
			}
		}
	}
	if c.id != "" {
		s.mu.Lock()
		cl := s.clients[strings.ToLower(c.id)]
		if cl == nil {
			cl = &Client{ID: c.id}
			s.clients[strings.ToLower(c.id)] = cl
		}
		cl.Name, cl.LastSeen = name, time.Now()
		cl.Calls++
		s.mu.Unlock()
	}
	return c
}

// trusted callers may name files anywhere on this Mac.
func (c caller) trusted() bool {
	return c.via == api.ViaLoopback || c.via == api.ViaLANKey || c.via == ""
}

// sandbox is the folder tools write audio to.
func (s *Server) sandbox() string {
	if s.o.API != nil {
		return s.o.API.OutputDir()
	}
	if d := strings.TrimSpace(s.o.Backend.Settings().API.MCPDir); d != "" {
		return d
	}
	return filepath.Join(s.o.Backend.DataDir(), "mcp")
}

// Sandbox is the folder MCP tools write to (for the settings page).
func (s *Server) Sandbox() string { return s.sandbox() }

// readable resolves a path argument: clients on this Mac (or with the
// key) may name any file; LAN clients only files in the sandbox.
func (s *Server) readable(c caller, path string) (string, error) {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid_request: the path must be absolute (%s)", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("invalid_request: cannot read %s: %v", path, err)
	}
	if c.trusted() {
		return real, nil
	}
	box, err := filepath.EvalSymlinks(s.sandbox())
	if err != nil {
		return "", fmt.Errorf("%s: LAN clients may only read files in AudioSmith's MCP folder; send the audio as base64", api.CodePathNotAllowed)
	}
	if rel, err := filepath.Rel(box, real); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%s: LAN clients may only read files in AudioSmith's MCP folder; send the audio as base64", api.CodePathNotAllowed)
	}
	return real, nil
}

// inputFile is a tool's audio input: a path, or base64 written to a
// temporary file (remove it with the returned func).
func (s *Server) inputFile(c caller, path, b64, name string) (string, func(), error) {
	switch {
	case path != "" && b64 != "":
		return "", nil, errors.New("invalid_request: give path or audio_base64, not both")
	case path != "":
		p, err := s.readable(c, path)
		return p, func() {}, err
	case b64 != "":
		if i := strings.Index(b64, ","); strings.HasPrefix(b64, "data:") && i > 0 {
			b64 = b64[i+1:]
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return "", nil, fmt.Errorf("invalid_request: audio_base64 is not base64: %v", err)
		}
		dir := filepath.Join(s.o.Backend.TempDir(), "mcp")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", nil, err
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == "" || len(ext) > 6 {
			ext = ".wav"
		}
		f, err := os.CreateTemp(dir, "input-*"+ext)
		if err != nil {
			return "", nil, err
		}
		_, err = f.Write(data)
		f.Close()
		if err != nil {
			os.Remove(f.Name())
			return "", nil, err
		}
		return f.Name(), func() { os.Remove(f.Name()) }, nil
	}
	return "", nil, errors.New("invalid_request: give the audio as path or audio_base64")
}

// toolError turns an API error into a tool error message with its code.
func toolError(err error) error {
	var e *api.Error
	if errors.As(err, &e) {
		return fmt.Errorf("%s: %s", e.Code, e.Message)
	}
	var lic *core.LicenceRequiredError
	if errors.As(err, &lic) {
		return fmt.Errorf("licence_required: the %s licence of %s must be accepted in AudioSmith → Models first", lic.Name, lic.Model)
	}
	if errors.Is(err, core.ErrNotDownloaded) {
		return fmt.Errorf("model_not_downloaded: %v (download it in AudioSmith → Models)", err)
	}
	// The services' sentinels (not found, invalid, busy…) get their code.
	if e := api.ErrorOf(err); e.Code != "internal" {
		return fmt.Errorf("%s: %s", e.Code, e.Message)
	}
	return err
}

// result is a tool result: its JSON as text and as structured content,
// after any other content.
func result(v any, extra ...sdk.Content) (*sdk.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	content := append(extra, &sdk.TextContent{Text: string(data)})
	return &sdk.CallToolResult{Content: content, StructuredContent: json.RawMessage(data)}, nil, nil
}

// maxInline bounds audio returned inline; longer takes come as files.
const maxInline = 20 << 20

// --- resources -------------------------------------------------------------------

func (s *Server) addResources() {
	s.srv.AddResourceTemplate(&sdk.ResourceTemplate{Name: "voice", Title: "A saved voice", URITemplate: "voice://{id}",
		Description: "A saved voice's profile (JSON).", MIMEType: "application/json"},
		func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			uri := req.Params.URI
			id := strings.TrimPrefix(uri, "voice://")
			v, err := s.o.Backend.Voice(ctx, id)
			if err != nil {
				return nil, sdk.ResourceNotFoundError(uri)
			}
			data, _ := json.MarshalIndent(voiceOut(v), "", "  ")
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}}}, nil
		})
	s.srv.AddResource(&sdk.Resource{Name: "history", Title: "Recent takes", URI: "history://recent",
		Description: "The 20 most recent takes of the History (JSON).", MIMEType: "application/json"},
		func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
			ts, err := s.o.Backend.Takes(ctx, core.TakeQuery{Limit: 20})
			if err != nil {
				return nil, err
			}
			var out []map[string]any
			for _, t := range ts {
				out = append(out, map[string]any{"id": t.ID, "created_at": t.CreatedAt, "text": t.Text, "model": t.Model,
					"voice_id": t.VoiceID, "audio_seconds": round2(t.AudioSeconds), "audio_url": "/api/takes/" + t.ID + "/audio"})
			}
			data, _ := json.MarshalIndent(map[string]any{"takes": out}, "", "  ")
			return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(data)}}}, nil
		})
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// readOnlyTool reports whether a tool only reads: its calls are not
// logged.
func readOnlyTool(name string) bool {
	for _, p := range []string{"list_", "get_", "check_", "search_", "describe_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// logTools logs the calls of the tools that change something (#108):
// the tool, who called (mcp, or mcp-lan for a LAN client), the outcome
// and the time. Their services see "mcp" as who asked.
func logTools(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		call, ok := req.(*sdk.CallToolRequest)
		if method != "tools/call" || !ok || call.Params == nil || readOnlyTool(call.Params.Name) {
			return next(ctx, method, req)
		}
		who := "mcp"
		if call.Extra != nil && strings.HasPrefix(call.Extra.Header.Get(api.HeaderVia), api.ViaLAN) {
			who = "mcp-lan"
		}
		start := time.Now()
		res, err := next(oplog.WithActor(ctx, who), method, req)
		kv := []any{"tool", call.Params.Name, "via", who, "ms", time.Since(start)}
		switch r, _ := res.(*sdk.CallToolResult); {
		case err != nil:
			oplog.Warn("mcp.call", append(kv, "error", err)...)
		case r != nil && r.IsError:
			oplog.Warn("mcp.call", append(kv, "error", toolErrorText(r))...)
		default:
			oplog.Info("mcp.call", kv...)
		}
		return res, err
	}
}

// toolErrorText is the start of a failed tool's text: its error code
// and message.
func toolErrorText(r *sdk.CallToolResult) string {
	for _, c := range r.Content {
		if t, ok := c.(*sdk.TextContent); ok {
			return t.Text
		}
	}
	return "error"
}
