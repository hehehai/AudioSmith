package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/dub/dubtest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/translate"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfFakeSched is the fake scheduler for workflows: a graph's jobs are
// all queued before the first runs (as the real scheduler holds them
// waiting), and List shows the unfinished ones, so that a run closes
// after its last job.
type wfFakeSched struct{ fakeSched }

func (s wfFakeSched) SubmitGraph(_ context.Context, nodes []jobs.Node) (map[string]string, error) {
	f := s.f
	ids := map[string]string{}
	var order []string
	for _, n := range nodes {
		id := "job" + itoa(len(f.jobs)+1)
		f.jobs[id] = jobs.Job{ID: id, Spec: n.Spec, State: jobs.StateQueued, Created: time.Now()}
		f.sent = append(f.sent, sentReq{ID: id, Spec: n.Spec, Cmd: n.Spec.Kind, Params: n.Spec.Params})
		ids[n.Key] = id
		order = append(order, id)
	}
	for _, id := range order {
		if ex := f.executors[f.jobs[id].Kind]; ex != nil && !f.jobs[id].State.Terminal() {
			f.run(id, ex)
		}
	}
	return ids, nil
}

func (s wfFakeSched) List(_ context.Context, fl jobs.Filter) ([]jobs.Job, error) {
	var out []jobs.Job
	for _, j := range s.f.jobs {
		if !j.State.Terminal() && j.State != jobs.StateInterrupted && strings.HasPrefix(j.Owner, fl.Owner) {
			out = append(out, j)
		}
	}
	return out, nil
}

type wfTest struct {
	a   *app
	eng *fakeEngine
	tt  *ui.Tester
	fe  *dubtest.Engine
	out string
	tr  *dubFakeTranslator
}

func newWfTest(t *testing.T) *wfTest {
	t.Helper()
	saved := wfSchedFor
	wfSchedFor = func(s jobs.Scheduler) jobs.Scheduler { return wfFakeSched{s.(fakeSched)} }
	t.Cleanup(func() { wfSchedFor = saved })
	savedSecrets := llmSecrets
	llmSecrets = llm.StaticSecrets{}
	t.Cleanup(func() { llmSecrets = savedSecrets })
	a, eng, tt := newTestApp(t)
	completeAux(t, a)
	d := &wfTest{a: a, eng: eng, tt: tt, fe: dubtest.New(), out: t.TempDir(), tr: &dubFakeTranslator{}}
	d.fe.Script = []string{"Hello there.", "How are you today?"}
	a.wfEngine = d.fe.Executor
	a.lib.useEngine(func() jobs.Executor { return d.fe.Executor(proto.MethodTTSSynthesize) })
	eng.executors[services.KindWorkflowStep] = a.wfSvc.Executor()
	useFakeLLM(a, &fakeLLM{answer: func(llm.Request) (string, error) { return "{}", nil }})
	savedTr := dubMakeTranslator
	dubMakeTranslator = func(*llm.Router, dub.Settings) (translate.Translator, error) { return d.tr, nil }
	t.Cleanup(func() { dubMakeTranslator = savedTr })
	savedDir := wfExportDir
	wfExportDir = d.out
	t.Cleanup(func() { wfExportDir = savedDir })
	tt.SetSize(1700, 900)
	a.navigate("/workflows")
	tt.Frame()
	return d
}

func (d *wfTest) click(t *testing.T, label string) {
	t.Helper()
	d.tt.Frame()
	if err := d.tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts %q)", label, err, d.tt.Texts())
	}
	d.tt.Frame()
}

func (d *wfTest) node(k workflow.Kind) *workflow.Node {
	for i := range d.a.wf.Nodes {
		if d.a.wf.Nodes[i].Kind == k {
			return &d.a.wf.Nodes[i]
		}
	}
	return nil
}

// screen is where a content point of the canvas shows.
func (d *wfTest) screen(t *testing.T, x, y float64) (float32, float32) {
	t.Helper()
	r, ok := d.tt.Find("Workflow canvas")
	if !ok {
		t.Fatalf("no canvas: %q", d.tt.Texts())
	}
	v := d.a.wf.View
	return r.X + float32((x-v.X)*v.Zoom), r.Y + float32((y-v.Y)*v.Zoom)
}

