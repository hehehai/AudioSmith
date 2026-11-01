package services

// A longform chapter's program: its lines and spans planned for the model
// (gaps, merged story turns, delivery, the dictionary with the book's
// lexicon) into the chunks and gaps the chapter is made of; split from
// longform_render.go.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/edit"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/lexicon"
	"github.com/hehehai/AudioSmith/internal/text/plan"
)

// gapsOf are the joins of a production.
func gapsOf(p Production) edit.GapPolicy {
	g := edit.DefaultGaps
	g.Line = float64(p.LineGapMs) / 1000
	g.Paragraph = float64(p.ParagraphGapMs) / 1000
	return g
}

// chapterProgram is what program plans a chapter's lines with.
type chapterProgram struct {
	cr        chapterRenderer
	p         *ChapterParams
	info      ModelInfo
	modelPath string
	dict      *lexicon.Dictionary
	gaps      edit.GapPolicy
	turns     TurnGaps
	paced     bool
	warnings  []core.TakeNotice
	reported  map[string]bool // the plan warnings already in warnings
}

// program plans every line of the chapter into steps, with its key.
// Under a pacing preset (story_pacing.go) the gaps follow the turns, the
// lines carry their delivery, and turns of one character are merged.
func (cr chapterRenderer) program(ctx context.Context, p *ChapterParams, info ModelInfo, modelPath string) ([]chapterStep, string, []core.TakeNotice, error) {
	r := cr.r
	pg := &chapterProgram{cr: cr, p: p, info: info, modelPath: modelPath, dict: cr.dictionary(ctx, p), gaps: gapsOf(p.Production), reported: map[string]bool{}}
	pg.turns, pg.paced = GapsOf(p.Production.Pacing)
	paced := pg.paced
	var steps []chapterStep
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%g\x00%g\x00%g\x00%g\x00%v\x00%s\x00%s\x00", chapterKeyVersion, p.Loudness, pg.gaps.Sentence, pg.gaps.Line,
		pg.gaps.Paragraph, pg.gaps.Crossfade, p.Production.KeepEdges, p.Model, info.Revision)
	if paced {
		fmt.Fprintf(h, "pacing %s level %v\x00", p.Production.Pacing, !p.Production.NoLevel)
	}
	// The tracks: each character's lines in stretches, by line.
	tracked := map[int]*trackStep{}
	if paced && !p.Production.NoTracks && r.engine != nil && r.aligner != "" {
		t, err := cr.tracks(p, pg.take, pg.planOf, pg.lineSteps, info, modelPath)
		if err != nil {
			return nil, "", nil, err
		}
		tracked = t
	}
	// Whole dialogue: the characters' lines by segment, their tracks (or
	// own steps) as the fallback.
	dialogued := map[int]*dialogueStep{}
	if p.Production.Dialogue && p.Production.DialogueModel != "" && r.engine != nil {
		d, err := cr.dialogues(p, pg.take, pg.planOf, pg.lineSteps, tracked, info, modelPath, r.model(p.Production.DialogueModel))
		if err != nil {
			return nil, "", nil, err
		}
		dialogued = d
		if p.book() && len(d) > 0 {
			// Its lines are levelled to the narration (book_dialogue_render.go).
			fmt.Fprintf(h, "book dialogue level %v\x00", !p.Production.NoLevel)
		}
	}
	// The merged turns, by their first line: of lines not in a track.
	merged := map[int][]int{}
	if paced && !p.Production.NoMerge {
		for _, g := range mergeGroups(p.Lines, info, p.Production.VaryRepeats) {
			if !slices.ContainsFunc(g, func(li int) bool { return tracked[li] != nil || dialogued[li] != nil }) {
				merged[g[0]] = g
			}
		}
	}
	for li := 0; li < len(p.Lines); li++ {
		if li > 0 {
			gap := pg.gapBefore(li)
			steps = append(steps, chapterStep{silence: gap, line: li})
			fmt.Fprintf(h, "gap %g\x00", gap)
		}
		if ds, ok := dialogued[li]; ok {
			steps = append(steps, chapterStep{chunk: ds.chunk, line: li, dialogue: ds})
			fmt.Fprintf(h, "dialogue %s %d\x00", ds.seg.job.key, ds.k)
			continue
		}
		if ts, ok := tracked[li]; ok {
			steps = append(steps, chapterStep{chunk: ts.chunk, brk: ts.seg.brk, line: li, track: ts})
			fmt.Fprintf(h, "track %s %s %d\x00", trackVersion, ts.seg.chunk.key, ts.k)
			continue
		}
		if g, ok := merged[li]; ok {
			st, err := cr.turn(p, g, pg.take, pg.planOf, pg.lineSteps, pg.gapBefore, info, modelPath)
			if err != nil {
				return nil, "", nil, err
			}
			if st != nil {
				steps = append(steps, *st)
				fmt.Fprintf(h, "turn %s %d\x00", st.chunk.key, len(g))
				li = g[len(g)-1]
				continue
			}
		}
		ls, err := pg.lineSteps(li, h)
		if err != nil {
			return nil, "", nil, err
		}
		steps = append(steps, ls...)
	}
	return steps, hex.EncodeToString(h.Sum(nil)), pg.warnings, nil
}

