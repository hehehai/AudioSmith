package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// promptBlocks are the outer fenced blocks of a prompt: language, code.
// A fence of n backticks closes with a line of the same n (the skill's
// script, fenced with four, holds fences of three).
func promptBlocks(s string) [][2]string {
	var out [][2]string
	fence, lang := "", ""
	var code []string
	for _, l := range strings.Split(s, "\n") {
		switch {
		case fence == "" && strings.HasPrefix(l, "```"):
			fence = l[:len(l)-len(strings.TrimLeft(l, "`"))]
			lang, code = strings.TrimPrefix(l, fence), nil
		case fence != "" && l == fence:
			out = append(out, [2]string{lang, strings.Join(code, "\n")})
			fence = ""
		case fence != "":
			code = append(code, l)
		}
	}
	return out
}

const testExe = "/Applications/AudioSmith.app/Contents/MacOS/AudioSmith"

// TestAgentPrompts: every agent's prompt names the port, the MCP URL, its
// client header, its configuration and the real tools; it follows the
// port and the key settings; JSON blocks parse; the key shows only when
// the user put it in the setups.
func TestAgentPrompts(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	envs := map[string]integEnv{
		"none":     {Base: "http://127.0.0.1:4000", Exe: testExe},
		"keychain": {Base: "http://127.0.0.1:4000", Exe: testExe, NeedKey: true},
		"included": {Base: "http://127.0.0.1:4000", Exe: testExe, Key: "as-SECRET"},
	}
	want := map[string][]string{
		"claude-code": {"X-AudioSmith-Client: claude-code", "claude mcp add --transport http --scope user audiosmith http://127.0.0.1:4000/mcp", "claude mcp list", "claude mcp get audiosmith", "`~/.claude/skills/audiosmith/`", "`.claude/skills/audiosmith/`", `D="$HOME/.claude/skills/audiosmith"`},
		"codex":       {`"X-AudioSmith-Client" = "codex-cli"`, "~/.codex/config.toml", "[mcp_servers.audiosmith]", `url = "http://127.0.0.1:4000/mcp"`, "tool_timeout_sec = 600", `"--mcp-stdio", "--client", "codex-cli"`, "codex mcp list", "`~/.agents/skills/audiosmith/`", `D="$HOME/.agents/skills/audiosmith"`},
		"cursor":      {`"X-AudioSmith-Client": "cursor"`, "~/.cursor/mcp.json", ".cursor/mcp.json", `"url": "http://127.0.0.1:4000/mcp"`, "`~/.cursor/skills/audiosmith/`", `D="$HOME/.cursor/skills/audiosmith"`},
		"mcp":         {`"X-AudioSmith-Client": "<your-name>"`, `"type": "http"`, testExe, `"--mcp-stdio"`, "`~/.agents/skills`", `D="$HOME/.agents/skills/audiosmith"`},
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocale(lang)
		for slug, words := range want {
			for mode, e := range envs {
				p := promptEnv{integEnv: e, Voices: []string{"Narrator", "Ann"}, Bindings: map[string]string{"claude-code": "Narrator"}, Dir: "/Users/me/AudioSmith/mcp"}
				s := agentPrompt(slug, p)
				for _, w := range append(words, "http://127.0.0.1:4000/health", "curl -sS http://127.0.0.1:4000/health", "port 4000", "URL=http://127.0.0.1:4000/mcp", "tools/list", "list_voices", `"Narrator", "Ann"`, "/Users/me/AudioSmith/mcp", "U='http://127.0.0.1:4000/api/agent-skill?agent="+slug+"&file='", "name: audiosmith`", "reference/tools.md", "Install skill") {
					if w == "Install skill" {
						if slug == "mcp" {
							w = "Save skill"
						}
						w = i18n.T(w)
					}
					if lang == "zh-Hans" && w == "port 4000" {
						w = "端口 4000"
					}
					if !strings.Contains(s, w) {
						t.Errorf("%s %s %s: no %q in\n%s", lang, slug, mode, w, s)
					}
				}
				if n := len(s); n > 8<<10 {
					t.Errorf("%s %s %s: the prompt is %d bytes: keep it under 8 KB", lang, slug, mode, n)
				}
				if strings.Contains(s, "## ") && strings.Contains(s, "What AudioSmith does") {
					t.Errorf("%s: the prompt holds the skill itself", slug)
				}
				for _, tool := range mcpToolNames {
					if !strings.Contains(s, tool) {
						t.Errorf("%s %s: tool %s missing", lang, slug, tool)
					}
				}
				if (slug == "claude-code") != strings.Contains(s, "Settings → Integrations → Each agent's voice): \"Narrator\"") && lang == "en" {
					t.Errorf("%s: the bound voice", slug)
				}
				switch mode {
				case "none":
					if strings.Contains(s, "Authorization") || strings.Contains(s, "security find-generic-password") {
						t.Errorf("%s: a key nobody needs:\n%s", slug, s)
					}
				case "keychain":
					if !strings.Contains(s, "security find-generic-password -s AudioSmith -a api.key -w") || strings.Contains(s, "as-SECRET") {
						t.Errorf("%s: the key from the Keychain:\n%s", slug, s)
					}
				case "included":
					if !strings.Contains(s, "Bearer as-SECRET") || strings.Contains(s, "security find-generic-password") {
						t.Errorf("%s: the included key:\n%s", slug, s)
					}
				}
				for _, blk := range promptBlocks(s) {
					if blk[0] == "json" {
						var v any
						if err := json.Unmarshal([]byte(blk[1]), &v); err != nil {
							t.Errorf("%s %s: not JSON: %v\n%s", slug, mode, err, blk[1])
						}
					}
				}
				// Another port, another prompt.
				p.Base = "http://127.0.0.1:4555"
				if s2 := agentPrompt(slug, p); strings.Contains(s2, "4000") || !strings.Contains(s2, "http://127.0.0.1:4555/mcp") {
					t.Errorf("%s: the port did not follow", slug)
				}
			}
		}
		// The prompt is in the UI's language; the commands stay as they are.
		s := agentPrompt("claude-code", promptEnv{integEnv: envs["none"]})
		if zh := strings.Contains(s, "步骤"); zh != (lang == "zh-Hans") {
			t.Errorf("%s: language of the prompt:\n%s", lang, s)
		}
	}
	if agentPrompt("openai-sdk", promptEnv{integEnv: envs["none"]}) != "" || agentPrompt("twilio", promptEnv{}) != "" {
		t.Error("prompts for pages that are not agents")
	}
	if os.Getenv("AUDIOSMITH_PROMPTS") != "" { // print them for review
		for _, lang := range []string{"en", "zh-Hans"} {
			i18n.SetLocale(lang)
			for _, slug := range []string{"claude-code", "codex", "cursor", "mcp"} {
				fmt.Printf("===== %s %s =====\n%s\n", slug, lang, agentPrompt(slug, promptEnv{integEnv: integEnv{Base: "http://127.0.0.1:3901", Exe: testExe},
					Voices: []string{"Narrator", "Ann"}, Bindings: map[string]string{"claude-code": "Narrator"}, Dir: "/Users/me/Library/Application Support/AudioSmith/mcp"}))
			}
		}
	}
}

