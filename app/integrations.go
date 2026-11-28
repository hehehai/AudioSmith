package main

// Integrations (M5, /integrations): cards for the tools that work with
// AudioSmith's Local API and MCP server, each with ready-made setup for
// this Mac's address. The snippets never carry the API key unless the
// user asks; with "Require the key" on they read it from the
// AUDIOSMITH_API_KEY environment variable.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// integKeyEnv is the environment variable snippets read the key from.
const integKeyEnv = "AUDIOSMITH_API_KEY"

// integEnv is what the snippets are made for.
type integEnv struct {
	Base    string // http://127.0.0.1:3901
	Exe     string // the app's executable, for the stdio shim
	NeedKey bool   // "Require the key" is on
	Key     string // the key itself, when the user asked to include it
}

// keyRef is how a snippet names the key: the key itself (asked for), or
// a reference to the environment in the snippet's syntax.
func (e integEnv) keyRef(envSyntax string) string {
	if e.Key != "" {
		return e.Key
	}
	return envSyntax
}

// auth reports whether snippets send a key.
func (e integEnv) auth() bool { return e.NeedKey || e.Key != "" }

// integBlock is one copyable setup block.
type integBlock struct {
	Title, Hint string
	Lang        string // "json", "toml", "shell", "python", "javascript"
	Code        string
	File        string // suggested file name for Save…; "" = none
}

// integration is a card.
type integration struct {
	Slug, Name string
	Kind       string   // category, English
	Caps       []string // capabilities, English
	URL        string   // the tool's site
	Blocks     func(e integEnv) []integBlock
}

// integrations are the cards, in order.
var integrations = []integration{
	{Slug: "claude-code", Name: "Claude Code", Kind: "Agents", Caps: []string{"MCP server"}, URL: "https://docs.anthropic.com/en/docs/claude-code/mcp", Blocks: claudeCodeBlocks},
	{Slug: "cursor", Name: "Cursor", Kind: "Agents", Caps: []string{"MCP server"}, URL: "https://docs.cursor.com/context/model-context-protocol", Blocks: cursorBlocks},
	{Slug: "codex", Name: "Codex CLI", Kind: "Agents", Caps: []string{"MCP server"}, URL: "https://github.com/openai/codex", Blocks: codexBlocks},
	{Slug: "mcp", Name: "Model Context Protocol", Kind: "MCP", Caps: []string{"MCP server"}, URL: "https://modelcontextprotocol.io", Blocks: mcpBlocks},
	{Slug: "openai-sdk", Name: "OpenAI SDK", Kind: "Developer tools", Caps: []string{"Speech API", "Transcription API"}, URL: "https://platform.openai.com/docs/api-reference/audio", Blocks: openAISDKBlocks},
	{Slug: "openai-agents", Name: "OpenAI Agents SDK", Kind: "Agents", Caps: []string{"Speech API", "Transcription API"}, URL: "https://openai.github.io/openai-agents-python/voice/quickstart/", Blocks: agentsBlocks},
	{Slug: "n8n", Name: "n8n", Kind: "Automation", Caps: []string{"Speech API", "Workflow template"}, URL: "https://n8n.io", Blocks: n8nBlocks},
	{Slug: "twilio", Name: "Twilio", Kind: "Automation", Caps: []string{"Phone calls"}, URL: "https://www.twilio.com/docs/voice/media-streams", Blocks: twilioBlocks}, // M6b: calls_pages.go, its page twilio_view.go (thread #23)
}

// integrationBySlug finds a card.
func integrationBySlug(slug string) (integration, bool) {
	for _, in := range integrations {
		if in.Slug == slug {
			return in, true
		}
	}
	return integration{}, false
}

// integEnvOf is the environment of the app now.
func (a *app) integEnvOf() integEnv {
	e := integEnv{Base: a.apiBaseURL(), Exe: appExecutable(), NeedKey: a.prefs.API.RequireKey}
	if a.apiForm.keyIncl && a.apiSrv != nil {
		e.Key = a.apiSrv.Key()
	}
	return e
}

// appExecutable is the app's executable inside its bundle, for the stdio
// shim; the installed app's when not running from a bundle.
var appExecutable = func() string {
	if exe, err := os.Executable(); err == nil && strings.Contains(exe, ".app/Contents/MacOS/") {
		return exe
	}
	return "/Applications/AudioSmith.app/Contents/MacOS/AudioSmith"
}

