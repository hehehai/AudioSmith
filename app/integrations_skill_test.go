package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/mcp"
	"github.com/hehehai/AudioSmith/internal/settings"
)

var promptSlugs = []string{"claude-code", "codex", "cursor", "mcp"}

func skillTestEnv(e integEnv) promptEnv {
	return promptEnv{integEnv: e, Voices: []string{"Narrator", "Ann"}, Bindings: map[string]string{"claude-code": "Narrator"}, Dir: "/Users/me/AudioSmith/mcp"}
}

// frontmatter parses a SKILL.md's frontmatter as the skill writes it:
// "key: value" lines, the description a YAML double-quoted (JSON) string.
func frontmatter(t *testing.T, s string) (map[string]string, string) {
	t.Helper()
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("no frontmatter:\n%s", s)
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		t.Fatalf("frontmatter not closed:\n%s", s)
	}
	fm := map[string]string{}
	for _, l := range strings.Split(s[4:4+end], "\n") {
		k, v, ok := strings.Cut(l, ": ")
		if !ok {
			t.Fatalf("frontmatter line %q", l)
		}
		if strings.HasPrefix(v, `"`) {
			if err := json.Unmarshal([]byte(v), &v); err != nil {
				t.Fatalf("%s: %v", k, err)
			}
		}
		fm[k] = v
	}
	return fm, s[4+end+5:]
}

var skillNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TestAgentSkill: each agent's SKILL.md has valid frontmatter (name,
// description of the Agent Skills spec), the live port, client, voices
// and folder, every MCP tool with exactly its schema's arguments, and
// never the key; it is in the UI's language.
func TestAgentSkill(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	e := newAPIEnv(t)
	// The MCP server's tools and their arguments, as an agent sees them.
	c := sdk.NewClient(&sdk.Implementation{Name: "skill-test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp", HTTPClient: http.DefaultClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	schemas := map[string]schema{}
	for _, tl := range tools.Tools {
		var s schema
		data, _ := json.Marshal(tl.InputSchema)
		json.Unmarshal(data, &s)
		schemas[tl.Name] = s
	}
	if init := cs.InitializeResult(); init == nil || init.Instructions != mcp.Instructions {
		t.Error("initialize has no instructions")
	}

	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocale(lang)
		// The tools as the skill explains them = the server's.
		var names []string
		for _, tl := range skillTools() {
			names = append(names, tl.Name)
			sc, ok := schemas[tl.Name]
			if !ok {
				t.Errorf("the skill explains %s, which the server lacks", tl.Name)
				continue
			}
			var args, req []string
			for _, a := range tl.Args {
				args = append(args, a.Names...)
				if a.Required {
					req = append(req, a.Names...)
				}
			}
			var props []string
			for p := range sc.Properties {
				props = append(props, p)
			}
			slices.Sort(args)
			slices.Sort(props)
			slices.Sort(req)
			r := slices.Clone(sc.Required)
			slices.Sort(r)
			if !slices.Equal(args, props) {
				t.Errorf("%s: the skill names %v, the schema has %v", tl.Name, args, props)
			}
			if !slices.Equal(req, r) {
				t.Errorf("%s: required %v, the schema's %v", tl.Name, req, r)
			}
		}
		slices.Sort(names)
		want := slices.Clone(mcpToolNames)
		slices.Sort(want)
		if !slices.Equal(names, want) || len(schemas) != len(want) {
			t.Errorf("tools %v, server %d, want %v", names, len(schemas), want)
		}

		for _, slug := range promptSlugs {
			for mode, ie := range map[string]integEnv{
				"none":     {Base: "http://127.0.0.1:4000", Exe: testExe},
				"keychain": {Base: "http://127.0.0.1:4000", Exe: testExe, NeedKey: true},
				"included": {Base: "http://127.0.0.1:4000", Exe: testExe, NeedKey: true, Key: "as-SECRET"},
			} {
				p := skillTestEnv(ie)
				s := agentSkill(slug, p)
				ref := skillReference()
				fm, body := frontmatter(t, s)
				all := body + ref
				if len(fm) != 2 || fm["name"] != skillName || !skillNameRE.MatchString(fm["name"]) || len(fm["name"]) > 64 {
					t.Errorf("%s %s: frontmatter %v", lang, slug, fm)
				}
				if d := fm["description"]; d == "" || len([]rune(d)) > 1024 || !strings.Contains(d, "AudioSmith") || !strings.Contains(d, "TTS") || !strings.Contains(d, "SRT") {
					t.Errorf("%s %s: description %q", lang, slug, d)
				}
				for _, w := range []string{"http://127.0.0.1:4000/health", "http://127.0.0.1:4000/mcp", "port 4000", `"Narrator", "Ann"`, "/Users/me/AudioSmith/mcp",
					"open -a AudioSmith", "consent: true", "timestamps: true", `output: "file"`, "voice://{id}", "history://recent", "127.0.0.1",
					"licence_required", "needs_reference", "/v1/audio/speech", "response_format=srt", "confirm: true", "wait_seconds: 30",
					"confirm_required", "busy", "job_id", "longform:…", "dub:…", "workflow:…", "reference/tools.md", `format: "m4b"`,
					`target_languages: ["en"]`, `loudness: "podcast"`, "/api/jobs/{id}?wait=30"} {
					if lang == "zh-Hans" && w == "port 4000" {
						w = "端口 4000"
					}
					if !strings.Contains(body, w) {
						t.Errorf("%s %s %s: no %q in\n%s", lang, slug, mode, w, s)
					}
				}
				if !strings.Contains(ref, "replaced_by") {
					t.Errorf("%s: the reference does not say removed models are replaced", lang)
				}
				// Speech to text: Qwen3-ASR, then Whisper (thread #46).
				if !strings.Contains(ref, "`"+catalog.DefaultSTT+"`") || !strings.Contains(ref, "`"+catalog.FallbackSTT+"`") {
					t.Errorf("%s: the reference does not name the speech-to-text defaults", lang)
				}
				for _, r := range catalog.Builtin().Removed {
					for _, name := range []string{r.ID, r.Repo, removedName(r)} {
						if name != "" && (strings.Contains(ref, name) || strings.Contains(body, name)) {
							t.Errorf("%s %s: names the removed %s", lang, slug, name)
						}
					}
				}
				for _, tl := range skillTools() {
					if !strings.Contains(ref, "### `"+tl.Name+"`") {
						t.Errorf("%s %s: no section for %s", lang, slug, tl.Name)
					}
					if !strings.Contains(body, "- `"+tl.Name+"`: ") {
						t.Errorf("%s %s: SKILL.md does not list %s", lang, slug, tl.Name)
					}
					for _, a := range tl.Args {
						for _, n := range a.Names {
							if !strings.Contains(ref, "`"+n+"`") {
								t.Errorf("%s %s: %s.%s missing", lang, slug, tl.Name, n)
							}
						}
					}
				}
				// The map of the app: every page of the sidebar, Settings
				// and Dictation, by the names the user sees, each with real
				// tools or what stays in the app.
				mods := skillModules()
				for _, pg := range pages {
					md, ok := mods[pg.path]
					if !ok || !strings.Contains(body, "**"+pageTitle(pg.path)+"**: ") {
						t.Errorf("%s %s: the map lacks %s", lang, slug, pg.path)
					}
					if len(md.Tools) == 0 && md.App == "" {
						t.Errorf("%s: %s has neither tools nor where it is in the app", lang, pg.path)
					}
					for _, tn := range md.Tools {
						if !slices.Contains(mcpToolNames, tn) {
							t.Errorf("%s: %s names the tool %s, which the server lacks", lang, pg.path, tn)
						}
					}
				}
				for _, k := range []string{"settings", "dictation"} {
					if md := mods[k]; md.Name == "" || !strings.Contains(body, "**"+md.Name+"**: ") {
						t.Errorf("%s: the map lacks %s", lang, k)
					}
				}
				for _, g := range navGroups {
					if !strings.Contains(body, "### "+groupTitle(g.id)+"\n") {
						t.Errorf("%s: no section %s", lang, g.id)
					}
				}
				if n := len(s); n > 25<<10 {
					t.Errorf("%s %s: SKILL.md is %d bytes: keep it under 25 KB (details go to %s)", lang, slug, n, skillRefPath)
				}
				if all == "" {
					t.Fatal("empty")
				}
				ag := promptAgents[slug]
				if hasClient := strings.Contains(s, "X-AudioSmith-Client: "+ag.Client+"'"); hasClient != (ag.Client != "") {
					t.Errorf("%s: client header %v", slug, hasClient)
				}
				if bound := strings.Contains(s, `Your own voice: "Narrator".`); lang == "en" && bound != (slug == "claude-code") {
					t.Errorf("%s: the bound voice", slug)
				}
				if strings.Contains(s, "as-SECRET") {
					t.Errorf("%s %s: the key is in the skill", slug, mode)
				}
				if strings.Contains(s, keychainRead) != (mode != "none") {
					t.Errorf("%s %s: the key from the Keychain", slug, mode)
				}
				if zh := strings.Contains(s, "检查"); zh != (lang == "zh-Hans") {
					t.Errorf("%s %s: language", lang, slug)
				}
				p.Base = "http://127.0.0.1:4555"
				if s2 := agentSkill(slug, p); strings.Contains(s2, "4000") || !strings.Contains(s2, "http://127.0.0.1:4555/mcp") {
					t.Errorf("%s: the port did not follow", slug)
				}
			}
		}
		// A real YAML parser agrees, when there is one.
		if py, err := exec.LookPath("python3"); err == nil && exec.Command(py, "-c", "import yaml").Run() == nil {
			s := agentSkill("claude-code", skillTestEnv(integEnv{Base: "http://127.0.0.1:3901"}))
			fm, _ := frontmatter(t, s)
			cmd := exec.Command(py, "-I", "-c", `import sys, yaml, json
t = sys.stdin.read().split("---\n")[1]
print(json.dumps(yaml.safe_load(t), ensure_ascii=False))`)
			cmd.Stdin = strings.NewReader(s)
			out, err := cmd.Output()
			var got map[string]string
			if err != nil || json.Unmarshal(out, &got) != nil || got["name"] != "audiosmith" || got["description"] != fm["description"] {
				t.Errorf("%s: YAML says %v %s", lang, err, out)
			}
		}
	}
	if agentSkill("openai-sdk", promptEnv{}) != "" {
		t.Error("a skill for a page that is not an agent")
	}
}

// TestAgentSkillScript runs the setup prompt's install script in bash and
// zsh with a temporary HOME against the real API server: it downloads
// the skill's files (GET /api/agent-skill), is idempotent, backs up a
// different file, and changes nothing when AudioSmith does not answer;
// and the skill's curl example speaks.
func TestAgentSkillScript(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	for _, slug := range promptSlugs {
		p := promptEnv{integEnv: integEnv{Base: e.ts.URL, Exe: testExe}, Dir: "/tmp/x"}
		prompt := agentPrompt(slug, p)
		var script string
		for _, blk := range promptBlocks(prompt) {
			if strings.Contains(blk[1], skillPath+"?agent="+slug) {
				script = blk[1]
			}
		}
		if script == "" {
			t.Fatalf("%s: no install script:\n%s", slug, prompt)
		}
		var want []skillDoc
		for _, f := range []string{"SKILL.md", skillRefPath} {
			text, err := e.b.AgentSkill(ctx, slug, f)
			if err != nil || !strings.HasPrefix(text, map[bool]string{true: "---\nname: audiosmith\n", false: "# "}[f == "SKILL.md"]) {
				t.Fatalf("%s %s: %v", slug, f, err)
			}
			want = append(want, skillDoc{f, text})
		}
		for _, sh := range []string{"bash", "zsh"} {
			if _, err := exec.LookPath(sh); err != nil {
				t.Logf("no %s", sh)
				continue
			}
			home := t.TempDir()
			dir := filepath.Join(home, strings.TrimPrefix(skillDir(slug), "~/"))
			run := func(script string) string {
				cmd := exec.Command(sh, "-c", script)
				cmd.Env = append(os.Environ(), "HOME="+home)
				cmd.Dir = home
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %s: %v\n%s", sh, slug, err, out)
				}
				return string(out)
			}
			files := func(sub string) []string {
				es, _ := os.ReadDir(filepath.Join(dir, sub))
				var n []string
				for _, e := range es {
					n = append(n, e.Name())
				}
				return n
			}
			same := func() bool {
				for _, f := range want {
					if got, err := os.ReadFile(filepath.Join(dir, f.Path)); err != nil || string(got) != f.Text {
						return false
					}
				}
				return true
			}
			out := run(script)
			if !same() || skillState(dir, want) != "same" {
				t.Fatalf("%s %s: wrote other files\n%s", sh, slug, out)
			}
			if !strings.Contains(out, "name: audiosmith") || !strings.Contains(out, "tools.md") {
				t.Errorf("%s %s: output %s", sh, slug, out)
			}
			run(script)
			if f, r := files(""), files("reference"); !slices.Equal(f, []string{"SKILL.md", "reference"}) || !slices.Equal(r, []string{"tools.md"}) {
				t.Errorf("%s %s: again: %v %v", sh, slug, f, r)
			}
			os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0o644)
			os.WriteFile(filepath.Join(dir, skillRefPath), []byte("old ref"), 0o644)
			run(script)
			f, r := files(""), files("reference")
			if !same() || len(f) != 3 || !strings.HasPrefix(f[1], "SKILL.md.bak-") || len(r) != 2 || !strings.HasPrefix(r[1], "tools.md.bak-") {
				t.Errorf("%s %s: after different files: %v %v", sh, slug, f, r)
			} else if b, _ := os.ReadFile(filepath.Join(dir, f[1])); string(b) != "old" {
				t.Errorf("%s: backup %q", sh, b)
			}
			// AudioSmith not answering: nothing changes, and it says so.
			down := strings.ReplaceAll(script, e.ts.URL, "http://127.0.0.1:1")
			if out := run(down); !strings.Contains(out, "AudioSmith did not serve the skill") || !same() || len(files("")) != 3 || len(files("reference")) != 2 {
				t.Errorf("%s %s: down: %s %v", sh, slug, out, files(""))
			}
		}
	}
	// The endpoint: the UI's language, no key, errors.
	e.lib.settings.Update(ctx, func(s *settings.Settings) { s.API.RequireKey = true })
	e.srv.SetKey("as-SECRET")
	get := func(q string, key bool) (int, string) {
		req, _ := http.NewRequest("GET", e.ts.URL+skillPath+q, nil)
		if key {
			req.Header.Set("Authorization", "Bearer as-SECRET")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := get("?agent=codex", false); code != 401 {
		t.Errorf("without the required key: %d", code)
	}
	if code, body := get("?agent=codex", true); code != 200 || strings.Contains(body, "as-SECRET") || !strings.Contains(body, "security find-generic-password") {
		t.Errorf("SKILL.md: %d", code)
	}
	if code, _ := get("?agent=nobody", true); code != 404 {
		t.Errorf("unknown agent: %d", code)
	}
	if code, _ := get("?agent=codex&file=../../etc/passwd", true); code != 400 {
		t.Errorf("unknown file: %d", code)
	}
	e.lib.settings.Update(ctx, func(s *settings.Settings) { s.API.RequireKey = false })
	i18n.SetLocale("zh-Hans")
	_, body := get("?agent=cursor&file="+skillRefPath, false)
	i18n.SetLocale("en")
	if !strings.Contains(body, "必填") {
		t.Error("not in the UI's language")
	}
	// The skill's curl example (its first line) speaks.
	for _, blk := range promptBlocks(agentSkill("claude-code", promptEnv{integEnv: integEnv{Base: e.ts.URL}})) {
		if !strings.Contains(blk[1], "/v1/audio/speech") {
			continue
		}
		dir := t.TempDir()
		cmd := exec.Command("bash", "-c", strings.Split(blk[1], "\n")[0])
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "hello.wav")); err != nil || string(b[:4]) != "RIFF" {
			t.Errorf("hello.wav: %v %.80q", err, b)
		}
	}
}

