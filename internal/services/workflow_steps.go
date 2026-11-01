package services

// The steps of a workflow run: each job takes its step's items one after
// another, reads each item's input from the step before it (the branch a
// condition took), makes the result and saves it with its key. Each
// kind of step is in workflow_step_kinds.go, the results in
// workflow_outputs.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/resample"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/translate"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfStep is one step's job at work.
type wfStep struct {
	s      *Workflows
	job    jobs.Job
	report func(jobs.Progress)
	w      *workflow.Workflow
	n      *workflow.Node
	runID  string
	in     workflow.Type // the type its input receives
}

// out is a result file's name for an item, relative to the folder.
func (st *wfStep) out(item, ext string) string { return "out/" + item + "/" + st.n.ID + "." + ext }

func (st *wfStep) path(name string) string { return st.s.Path(st.w.ID, name) }

// step runs a workflow.step job.
func (s *Workflows) step(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var p workflowStepParams
	if err := json.Unmarshal(job.Params, &p); err != nil || p.Workflow == "" || p.Node == "" {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "workflow.step: bad params"}
	}
	w, run, err := s.Get(ctx, p.Workflow)
	if err != nil {
		return nil, err
	}
	if run == nil || run.ID != p.Run || run.State != workflow.RunRunning {
		return json.RawMessage(`{"stale":true}`), nil // a run since replaced or stopped
	}
	n := w.Node(p.Node)
	if n == nil {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "workflow.step: no step " + p.Node}
	}
	rep := workflow.Validate(w, nil)
	st := &wfStep{s: s, job: job, report: report, w: w, n: n, runID: p.Run, in: rep.In[n.ID]}
	total := len(run.Items)
	for i := range run.Items {
		item := run.Items[i]
		cur := item.Steps[n.ID]
		if cur == nil || cur.Finished() || cur.Status == workflow.StepFailed {
			// Done, or failed in an earlier attempt of this job (a retry).
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report(jobs.Progress{Stage: string(n.Kind), Fraction: float64(i) / float64(total), Message: fmt.Sprintf("%d/%d", i+1, total)})
		// The item's input, from the run as saved now.
		_, latest, err := s.Get(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		if latest == nil || latest.ID != p.Run {
			return json.RawMessage(`{"stale":true}`), nil
		}
		ri := latest.Item(item.Key)
		if ri == nil {
			continue
		}
		in, skip := wfInput(w, ri, n.ID)
		if skip != "" {
			st.save(ctx, item.Key, func(x *workflow.Step) { x.Status, x.Code, x.Out = workflow.StepSkipped, skip, nil })
			continue
		}
		if n.Kind.IsInput() {
			in = &workflow.Value{Type: workflow.TypeAudio, File: ri.Path}
		}
		if in == nil {
			st.save(ctx, item.Key, func(x *workflow.Step) {
				x.Status, x.Code, x.Err = workflow.StepFailed, WorkflowCodeNoInput, "no input"
			})
			continue
		}
		st.save(ctx, item.Key, func(x *workflow.Step) { x.Status, x.Code, x.Err, x.Warnings = workflow.StepRunning, "", "", nil })
		started := time.Now()
		res, err := st.run(ctx, ri, i, in)
		if err != nil {
			if ctx.Err() != nil {
				st.save(context.WithoutCancel(ctx), item.Key, func(x *workflow.Step) { x.Status = workflow.StepPending })
				return nil, ctx.Err()
			}
			var je *jobs.Error
			if !errors.As(err, &je) {
				je = &jobs.Error{Code: WorkflowCodeFile, Message: err.Error()}
			}
			st.save(ctx, item.Key, func(x *workflow.Step) {
				x.Status, x.Code, x.Err, x.Seconds, x.At = workflow.StepFailed, je.Code, je.Message, time.Since(started).Seconds(), s.cfg.Now()
			})
			if wfFatal(je.Code) {
				// The step cannot work at all: the run stops (the steps
				// after this one fail with the job).
				s.update(context.WithoutCancel(ctx), w.ID, p.Run, func(r *workflow.Run) { r.Code, r.Err = je.Code, je.Message })
				return nil, je
			}
			continue
		}
		st.save(ctx, item.Key, func(x *workflow.Step) {
			x.Status, x.Out, x.Branch, x.Warnings = workflow.StepDone, res.out, res.branch, res.warnings
			x.Code, x.Err, x.Seconds, x.At = "", "", time.Since(started).Seconds(), s.cfg.Now()
		})
	}
	report(jobs.Progress{Stage: string(n.Kind), Fraction: 1})
	return json.RawMessage(`{}`), nil
}

// wfFatal reports whether an error stops the run: every other item would
// fail the same way.
func wfFatal(code string) bool {
	switch code {
	case proto.CodeModelNotDownloaded, proto.CodeLicenceRequired, CodeEngineUnavailable, proto.CodeOOM, proto.CodeModelLoadFailed,
		jobs.CodeInterrupted, jobs.CodeShutdown, proto.CodeCancelled, proto.CodeMethodNotFound, WorkflowCodeNoSTT, WorkflowCodeEncode,
		translate.CodeNoLLM, translate.CodeNoKey, translate.CodeAuth, translate.CodeQuota, translate.CodeConfig:
		return true
	}
	return false
}

// wfInput is an item's input at step id: the value of the first link in
// whose step is done (a condition's on the branch it took), or why the
// step is skipped.
func wfInput(w *workflow.Workflow, ri *workflow.RunItem, id string) (*workflow.Value, string) {
	in := w.Incoming(id)
	if len(in) == 0 {
		return nil, ""
	}
	skip := workflow.SkipBranch
	for _, e := range in {
		src := ri.Steps[e.From]
		if src == nil {
			continue
		}
		switch src.Status {
		case workflow.StepDone:
			if w.Node(e.From).Kind == workflow.KindCondition && src.Branch != e.FromPort {
				continue
			}
			return src.Out, ""
		case workflow.StepFailed:
			skip = workflow.SkipUpstream
		case workflow.StepSkipped:
			if src.Code == workflow.SkipUpstream {
				skip = workflow.SkipUpstream
			}
		default:
			return nil, "" // not made: the job before this one stopped
		}
	}
	return nil, skip
}

// save applies fn to the item's step in the saved run.
func (st *wfStep) save(ctx context.Context, item string, fn func(*workflow.Step)) {
	_, err := st.s.update(ctx, st.w.ID, st.runID, func(r *workflow.Run) {
		if ri := r.Item(item); ri != nil {
			if x := ri.Steps[st.n.ID]; x != nil {
				fn(x)
			}
		}
	})
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		st.s.cfg.Logf("workflow %s: saving step %s of %s: %v", st.w.ID, st.n.ID, item, err)
	}
}

