package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// integOS controls platform-specific MCP setup paths and shell commands.
// Defaults to runtime.GOOS; tests may override it temporarily.
var integOS = runtime.GOOS

func isWindowsInteg() bool { return integOS == "windows" }

// mcpClientConfig describes a known MCP client's config file location.
type mcpClientConfig struct {
	Slug   string // "claude-desktop", "codex", "workbuddy", "cursor", "claude-code"
	Name   string
	Path   string // resolved path (or display path with %APPDATA%/%USERPROFILE%)
	Exists bool
}

// mcpClientConfigPath returns the config file path for a known MCP client
// on the current platform (using %APPDATA% / %USERPROFILE% on Windows,
// ~/Library/Application Support / ~ on macOS).
func mcpClientConfigPath(slug string) string {
	if isWindowsInteg() {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = `%APPDATA%`
		}
		profile := os.Getenv("USERPROFILE")
		if profile == "" {
			profile = `%USERPROFILE%`
		}
		switch slug {
		case "claude-desktop":
			return winJoin(appData, "Claude", "claude_desktop_config.json")
		case "codex":
			return winJoin(profile, ".codex", "config.toml")
		case "workbuddy":
			// WorkBuddy may store config under %APPDATA%\WorkBuddy or %USERPROFILE%\.workbuddy.
			p1 := winJoin(appData, "WorkBuddy", "mcp.json")
			p2 := winJoin(profile, ".workbuddy", "mcp.json")
			if _, err := os.Stat(p2); err == nil {
				return p2
			}
			return p1
		case "cursor":
			return winJoin(profile, ".cursor", "mcp.json")
		case "claude-code":
			return winJoin(profile, ".claude.json")
		}
		return ""
	}
	switch slug {
	case "claude-desktop":
		return "~/Library/Application Support/Claude/claude_desktop_config.json"
	case "codex":
		return "~/.codex/config.toml"
	case "workbuddy":
		return "~/Library/Application Support/WorkBuddy/mcp.json"
	case "cursor":
		return "~/.cursor/mcp.json"
	case "claude-code":
		return "~/.claude.json"
	}
	return ""
}

// winJoin joins path segments with Windows backslashes even when tested
// on Linux/macOS.
func winJoin(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		clean = append(clean, strings.TrimRight(p, `\/`))
	}
	return strings.Join(clean, `\`)
}

// detectMCPClients returns the known desktop/CLI MCP clients and whether
// their config directory or file exists on disk.
func detectMCPClients() []mcpClientConfig {
	specs := []struct{ slug, name string }{
		{"claude-desktop", "Claude Desktop"},
		{"codex", "Codex"},
		{"workbuddy", "WorkBuddy"},
		{"cursor", "Cursor"},
		{"claude-code", "Claude Code"},
	}
	out := make([]mcpClientConfig, 0, len(specs))
	for _, sp := range specs {
		p := mcpClientConfigPath(sp.slug)
		exists := false
		if p != "" && !strings.HasPrefix(p, "%") && !strings.HasPrefix(p, "~") {
			disk := filepath.FromSlash(strings.ReplaceAll(p, `\`, `/`))
			if _, err := os.Stat(disk); err == nil {
				exists = true
			} else if _, err := os.Stat(filepath.Dir(disk)); err == nil {
				exists = true
			}
		}
		out = append(out, mcpClientConfig{Slug: sp.slug, Name: sp.name, Path: p, Exists: exists})
	}
	return out
}

// devCurlTextWin formats the Developers quick-test request for PowerShell.
func devCurlTextWin(e integEnv) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Invoke-RestMethod -Method Post -Uri \"%s/v1/audio/speech\" `\n", e.Base)
	headers := fmt.Sprintf("\"Content-Type\" = \"application/json\"; \"%s\" = \"powershell\"", api.HeaderClient)
	if e.auth() {
		headers += fmt.Sprintf("; \"Authorization\" = \"Bearer %s\"", e.keyRef("$env:"+integKeyEnv))
	}
	fmt.Fprintf(&b, "  -Headers @{ %s } `\n", headers)
	fmt.Fprintf(&b, "  -Body '{\"input\": %q, \"response_format\": \"wav\"}' `\n", i18n.T("Hello from AudioSmith."))
	b.WriteString("  -OutFile hello.wav")
	return b.String()
}

// mcpHandshakeWin formats the MCP verification handshake for PowerShell.
func mcpHandshakeWin(p promptEnv, client string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$url = %q\n", p.mcpURL())
	headers := fmt.Sprintf("\"Content-Type\" = \"application/json\"; \"Accept\" = \"application/json, text/event-stream\"; \"%s\" = %q", api.HeaderClient, client)
	switch p.keyMode() {
	case keyKeychain:
		headers += fmt.Sprintf("; \"Authorization\" = \"Bearer $env:%s\"", integKeyEnv)
	case keyIncluded:
		headers += fmt.Sprintf("; \"Authorization\" = \"Bearer %s\"", p.Key)
	}
	fmt.Fprintf(&b, "$h = @{ %s }\n", headers)
	b.WriteString("$r = Invoke-WebRequest -Method Post -Uri $url -Headers $h -Body '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"audiosmith-setup\",\"version\":\"1.0\"}}}'\n")
	b.WriteString("$h['Mcp-Session-Id'] = $r.Headers['Mcp-Session-Id']\n")
	b.WriteString("Write-Host \"session: $($h['Mcp-Session-Id'])\"\n")
	b.WriteString("Invoke-RestMethod -Method Post -Uri $url -Headers $h -Body '{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}' | Out-Null\n")
	b.WriteString("Invoke-RestMethod -Method Post -Uri $url -Headers $h -Body '{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}'\n")
	b.WriteString("Invoke-RestMethod -Method Post -Uri $url -Headers $h -Body '{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"list_voices\",\"arguments\":{}}}'\n")
	b.WriteString("Invoke-RestMethod -Method Delete -Uri $url -Headers $h | Out-Null\n")
	return b.String()
}

// isAbsIntegPath reports whether p is an absolute path for the target
// integration OS (integOS), so darwin-targeted prompts/skills formatted
// on Windows (and vice versa) still recognize their output folder.
func isAbsIntegPath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	if integOS != "windows" {
		return strings.HasPrefix(p, "/")
	}
	return len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}