func TestWorkflowsTemplateRunAndResults(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	if !tt.HasText("Design a voice workflow") {
		t.Fatalf("empty page: %q", tt.Texts())
	}
	d.click(t, "Narration + normalize")
	if a.wf == nil || len(a.wf.Nodes) != 4 || len(a.wfList) != 1 {
		t.Fatalf("template: %+v", a.wf)
	}
	start := d.node(workflow.KindStart)
	if a.wfSel != start.ID || !tt.HasText("Scripts") {
		t.Errorf("start selected: %q", a.wfSel)
	}
	sp := d.node(workflow.KindSpeak)
	if sp.P.Model == "" {
		t.Error("Speak has no model")
	}
	// Two items: a script with a separator; the folder.
	start.P.Scripts[0].Text = "Hello there.\n---\nGood morning."
	a.wfEdit(func(*workflow.Workflow) {})
	a.wfSel = d.node(workflow.KindExport).ID
	tt.Frame()
	d.click(t, "Choose")
	if d.node(workflow.KindExport).P.Folder != d.out {
		t.Fatalf("folder %q", d.node(workflow.KindExport).P.Folder)
	}
	tt.Frame()
	if !tt.HasText("2 scripts") {
		t.Errorf("card summary: %q", tt.Texts())
	}
	// Saved in the background (here at once).
	if w, _, err := a.wfSvc.Get(context.Background(), a.wf.ID); err != nil || len(w.Nodes) != 4 || w.Node(start.ID).P.Scripts[0].Text != start.P.Scripts[0].Text {
		t.Fatalf("saved: %v", err)
	}
	d.click(t, "Run workflow")
	run := a.wfRun
	if run == nil || run.State != workflow.RunDone {
		t.Fatalf("run %+v (err %q)", run, a.wfErr)
	}
	if done, _, total := run.Counts(); done != 2 || total != 2 {
		t.Fatalf("%d/%d", done, total)
	}
	files, _ := filepath.Glob(filepath.Join(d.out, "*.wav"))
	if len(files) != 2 {
		t.Fatalf("exports %v", files)
	}
	tt.Frame()
	if a.wfPanel != wfPanelRun || !tt.HasText("2 / 2 items ready") || !tt.HasText("Made") {
		t.Errorf("run panel: %q", tt.Texts())
	}
	// A second run makes nothing.
	d.click(t, "Run workflow")
	if a.wfRun.State != workflow.RunDone || len(a.wfRun.Steps) != 0 {
		t.Errorf("again: %+v", a.wfRun.Steps)
	}
	// Export all copies the results.
	all := t.TempDir()
	wfExportDir = all
	d.click(t, "Export all")
	if got, _ := filepath.Glob(filepath.Join(all, "*.wav")); len(got) != 2 {
		t.Errorf("export all %v", got)
	}
	// The Projects page lists it.
	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	if !tt.HasText(a.wf.Name) || !tt.HasText("Workflow · Done") && !strings.Contains(strings.Join(tt.Texts(), "|"), "Workflow") {
		t.Errorf("projects: %q", tt.Texts())
	}
}

func TestWorkflowsValidationOnCards(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	a.wfNew("transcript")
	tt.Frame()
	// No clips: the Source card says so; the toolbar counts the problem.
	if !tt.HasText("Add audio clips.") || !tt.HasText("1 problem") {
		t.Fatalf("diagnostics: %q", tt.Texts())
	}
	src := d.node(workflow.KindSource)
	clip := filepath.Join(t.TempDir(), "talk.wav")
	dubtest.Speech(clip, 2)
	a.wfClipFiles(src.ID, []string{clip, "/nope/missing.wav"})
	tt.Frame()
	if len(src.P.Clips) != 1 || tt.HasText("Add audio clips.") {
		t.Fatalf("clips %+v", src.P.Clips)
	}
	// Remove the link into Export.
	ex := d.node(workflow.KindExport)
	a.wfSelEdge = a.wf.Incoming(ex.ID)[0].ID
	a.wfRemoveSelected()
	tt.Frame()
	if !tt.HasText("Connect this step's input.") {
		t.Errorf("unconnected: %q", tt.Texts())
	}
	a.wfStart(false)
	if a.wfErr == "" || a.wfRun != nil {
		t.Errorf("an invalid workflow ran: %q", a.wfErr)
	}
	tt.Frame()
	if !tt.HasText("1 problem to fix before running") {
		t.Errorf("run panel: %q", tt.Texts())
	}
}

