package main

// TestV02's Workflow, Local API, MCP stdio, watermark and crash steps;
// split from e2e_release_test.go.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// workflowFlow: the "Translate and narrate" template on two recordings,
// into Chinese, spoken by Kokoro, exported to a folder.
func (v *v02) workflowFlow(t *testing.T) string {
	a := v.a
	clips := []string{
		say(t, v.dir, "wf1", "Samantha", "The museum opens at nine in the morning and closes at six.", 24000),
		say(t, v.dir, "wf2", "Daniel", "Tickets are free for children under twelve years old.", 24000),
	}
	a.navigate("/workflows")
	v.drain()
	a.wfNew("translate-narrate")
	v.drain()
	w := a.wf
	if w == nil {
		t.Fatal("no workflow")
	}
	outDir := filepath.Join(v.dir, "workflow-out")
	os.MkdirAll(outDir, 0o755)
	kokoro := v.model(t, "kokoro-82m-bf16").Repo
	var src string
	a.wfEdit(func(w *workflow.Workflow) {
		for i := range w.Nodes {
			n := &w.Nodes[i]
			switch n.Kind {
			case workflow.KindSource:
				src = n.ID
			case workflow.KindTranslate:
				n.P.To = "zh"
			case workflow.KindSpeak:
				n.P.Model, n.P.Voice, n.P.Language = kokoro, services.SpeakerRef+"zf_xiaobei", "zh"
			case workflow.KindExport:
				n.P.Folder = outDir
			}
		}
	})
	a.wfClipFiles(src, clips)
	v.drain()
	if rep := a.wfReport(); !rep.OK() {
		t.Fatalf("invalid: %+v", rep)
	}
	started := time.Now()
	a.wfStart(false)
	v.wait(t, "workflow run", 40*time.Minute, func() bool {
		return a.wfRun != nil && a.wfRun.State != workflow.RunRunning && a.wfRun.State != workflow.RunIdle && !a.wfBusy()
	})
	run := a.wfRun
	if run.State != workflow.RunDone {
		t.Errorf("run %s %s %s", run.State, run.Code, run.Err)
	}
	files, _ := filepath.Glob(filepath.Join(outDir, "*"))
	outs := services.Outputs(a.wf, run)
	if len(outs) != 2 || len(files) != 2 {
		t.Errorf("outputs %d, files %v", len(outs), files)
	}
	var said []string
	for _, f := range files {
		if strings.HasSuffix(f, ".wav") || strings.HasSuffix(f, ".m4a") {
			said = append(said, v.sttCheck(t, f, "zh"))
		}
	}
	for _, s := range said {
		if !hanRE.MatchString(s) {
			t.Errorf("narration not Chinese: %q", s)
		}
	}
	return fmt.Sprintf("2 clips → %d files in %.0f s; heard %q", len(files), time.Since(started).Seconds(), said)
}

