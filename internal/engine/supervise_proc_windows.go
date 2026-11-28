//go:build windows

package engine

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type osProc struct {
	jobMu sync.Mutex
	job   windows.Handle
}

func configureEngineCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// attachOS binds the child engine process to a Windows Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so the kernel terminates the entire
// engine process tree if AudioSmith crashes or is killed.
func (p *proc) attachOS(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		p.s.cfg.Logf("engine %s: CreateJobObject: %v", p.w.role, err)
		return
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		p.s.cfg.Logf("engine %s: SetInformationJobObject: %v", p.w.role, err)
		_ = windows.CloseHandle(job)
		return
	}
	procHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		p.s.cfg.Logf("engine %s: OpenProcess(%d): %v", p.w.role, cmd.Process.Pid, err)
		_ = windows.CloseHandle(job)
		return
	}
	err = windows.AssignProcessToJobObject(job, procHandle)
	_ = windows.CloseHandle(procHandle)
	if err != nil {
		p.s.cfg.Logf("engine %s: AssignProcessToJobObject(%d): %v", p.w.role, cmd.Process.Pid, err)
		_ = windows.CloseHandle(job)
		return
	}
	p.jobMu.Lock()
	p.job = job
	p.jobMu.Unlock()
}

func (p *proc) killOS() {
	p.jobMu.Lock()
	job := p.job
	p.jobMu.Unlock()
	if job != 0 {
		if err := windows.TerminateJobObject(job, 1); err == nil {
			return
		}
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (p *proc) cleanupOS() {
	p.jobMu.Lock()
	job := p.job
	p.job = 0
	p.jobMu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}

// writePipe uses writeAsync on Windows because anonymous os.Pipe handles
// do not support SetWriteDeadline.
func (p *proc) writePipe(ctx context.Context, line []byte) (int, error) {
	return writeAsync(ctx, p.stdin, line, p.s.cfg.WriteTimeout)
}
