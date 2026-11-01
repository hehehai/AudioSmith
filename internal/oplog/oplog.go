// Package oplog is AudioSmith's operations log (#108): one quiet line per
// change the user (or the Local API, MCP, a job) makes to their data,
// per job's start and end, per download, per error shown, as
//
//	voice.delete id=01J… name="Mara"
//	job.fail id=01J… kind=tts.synthesize code=oom error="out of memory" ms=5123
//
// an event name, then key=value pairs, so lines are greppable. Lines go
// to the app's log (source "ops") through the sink main sets; the log
// redacts secrets and shortens the home folder.
//
// What goes in: IDs, short names (cut to 80 characters), counts, kinds,
// durations, error messages. Never script text, transcripts, prompts or
// secrets: callers pass only those fields, and setting changes are
// logged by name only.
//
// Imports: core, jobs.
package oplog

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Source is the log source of the lines.
const Source = "ops"

var sink atomic.Pointer[func(core.LogEntry)]

// SetSink sends the lines to fn (nil: nowhere) and returns a function
// that restores the sink before.
func SetSink(fn func(core.LogEntry)) (restore func()) {
	var p *func(core.LogEntry)
	if fn != nil {
		p = &fn
	}
	old := sink.Swap(p)
	return func() { sink.Store(old) }
}

// Info logs an event: something done as asked.
func Info(event string, kv ...any) { emit(core.SeverityInfo, event, kv) }

// Warn logs an event that did not do what was asked: a failure the user
// sees, a refusal.
func Warn(event string, kv ...any) { emit(core.SeverityWarning, event, kv) }

// Err logs event at Warn with "error"=err when err is not nil, at Info
// otherwise: a step whose failure is otherwise swallowed.
func Err(event string, err error, kv ...any) {
	if err == nil {
		emit(core.SeverityInfo, event, kv)
		return
	}
	emit(core.SeverityWarning, event, append(kv, "error", err))
}

func emit(level core.Severity, event string, kv []any) {
	p := sink.Load()
	if p == nil {
		return
	}
	(*p)(core.LogEntry{Time: time.Now(), Level: level, Source: Source, Message: Line(event, kv...)})
}

// maxName and maxError bound a value's length: a name, an error message.
const (
	maxName  = 80
	maxError = 300
)

// Msg is a message to log whole (up to 300 characters, as an error's),
// not cut as a name: a toast's text.
type Msg string

// Line formats an event and its pairs: "event k=v k2="v 2"". Strings are
// cut to 80 characters, errors to 300, on one line; durations are in
// milliseconds; a value with spaces, quotes or "=" is quoted.
func Line(event string, kv ...any) string {
	var b strings.Builder
	b.WriteString(event)
	for i := 0; i < len(kv); i += 2 {
		key := fmt.Sprint(kv[i])
		var v any = "(missing)"
		if i+1 < len(kv) {
			v = kv[i+1]
		}
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value(v))
	}
	return b.String()
}

func value(v any) string {
	switch x := v.(type) {
	case nil:
		return `""`
	case error:
		return quote(clip(x.Error(), maxError))
	case Msg:
		return quote(clip(string(x), maxError))
	case time.Duration:
		return strconv.FormatInt(x.Milliseconds(), 10)
	case string:
		return quote(clip(x, maxName))
	case bool, int, int64, int32, uint, uint64, float32:
		return fmt.Sprint(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case fmt.Stringer:
		return quote(clip(x.String(), maxName))
	}
	return quote(clip(fmt.Sprint(v), maxName))
}

// clip puts s on one line and cuts it to n characters, with "…".
func clip(s string, n int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == '\t' }), " ")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '"' || r == '=' || r == '\\' }) < 0 {
		return s
	}
	return strconv.Quote(s)
}

// WithActor marks ctx as a request of who: "api", "lan", "mcp", "quit".
// Services log it as "by", and a job cancelled with it says who
// cancelled it (jobs.WithActor).
func WithActor(ctx context.Context, who string) context.Context { return jobs.WithActor(ctx, who) }

// Actor is who ctx is a request of: "app" (the window, or the app's own
// work) unless WithActor said.
func Actor(ctx context.Context) string {
	if who := jobs.ActorOf(ctx); who != "" {
		return who
	}
	return "app"
}