func TestWorkflowsCanvasGestures(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	a.wfNew("")
	tt.Frame()
	tt.Frame()
	sp := d.node(workflow.KindSpeak)
	// Drag the Speak card by 60, 40.
	x0, y0 := sp.X, sp.Y
	px, py := d.screen(t, sp.X+wfCardW/2, sp.Y+20)
	tt.Press(px, py)
	tt.Move(px+30, py+20)
	tt.Move(px+60, py+40)
	tt.Release(px+60, py+40)
	tt.Frame()
	z := a.wf.View.Zoom
	if a.wfSel != sp.ID || sp.X != x0+float64(60)/z || sp.Y != y0+float64(40)/z {
		t.Fatalf("moved to %v,%v from %v,%v (sel %q)", sp.X, sp.Y, x0, y0, a.wfSel)
	}
	// Arrows move it.
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if sp.X != x0+float64(60)/z+wfMoveBy {
		t.Errorf("arrow: %v", sp.X)
	}
	// A new Normalize step, linked from Speak's output by dragging.
	a.wfSel = ""
	a.wfAddStep(workflow.KindNormalize, sp.X, sp.Y+200)
	tt.Frame()
	norm := a.wfNode(a.wfSel)
	if norm == nil || norm.Kind != workflow.KindNormalize {
		t.Fatalf("added %+v", norm)
	}
	ox, oy := wfPortAt(sp, workflow.PortOut, false)
	ix, iy := wfPortAt(norm, workflow.PortIn, true)
	fx, fy := d.screen(t, ox, oy)
	tx, ty := d.screen(t, ix, iy)
	tt.Press(fx, fy)
	tt.Move((fx+tx)/2, (fy+ty)/2)
	tt.Move(tx, ty)
	tt.Release(tx, ty)
	tt.Frame()
	in := a.wf.Incoming(norm.ID)
	if len(in) != 1 || in[0].From != sp.ID || a.wfSelEdge != in[0].ID {
		t.Fatalf("link %+v (sel %q)", in, a.wfSelEdge)
	}
	// Delete removes the selected link.
	tt.Key(0, ui.KeyDelete)
	tt.Frame()
	if len(a.wf.Incoming(norm.ID)) != 0 {
		t.Errorf("link not removed")
	}
	// Clicking a link selects it.
	e := a.wf.Incoming(sp.ID)[0]
	c, _ := wfEdgeCurve(a.wf, e)
	mx, my := wfBezier(c, 0.5)
	sx, sy := d.screen(t, mx, my)
	tt.Press(sx, sy)
	tt.Release(sx, sy)
	tt.Frame()
	if a.wfSelEdge != e.ID {
		t.Errorf("edge not selected: %q", a.wfSelEdge)
	}
	// Dragging the background pans; ⌘-scroll zooms.
	v0 := a.wf.View
	bx, by := d.screen(t, v0.X+5, v0.Y+5)
	tt.Press(bx, by)
	tt.Move(bx+50, by+30)
	tt.Release(bx+50, by+30)
	tt.Frame()
	if a.wf.View.X != v0.X-50/v0.Zoom || a.wfSel != "" || a.wfSelEdge != "" {
		t.Errorf("pan: %+v from %+v", a.wf.View, v0)
	}
	tt.HoldModifiers(ui.Cmd)
	tt.Frame()
	tt.Scroll(bx+100, by+100, 0, -40)
	tt.HoldModifiers(0)
	tt.Frame()
	if a.wf.View.Zoom <= v0.Zoom {
		t.Errorf("zoom %v", a.wf.View.Zoom)
	}
	// Delete the selected step with the key.
	a.wfSel = norm.ID
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if a.wfNode(norm.ID) != nil {
		t.Error("step not removed")
	}
	// The layout is saved.
	tt.Frame()
	w, _, _ := a.wfSvc.Get(context.Background(), a.wf.ID)
	if n := w.Node(sp.ID); n == nil || n.X != sp.X {
		t.Errorf("saved %+v", n)
	}
}

