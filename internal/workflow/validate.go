package workflow

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Severity is how bad a diagnostic is: errors stop a run, warnings don't.
type Severity int

const (
	Error Severity = iota
	Warning
)

// Diagnostic codes.
const (
	CodeEmpty         = "graph.empty"       // no steps
	CodeNoInput       = "graph.no_input"    // no Start or Source step
	CodeNoSteps       = "graph.no_steps"    // only the input step
	CodeExtraInput    = "node.extra_input"  // a second Start or Source step
	CodeUnconnected   = "node.unconnected"  // its input has no link
	CodeUnreachable   = "node.unreachable"  // no path from the input step
	CodeCycle         = "edge.cycle"        // the link closes a loop
	CodeType          = "edge.type"         // the link carries a type the input does not take (Arg: the type)
	CodeMergeTypes    = "edge.merge_types"  // links into one input carry different types
	CodeMerge         = "edge.merge"        // links into one input that are not alternative branches of a condition
	CodeStartEmpty    = "start.empty"       // no script
	CodeStartTooMany  = "start.too_many"    // more than MaxScripts items (Arg: the count)
	CodeStartTooLong  = "start.too_long"    // an item over MaxScriptChars (Arg: its number, 1-based)
	CodeSourceEmpty   = "source.empty"      // no clip
	CodeSourceTooMany = "source.too_many"   // more than MaxClips
	CodeSourceLarge   = "source.too_large"  // a clip over MaxClipBytes (Arg: its name)
	CodeSourceZero    = "source.empty_file" // a clip of 0 bytes (Arg: its name)
	CodeNoModel       = "speak.no_model"    // Speak/Convert without a model
	CodeNoTarget      = "translate.no_target"
	CodeSameLanguage  = "translate.same"
	CodeNoMatchText   = "condition.no_text"
	CodeFormat        = "export.format"    // the format does not suit the value (Arg: the format)
	CodeTemplate      = "export.template"  // the file name template is not a name
	CodeNoFolder      = "export.no_folder" // warning: the results stay in the app
)

// Diagnostic is a finding about the workflow, a step or a link.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Node     string   `json:"node,omitempty"`
	Edge     string   `json:"edge,omitempty"`
	Arg      string   `json:"arg,omitempty"`
}

// Check adds the diagnostics only the app can make about a step (a model
// not downloaded, a saved voice deleted, no translator set up), given
// the type its input receives ("" when unknown).
type Check func(n *Node, in Type) []Diagnostic

// Report is what Validate found.
type Report struct {
	Diags []Diagnostic
	// Input is the input step's id ("" without one).
	Input string
	// Order lists the steps reachable from the input in an order that
	// runs every step after the steps feeding it (without cycles).
	Order []string
	// In and Out are the types of each step's input and outputs, where
	// they are known.
	In, Out map[string]Type
	// Items counts the input's items.
	Items int
}

// OK reports whether nothing stops a run.
func (r *Report) OK() bool {
	for _, d := range r.Diags {
		if d.Severity == Error {
			return false
		}
	}
	return true
}

// For lists the diagnostics of a step (and of the links into it).
func (r *Report) For(node string) []Diagnostic {
	var out []Diagnostic
	for _, d := range r.Diags {
		if d.Node == node {
			out = append(out, d)
		}
	}
	return out
}

// ForEdge lists the diagnostics of a link.
func (r *Report) ForEdge(edge string) []Diagnostic {
	var out []Diagnostic
	for _, d := range r.Diags {
		if d.Edge == edge {
			out = append(out, d)
		}
	}
	return out
}

// Errors counts the errors.
func (r *Report) Errors() int {
	n := 0
	for _, d := range r.Diags {
		if d.Severity == Error {
			n++
		}
	}
	return n
}

// Validate checks the graph: one input step, every other step fed and
// reachable from it, no loops, links of types the inputs take, inputs fed
// by several links only from alternative branches of conditions, and each
// step's settings; check (optional) adds the app's own findings.
func Validate(w *Workflow, check Check) *Report {
	r := &Report{In: map[string]Type{}, Out: map[string]Type{}}
	if len(w.Nodes) == 0 {
		r.add(Error, CodeEmpty, "", "", "")
		return r
	}
	r.findInput(w)
	topo := r.checkLoops(w)
	r.checkReach(w, topo)
	r.checkTypes(w, topo)
	for i := range w.Nodes {
		n := &w.Nodes[i]
		r.checkSettings(w, n)
		if check != nil {
			r.Diags = append(r.Diags, check(n, r.In[n.ID])...)
		}
	}
	return r
}