// take is the recipe of span sp of line li, saying say.
func (pg *chapterProgram) take(li int, sp SpanSpec, say string) (core.Take, *int64, map[string]string, error) {
	p, info := pg.p, pg.info
	l := p.Lines[li]
	v, ok := p.Voices[sp.Voice]
	if !ok {
		return core.Take{}, nil, nil, &jobs.Error{Code: proto.CodeBadRequest, Message: fmt.Sprintf("line %d: no voice %q", li+1, sp.Voice)}
	}
	t := v
	t.Model, t.Text = p.Model, say
	if l.Speed > 0 && l.Speed != 1 {
		t.Speed = l.Speed
	}
	if p.Production.Steps > 0 {
		t.Steps = p.Production.Steps
	}
	if p.Production.Guidance > 0 {
		t.Guidance = p.Production.Guidance
	}
	switch {
	case l.Emotion != "":
		// Directed by emotion: a short phrase, no persona or scene.
		withDelivery(&t, info, EmotionInstruct(l.Emotion, l.Intensity, l.Direction, emotionCont(p.Lines, li), cjkShare(say) >= 0.3))
		if info.Exaggeration {
			t.Exaggeration = EmotionExaggeration(l.Intensity)
		}
	case l.Direction != "" || l.Persona != "" || p.Scene != "":
		prev := ""
		if li > 0 && pg.paced && !sameSpeaker(p.Lines[li-1], l) && p.Lines[li-1].Character != "" && StoryPrevDirection {
			prev = p.Lines[li-1].Direction
		}
		withDelivery(&t, info, StoryInstruct(say, p.Scene, l.Persona, l.Direction, l.Intensity, prev))
	}
	seed := p.Seed
	if v.Seed != 0 {
		seed = v.Seed // a designed or locked voice's identity
	}
	var extra map[string]string
	if p.Production.VaryRepeats && l.Repeat > 0 {
		seed += int64(l.Repeat)
		extra = map[string]string{"repeat": strconv.Itoa(l.Repeat)}
	}
	return t, &seed, extra, nil
}

// planOf plans what t says, keeping each plan warning once.
func (pg *chapterProgram) planOf(t core.Take, say string) plan.Plan {
	info := pg.info
	lang := pg.p.Lang
	if lang == "" {
		lang = info.Lang(t.Language, t.Voice)
	}
	pl := plan.Build(say, plan.Options{
		Lang: lang, NonVerbal: info.NonVerbal, NonVerbalTags: info.NonVerbalTags, Speed: info.Speed, Voices: true,
		MaxChars: info.MaxChars, MaxCharsCJK: info.MaxCharsCJK, Lexicon: pg.dict,
	})
	for _, d := range pl.Diags {
		if d.Severity >= text.Warning && d.Code != plan.CodeNothingToSay {
			k := d.Code
			for _, v := range d.Params {
				k += "\x00" + v
			}
			if !pg.reported[k] {
				pg.reported[k] = true
				pg.warnings = append(pg.warnings, core.TakeNotice{Code: d.Code, Severity: "warning", Params: d.Params})
			}
		}
	}
	return pl
}

// lineSteps are the steps of line li's spans, each step's key part
// written to key.
func (pg *chapterProgram) lineSteps(li int, key io.Writer) ([]chapterStep, error) {
	r, p := pg.cr.r, pg.p
	l := p.Lines[li]
	var out []chapterStep
	for si, sp := range l.Spans {
		if si > 0 && !strings.HasPrefix(strings.ToLower(sp.Text), "[pause") {
			out = append(out, chapterStep{silence: spanGap, line: li, span: si})
			fmt.Fprintf(key, "span\x00")
		}
		t, seed, extra, err := pg.take(li, sp, sp.Text)
		if err != nil {
			return nil, err
		}
		pl := pg.planOf(t, sp.Text)
		chunks, err := r.chunks(&RenderParams{Take: t, Workflow: p.Workflow}, &pl, pg.info, pg.modelPath, seed, r.longformChunkDir(), extra)
		if err != nil {
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
		}
		k := 0
		for _, seg := range pl.Segments {
			if seg.Kind == plan.Silence {
				out = append(out, chapterStep{silence: float64(seg.Millis) / 1000, line: li, span: si})
				fmt.Fprintf(key, "pause %d\x00", seg.Millis)
				continue
			}
			c := chunks[k]
			k++
			out = append(out, chapterStep{chunk: &c, brk: seg.Break, line: li, span: si})
			fmt.Fprintf(key, "chunk %s %d\x00", c.key, seg.Break)
		}
	}
	return out, nil
}

