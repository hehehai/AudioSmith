package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// blockingWriteCloser blocks Write until Close is called.
type blockingWriteCloser struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingWriteCloser() *blockingWriteCloser {
	return &blockingWriteCloser{closed: make(chan struct{})}
}

func (b *blockingWriteCloser) Write(p []byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingWriteCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

type bufWriteCloser struct {
	bytes.Buffer
}

func (*bufWriteCloser) Close() error { return nil }

func TestWriteAsyncNormal(t *testing.T) {
	var buf bufWriteCloser
	n, err := writeAsync(context.Background(), &buf, []byte("hello\n"), time.Second)
	if err != nil || n != 6 || buf.String() != "hello\n" {
		t.Fatalf("n=%d err=%v buf=%q", n, err, buf.String())
	}
}

func TestWriteAsyncTimeoutClosesWriter(t *testing.T) {
	w := newBlockingWriteCloser()
	start := time.Now()
	n, err := writeAsync(context.Background(), w, []byte("stuck\n"), 50*time.Millisecond)
	if !errors.Is(err, os.ErrDeadlineExceeded) || n == 0 {
		t.Fatalf("n=%d err=%v, want n>0 and ErrDeadlineExceeded", n, err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took too long: %v", time.Since(start))
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("writer was not closed on timeout")
	}
}

func TestWriteAsyncCancelClosesWriter(t *testing.T) {
	w := newBlockingWriteCloser()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	n, err := writeAsync(ctx, w, []byte("stuck\n"), 5*time.Second)
	if !errors.Is(err, os.ErrDeadlineExceeded) || n == 0 {
		t.Fatalf("n=%d err=%v, want n>0 and ErrDeadlineExceeded", n, err)
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("writer was not closed on cancel")
	}
}
