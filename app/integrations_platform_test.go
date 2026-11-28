package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPClientPathsAndPowerShellOnWindows(t *testing.T) {
	orig := integOS
	t.Cleanup(func() { integOS = orig })

	// macOS paths stay unchanged.
	integOS = "darwin"
	if got := mcpClientConfigPath("claude-desktop"); got != "~/Library/Application Support/Claude/claude_desktop_config.json" {
		t.Errorf("darwin claude-desktop = %q", got)
	}
	if got := mcpClientConfigPath("codex"); got != "~/.codex/config.toml" {
		t.Errorf("darwin codex = %q", got)
	}

	// Windows paths use %APPDATA% and %USERPROFILE%.
	integOS = "windows"
	t.Setenv("APPDATA", `C:\Users\alice\AppData\Roaming`)
	t.Setenv("USERPROFILE", `C:\Users\alice`)

	if got := mcpClientConfigPath("claude-desktop"); got != `C:\Users\alice\AppData\Roaming\Claude\claude_desktop_config.json` {
		t.Errorf("windows claude-desktop = %q", got)
	}
	if got := mcpClientConfigPath("codex"); got != `C:\Users\alice\.codex\config.toml` {
		t.Errorf("windows codex = %q", got)
	}
	if got := mcpClientConfigPath("workbuddy"); got != `C:\Users\alice\AppData\Roaming\WorkBuddy\mcp.json` {
		t.Errorf("windows workbuddy = %q", got)
	}

	// Detect existing config directories on disk.
	root := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(root, "Roaming"))
	t.Setenv("USERPROFILE", root)
	if err := os.MkdirAll(filepath.Join(root, "Roaming", "Claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	clients := detectMCPClients()
	bySlug := map[string]mcpClientConfig{}
	for _, c := range clients {
		bySlug[c.Slug] = c
	}
	if !bySlug["claude-desktop"].Exists {
		t.Errorf("expected claude-desktop Exists=true: %+v", bySlug["claude-desktop"])
	}
	if !bySlug["codex"].Exists {
		t.Errorf("expected codex Exists=true: %+v", bySlug["codex"])
	}
	if bySlug["workbuddy"].Exists {
		t.Errorf("expected workbuddy Exists=false: %+v", bySlug["workbuddy"])
	}

	// PowerShell snippets on Windows.
	env := integEnv{Base: "http://127.0.0.1:3901", NeedKey: true}
	ps := devCurlTextWin(env)
	if !strings.Contains(ps, "Invoke-RestMethod") || !strings.Contains(ps, "$env:"+integKeyEnv) {
		t.Errorf("unexpected Windows PowerShell snippet:\n%s", ps)
	}
	blocks := claudeCodeBlocks(env)
	if !strings.Contains(blocks[0].Code, "$env:"+integKeyEnv) {
		t.Errorf("expected PowerShell env var in claudeCodeBlocks on Windows: %s", blocks[0].Code)
	}
	prompt := agentPrompt("mcp", promptEnv{integEnv: env})
	if !strings.Contains(prompt, "```powershell") || !strings.Contains(prompt, "Invoke-RestMethod") {
		t.Errorf("expected PowerShell commands in agentPrompt on Windows:\n%s", prompt)
	}
}