// apiFlow: the Local API on a test port: health, models, speech (wav)
// and transcription with curl, the openai SDK when there is a python
// with it, and the MCP stdio shim of the built app.
func (v *v02) apiFlow(t *testing.T, data string) string {
	a := v.a
	port := 3917
	a.updateSettings(func(s *settings.Settings) { s.API.Enabled, s.API.Port = true, port })
	v.drain()
	a.startAPI(data)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	v.wait(t, "api up", 30*time.Second, func() bool { return a.apiPort == port || a.apiErr != "" })
	if a.apiErr != "" {
		t.Fatalf("api: %s", a.apiErr)
	}
	var notes []string
	curl := func(what string, args ...string) (string, error) {
		var out []byte
		err := v.do(t, what, 10*time.Minute, func() error {
			var err error
			out, err = exec.Command("curl", append([]string{"-sS", "--noproxy", "*", "-w", "\n%{http_code} %{time_total}"}, args...)...).CombinedOutput()
			return err
		})
		return string(out), err
	}
	out, err := curl("health", base+"/health")
	if err != nil || !strings.Contains(out, "\n200 ") {
		t.Errorf("health: %v %s", err, out)
	}
	out, err = curl("models", base+"/v1/models")
	if err != nil || !strings.Contains(out, "\n200 ") || !strings.Contains(out, "tts-1") {
		t.Errorf("models: %v %.300s", err, out)
	}
	speech := filepath.Join(v.dir, "api-speech.wav")
	out, err = curl("speech", "-o", speech, "-H", "Content-Type: application/json", "-d",
		`{"model":"tts-1","voice":"alloy","input":"Hello from the AudioSmith local API.","response_format":"wav"}`, base+"/v1/audio/speech")
	if err != nil || !strings.Contains(out, "200 ") || wavBits(speech) == 0 {
		t.Errorf("speech: %v %s", err, out)
	}
	notes = append(notes, "curl speech "+strings.TrimSpace(out))
	out, err = curl("transcription", "-F", "file=@"+speech, "-F", "model=whisper-1", base+"/v1/audio/transcriptions")
	if err != nil || !strings.Contains(strings.ToLower(out), "local api") {
		t.Errorf("transcription: %v %s", err, out)
	}
	notes = append(notes, "curl transcription "+strings.ReplaceAll(strings.TrimSpace(out), "\n", " "))
	if py := os.Getenv("AUDIOSMITH_V02_PY"); py != "" {
		script := filepath.Join(v.dir, "openai_check.py")
		os.WriteFile(script, []byte(`import sys, time
from openai import OpenAI
c = OpenAI(base_url=sys.argv[1] + "/v1", api_key="local")
t = time.time()
with c.audio.speech.with_streaming_response.create(model="gpt-4o-mini-tts", voice="nova", input="The official OpenAI SDK works with AudioSmith.", response_format="wav") as r:
    r.stream_to_file(sys.argv[2])
print("speech", round(time.time() - t, 2))
t = time.time()
tr = c.audio.transcriptions.create(model="whisper-1", file=open(sys.argv[2], "rb"))
print("transcription", round(time.time() - t, 2), tr.text)
tr = c.audio.transcriptions.create(model="whisper-1", file=open(sys.argv[2], "rb"), response_format="srt")
print("srt", "-->" in tr)
`), 0o644)
		var pyOut []byte
		err := v.do(t, "openai sdk", 10*time.Minute, func() error {
			cmd := exec.Command(py, "-I", script, base, filepath.Join(v.dir, "sdk-speech.wav"))
			cmd.Env = append(os.Environ(), "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
			var err error
			pyOut, err = cmd.CombinedOutput()
			return err
		})
		s := strings.TrimSpace(string(pyOut))
		if err != nil || !strings.Contains(strings.ToLower(s), "openai sdk") || !strings.Contains(s, "srt True") {
			t.Errorf("openai sdk: %v\n%s", err, s)
		}
		notes = append(notes, "openai-python: "+strings.ReplaceAll(s, "\n", "; "))
	}
	if exe := os.Getenv("AUDIOSMITH_V02_APP"); exe != "" {
		notes = append(notes, v.mcpStdio(t, exe, base))
	}
	return strings.Join(notes, " · ")
}

// mcpStdio talks MCP to the app's stdio shim: initialize, list_voices,
// generate_speech.
func (v *v02) mcpStdio(t *testing.T, exe, base string) string {
	cmd := exec.Command(exe, "--mcp-stdio", "--client", "v02-test")
	cmd.Env = append(os.Environ(), "AUDIOSMITH_API_URL="+base, "NO_PROXY=127.0.0.1")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Errorf("mcp: %v", err)
		return "mcp: " + err.Error()
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	rd := bufio.NewReaderSize(stdout, 1<<20)
	call := func(id int, method string, params any) (map[string]any, error) {
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		var res map[string]any
		err := v.do(t, "mcp "+method, 10*time.Minute, func() error {
			if _, err := stdin.Write(append(msg, '\n')); err != nil {
				return err
			}
			for {
				line, err := rd.ReadBytes('\n')
				if err != nil {
					return fmt.Errorf("%v (stderr %s)", err, stderr.String())
				}
				var m map[string]any
				if json.Unmarshal(line, &m) == nil && m["id"] == float64(id) {
					res = m
					return nil
				}
			}
		})
		return res, err
	}
	started := time.Now()
	init, err := call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "v02-test", "version": "1"}})
	if err != nil || init["result"] == nil {
		t.Errorf("mcp initialize: %v %v", err, init)
		return "mcp initialize failed"
	}
	stdin.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	tools, _ := call(2, "tools/list", map[string]any{})
	lv, err := call(3, "tools/call", map[string]any{"name": "list_voices", "arguments": map[string]any{}})
	lvs, _ := json.Marshal(lv)
	if err != nil || !strings.Contains(string(lvs), "V02") && !strings.Contains(string(lvs), "af_heart") {
		t.Errorf("list_voices: %v %.400s", err, lvs)
	}
	gs, err := call(4, "tools/call", map[string]any{"name": "generate_speech", "arguments": map[string]any{"text": "Hello from MCP over stdio."}})
	gss, _ := json.Marshal(gs)
	if err != nil || !strings.Contains(string(gss), "audio/wav") && !strings.Contains(string(gss), ".wav") {
		t.Errorf("generate_speech: %v %.400s", err, gss)
	}
	nt := 0
	if r, ok := tools["result"].(map[string]any); ok {
		ts, _ := r["tools"].([]any)
		nt = len(ts)
	}
	return fmt.Sprintf("MCP stdio: %d tools, list_voices %d B, generate_speech %d B in %.1f s", nt, len(lvs), len(gss), time.Since(started).Seconds())
}