// TestAgentPromptHandshake runs the prompt's commands (the health check
// and the MCP handshake) with curl against the real API and MCP server.
func TestAgentPromptHandshake(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	e := newAPIEnv(t)
	s := agentPrompt("claude-code", promptEnv{integEnv: integEnv{Base: e.ts.URL, Exe: testExe}})
	var health, handshake string
	for _, blk := range promptBlocks(s) {
		switch {
		case strings.Contains(blk[1], "/health") && health == "": // the first: the skill's script mentions it too
			health = blk[1]
		case strings.Contains(blk[1], "tools/list"):
			handshake = blk[1]
		}
	}
	if health == "" || handshake == "" {
		t.Fatalf("blocks missing:\n%s", s)
	}
	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		out, err := exec.Command(sh, "-c", health).CombinedOutput()
		if err != nil || !strings.Contains(string(out), `"status":"ok"`) {
			t.Fatalf("%s health: %v %s", sh, err, out)
		}
		out, err = exec.Command(sh, "-c", handshake).CombinedOutput()
		o := string(out)
		if err != nil || !strings.Contains(o, "session: ") || strings.Contains(o, "session: \n") {
			t.Fatalf("%s handshake: %v\n%s", sh, err, o)
		}
		for _, tool := range mcpToolNames {
			if !strings.Contains(o, `"name":"`+tool+`"`) {
				t.Errorf("%s: tools/list lacks %s:\n%s", sh, tool, o)
			}
		}
		if !strings.Contains(o, `"id":3,"result"`) || strings.Contains(o, `"isError":true`) {
			t.Errorf("%s: list_voices did not answer:\n%s", sh, o)
		}
	}
}