type wfResult struct {
	out      *workflow.Value
	branch   string
	warnings []string
}

// run makes one item's result.
func (st *wfStep) run(ctx context.Context, ri *workflow.RunItem, index int, in *workflow.Value) (wfResult, error) {
	switch st.n.Kind {
	case workflow.KindSource:
		return st.source(ctx, ri, in)
	case workflow.KindSpeak:
		return st.speak(ctx, ri, in)
	case workflow.KindTranscribe:
		return st.transcribe(ctx, ri, in)
	case workflow.KindTranslate:
		return st.translate(ctx, ri, in)
	case workflow.KindConvert:
		return st.convert(ctx, ri, in)
	case workflow.KindNormalize:
		return st.normalize(ctx, ri, in)
	case workflow.KindCondition:
		txt, err := st.readText(in)
		if err != nil {
			return wfResult{}, err
		}
		b := workflow.PortNo
		if workflow.Evaluate(st.n.P.Match, st.n.P.Text, txt) {
			b = workflow.PortYes
		}
		out := *in
		return wfResult{out: &out, branch: b}, nil
	case workflow.KindExport:
		return st.export(ctx, ri, index, in)
	}
	return wfResult{}, &jobs.Error{Code: proto.CodeBadRequest, Message: "unknown step " + string(st.n.Kind)}
}