// watermarkFlow: AudioSeal finds the mark in a Studio take and in the
// API's take, not in macOS speech.
func (v *v02) watermarkFlow(t *testing.T, takeID string) string {
	a := v.a
	if takeID == "" && a.lastClip() != nil {
		takeID = a.lastClip().ID
	}
	take, err := a.lib.svc.Takes.Get(context.Background(), takeID)
	if err != nil {
		t.Fatalf("no take to check: %v", err)
	}
	clean := say(t, v.dir, "clean", "Samantha", "This recording was never watermarked by AudioSmith.", 24000)
	var notes []string
	for _, c := range []struct {
		name, path string
		want       bool
	}{{"take", a.takeStore.AudioPath(take), true}, {"say", clean, false}} {
		a.checkWatermark(c.path)
		v.wait(t, "detect "+c.name, 5*time.Minute, func() bool { return !a.wmCheck.busy })
		if a.wmCheck.err != "" || a.wmCheck.res == nil {
			t.Fatalf("%s: %s", c.name, a.wmCheck.err)
		}
		r := a.wmCheck.res
		notes = append(notes, fmt.Sprintf("%s: detected %v, p=%.2f, message %d, %.0f s in %.2f s", c.name, r.Detected, r.Probability, r.Message, r.AudioSeconds, r.Seconds))
		if r.Detected != c.want {
			t.Errorf("%s: detected %v, want %v (%+v)", c.name, r.Detected, c.want, *r)
		}
	}
	return strings.Join(notes, "; ")
}

// crashFlow: the gpu worker killed in the middle of a render; the
// supervisor restarts it, the job is retried or fails cleanly, and the
// next take works.
func (v *v02) crashFlow(t *testing.T) string {
	a := v.a
	a.selectModel(v.model(t, "kokoro-82m-bf16"))
	a.studioVoice, a.voice, a.text = "", "af_heart", longText(enSentences, 2500)
	a.generate()
	v.wait(t, "rendering", 2*time.Minute, func() bool { return a.job != nil && a.job.fraction > 0.05 })
	pid := 0
	for _, w := range a.workers {
		if w.Role == proto.RoleGPU {
			pid = w.PID
		}
	}
	if pid == 0 {
		t.Fatal("no gpu worker pid")
	}
	v.restarting = true
	defer func() { v.restarting = false }()
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
	killed := time.Now()
	v.wait(t, "after the crash", 15*time.Minute, func() bool { return a.job == nil && a.awaitTake == "" })
	outcome := "retried: take made"
	if a.jobError != "" {
		outcome = "failed: " + a.jobError
	}
	restarts := 0
	for _, w := range a.workers {
		if w.Role == proto.RoleGPU {
			restarts = w.Restarts
		}
	}
	v.wait(t, "engine back", 2*time.Minute, func() bool { return a.engState == engineReady })
	a.text = "After the crash, AudioSmith keeps working."
	take := v.generate(t, "after crash", 5*time.Minute)
	return fmt.Sprintf("killed pid %d; %s after %.0f s; restarts %d; next take %.1f s", pid, outcome, time.Since(killed).Seconds(), restarts, take.AudioSeconds)
}