func TestWorkflowsLibrary(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	a.wfNew("")
	first := a.wf.ID
	a.wfDuplicate()
	tt.Frame()
	if len(a.wfList) != 2 || a.wf.ID == first || a.wf.Name != "Untitled workflow (2)" {
		t.Fatalf("duplicate: %+v", a.wfList)
	}
	a.wfRename(first, "Lessons")
	tt.Frame()
	if !tt.HasText("Lessons") {
		t.Errorf("rename: %q", tt.Texts())
	}
	a.wfConfirm, a.wfAsk = a.wf.ID, true
	tt.Frame()
	d.click(t, "Delete")
	if len(a.wfList) != 1 || a.wf == nil || a.wf.ID != first {
		t.Fatalf("delete: %+v %+v", a.wfList, a.wf)
	}
	recs, _ := a.wfSvc.List(context.Background())
	if len(recs) != 1 || recs[0].Name != "Lessons" {
		t.Errorf("stored %+v", recs)
	}
	// Adding from the palette links after the selected step when it can.
	a.wfSel = ""
	for i := range a.wf.Nodes {
		if a.wf.Nodes[i].Kind == workflow.KindSpeak {
			a.wfSel = a.wf.Nodes[i].ID
		}
	}
	ex := d.node(workflow.KindExport)
	a.wfEdit(func(w *workflow.Workflow) { w.RemoveNode(ex.ID) })
	sp := a.wfSel
	d.click(t, "Add Normalize audio")
	norm := a.wfNode(a.wfSel)
	if norm == nil || norm.Kind != workflow.KindNormalize || len(a.wf.Incoming(norm.ID)) != 1 || a.wf.Incoming(norm.ID)[0].From != sp {
		t.Fatalf("palette: %+v", norm)
	}
	// Reopened from the store, it is the same.
	id := a.wf.ID
	a.wf = nil
	a.wfOpen(id)
	if a.wf == nil || a.wfNode(norm.ID) == nil {
		t.Error("reopen")
	}
}

func TestWorkflowsConditionAndTranslate(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	a.wfNew("translate-narrate")
	src := d.node(workflow.KindSource)
	clip := filepath.Join(t.TempDir(), "talk.wav")
	dubtest.Speech(clip, 2)
	a.wfClipFiles(src.ID, []string{clip})
	// A condition between Transcribe and Translate: only clips saying
	// "hello" are translated.
	tr := d.node(workflow.KindTranscribe)
	tl := d.node(workflow.KindTranslate)
	a.wfEdit(func(w *workflow.Workflow) {
		for _, e := range w.Incoming(tl.ID) {
			w.RemoveEdge(e.ID)
		}
		c := w.AddNode(workflow.KindCondition, 300, 300)
		w.Node(c).P.Text = "hello"
		w.Connect(tr.ID, workflow.PortOut, c, workflow.PortIn)
		w.Connect(c, workflow.PortYes, tl.ID, workflow.PortIn)
	})
	tt.Frame()
	if rep := a.wfReport(); !rep.OK() {
		t.Fatalf("%+v", rep.Diags)
	}
	a.wfStart(false)
	tt.Frame()
	if a.wfRun == nil || a.wfRun.State != workflow.RunDone {
		t.Fatalf("run %+v %q", a.wfRun, a.wfErr)
	}
	step := a.wfRun.Items[0].Steps[tl.ID]
	if step.Status != workflow.StepDone || !strings.Contains(step.Out.Text, "Hello there.") {
		t.Errorf("translated %+v", step)
	}
	var sp proto.SynthesizeParams
	json.Unmarshal(d.fe.Last(proto.MethodTTSSynthesize), &sp)
	if sp.Text == "" {
		t.Error("nothing spoken")
	}
	files, _ := os.ReadDir(a.wfSvc.Dir(a.wf.ID))
	if len(files) == 0 {
		t.Error("no run files")
	}
	_ = store.LibWorkflow
}

func TestWorkflowsImportScripts(t *testing.T) {
	d := newWfTest(t)
	a, tt := d.a, d.tt
	a.wfNew("narration")
	start := a.wf.InputNode()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "one.txt"), []byte("First lesson.\n\nIt has two paragraphs."), 0o644)
	os.WriteFile(filepath.Join(dir, "two.md"), []byte("# Title\n\nSecond **lesson**."), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.docx"), []byte("not a zip"), 0o644)
	a.wfScriptFiles(start.ID, []string{filepath.Join(dir, "one.txt"), filepath.Join(dir, "two.md"), filepath.Join(dir, "bad.docx")})
	tt.Frame()
	ss := a.wfNode(start.ID).P.Scripts
	// The template's sample script stays; the imports follow it.
	if len(ss) != 3 || ss[1].Name != "one.txt" || !strings.Contains(ss[2].Text, "Second lesson.") {
		t.Fatalf("scripts %+v", ss)
	}
	if rep := a.wfReport(); rep.Items != 3 {
		t.Errorf("items %d", rep.Items)
	}
	if !tt.HasText("one.txt") {
		t.Errorf("inspector: %q", tt.Texts())
	}
}
