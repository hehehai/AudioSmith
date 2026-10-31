package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestIntegrationSnippets: JSON blocks parse; the key never shows unless
// asked; with "Require the key" the snippets read it from the environment.
func TestIntegrationSnippets(t *testing.T) {
	base := integEnv{Base: "http://127.0.0.1:4000", Exe: "/Applications/AudioSmith.app/Contents/MacOS/AudioSmith"}
	for _, env := range []integEnv{base, {Base: base.Base, Exe: base.Exe, NeedKey: true}, {Base: base.Base, Exe: base.Exe, Key: "as-SECRET"}} {
		for _, in := range integrations {
			if in.Slug == "twilio" { // M6b (thread #23): the call gateway's setup, not the Local API's
				continue
			}
			blocks := in.Blocks(env)
			if len(blocks) == 0 {
				t.Fatalf("%s: no blocks", in.Slug)
			}
			all := ""
			for _, b := range blocks {
				all += b.Code
				if b.Lang == "json" {
					var v any
					if err := json.Unmarshal([]byte(b.Code), &v); err != nil {
						t.Errorf("%s %q: not JSON: %v", in.Slug, b.Title, err)
					}
				}
				if !strings.Contains(b.Code, "127.0.0.1:4000") && b.Lang != "text" && !strings.Contains(b.Code, "--mcp-stdio") {
					t.Errorf("%s %q: no address", in.Slug, b.Title)
				}
			}
			switch {
			case env.Key != "":
				if !strings.Contains(all, "as-SECRET") {
					t.Errorf("%s: the key was asked for but is missing", in.Slug)
				}
			case env.NeedKey:
				if !strings.Contains(all, integKeyEnv) && in.Slug != "mcp" {
					t.Errorf("%s: required key not referenced", in.Slug)
				}
			default:
				if strings.Contains(all, "Authorization") || strings.Contains(all, "bearer_token") {
					t.Errorf("%s: sends a key nobody needs", in.Slug)
				}
			}
		}
	}
	// Spot checks.
	cc := claudeCodeBlocks(base)[1].Code
	if !strings.Contains(cc, `"type": "http"`) || !strings.Contains(cc, `"url": "http://127.0.0.1:4000/mcp"`) || !strings.Contains(cc, `"X-AudioSmith-Client": "claude-code"`) {
		t.Fatalf("claude code: %s", cc)
	}
	cx := codexBlocks(integEnv{Base: base.Base, Exe: base.Exe, NeedKey: true})[0].Code
	if !strings.Contains(cx, "[mcp_servers.audiosmith]") || !strings.Contains(cx, `bearer_token_env_var = "AUDIOSMITH_API_KEY"`) {
		t.Fatalf("codex: %s", cx)
	}
	var wf struct {
		Nodes []struct {
			Type       string         `json:"type"`
			Parameters map[string]any `json:"parameters"`
		} `json:"nodes"`
		Connections map[string]any `json:"connections"`
	}
	json.Unmarshal([]byte(n8nWorkflow(base)), &wf)
	if len(wf.Nodes) != 2 || wf.Nodes[1].Type != "n8n-nodes-base.httpRequest" || wf.Nodes[1].Parameters["url"] != "http://127.0.0.1:4000/v1/audio/speech" || wf.Connections["Manual Trigger"] == nil {
		t.Fatalf("n8n %+v", wf)
	}
	py := openAISDKBlocks(base)[3].Code
	if !strings.Contains(py, `base_url="http://127.0.0.1:4000/v1"`) || !strings.Contains(py, `api_key="not-needed"`) {
		t.Fatalf("python: %s", py)
	}
}

func TestIntegrationsPage(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.API.Port = 4123 })
	a.navigate("/integrations")
	tt.Frame()
	for _, in := range integrations {
		if !tt.HasText(in.Name) {
			t.Fatalf("no card for %s", in.Name)
		}
	}
	if err := tt.Click("Claude Code"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.shownPath() != "/developers/claude-code" || !tt.HasText("http://127.0.0.1:4123/mcp") {
		t.Fatalf("detail %s", a.shownPath())
	}
	a.navigate("/integrations/nope")
	tt.Frame()
	if !tt.HasText("No matches") {
		t.Fatal("unknown slug")
	}
}
