//go:build unix

package dub

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// #101 L4: yt-dlp runs left by an earlier AudioSmith (started from its
// tools folder, not this process's children) are killed with their
// group; this process's own runs and other programs are not.
func TestKillOrphans(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	data, _ := os.ReadFile(sleep)
	dir := filepath.Join(t.TempDir(), "Application Support", "tools")
	os.MkdirAll(dir, 0o755)
	fake := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(fake, data, 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(bin string) *exec.Cmd {
		cmd := exec.Command(bin, "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		return cmd
	}
	ours, other := start(fake), start(sleep)
	// ps shows a child by the command it runs only once it has exec'd:
	// wait for that, or a slow runner sees the test binary's own name.
	waitExec := func(cmd *exec.Cmd, bin string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			out, _ := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(cmd.Process.Pid)).Output()
			if strings.HasPrefix(strings.TrimSpace(string(out)), bin) {
				return
			}
		}
		t.Fatalf("%s did not start", bin)
	}
	waitExec(ours, fake)
	waitExec(other, sleep)
	if got := killOrphans(dir, os.Getpid()); len(got) != 0 {
		t.Fatalf("killed this process's own run: %v", got)
	}
	got := killOrphans(dir, -1) // as if an earlier app had started it
	if !slices.Contains(got, ours.Process.Pid) || slices.Contains(got, other.Process.Pid) {
		t.Fatalf("killed %v (ours %d, other %d)", got, ours.Process.Pid, other.Process.Pid)
	}
	done := make(chan error, 1)
	go func() { done <- ours.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the orphan still runs")
	}
	if other.ProcessState != nil {
		t.Error("the other program was killed")
	}
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the other program: %v", err)
	}
}

// #101 L4: yt-dlp that exits well while something it started still holds
// its output (past WaitDelay) is not a failed run.
func TestYtDlpWaitDelayIsNotFailure(t *testing.T) {
	script := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 4 &\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := YtDlp{Path: script}.run(t.Context(), nil, nil)
	if err != nil || string(out) != "done\n" {
		t.Fatalf("%q %v", out, err)
	}
}