func wfErr(code, format string, args ...any) error {
	return &jobs.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// --- values ---------------------------------------------------------------------------

// readText reads a text value, or a transcript's text.
func (st *wfStep) readText(v *workflow.Value) (string, error) {
	data, err := os.ReadFile(st.path(v.File))
	if err != nil {
		return "", wfErr(WorkflowCodeFile, "%v", err)
	}
	if v.Type == workflow.TypeTranscript {
		var t core.Transcript
		if err := json.Unmarshal(data, &t); err != nil {
			return "", wfErr(WorkflowCodeFile, "transcript: %v", err)
		}
		return t.Preferred(), nil
	}
	return string(data), nil
}

func (st *wfStep) readTranscript(v *workflow.Value) (core.Transcript, error) {
	var t core.Transcript
	data, err := os.ReadFile(st.path(v.File))
	if err != nil {
		return t, wfErr(WorkflowCodeFile, "%v", err)
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return t, wfErr(WorkflowCodeFile, "transcript: %v", err)
	}
	return t, nil
}

// textValue writes a text result.
func (st *wfStep) textValue(item, s, lang string) (*workflow.Value, error) {
	name := st.out(item, "txt")
	if err := wfWriteFile(st.path(name), []byte(s)); err != nil {
		return nil, err
	}
	return &workflow.Value{Type: workflow.TypeText, File: name, Text: workflow.Excerpt(s, 400), Chars: utf8.RuneCountInString(s), Lang: lang}, nil
}

// transcriptValue writes a transcript result.
func (st *wfStep) transcriptValue(item string, t core.Transcript) (*workflow.Value, error) {
	name := st.out(item, "json")
	data, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}
	if err := wfWriteFile(st.path(name), data); err != nil {
		return nil, err
	}
	txt := t.Preferred()
	return &workflow.Value{Type: workflow.TypeTranscript, File: name, Text: workflow.Excerpt(txt, 400), Chars: utf8.RuneCountInString(txt),
		Seconds: round3(t.Duration), Lang: t.Language}, nil
}

// audioValue describes an audio result.
func (st *wfStep) audioValue(name string) (*workflow.Value, error) {
	f, err := os.Open(st.path(name))
	if err != nil {
		return nil, wfErr(WorkflowCodeFile, "%v", err)
	}
	defer f.Close()
	rd, err := wav.NewReader(f)
	if err != nil {
		return nil, wfErr(WorkflowCodeFile, "%v", err)
	}
	fm := rd.Format()
	secs := 0.0
	if fm.Rate > 0 {
		secs = float64(rd.Frames()) / float64(fm.Rate)
	}
	if secs <= 0 {
		return nil, wfErr(WorkflowCodeEmpty, "The audio is empty.")
	}
	return &workflow.Value{Type: workflow.TypeAudio, File: name, Seconds: round3(secs)}, nil
}

// --- engine calls -------------------------------------------------------------------------

// call runs one engine method inside the job (as Transcripts.call).
func (st *wfStep) call(ctx context.Context, method, model string, params any, modelPath *string, out any, report func(jobs.Progress)) error {
	s := st.s
	if model != "" && modelPath != nil && s.cfg.ModelPath != nil {
		dir, err := s.cfg.ModelPath(ctx, model)
		if err != nil {
			if errors.Is(err, core.ErrNotDownloaded) {
				data, _ := json.Marshal(map[string]string{"model": model})
				return &jobs.Error{Code: proto.CodeModelNotDownloaded, Message: err.Error(), Data: data}
			}
			return &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
		}
		*modelPath = dir
	}
	var ex jobs.Executor
	if s.cfg.Engine != nil {
		ex = s.cfg.Engine(method)
	}
	if ex == nil {
		return &jobs.Error{Code: CodeEngineUnavailable, Message: "The engine is not connected.", Retryable: true}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if report == nil {
		report = func(jobs.Progress) {}
	}
	role, m := proto.SplitKind(method)
	sub := jobs.Job{ID: st.job.ID + "." + m, Spec: jobs.Spec{Kind: method, Lane: jobs.Lane(role), Params: raw, Owner: st.job.Owner}}
	res, err := ex.Run(ctx, sub, report, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return json.Unmarshal(res, out)
}

// decode writes an audio file as a WAV at rate and channels (0 keeps
// the source's): in Go for WAV files, else with the engine.
func (st *wfStep) decode(ctx context.Context, src, dst string, rate, ch int) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(src), ".wav") {
		b, _, err := wav.ReadFile(src)
		if err == nil {
			if rate > 0 || ch > 0 {
				b = resample.Convert(b, dubFirstInt(rate, b.Rate), dubFirstInt(ch, b.Channels), resample.Good)
			}
			return writeWAVFloat(dst, b)
		}
		if os.IsNotExist(err) {
			return wfErr(proto.CodeFileNotFound, "%v", err)
		}
	}
	if _, err := os.Stat(src); err != nil {
		return wfErr(proto.CodeFileNotFound, "%v", err)
	}
	p := proto.DecodeParams{Input: src, Output: proto.Output{Path: dst, Format: "wav", Bits: 16}, SampleRate: rate, Channels: ch}
	var dr proto.DecodeResult
	return st.call(ctx, proto.MethodMediaDecode, "", &p, nil, &dr, nil)
}

func dubFirstInt(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}
