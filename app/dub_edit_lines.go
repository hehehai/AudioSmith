package main

// Line edits of the open dub: split, merge, insert, delete, bulk actions,
// timing, shorter rewrites, cleanup and the mirror of a line's source.

import (
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// dubSplit cuts the selected line in two (⌘D), at the caret when it is
// being edited, else at the boundary nearest its middle.
func (a *app) dubSplit(id string, at int) {
	l := ""
	if a.dubEdit == id {
		l = a.dubEditL
		a.dubCommitEdit()
	}
	var second string
	if a.dubDo("split", func(p *dub.Project) bool {
		var ok bool
		second, ok = p.Split(id, l, at)
		return ok
	}) {
		a.dubCur = second
	}
}

// dubMerge joins the selected line with the next (⌘M) or the one before (⇧⌘M).
func (a *app) dubMerge(id string, next bool) {
	a.dubCommitEdit()
	i := a.dub.Index(id)
	if a.dubDo("merge", func(p *dub.Project) bool { return p.Merge(id, next) }) {
		if !next && i > 0 {
			a.dubCur = a.dub.Segments[i-1].ID
		}
	}
}

// dubInsert adds an empty line after id ("" = at the start).
func (a *app) dubInsert(after string) {
	a.dubCommitEdit()
	var id string
	d := a.dub.Source.Duration
	if a.dubDo("insert", func(p *dub.Project) bool {
		n := len(p.Segments)
		id = p.Insert(after, d)
		return len(p.Segments) > n
	}) {
		a.dubCur = id
		a.dubBeginEdit(id, a.dubLang)
	}
}

// dubDelete deletes lines.
func (a *app) dubDelete(ids ...string) {
	if len(ids) == 0 {
		return
	}
	a.dubCommitEdit()
	i := a.dub.Index(ids[0])
	if a.dubDo("delete", func(p *dub.Project) bool { return p.Delete(ids...) }) {
		for _, id := range ids {
			delete(a.dubSel, id)
		}
		a.dubCur = ""
		if n := len(a.dub.Segments); n > 0 && i >= 0 {
			a.dubCur = a.dub.Segments[min(i, n-1)].ID
		}
	}
}

// dubSelected are the lines ticked, in order.
func (a *app) dubSelected() []string {
	var out []string
	if a.dub == nil {
		return nil
	}
	for _, s := range a.dub.Segments {
		if a.dubSel[s.ID] {
			out = append(out, s.ID)
		}
	}
	return out
}

// dubBulkVoice casts the ticked lines with a voice.
func (a *app) dubBulkVoice(ref string) {
	ids := a.dubSelected()
	a.dubDo("voice", func(p *dub.Project) bool { return p.SetVoice(ref, ids...) })
}

// dubBulkLang makes the ticked lines speak another language.
func (a *app) dubBulkLang(l string) {
	ids := a.dubSelected()
	a.dubDo("lang", func(p *dub.Project) bool { return p.SetLang(l, ids...) })
}

// dubBulkDelete deletes the ticked lines.
func (a *app) dubBulkDelete() {
	ids := a.dubSelected()
	a.dubDelete(ids...)
	a.dubSel = map[string]bool{}
}

// dubSetLine changes a line's options (volume, speed, direction, voice,
// speaker).
func (a *app) dubSetLine(id, label string, fn func(*dub.Segment)) {
	a.dubDo(label+":"+id, func(p *dub.Project) bool {
		s, ok := p.Segment(id)
		if !ok {
			return false
		}
		before := *s
		fn(s)
		return !segEqual(before, *s)
	})
}

func segEqual(a, b dub.Segment) bool {
	return a.Volume == b.Volume && a.Speed == b.Speed && a.Direction == b.Direction && a.Voice == b.Voice &&
		a.Speaker == b.Speaker && a.Lang == b.Lang && a.Locked == b.Locked && a.Start == b.Start && a.End == b.End && a.NoDialogue == b.NoDialogue
}

// dubSetTiming moves a line (the timeline, the start/end fields).
func (a *app) dubSetTiming(id string, start, end float64, clamp bool) {
	d := a.dub.Source.Duration
	a.dubDo("timing", func(p *dub.Project) bool { return p.SetTiming(id, start, end, d, clamp) })
}

// dubNudge moves a line's edge by delta seconds.
func (a *app) dubNudge(id, edge string, delta float64) {
	d := a.dub.Source.Duration
	a.dubDo("nudge:"+id+":"+edge, func(p *dub.Project) bool { return p.Nudge(id, edge, delta, d) })
}

// dubUseShorter takes a line's shorter rewrite.
func (a *app) dubUseShorter(id string) {
	l := a.dubLang
	a.dubDo("shorter", func(p *dub.Project) bool { return p.UseShorter(id, l) })
}

// dubCleanup merges fragments.
func (a *app) dubCleanup() {
	n := 0
	a.dubDo("cleanup", func(p *dub.Project) bool {
		n = p.Cleanup()
		return n > 0
	})
	if n == 0 {
		a.toast(i18n.T("The lines are already clean."))
	} else {
		a.toast(i18n.N(n, "Merged %d fragment", "Merged %d fragments", n))
	}
}

// dubRestore drops every translation.
func (a *app) dubRestore() {
	a.dubDo("restore", func(p *dub.Project) bool { return p.RestoreSource() })
}

// dubSkipFailed silences the lines whose translation failed.
func (a *app) dubSkipFailed() {
	l := a.dubLang
	a.dubDo("skip", func(p *dub.Project) bool { return p.SkipFailed(l) > 0 })
}

// dubFailedCount is how many lines of lang failed to translate.
func (a *app) dubFailedCount(l string) int {
	n := 0
	if a.dub == nil {
		return 0
	}
	for _, s := range a.dub.Segments {
		if line := s.Lines[l]; line.Err != "" && !line.Skipped {
			n++
		}
	}
	return n
}

// dubMirror fills empty directions from the source's delivery (Prosody
// Mirror), measured on the separated voices when there are.
func (a *app) dubMirror() {
	p := a.dub
	if p == nil {
		return
	}
	name := p.Media.Vocals
	if name == "" {
		name = p.Media.Audio
	}
	if name == "" {
		a.toast(i18n.T("Prepare the source first."))
		return
	}
	path := a.dubSvc.Path(p.ID, name)
	q := p.Clone()
	a.background(func() {
		b, err := readWAVMono(path)
		var res dub.MirrorResult
		if err == nil {
			res = q.Mirror(b)
		}
		a.post(func() {
			switch {
			case err != nil:
				a.toastWarn(i18n.T("Could not read the audio: %v", err))
			case res.Measured < dub.MirrorMinLines:
				a.toast(i18n.T("Not enough clear speech to mirror the delivery."))
			case len(res.Applied) == 0:
				a.toast(i18n.T("No line needed a direction."))
			default:
				applied := res.Applied
				a.dubDo("mirror", func(p *dub.Project) bool {
					changed := false
					for i := range p.Segments {
						if d, ok := applied[p.Segments[i].ID]; ok && p.Segments[i].Direction == "" {
							p.Segments[i].Direction, changed = d, true
						}
					}
					return changed
				})
				a.toast(i18n.N(len(applied), "Gave %d line a direction", "Gave %d lines a direction", len(applied)))
			}
		})
	})
}