// gapBefore is the silence before line li.
func (pg *chapterProgram) gapBefore(li int) float64 {
	p := pg.p
	l, prev := p.Lines[li], p.Lines[li-1]
	if g, ok := blockGap(prev, l); ok { // inside a book's dialogue block (book_dialogue_render.go)
		return g
	}
	if pg.paced {
		if p.Production.NoEmotionGaps {
			return TurnGap(pg.turns, prev, l)
		}
		return emotionGap(TurnGap(pg.turns, prev, l), prev, l)
	}
	gap := pg.gaps.Line
	if prev.PauseAfter > 0 {
		gap = float64(prev.PauseAfter) / 1000
	} else if l.Paragraph {
		gap = pg.gaps.Paragraph
	}
	return gap
}

// turn plans the lines g of one character as one chunk; nil when their
// text does not make exactly one chunk (the lines are made one by one).
func (cr chapterRenderer) turn(p *ChapterParams, g []int,
	take func(int, SpanSpec, string) (core.Take, *int64, map[string]string, error), planOf func(core.Take, string) plan.Plan,
	lineSteps func(int, io.Writer) ([]chapterStep, error), gapBefore func(int) float64, info ModelInfo, modelPath string) (*chapterStep, error) {
	texts := make([]string, len(g))
	for i, li := range g {
		texts[i] = p.Lines[li].Spans[0].Text
	}
	joined := joinTurn(texts)
	// The turn speaks as its first line, with the directions of all.
	saved := p.Lines[g[0]]
	lines := make([]LineSpec, len(g))
	for i, li := range g {
		lines[i] = p.Lines[li]
	}
	if e, own, in := turnEmotion(lines); e != "" {
		p.Lines[g[0]].Emotion, p.Lines[g[0]].Direction, p.Lines[g[0]].Intensity = e, own, in
	} else {
		p.Lines[g[0]].Direction, p.Lines[g[0]].Intensity = turnDirection(lines)
	}
	t, seed, extra, err := take(g[0], p.Lines[g[0]].Spans[0], joined)
	p.Lines[g[0]] = saved
	if err != nil {
		return nil, err
	}
	pl := planOf(t, joined)
	if len(pl.Segments) != 1 || pl.Segments[0].Kind == plan.Silence {
		return nil, nil
	}
	chunks, err := cr.r.chunks(&RenderParams{Take: t, Workflow: p.Workflow}, &pl, info, modelPath, seed, cr.r.longformChunkDir(), extra)
	if err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	c := chunks[0]
	ts := &turnStep{lines: g, weights: turnWeights(texts)}
	for i, li := range g {
		if i > 0 {
			ts.fallback = append(ts.fallback, chapterStep{silence: gapBefore(li), line: li})
		}
		ls, err := lineSteps(li, io.Discard)
		if err != nil {
			return nil, err
		}
		ts.fallback = append(ts.fallback, ls...)
	}
	return &chapterStep{chunk: &c, brk: pl.Segments[0].Break, line: g[0], turn: ts}, nil
}

// StoryPrevDirection adds the other character's last direction to a
// line's instruction ("对方刚才…"): an experiment of the A/B listening
// (e2e_story_ab_test.go), off in the app.
var StoryPrevDirection = false

// dictionary is the pronunciation dictionary with the book's lexicon on
// top (its words win).
func (cr chapterRenderer) dictionary(ctx context.Context, p *ChapterParams) *lexicon.Dictionary {
	var entries []lexicon.Entry
	if cr.r.pron != nil {
		e, err := cr.r.pron.Entries(ctx)
		if err != nil {
			cr.r.logf("chapter: pronunciation dictionary: %v", err)
		}
		entries = e
	}
	scope := lexicon.Global
	if p.Lang != "" {
		scope = p.Lang
	}
	for _, l := range p.Lexicon {
		entries = append(entries, lexicon.Entry{Term: l.Word, Replacement: l.Say, Type: lexicon.Respelling, Scope: scope, Enabled: true})
	}
	return lexicon.Compile(entries, p.Lang)
}
