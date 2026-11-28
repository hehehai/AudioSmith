package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/engine-win/server"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

func TestServer_NotInitializedAndProtocolMismatch(t *testing.T) {
	srv := server.New(server.Config{Role: proto.RoleGPU, Version: "test", Hardware: hw.Probe()})
	reqs := []proto.Message{
		mustReq(t, "c0", server.MethodPing, nil),
		mustReq(t, "c1", proto.MethodHello, proto.HelloParams{Protocol: 99, Role: proto.RoleGPU}),
		mustReq(t, "c2", proto.MethodHello, proto.HelloParams{Protocol: proto.Version, Role: proto.RoleGPU}),
		mustReq(t, "c3", "unknown.method", nil),
		mustReq(t, "c4", proto.MethodShutdown, nil),
	}
	var in bytes.Buffer
	for _, r := range reqs {
		b, _ := proto.Encode(r)
		in.Write(b)
	}
	var out bytes.Buffer
	if err := srv.Serve(&in, &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}
	byID := map[string]proto.Message{}
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		m, err := proto.Decode([]byte(line))
		if err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		byID[m.ID] = m
	}
	if byID["c0"].Error == nil || byID["c0"].Error.Code != proto.CodeNotInitialized {
		t.Fatalf("c0 = %+v, want not_initialized", byID["c0"])
	}
	if byID["c1"].Error == nil || byID["c1"].Error.Code != proto.CodeProtocolMismatch {
		t.Fatalf("c1 = %+v, want protocol_mismatch", byID["c1"])
	}
	if byID["c2"].Error != nil {
		t.Fatalf("c2 error = %v", byID["c2"].Error)
	}
	if byID["c3"].Error == nil || byID["c3"].Error.Code != proto.CodeMethodNotFound {
		t.Fatalf("c3 = %+v, want method_not_found", byID["c3"])
	}
}

func TestSupervisorIntegration_RealBinaryAllRoles(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "audiosmith-engine")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	build := exec.Command("go", "build", "-o", bin, "../cmd/audiosmith-engine")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build audiosmith-engine failed: %v\n%s", err, out)
	}

	sup := engine.NewSupervisor(engine.Config{
		Bin:          bin,
		Roles:        []proto.Role{proto.RoleGPU, proto.RoleMedia, proto.RoleListen},
		HelloTimeout: 10 * time.Second,
		StopTimeout:  3 * time.Second,
		StatsEvery:   -1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatalf("Supervisor.Start: %v", err)
	}
	defer sup.Stop()

	// 1. Verify gpu role engine.info & engine.ping via real Supervisor.
	raw, err := sup.Call(ctx, proto.RoleGPU, "", server.MethodInfo, nil, nil)
	if err != nil {
		t.Fatalf("gpu engine.info: %v", err)
	}
	var info server.InfoResult
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("unmarshal engine.info: %v", err)
	}
	if info.Role != proto.RoleGPU || info.Hardware.GPUTier == "" {
		t.Fatalf("unexpected gpu engine.info: %+v", info)
	}

	// 2. Verify media role real WAV probe + decode roundtrip via real Supervisor.
	inWAV := filepath.Join(dir, "sample.wav")
	outWAV := filepath.Join(dir, "sample_16k.wav")
	w, err := wav.Create(inWAV, wav.Format{Rate: 24000, Channels: 1, Encoding: wav.PCM16}, wav.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Write(make([]float32, 2400))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err = sup.Call(ctx, proto.RoleMedia, "", proto.MethodMediaProbe, proto.ProbeParams{Input: inWAV}, nil)
	if err != nil {
		t.Fatalf("media.probe: %v", err)
	}
	var pr proto.ProbeResult
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatalf("unmarshal media.probe: %v", err)
	}
	if !pr.HasAudio || pr.SampleRate != 24000 {
		t.Fatalf("unexpected media.probe: %+v", pr)
	}

	raw, err = sup.Call(ctx, proto.RoleMedia, "j-dec-1", proto.MethodMediaDecode, proto.DecodeParams{
		Input:      inWAV,
		Output:     proto.Output{Path: outWAV, Format: "wav", Bits: 16},
		SampleRate: 16000,
		Channels:   1,
	}, nil)
	if err != nil {
		t.Fatalf("media.decode: %v", err)
	}
	var dec proto.DecodeResult
	if err := json.Unmarshal(raw, &dec); err != nil {
		t.Fatalf("unmarshal media.decode: %v", err)
	}
	if dec.SampleRate != 16000 || dec.Channels != 1 || dec.Frames == 0 {
		t.Fatalf("unexpected media.decode: %+v", dec)
	}

	// 3. Verify listen role capture.devices via real Supervisor.
	raw, err = sup.Call(ctx, proto.RoleListen, "", proto.MethodCaptureDevices, nil, nil)
	if err != nil {
		t.Fatalf("capture.devices: %v", err)
	}
	var devs proto.DevicesResult
	if err := json.Unmarshal(raw, &devs); err != nil {
		t.Fatalf("unmarshal capture.devices: %v", err)
	}
}

func mustReq(t *testing.T, id, method string, params any) proto.Message {
	t.Helper()
	m, err := proto.NewRequest(id, method, params)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