// TestInstallSkill: a new file, the same (nothing), a different one
// (kept as a backup).
func TestInstallSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "audiosmith")
	one := []skillDoc{{"SKILL.md", "one"}, {skillRefPath, "ref one"}}
	if s := skillState(dir, one); s != "" {
		t.Fatal(s)
	}
	if b, err := installSkill(dir, one); err != nil || len(b) != 0 {
		t.Fatal(b, err)
	}
	if b, err := installSkill(dir, one); err != nil || len(b) != 0 || skillState(dir, one) != "same" {
		t.Fatal(b, err)
	}
	two := []skillDoc{{"SKILL.md", "one"}, {skillRefPath, "ref two"}}
	if s := skillState(dir, two); s != "differs" {
		t.Fatal(s)
	}
	b, err := installSkill(dir, two)
	if got, _ := os.ReadFile(b[0]); err != nil || len(b) != 1 || string(got) != "ref one" {
		t.Fatalf("backup %v %v %q", b, err, got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, skillRefPath)); string(got) != "ref two" {
		t.Fatal(string(got))
	}
	es, _ := os.ReadDir(filepath.Join(dir, "reference"))
	if len(es) != 2 {
		t.Errorf("left %d files", len(es))
	}
}

// TestAgentSkillPage: the skill card installs the skill after a
// confirmation, shows whether it is up to date, saves and copies it; the
// generic MCP page saves but does not install; other pages have no card.
func TestAgentSkillPage(t *testing.T) {
	home := t.TempDir()
	skillHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { skillHome = os.UserHomeDir })
	a, _, tt := newTestApp(t)
	a.updateSettings(func(s *settings.Settings) { s.API.Port = 4123 })
	a.voices = append(a.voices, core.Voice{ID: "v1", Name: "Narrator"})
	tt.SetSize(1100, 1400)
	a.navigate("/integrations/claude-code")
	tt.Frame()
	if !tt.HasText("Agent skill") || tt.HasText("Installed: ~/.claude/skills/audiosmith/") {
		t.Fatal("skill card")
	}
	if err := tt.Click("Install skill"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Install the skill for Claude Code?") {
		t.Fatal("no confirmation")
	}
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	dir := filepath.Join(home, ".claude", "skills", "audiosmith")
	want := agentSkill("claude-code", a.promptEnvOf())
	if got, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err != nil || string(got) != want || !strings.Contains(want, "http://127.0.0.1:4123/mcp") || !strings.Contains(want, "Narrator") {
		t.Fatalf("installed %v:\n%s", err, got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, skillRefPath)); err != nil || string(got) != skillReference() {
		t.Fatalf("reference %v", err)
	}
	tt.Frame()
	if !tt.HasText("Skill installed: start a new chat in Claude Code") || !tt.HasText("Installed: ~/.claude/skills/audiosmith/") {
		t.Error("toast or state")
	}
	// The voices change: the card says so; installing again keeps a backup.
	a.voices = append(a.voices, core.Voice{ID: "v2", Name: "Ann"})
	tt.Frame()
	if !tt.HasText("~/.claude/skills/audiosmith/ holds another version (your voices or settings changed since): install again to update it.") {
		t.Error("no out-of-date note")
	}
	tt.Click("Install skill")
	tt.Frame()
	if !tt.HasText("Different files are there: each is kept beside it as a backup (SKILL.md.bak-…).") {
		t.Error("no backup note")
	}
	tt.Click("Install")
	tt.Frame()
	if es, _ := os.ReadDir(dir); len(es) != 3 {
		t.Errorf("files %v", es)
	}
	// Save skill… and Copy skill.
	integSaveDir = t.TempDir()
	t.Cleanup(func() { integSaveDir = "" })
	if err := tt.Click("Save skill"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	zr, err := zip.OpenReader(filepath.Join(integSaveDir, "audiosmith-skill.zip"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name == "audiosmith/SKILL.md" {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			if string(data) != agentSkill("claude-code", a.promptEnvOf()) {
				t.Error("zipped SKILL.md")
			}
		}
	}
	zr.Close()
	if !slices.Equal(names, []string{"audiosmith/SKILL.md", "audiosmith/" + skillRefPath}) {
		t.Errorf("zip %v", names)
	}
	if err := tt.Click("Copy skill"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Clipboard() != agentSkillText("claude-code", a.promptEnvOf()) || !strings.Contains(tt.Clipboard(), "### `generate_speech`") {
		t.Error("clipboard")
	}
	// The generic page: no Install; the other pages: no card.
	a.navigate("/integrations/mcp")
	tt.Frame()
	if !tt.HasText("Agent skill") || tt.HasText("Install skill") || !tt.HasText("Save skill") {
		t.Error("generic MCP page")
	}
	for _, in := range integrations {
		a.navigate("/integrations/" + in.Slug)
		tt.Frame()
		_, agent := promptAgents[in.Slug]
		if tt.HasText("Agent skill") != agent {
			t.Errorf("%s: skill card %v", in.Slug, !agent)
		}
	}
}

// TestSnapshotsAgentSkill renders the skill card and its dialog, and
// writes the review texts (skills and prompts):
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsAgentSkill .
func TestSnapshotsAgentSkill(t *testing.T) { runSnapshots(t, snapshotsAgentSkill) }

func snapshotsAgentSkill(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	home := testDir(t)
	skillHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { skillHome = os.UserHomeDir })
	useMemKeys(t)
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocale(lang)
		p := promptEnv{integEnv: integEnv{Base: "http://127.0.0.1:3901", Exe: testExe}, Voices: []string{"Narrator", "Ann"},
			Bindings: map[string]string{"claude-code": "Narrator"}, Dir: "/Users/me/Library/Application Support/AudioSmith/mcp"}
		for _, slug := range promptSlugs {
			os.WriteFile(filepath.Join(out, fmt.Sprintf("SKILL-%s-%s.md", slug, lang)), []byte(agentSkill(slug, p)), 0o644)
			os.WriteFile(filepath.Join(out, fmt.Sprintf("tools-%s.md", lang)), []byte(skillReference()), 0o644)
			os.WriteFile(filepath.Join(out, fmt.Sprintf("prompt-%s-%s.txt", slug, lang)), []byte(agentPrompt(slug, p)), 0o644)
		}
	}
	for _, dark := range []bool{false, true} {
		for _, lang := range []string{"en", "zh-Hans"} {
			i18n.SetLocale("en")
			a, _, _ := newTestApp(t)
			a.updateSettings(func(s *settings.Settings) {
				if lang != "en" {
					s.General.Language = lang
				}
				s.API.Bindings = []settings.APIBinding{{Client: "claude-code", Voice: "v1"}}
			})
			a.voices = append(a.voices, core.Voice{ID: "v1", Name: "Narrator", Kind: core.VoiceCloned}, core.Voice{ID: "v2", Name: "Ann", Kind: core.VoiceDesigned})
			a.apiPort = 3901
			suffix := "light"
			if dark {
				suffix = "dark"
			}
			if lang != "en" {
				suffix += "-" + lang
			}
			shot := func(name string, w, h int) {
				tt := ui.NewTester(a.view, w, h)
				tt.SetScale(2)
				tt.SetDark(dark)
				tt.Frame()
				tt.Frame()
				f, err := os.Create(filepath.Join(out, name+"-"+suffix+".png"))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, tt.Image())
				f.Close()
			}
			a.navigate("/integrations/claude-code")
			os.RemoveAll(filepath.Join(home, ".claude"))
			shot("agent-skill-claude-code-1100x1100", 1100, 1100)
			if !dark {
				a.skillAsk = true
				shot("agent-skill-dialog", 1100, 720)
				a.skillAsk = false
				dir, _ := skillTarget("claude-code")
				installSkill(dir, agentSkillFiles("claude-code", a.promptEnvOf()))
				shot("agent-skill-installed", 1100, 1100)
				a.navigate("/integrations/mcp")
				shot("agent-skill-mcp", 1100, 1100)
			}
		}
	}
}

// removedName is a removed model's label, or "" when a kept model's label
// contains it (Chatterbox → Chatterbox Turbo).
func removedName(r catalog.Removed) string {
	for _, e := range catalog.Builtin().Entries {
		if strings.Contains(e.Label, r.Label) {
			return ""
		}
	}
	return r.Label
}
