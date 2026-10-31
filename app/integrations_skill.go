package main

// The agent skill (thread #34, a map of the app since #35): a SKILL.md
// (the Agent Skills format that Claude Code, Codex and Cursor read) that
// tells an agent in every new chat what AudioSmith is, what each of its
// pages does and which tools do it (or that it stays in the app), how to
// check that it runs, the long-job pattern, every tool, recipes and
// manners; every tool's arguments are in reference/tools.md beside it. Without it a new
// chat sees only bare tool names. The setup prompt (integrations_prompt.go)
// installs it with a shell script; the agent pages can install, save or
// copy it (integrations_view.go). It is made from the live settings (port,
// client, voices, the agents' folder) in the UI's language; commands,
// tool and argument names stay literal; it never carries the API key.
//
// Where each agent reads user-level skills (checked 2026-10-14):
//   - Claude Code: ~/.claude/skills/<name>/SKILL.md (code.claude.com/docs/en/skills)
//   - Codex: ~/.agents/skills/<name>/SKILL.md (developers.openai.com/codex/skills)
//   - Cursor: ~/.cursor/skills/<name>/SKILL.md, also ~/.agents/skills and
//     ~/.claude/skills (cursor.com/docs/skills)
//
// TestAgentSkill keeps the tools and arguments equal to the MCP server's.
//
// Files: integrations_skill.go (this: names, areas, modules, installing),
// integrations_skill_tools.go (the tools and their arguments),
// integrations_skill_doc.go (SKILL.md, the reference, the zip, the script).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// skillName is the skill's name and folder.
const skillName = "audiosmith"

// skillPath is the route the running app serves the skill's files at
// (GET /api/agent-skill?agent=<slug>&file=SKILL.md|reference/tools.md).
const skillPath = "/api/agent-skill"

// skillDir is the user-level folder of an agent's skill, from the home
// folder ("~/.claude/skills/audiosmith").
func skillDir(slug string) string { return promptAgents[slug].Skills + "/" + skillName }

// skillFile is the user-level path of an agent's SKILL.md
// ("~/.claude/skills/audiosmith/SKILL.md").
func skillFile(slug string) string { return skillDir(slug) + "/SKILL.md" }

// Areas of the tools, in the skill's order.
const (
	areaSpeech   = "speech"
	areaText     = "transcribe"
	areaLongform = "longform"
	areaDub      = "dub"
	areaLibrary  = "library"
	areaWorkflow = "workflow"
	areaJobs     = "jobs"
)

var skillAreaOrder = []string{areaSpeech, areaText, areaLongform, areaDub, areaLibrary, areaWorkflow, areaJobs}

// skillAreas puts every tool in an area.
var skillAreas = map[string]string{
	"generate_speech": areaSpeech, "list_voices": areaSpeech, "list_models": areaSpeech, "list_languages": areaSpeech,
	"design_voice": areaSpeech, "describe_voice": areaSpeech, "clone_voice": areaSpeech, "search_voices": areaSpeech, "use_gallery_voice": areaSpeech,
	"transcribe": areaText, "export_transcript": areaText,
	"create_audiobook": areaLongform, "create_story": areaLongform, "get_longform": areaLongform, "update_longform": areaLongform,
	"render_longform": areaLongform, "export_longform": areaLongform,
	"create_dub": areaDub, "get_dub": areaDub, "edit_dub": areaDub, "run_dub": areaDub, "export_dub": areaDub, "batch_dub": areaDub, "list_batch": areaDub,
	"list_projects": areaLibrary, "list_takes": areaLibrary, "export_audio": areaLibrary, "delete_item": areaLibrary,
	"list_workflows": areaWorkflow, "run_workflow": areaWorkflow,
	"get_job": areaJobs, "cancel_job": areaJobs, "check_health": areaJobs,
}

// areaTitle names an area.
func areaTitle(area string) string {
	switch area {
	case areaSpeech:
		return i18n.T("Speech and voices")
	case areaText:
		return i18n.T("Transcription")
	case areaLongform:
		return i18n.T("Stories and audiobooks")
	case areaDub:
		return i18n.T("Dubbing and batch")
	case areaLibrary:
		return i18n.T("Library, History and exports")
	case areaWorkflow:
		return i18n.T("Workflows")
	}
	return i18n.T("Jobs and health")
}