// mcpURL is the MCP endpoint.
func (e integEnv) mcpURL() string { return e.Base + "/mcp" }

// jsonText is v as indented JSON, with <, > and & as they are (snippets
// show placeholders like <your-name>).
func jsonText(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// obj is a JSON object that keeps its keys in order (configurations read
// better with "type" and "url" before "headers").
type obj []kv

type kv struct {
	K string
	V any
}

func (o obj) MarshalJSON() ([]byte, error) {
	b := []byte{'{'}
	for i, x := range o {
		if i > 0 {
			b = append(b, ',')
		}
		k, err := marshalPlain(x.K)
		if err != nil {
			return nil, err
		}
		v, err := marshalPlain(x.V)
		if err != nil {
			return nil, err
		}
		b = append(append(append(b, k...), ':'), v...)
	}
	return append(b, '}'), nil
}

// marshalPlain is json.Marshal without HTML escapes.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// headersObj is a client's headers: its name, then the key when sent.
func (e integEnv) headersObj(client, keySyntax string) obj {
	h := obj{{api.HeaderClient, client}}
	if e.auth() {
		h = append(h, kv{"Authorization", "Bearer " + e.keyRef(keySyntax)})
	}
	return h
}

func claudeCodeBlocks(e integEnv) []integBlock {
	conf := obj{{"mcpServers", obj{{"audiosmith", obj{{"type", "http"}, {"url", e.mcpURL()},
		{"headers", e.headersObj("claude-code", "${"+integKeyEnv+"}")}}}}}}
	cmd := fmt.Sprintf("claude mcp add --transport http audiosmith %s --header \"%s: claude-code\"", e.mcpURL(), api.HeaderClient)
	if e.auth() {
		envRef := "$" + integKeyEnv
		if isWindowsInteg() {
			envRef = "$env:" + integKeyEnv
		}
		cmd += fmt.Sprintf(" --header \"Authorization: Bearer %s\"", e.keyRef(envRef))
	}
	return []integBlock{
		{Title: i18n.T("Add it from the terminal"), Lang: "shell", Code: cmd},
		{Title: i18n.T("Or in the project's .mcp.json"), Hint: i18n.T("Merge this into .mcp.json at the root of your project."), Lang: "json", Code: jsonText(conf), File: ".mcp.json"},
	}
}

func cursorBlocks(e integEnv) []integBlock {
	conf := obj{{"mcpServers", obj{{"audiosmith", obj{{"url", e.mcpURL()},
		{"headers", e.headersObj("cursor", "${env:"+integKeyEnv+"}")}}}}}}
	hint := i18n.T("Merge this into .cursor/mcp.json in your project, or ~/.cursor/mcp.json for every project.")
	if isWindowsInteg() {
		hint = i18n.T("Merge this into .cursor/mcp.json in your project, or %s for every project.", mcpClientConfigPath("cursor"))
	}
	return []integBlock{{Title: i18n.T("Client configuration"), Hint: hint,
		Lang: "json", Code: jsonText(conf), File: "mcp.json"}}
}

func codexBlocks(e integEnv) []integBlock {
	var b strings.Builder
	// Codex reads no bearer_token (only bearer_token_env_var): a key the
	// user put in the setups goes in the headers. tool_timeout_sec: Codex
	// stops a tool after 60 s by default, too short for long speech.
	headers := fmt.Sprintf("%q = \"codex-cli\"", api.HeaderClient)
	if e.Key != "" {
		headers += fmt.Sprintf(", \"Authorization\" = %q", "Bearer "+e.Key)
	}
	fmt.Fprintf(&b, "[mcp_servers.audiosmith]\nurl = %q\nhttp_headers = { %s }\ntool_timeout_sec = 600\n", e.mcpURL(), headers)
	if e.Key == "" && e.NeedKey {
		fmt.Fprintf(&b, "bearer_token_env_var = %q\n", integKeyEnv)
	}
	stdio := fmt.Sprintf("[mcp_servers.audiosmith]\ncommand = %q\nargs = [\"--mcp-stdio\", \"--client\", \"codex-cli\"]\ntool_timeout_sec = 600\n", e.Exe)
	if e.Key != "" {
		stdio += fmt.Sprintf("env = { %q = %q }\n", integKeyEnv, e.Key)
	}
	codexHint := i18n.T("Merge this into ~/.codex/config.toml.")
	if isWindowsInteg() {
		codexHint = i18n.T("Merge this into %s.", mcpClientConfigPath("codex"))
	}
	return []integBlock{
		{Title: i18n.T("Client configuration"), Hint: codexHint, Lang: "toml", Code: b.String(), File: "config.toml"},
		{Title: i18n.T("Through the stdio shim"), Hint: i18n.T("For Codex versions without HTTP servers: AudioSmith relays to the running app."), Lang: "toml", Code: stdio},
	}
}

func mcpBlocks(e integEnv) []integBlock {
	httpConf := obj{{"url", e.mcpURL()}, {"transport", "streamable-http"}, {"headers", e.headersObj("my-agent", "<"+integKeyEnv+">")}}
	env := obj{{"AUDIOSMITH_CLIENT", "my-agent"}}
	if e.Key != "" {
		env = append(env, kv{integKeyEnv, e.Key})
	}
	stdio := obj{{"mcpServers", obj{{"audiosmith", obj{{"command", e.Exe}, {"args", []string{"--mcp-stdio"}}, {"env", env}}}}}}
	// The tools by area, as the agent skill groups them (thread #35).
	var lines []string
	for _, area := range skillAreaOrder {
		var names []string
		for _, n := range mcpToolNames {
			if skillAreas[n] == area {
				names = append(names, n)
			}
		}
		lines = append(lines, areaTitle(area)+": "+strings.Join(names, " · "))
	}
	tools := strings.Join(lines, "\n") + "\nresources: voice://{id} · history://recent"
	return []integBlock{
		{Title: i18n.T("Streamable HTTP"), Hint: i18n.T("Name your agent in the header: it gets its own voice (Settings → Integrations → Local API)."), Lang: "json", Code: jsonText(httpConf)},
		{Title: i18n.T("stdio"), Hint: i18n.T("For clients that start servers as commands. AudioSmith must be running; the shim finds it by itself."), Lang: "json", Code: jsonText(stdio), File: "mcp.json"},
		{Title: i18n.T("What it offers"), Lang: "text", Code: tools},
	}
}

func openAISDKBlocks(e integEnv) []integBlock {
	var curlSpeech, curlSTT string
	if isWindowsInteg() {
		curlSpeech = devCurlTextWin(e)
		curlSTT = fmt.Sprintf("curl.exe %s/v1/audio/transcriptions -F file=@hello.wav -F model=whisper-1", e.Base)
		if e.auth() {
			curlSTT = fmt.Sprintf("curl.exe %s/v1/audio/transcriptions -H \"Authorization: Bearer %s\" -F file=@hello.wav -F model=whisper-1", e.Base, e.keyRef("$env:"+integKeyEnv))
		}
	} else {
		auth := ""
		if e.auth() {
			auth = fmt.Sprintf(" \\\n  -H \"Authorization: Bearer %s\"", e.keyRef("$"+integKeyEnv))
		}
		curlSpeech = fmt.Sprintf("curl %s/v1/audio/speech%s \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"model\": \"tts-1\", \"voice\": \"alloy\", \"input\": \"Hello from AudioSmith.\", \"response_format\": \"wav\"}' \\\n  --output speech.wav", e.Base, auth)
		curlSTT = fmt.Sprintf("curl %s/v1/audio/transcriptions%s \\\n  -F file=@speech.wav -F model=whisper-1", e.Base, auth)
	}
	pyKey, jsKey := `"not-needed"`, `"not-needed"`
	if e.auth() {
		pyKey, jsKey = fmt.Sprintf("os.environ[%q]", integKeyEnv), "process.env."+integKeyEnv
		if e.Key != "" {
			pyKey, jsKey = fmt.Sprintf("%q", e.Key), fmt.Sprintf("%q", e.Key)
		}
	}
	py := fmt.Sprintf(`import os
from openai import OpenAI

client = OpenAI(base_url="%s/v1", api_key=%s)

# Speech, streamed to a file as it is made.
with client.audio.speech.with_streaming_response.create(
    model="tts-1", voice="alloy", input="Hello from AudioSmith.", response_format="wav",
) as response:
    response.stream_to_file("speech.wav")

# And back to text.
with open("speech.wav", "rb") as f:
    print(client.audio.transcriptions.create(model="whisper-1", file=f).text)
`, e.Base, pyKey)
	js := fmt.Sprintf(`import fs from "node:fs";
import OpenAI from "openai";

const client = new OpenAI({ baseURL: "%s/v1", apiKey: %s });

const speech = await client.audio.speech.create({
  model: "tts-1", voice: "alloy", input: "Hello from AudioSmith.", response_format: "wav",
});
fs.writeFileSync("speech.wav", Buffer.from(await speech.arrayBuffer()));

const text = await client.audio.transcriptions.create({ model: "whisper-1", file: fs.createReadStream("speech.wav") });
console.log(text.text);
`, e.Base, jsKey)
	return []integBlock{
		{Title: i18n.T("Base URL"), Hint: i18n.T("Any OpenAI client works: point its base URL here. tts-1, gpt-4o-mini-tts and whisper-1 use AudioSmith's defaults; voices are your saved ones, built-in speakers, or OpenAI's names."), Lang: "text", Code: e.Base + "/v1"},
		{Title: i18n.T("Speech (curl)"), Lang: "shell", Code: curlSpeech},
		{Title: i18n.T("Transcription (curl)"), Lang: "shell", Code: curlSTT},
		{Title: i18n.T("Python (openai)"), Lang: "python", Code: py, File: "audiosmith_demo.py"},
		{Title: i18n.T("JavaScript (openai)"), Lang: "javascript", Code: js, File: "audiosmith-demo.mjs"},
	}
}

func agentsBlocks(e integEnv) []integBlock {
	key := `"not-needed"`
	if e.auth() {
		key = fmt.Sprintf("os.environ[%q]", integKeyEnv)
		if e.Key != "" {
			key = fmt.Sprintf("%q", e.Key)
		}
	}
	py := fmt.Sprintf(`# pip install "openai-agents[voice]" numpy
import asyncio
import os

import numpy as np
from openai import AsyncOpenAI
from agents import Agent, OpenAIChatCompletionsModel, set_tracing_disabled
from agents.voice import (AudioInput, OpenAIVoiceModelProvider, SingleAgentVoiceWorkflow,
                          VoicePipeline, VoicePipelineConfig)

set_tracing_disabled(True)

# Speech in and out: AudioSmith, on this Mac.
audiosmith = AsyncOpenAI(base_url="%s/v1", api_key=%s)
# The conversation: any OpenAI-compatible model (here a local Ollama).
llm = AsyncOpenAI(base_url=os.environ.get("AGENT_LLM_BASE_URL", "http://127.0.0.1:11434/v1"),
                  api_key=os.environ.get("AGENT_LLM_API_KEY", "ollama"))

agent = Agent(
    name="Assistant",
    instructions="You are a helpful assistant. Keep answers short: they are spoken.",
    model=OpenAIChatCompletionsModel(model=os.environ.get("AGENT_LLM_MODEL", "qwen3:4b"), openai_client=llm),
)

pipeline = VoicePipeline(
    workflow=SingleAgentVoiceWorkflow(agent),
    stt_model="gpt-4o-transcribe",
    tts_model="gpt-4o-mini-tts",
    config=VoicePipelineConfig(model_provider=OpenAIVoiceModelProvider(openai_client=audiosmith)),
)


async def main():
    # 3 s of silence: replace with 24 kHz mono int16 samples from a microphone.
    result = await pipeline.run(AudioInput(buffer=np.zeros(24000 * 3, dtype=np.int16)))
    async for event in result.stream():
        if event.type == "voice_stream_event_audio":
            pass  # play event.data (24 kHz mono int16)


asyncio.run(main())
`, e.Base, key)
	return []integBlock{{Title: i18n.T("Voice pipeline"), Hint: i18n.T("AudioSmith hears and speaks; the agent's language model is any OpenAI-compatible server (AGENT_LLM_BASE_URL)."),
		Lang: "python", Code: py, File: "voice_agent.py"}}
}

// n8nWorkflow is an n8n workflow: a manual trigger and an HTTP request to
// /v1/audio/speech whose answer is the binary property "audio".
func n8nWorkflow(e integEnv) string {
	headers := []map[string]string{{"name": "Content-Type", "value": "application/json"}}
	if e.auth() {
		headers = append(headers, map[string]string{"name": "Authorization", "value": "Bearer " + e.keyRef("{{ $env."+integKeyEnv+" }}")})
	}
	wf := obj{
		{"name", "AudioSmith: text to speech"},
		{"nodes", []obj{
			{{"parameters", obj{}}, {"name", "Manual Trigger"}, {"type", "n8n-nodes-base.manualTrigger"}, {"typeVersion", 1}, {"position", []int{0, 0}}},
			{{"parameters", obj{
				{"method", "POST"}, {"url", e.Base + "/v1/audio/speech"},
				{"sendHeaders", true}, {"headerParameters", obj{{"parameters", headers}}},
				{"sendBody", true}, {"specifyBody", "json"},
				{"jsonBody", `{"model": "tts-1", "voice": "alloy", "input": "Hello from n8n and AudioSmith.", "response_format": "wav"}`},
				{"options", obj{
					{"response", obj{{"response", obj{{"responseFormat", "file"}, {"outputPropertyName", "audio"}}}}},
					{"timeout", 1860000},
					{"redirect", obj{{"redirect", obj{{"followRedirects", false}}}}},
				}},
			}}, {"name", "AudioSmith speech"}, {"type", "n8n-nodes-base.httpRequest"}, {"typeVersion", 4.2}, {"position", []int{240, 0}}},
		}},
		{"connections", obj{{"Manual Trigger", obj{{"main", [][]obj{{{{"node", "AudioSmith speech"}, {"type", "main"}, {"index", 0}}}}}}}}},
	}
	return jsonText(wf)
}

func n8nBlocks(e integEnv) []integBlock {
	return []integBlock{{Title: i18n.T("n8n workflow"),
		Hint: i18n.T("Paste it into an n8n canvas (or import the file). If n8n runs in Docker it cannot reach 127.0.0.1: turn on sharing in Settings → Integrations → Local API and use the shared address with the PIN header."),
		Lang: "json", Code: n8nWorkflow(e), File: "audiosmith-n8n.json"}}
}

// integCaption translates a category or capability (literal calls, for
// the key scan).
func integCaption(s string) string {
	switch s {
	case "Agents":
		return i18n.T("Agents")
	case "MCP":
		return "MCP"
	case "Developer tools":
		return i18n.T("Developer tools")
	case "Automation":
		return i18n.T("Automation")
	case "MCP server":
		return i18n.T("MCP server")
	case "Speech API":
		return i18n.T("Speech API")
	case "Transcription API":
		return i18n.T("Transcription API")
	case "Workflow template":
		return i18n.T("Workflow template")
	case "Phone calls": // M6b (thread #23)
		return i18n.T("Phone calls")
	}
	return s
}

// integAbout is a card's one line.
func integAbout(slug string) string {
	switch slug {
	case "claude-code":
		return i18n.T("Let Claude Code use AudioSmith through MCP: speech, voices, audiobooks, dubbing, transcripts.")
	case "cursor":
		return i18n.T("Give Cursor's agent AudioSmith's speech tools.")
	case "codex":
		return i18n.T("Add AudioSmith to Codex's MCP servers.")
	case "mcp":
		return i18n.T("Any MCP client: Streamable HTTP, or the stdio shim.")
	case "openai-sdk":
		return i18n.T("Use AudioSmith wherever OpenAI's audio API works: curl, Python, JavaScript.")
	case "openai-agents":
		return i18n.T("A voice agent whose ears and voice run on this Mac.")
	case "n8n":
		return i18n.T("A workflow that turns text into speech.")
	case "twilio": // M6b (thread #23)
		return i18n.T("An AI agent that places and answers phone calls in your voice.")
	}
	return ""
}

// saveSnippet writes a block to a file the user picks.
func saveSnippet(path, code string) error {
	if filepath.Ext(path) == "" {
		path += ".txt"
	}
	return os.WriteFile(path, []byte(code), 0o644)
}