// TestAgentPromptPage: the prompt card on an agent's page copies the
// prompt of the live settings and saves it as a .txt; the manual setup
// folds below; pages of other kinds have none.
func TestAgentPromptPage(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.API.Port = 4123 })
	a.voices = append(a.voices, core.Voice{ID: "v1", Name: "Narrator"})
	a.navigate("/integrations/codex")
	tt.Frame()
	if !tt.HasText("Agent setup prompt") || !tt.HasText("Manual setup") || tt.HasText("Through the stdio shim") {
		t.Fatal("prompt card, manual setup folded")
	}
	if err := tt.Click("Copy prompt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := tt.Clipboard()
	if got != agentPrompt("codex", a.promptEnvOf()) || !strings.Contains(got, "http://127.0.0.1:4123/mcp") || !strings.Contains(got, "Narrator") {
		t.Fatalf("clipboard:\n%s", got)
	}
	if !tt.HasText("Copied: paste it into Codex") {
		t.Error("no toast")
	}
	// The settings change, the prompt follows.
	a.updateSettings(func(s *settings.Settings) { s.API.Port = 4124; s.API.RequireKey = true })
	tt.Frame()
	tt.Click("Copy prompt")
	tt.Frame()
	if got := tt.Clipboard(); !strings.Contains(got, "http://127.0.0.1:4124/mcp") || !strings.Contains(got, keychainRead) {
		t.Fatalf("after the change:\n%s", got)
	}
	if !tt.HasText("AudioSmith requires its key: the agent reads it from the Keychain (macOS may ask you to allow it) and keeps it out of shared files.") {
		t.Error("no note on the key")
	}
	// Save as .txt….
	integSaveDir = t.TempDir()
	t.Cleanup(func() { integSaveDir = "" })
	if err := tt.Click("Save as .txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	data, err := os.ReadFile(filepath.Join(integSaveDir, "audiosmith-setup-codex.txt"))
	if err != nil || string(data) != agentPrompt("codex", a.promptEnvOf()) {
		t.Fatalf("saved %v:\n%s", err, data)
	}
	// The manual setup opens (under the skill's card: a tall window).
	tt.SetSize(1100, 1600)
	if err := tt.Click("Manual setup"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Through the stdio shim") {
		t.Error("manual setup did not open")
	}
	// Every agent page has the card; the others show their blocks as before.
	for _, in := range integrations {
		a.navigate("/integrations/" + in.Slug)
		tt.Frame()
		_, agent := promptAgents[in.Slug]
		if tt.HasText("Agent setup prompt") != agent {
			t.Errorf("%s: prompt card %v", in.Slug, !agent)
		}
	}
	a.navigate("/integrations/openai-sdk")
	tt.Frame()
	if !tt.HasText("Speech (curl)") {
		t.Error("openai-sdk blocks")
	}
}

// TestSnapshotsAgentPrompt renders an agent's page with its prompt at
// 1100×720 and 820×520 @125 %, light and dark, English and Chinese:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsAgentPrompt .
func TestSnapshotsAgentPrompt(t *testing.T) { runSnapshots(t, snapshotsAgentPrompt) }

func snapshotsAgentPrompt(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	useMemKeys(t)
	for _, run := range []struct {
		lang string
		dark bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		for _, sz := range []struct {
			name  string
			w, h  int
			scale float64
		}{{"1100", 1100, 720, 1}, {"min125", 820, 520, 1.25}} {
			i18n.SetLocale("en")
			a, _, tt := newTestApp(t)
			lang, scale := run.lang, sz.scale
			a.updateSettings(func(s *settings.Settings) {
				if lang != "en" {
					s.General.Language = lang
				}
				s.Appearance.Scale = scale
				s.API.Bindings = []settings.APIBinding{{Client: "claude-code", Voice: "v1"}}
			})
			a.voices = append(a.voices, core.Voice{ID: "v1", Name: "Narrator", Kind: core.VoiceCloned}, core.Voice{ID: "v2", Name: "Ann", Kind: core.VoiceDesigned})
			a.apiPort = 3901
			tt.SetSize(sz.w, sz.h)
			tt.SetDark(run.dark)
			tt.SetScale(2)
			suffix := sz.name + "-light"
			if run.dark {
				suffix = sz.name + "-dark"
			}
			if run.lang != "en" {
				suffix += "-" + run.lang
			}
			save := func(name string) {
				tt.Frame()
				tt.Frame()
				f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
			a.navigate("/integrations/claude-code")
			save("agent-prompt-claude-code")
			if sz.name == "1100" && !run.dark {
				a.integManual = true
				tall := ui.NewTester(a.view, 1100, 1500)
				tall.SetScale(2)
				tall.Frame()
				tall.Frame()
				f, _ := os.Create(filepath.Join(out, "agent-prompt-claude-code-manual-"+suffix+".png"))
				png.Encode(f, tall.Image())
				f.Close()
				a.integManual = false
				a.updateSettings(func(s *settings.Settings) { s.API.RequireKey = true })
				save("agent-prompt-claude-code-key")
				a.updateSettings(func(s *settings.Settings) { s.API.RequireKey = false })
			}
		}
	}
}
