//go:build !windows

package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

type osProc struct{}

func configureEngineCmd(*exec.Cmd) {}

func (*proc) attachOS(*exec.Cmd) {}

func (*proc) cleanupOS() {}

func (p *proc) killOS() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
}

func (p *proc) writePipe(ctx context.Context, line []byte) (int, error) {
	f, ok := p.stdin.(interface{ SetWriteDeadline(time.Time) error })
	if !ok {
		return writeAsync(ctx, p.stdin, line, p.s.cfg.WriteTimeout)
	}
	dl := time.Now().Add(p.s.cfg.WriteTimeout)
	if err := f.SetWriteDeadline(dl); errors.Is(err, os.ErrNoDeadline) {
		return writeAsync(ctx, p.stdin, line, p.s.cfg.WriteTimeout)
	}
	// ctx ending stops the write at once; wait for that to be done
	// before the next write sets its own deadline.
	expired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(expired)
		_ = f.SetWriteDeadline(time.Now())
	})
	n, err := p.stdin.Write(line)
	if !stop() {
		<-expired
	}
	return n, err
}