// skillToolsByArea are the tools in the skill's order.
func skillToolsByArea() []skillTool {
	tools := skillTools()
	var out []skillTool
	for _, area := range skillAreaOrder {
		for _, tl := range tools {
			if skillAreas[tl.Name] == area {
				out = append(out, tl)
			}
		}
	}
	return out
}

// skillModule is a page of AudioSmith in the skill's map of the app.
type skillModule struct {
	Path  string   // its route ("/studio"); "" for Settings and Dictation
	Name  string   // as the sidebar names it (translated)
	What  string   // what it is for
	Tools []string // the tools that do it; none: only in the app
	App   string   // what stays in the app, and where (with Tools), or why
}

// skillModules are every page (the sidebar's, then the others), then
// Settings and its sections that were pages (models, engine, logs), and
// "dictation" for the Dictation page (TestAgentSkill checks that none is
// missing).
func skillModules() map[string]skillModule {
	m := func(path, what, app string, tools ...string) skillModule {
		return skillModule{Path: path, Name: pageTitle(path), What: what, Tools: tools, App: app}
	}
	set := pageTitle("/settings")
	l := []skillModule{
		m("/home", i18n.T("the start page: what the user types, drops or links there opens in the right tool; the latest work."), i18n.T("Only in the app: agents call the tools directly.")),
		m("/voices", i18n.T("two tabs: Discover, about 35 curated voices ready to speak, to search and filter; Mine, the saved voices (designed, cloned and gallery voices)."),
			i18n.T("Editing a voice (name, tags, locking its sample) only in the app."), "search_voices", "use_gallery_voice", "list_voices", "delete_item"),
		m("/projects", i18n.T("everything made: stories, audiobooks, dubs, workflows, voices, transcripts, takes, exports."), "",
			"list_projects", "delete_item"),
		m("/assets", i18n.T("every take generated, by anyone (you too), with its recipe."), "", "list_takes", "export_audio", "delete_item"),
		m("/studio", i18n.T("text to speech with any model and voice: script markup (pauses, tones), takes, preview."), "",
			"generate_speech", "export_audio"),
		m("/voices/new", i18n.T("a new voice: Quick clone, from a recording of someone (with their consent), or Design, from a description (gender, age, accent, style)."),
			i18n.T("Recording from the microphone only in the app."), "clone_voice", "describe_voice", "design_voice"),
		m("/stories", i18n.T("stories and dialogues with several voices: lines, characters (the cast), chapters, one render."), "",
			"create_story", "get_longform", "update_longform", "render_longform", "export_longform"),
		m("/audiobook", i18n.T("a whole book narrated chapter by chapter, with a narrator and character voices, as WAV or M4B with chapters and cover."), "",
			"create_audiobook", "get_longform", "update_longform", "render_longform", "export_longform"),
		m("/dub", i18n.T("dubs a video or audio into other languages in the speakers' own voices, with subtitles."),
			i18n.T("Timeline edits (timing, splitting lines) only in the app."),
			"create_dub", "get_dub", "edit_dub", "run_dub", "export_dub"),
		m("/transcripts", i18n.T("transcripts of recordings and videos, with times and speakers."),
			i18n.T("Editing a transcript and its clean-up only in the app."), "transcribe", "export_transcript"),
		m("/isolate", i18n.T("the voices of an audio or video file alone, its music and effects, and the voices denoised."), i18n.T("Only in the app.")),
		m("/dictation", i18n.T("speak into any app with a global hotkey."), i18n.T("Only in the app (%s → %s): not for agents.", set, i18n.T("Dictation"))),
		m("/workflows", i18n.T("saved pipelines (scripts or recordings → speak, transcribe, translate, convert, normalize → export), run over many items."),
			i18n.T("Building or changing a workflow only in the app."), "list_workflows", "run_workflow"),
		m("/batch", i18n.T("many files dubbed one after another, and a watch folder that adds new ones."),
			i18n.T("Watch folders only in the app."), "batch_dub", "list_batch"),
		m("/calls", i18n.T("AI phone calls through the user's Twilio number."),
			i18n.T("Only in the app (sidebar → %s → %s): you never place calls.", groupTitle("More"), pageTitle("/calls"))),
		m("/developers", i18n.T("setups for agents and SDKs (this skill comes from there), the Local API, and tools: A/B voices, Convert (a recording in another voice), Rate fit, Direction, File probe, Watermark check."),
			i18n.T("In the app; probe, rate fit, direction and the watermark check also through the Local API (/api/tools/…, /api/watermark/detect).")),
		m("/tasks", i18n.T("every job that runs or waits: renders, dubs, takes, exports, downloads."),
			i18n.T("In the app: the bell at the top right opens it. Agents follow their own jobs with get_job.")),
	}
	out := map[string]skillModule{}
	for _, x := range l {
		out[x.Path] = x
	}
	out["dictation"] = out["/dictation"]
	out["settings"] = skillModule{Name: set, What: i18n.T("everything the user chose: default models, translation, dubbing, privacy, the Local API."),
		App: i18n.T("Only in the app (⌘,). Never change it yourself: tell the user what to change.")}
	out["models"] = skillModule{Name: set + " → " + i18n.T("Models & network"), What: i18n.T("the speech models: downloads, licences, sizes."),
		Tools: []string{"list_models", "list_languages"}, App: i18n.T("Downloading models and accepting licences only in the app: ask the user.")}
	out["engine"] = skillModule{Name: set + " → " + i18n.T("Engine"), What: i18n.T("the speech engine: its state, memory, restart."),
		Tools: []string{"check_health"}, App: i18n.T("Restarting it only in the app.")}
	out["logs"] = skillModule{Name: set + " → " + i18n.T("Logs"), What: i18n.T("the app's and the engine's logs."),
		App: i18n.T("Only in the app: when something fails without a clear error, ask the user to look there.")}
	return out
}