func (r *Report) add(sev Severity, code, node, edge, arg string) {
	r.Diags = append(r.Diags, Diagnostic{Severity: sev, Code: code, Node: node, Edge: edge, Arg: arg})
}

// findInput sets the input step: there must be one, and other steps.
func (r *Report) findInput(w *Workflow) {
	for _, n := range w.Nodes {
		if !n.Kind.IsInput() {
			continue
		}
		if r.Input == "" {
			r.Input = n.ID
		} else {
			r.add(Error, CodeExtraInput, n.ID, "", "")
		}
	}
	if r.Input == "" {
		r.add(Error, CodeNoInput, "", "", "")
	} else if len(w.Nodes) == 1 {
		r.add(Error, CodeNoSteps, "", "", "")
	}
}

// checkLoops returns the steps in a topological order; the links left
// after removing every step reachable in it close a loop.
func (r *Report) checkLoops(w *Workflow) []string {
	indeg := map[string]int{}
	for _, e := range w.Edges {
		indeg[e.To]++
	}
	var queue, topo []string
	for _, n := range w.Nodes {
		if indeg[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		topo = append(topo, id)
		for _, e := range w.Edges {
			if e.From == id {
				if indeg[e.To]--; indeg[e.To] == 0 {
					queue = append(queue, e.To)
				}
			}
		}
	}
	inTopo := map[string]bool{}
	for _, id := range topo {
		inTopo[id] = true
	}
	for _, e := range w.Edges {
		if !inTopo[e.From] && !inTopo[e.To] && onCycle(w, e) {
			r.add(Error, CodeCycle, e.To, e.ID, "")
		}
	}
	return topo
}

// checkReach finds the steps unconnected or unreachable from the input,
// and sets the run order: the reachable steps of topo.
func (r *Report) checkReach(w *Workflow, topo []string) {
	reach := map[string]bool{}
	if r.Input != "" {
		stack := []string{r.Input}
		reach[r.Input] = true
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, e := range w.Edges {
				if e.From == id && !reach[e.To] {
					reach[e.To] = true
					stack = append(stack, e.To)
				}
			}
		}
	}
	for _, n := range w.Nodes {
		if n.Kind.IsInput() {
			continue
		}
		switch {
		case len(w.Incoming(n.ID)) == 0:
			r.add(Error, CodeUnconnected, n.ID, "", "")
		case r.Input != "" && !reach[n.ID]:
			r.add(Error, CodeUnreachable, n.ID, "", "")
		}
	}
	for _, id := range topo {
		if reach[id] {
			r.Order = append(r.Order, id)
		}
	}
}

// checkTypes types the links and checks merges, in topological order.
func (r *Report) checkTypes(w *Workflow, topo []string) {
	tokens := map[string]map[string]bool{} // the condition branches every path to a step goes through
	for _, id := range topo {
		n := w.Node(id)
		spec, _ := SpecOf(n.Kind)
		var t Type
		var sets []map[string]bool
		for i, e := range w.Incoming(id) {
			src := w.Node(e.From)
			st, known := r.Out[e.From]
			set := map[string]bool{}
			for k := range tokens[e.From] {
				set[k] = true
			}
			if src.Kind == KindCondition {
				set[e.From+":"+e.FromPort] = true
			}
			for _, prev := range sets {
				if !exclusive(prev, set) {
					r.add(Error, CodeMerge, id, e.ID, "")
					break
				}
			}
			sets = append(sets, set)
			if !known {
				continue
			}
			if len(spec.In) > 0 && !slices.Contains(spec.In[0].Accepts, st) {
				r.add(Error, CodeType, id, e.ID, string(st))
				continue
			}
			if t != "" && st != t {
				r.add(Error, CodeMergeTypes, id, e.ID, string(st))
				continue
			}
			if i == 0 || t == "" {
				t = st
			}
		}
		// What every path in goes through.
		var common map[string]bool
		for i, s := range sets {
			if i == 0 {
				common = s
				continue
			}
			for k := range common {
				if !s[k] {
					delete(common, k)
				}
			}
		}
		tokens[id] = common
		if t != "" {
			r.In[id] = t
		}
		if len(spec.Out) > 0 {
			switch o := spec.Out[0].Type; o {
			case TypeSame:
				if t != "" {
					r.Out[id] = t
				}
			default:
				r.Out[id] = o
			}
		}
	}
}