// skillDescription is the frontmatter's description: what the skill is
// for and when to use it, trigger words first (agents shorten long lists).
func skillDescription() string {
	return i18n.T("Text to speech, audiobooks, multi-voice stories, video dubbing, voices and transcription on this Mac with AudioSmith (local and private; MCP server audiosmith). Use it when the user wants speech or TTS (read text aloud, narration, a voice-over, a WAV), to turn a book or EPUB into an audiobook (M4B), a story with several voices, to dub a video into another language with subtitles, to find, design or clone a voice, to transcribe audio or video into text or subtitles (SRT, VTT), to export audio for a podcast, or to run AudioSmith's workflows.")
}

// yamlString is s as a YAML double-quoted scalar (a JSON string is one).
func yamlString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// skillHome is the home folder skills are installed under (tests set it).
var skillHome = os.UserHomeDir

// skillTarget is the absolute path of an agent's user-level skill folder.
func skillTarget(slug string) (string, error) {
	home, err := skillHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(skillDir(slug), "~/")), nil
}

// skillState is what installing files into dir would do: "" new, "same"
// nothing, "differs" replace (with backups).
func skillState(dir string, files []skillDoc) string {
	state := "same"
	for _, f := range files {
		old, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		switch {
		case err != nil && f.Path == "SKILL.md":
			return ""
		case err != nil || string(old) != f.Text:
			state = "differs"
		}
	}
	return state
}

// installSkill writes files into dir like the setup prompt's script: a
// different file there is kept as <name>.bak-<time>, the same is left
// alone. It returns the backups made.
func installSkill(dir string, files []skillDoc) (backups []string, err error) {
	stamp := time.Now().Format("20060102-150405")
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return backups, err
		}
		old, rerr := os.ReadFile(path)
		if rerr == nil && string(old) == f.Text {
			continue
		}
		if rerr == nil {
			backup := path + ".bak-" + stamp
			if err := os.WriteFile(backup, old, 0o644); err != nil {
				return backups, err
			}
			backups = append(backups, backup)
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".skill-*")
		if err != nil {
			return backups, err
		}
		_, err = tmp.WriteString(f.Text)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(tmp.Name(), 0o644)
		}
		if err == nil {
			err = os.Rename(tmp.Name(), path)
		}
		if err != nil {
			os.Remove(tmp.Name())
			return backups, err
		}
	}
	return backups, nil
}

// skillModel names a model for an agent: its id, then the friendly name
// the user sees ("`omnivoice-bf16` (Multilingual all-rounder)").
func skillModel(id string) string {
	if e, ok := catalog.Builtin().ByID(id); ok {
		return "`" + id + "` (" + e.DisplayName() + ")"
	}
	return "`" + id + "`"
}