// checkSettings checks step n's settings.
func (r *Report) checkSettings(w *Workflow, n *Node) {
	p := &n.P
	switch n.Kind {
	case KindStart:
		if n.ID != r.Input {
			break
		}
		items := w.Items()
		r.Items = len(items)
		switch {
		case len(items) == 0:
			r.add(Error, CodeStartEmpty, n.ID, "", "")
		case len(items) > MaxScripts:
			r.add(Error, CodeStartTooMany, n.ID, "", strconv.Itoa(len(items)))
		}
		for j, it := range items {
			if utf8.RuneCountInString(it.Text) > MaxScriptChars {
				r.add(Error, CodeStartTooLong, n.ID, "", strconv.Itoa(j+1))
				break
			}
		}
	case KindSource:
		if n.ID == r.Input {
			r.Items = len(p.Clips)
		}
		switch {
		case len(p.Clips) == 0:
			r.add(Error, CodeSourceEmpty, n.ID, "", "")
		case len(p.Clips) > MaxClips:
			r.add(Error, CodeSourceTooMany, n.ID, "", strconv.Itoa(len(p.Clips)))
		}
		for _, c := range p.Clips {
			switch {
			case c.Size > MaxClipBytes:
				r.add(Error, CodeSourceLarge, n.ID, "", c.Name)
			case c.Size == 0:
				r.add(Error, CodeSourceZero, n.ID, "", c.Name)
			}
		}
	case KindSpeak, KindConvert:
		if p.Model == "" {
			r.add(Error, CodeNoModel, n.ID, "", "")
		}
	case KindTranslate:
		switch {
		case p.To == "":
			r.add(Error, CodeNoTarget, n.ID, "", "")
		case p.From != "" && strings.EqualFold(p.From, p.To):
			r.add(Error, CodeSameLanguage, n.ID, "", "")
		}
	case KindCondition:
		if strings.TrimSpace(p.Text) == "" {
			r.add(Error, CodeNoMatchText, n.ID, "", "")
		}
	case KindExport:
		if t, ok := r.In[n.ID]; ok {
			if f := ExportFormat(p.Format, t); !slices.Contains(FormatsFor(t), f) {
				r.add(Error, CodeFormat, n.ID, "", f)
			}
		}
		if !ValidTemplate(p.Template) {
			r.add(Error, CodeTemplate, n.ID, "", "")
		}
		if strings.TrimSpace(p.Folder) == "" {
			r.add(Warning, CodeNoFolder, n.ID, "", "")
		}
	}
}

// onCycle reports whether e lies on a loop: e.From can be reached from e.To.
func onCycle(w *Workflow, e Edge) bool {
	seen := map[string]bool{e.To: true}
	stack := []string{e.To}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == e.From {
			return true
		}
		for _, x := range w.Edges {
			if x.From == id && !seen[x.To] {
				seen[x.To] = true
				stack = append(stack, x.To)
			}
		}
	}
	return false
}

// exclusive reports whether two paths go through different branches of
// one condition (so at most one of them carries an item).
func exclusive(a, b map[string]bool) bool {
	for k := range a {
		cond, port, _ := strings.Cut(k, ":")
		other := PortYes
		if port == PortYes {
			other = PortNo
		}
		if b[cond+":"+other] {
			return true
		}
	}
	return false
}

// ExportFormat is the format an export writes for type t: its own, or
// the type's first.
func ExportFormat(format string, t Type) string {
	if format != "" {
		return format
	}
	if fs := FormatsFor(t); len(fs) > 0 {
		return fs[0]
	}
	return ""
}

// ValidTemplate reports whether a file name template makes names: not
// empty, no folders.
func ValidTemplate(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && !strings.ContainsAny(s, `/\:`) && s != "." && s != ".." && utf8.RuneCountInString(s) <= MaxTemplate
}
