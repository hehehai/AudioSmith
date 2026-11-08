package services

// Whole dialogue (Production.Dialogue, thread #80): a story's character
// lines are spoken by a dialogue model (catalogue capability "dialogue":
// SoulX-Podcast, MOSS-TTSD, thread #82, or VibeVoice, #166) a scene at a time, every line
// hearing the lines before it, instead of line by line or per character
// stretch; narration stays on the story's model. One tts.dialogue job
// speaks a segment: consecutive character lines of a chapter (a short
// narration line between them does not end it; a long one does), within
// the model's limits (internal/dialogue: SoulX-Podcast 4 voices, 16
// lines, about 90 s; MOSS-TTSD 5 voices, VibeVoice 4, 24 lines, about 60 s), made by
// the shared runner (dialogue_run.go). SoulX-Podcast writes each line to
// its own file, so nothing is cut; MOSS-TTSD and VibeVoice speak the segment
// as one track, cut at the lines with the forced aligner (dialogue_run_mixed.go).
// The lines go back into the chapter with the pacing's gaps (and the
// emotion's, E).
//
// Each speaker is cloned from a reference: a cloned voice's own clip and
// transcript, a designed voice's reference (Take.CloneRef, thread #83),
// else the voice says a reference text (about 12 s, PortableLine) on the
// story's model first. Every reference is tidied as the voice pages tidy
// them (refcheck.Tidy: edges trimmed, long pauses shortened, −18 LUFS).
// Each line is then checked: the recognizer must hear it (error rate ≤ cutMaxCER) and it must last a plausible time
// for its text (no runaway "singing", no cut-off end). A line that fails is
// made again by the dialogue model with the same context (the lines before
// it, from their codes) and another seed; failing twice, it is made on the
// story's model, as are all the segment's lines when the dialogue model
// fails (its character tracks when they apply, else line by line). The
// files and failures are cached by the segment's text and recipe: editing a
// line makes its segment again.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dialogue"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/plan"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// dialogueVersion names a story segment's files and starts its key.
const dialogueVersion = "dlg2"

// PortableLine is what a voice says to become a reference clip when there
// is no time to pick among takes (about 12 s), in its language: the first
// of the adult reference texts (voicedesign.ReferenceTexts), lively
// everyday narration rather than a sentence read about the recording.
func PortableLine(lang string) string {
	return vd.ReferenceTexts(lang, vd.RegisterAdult)[0]
}

// dialogueSeg is a story segment: its lines (chapter indices), the job
// that makes them, and what became of it.
type dialogueSeg struct {
	lines  []int
	job    dialogueJob
	voices []string       // the refs' voice keys
	chunks []*renderChunk // per ref: the portable line on the story's model; nil: a clip
	// made: done (or tried); err: the segment could not be made; out: its
	// lines made otherwise and made twice.
	made bool
	err  error
	out  dialogueOutcome
}

// dialogueStep is line k of a segment, with the steps that make it on the
// story's model.
type dialogueStep struct {
	seg      *dialogueSeg
	k        int
	chunk    *renderChunk // a stand-in for the progress: the line's text
	fallback []chapterStep
}

// dialogueLine reports whether line l can be a turn: one span of a
// character, with speech and no pause markup.
func dialogueLine(l LineSpec) bool {
	return len(l.Spans) == 1 && l.Character != "" && hasSpeech(l.Spans[0].Text) &&
		!strings.Contains(strings.ToLower(l.Spans[0].Text), "[pause")
}

// dialogueItems are a chapter's lines for grouping: the character lines
// are turns of their voice, a short narration line an aside.
func dialogueItems(lines []LineSpec) []dialogue.Item {
	items := make([]dialogue.Item, len(lines))
	for i, l := range lines {
		if dialogueLine(l) {
			items[i] = dialogue.Item{Voice: l.Spans[0].Voice, Text: l.Spans[0].Text, Turn: true}
			continue
		}
		if l.Character == "" {
			texts := make([]string, len(l.Spans))
			for k, s := range l.Spans {
				texts[k] = s.Text
			}
			items[i].Aside = dialogue.ShortAside(texts...)
		}
	}
	return items
}

// dialogueGroups are a chapter's segments: runs of character lines,
// within the limits.
func dialogueGroups(lines []LineSpec, lim dialogue.Limits) [][]int {
	return dialogue.Group(dialogueItems(lines), lim, dialogue.StoryRules)
}

// dialogueTag is the non-verbal tag a line's emotion becomes when
// Production.DialogueTags puts it into the dialogue model's text (an
// experiment; the words never change): laughter for clear joy, a sigh for
// clear sadness.
func dialogueTag(l LineSpec) string {
	if l.Intensity < IntensityMedium {
		return ""
	}
	switch l.Emotion {
	case EmotionHappy, EmotionExcited:
		return "<|laughter|>"
	case EmotionSad, EmotionHurt, EmotionApologetic:
		return "<|sigh|>"
	}
	return ""
}

// dialogues plans the chapter's segments: by line, its step. lineSteps
// gives each line's own steps (its fallback) unless fallback has a step
// for it (its character track).
func (cr chapterRenderer) dialogues(p *ChapterParams,
	take func(int, SpanSpec, string) (core.Take, *int64, map[string]string, error), planOf func(core.Take, string) plan.Plan,
	lineSteps func(int, io.Writer) ([]chapterStep, error), fallback map[int]*trackStep, info ModelInfo, modelPath string,
	dialogueInfo ModelInfo) (map[int]*dialogueStep, error) {
	out := map[int]*dialogueStep{}
	r := cr.r
	refs := map[string]int{} // voice key → index in a segment's refs
	oneTrack := OneTrackDialogue(dialogueInfo.Family)
	lim := dialogue.LimitsFor(dialogueInfo.Family)
	groups, seeds := dialogueGroups(p.Lines, lim), []int64(nil)
	if p.book() {
		groups, seeds = bookDialogueGroups(p.Lines, lim, p.Lang) // book_dialogue_render.go
	}
	for gi, g := range groups {
		seg := &dialogueSeg{lines: g}
		j := &seg.job
		off := int64(1000*p.Index + gi)
		if seeds != nil {
			off = seeds[gi]
		}
		j.model, j.oneTrack, j.seed, j.lang = p.Production.DialogueModel, oneTrack, p.Seed+off, p.Lang
		clear(refs)
		h := sha256.New()
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%v\x00", dialogueVersion, p.Production.DialogueModel, dialogueInfo.Revision, j.seed, p.Production.DialogueTags)
		for _, li := range g {
			l := p.Lines[li]
			sp := l.Spans[0]
			k, ok := refs[sp.Voice]
			if !ok {
				ref, chunk, err := cr.dialogueRef(p, li, take, planOf, info, modelPath)
				if err != nil {
					return nil, err
				}
				k = len(j.refs)
				refs[sp.Voice] = k
				j.refs = append(j.refs, ref)
				seg.chunks = append(seg.chunks, chunk)
				seg.voices = append(seg.voices, sp.Voice)
				fmt.Fprintf(h, "ref %s\x00%s\x00", ref.key, ref.text)
			}
			t, _, _, err := take(li, sp, sp.Text)
			if err != nil {
				return nil, err
			}
			say := spokenText(planOf(t, sp.Text))
			if say == "" {
				say = stripTags(sp.Text)
			}
			if j.lang == "" {
				j.lang = "en"
				if cjkShare(say) >= 0.3 {
					j.lang = "zh"
				}
			}
			tagged := say
			if p.Production.DialogueTags && !oneTrack { // SoulX-Podcast's tags
				tagged = dialogueTag(l) + say
			}
			j.speakers = append(j.speakers, k)
			j.texts = append(j.texts, say)
			j.tagged = append(j.tagged, tagged)
			fmt.Fprintf(h, "turn %d\x00%s\x00", k, tagged)
		}
		j.key = hex.EncodeToString(h.Sum(nil))[:32]
		for k, li := range g {
			j.paths = append(j.paths, filepath.Join(r.longformChunkDir(), fmt.Sprintf("%s-%s-%d.wav", j.key, dialogueVersion, k)))
			var fb []chapterStep
			if ts := fallback[li]; ts != nil {
				fb = []chapterStep{{chunk: ts.chunk, brk: ts.seg.brk, line: li, track: ts}}
			} else {
				steps, err := lineSteps(li, io.Discard)
				if err != nil {
					return nil, err
				}
				fb = steps
			}
			w, c := text.Count(j.texts[k])
			stand := renderChunk{key: j.key, path: j.paths[k], chars: max(c+w*5, 1), seg: plan.Segment{Text: j.texts[k]}}
			out[li] = &dialogueStep{seg: seg, k: k, chunk: &stand, fallback: fb}
		}
	}
	return out, nil
}

// spokenText is what a plan says, its speech segments joined.
func spokenText(pl plan.Plan) string {
	var parts []string
	for _, s := range pl.Segments {
		if s.Kind != plan.Silence && strings.TrimSpace(s.Text) != "" {
			parts = append(parts, strings.TrimSpace(s.Text))
		}
	}
	return joinTurn(parts)
}

// dialogueRef is the reference of line li's voice: a cloned voice's clip
// and transcript, a designed voice's reference, else the portable line on
// the story's model (its chunk, made when the segment is: dialogueRun).
func (cr chapterRenderer) dialogueRef(p *ChapterParams, li int,
	take func(int, SpanSpec, string) (core.Take, *int64, map[string]string, error), planOf func(core.Take, string) plan.Plan,
	info ModelInfo, modelPath string) (dialogueRef, *renderChunk, error) {
	sp := p.Lines[li].Spans[0]
	v := p.Voices[sp.Voice]
	audio, text := v.RefAudio, strings.TrimSpace(v.RefText)
	if audio == "" || text == "" {
		audio, text = v.CloneRef, strings.TrimSpace(v.CloneRefText)
	}
	if audio != "" && text != "" {
		key := audio
		if fi, err := os.Stat(audio); err == nil {
			key = fmt.Sprintf("%s|%d|%d", audio, fi.Size(), fi.ModTime().Unix())
		}
		h := sha256.Sum256([]byte(key))
		tidy := filepath.Join(cr.r.longformChunkDir(), "ref-"+hex.EncodeToString(h[:12])+".wav")
		return dialogueRef{audio: audio, text: text, key: key, tidy: tidy}, nil, nil
	}
	lang := p.Lang
	if lang == "" {
		lang = info.Lang(v.Language, v.Voice)
	}
	line := PortableLine(lang)
	// The voice in its plain delivery: no emotion, scene or instruction.
	saved, scene := p.Lines[li], p.Scene
	p.Lines[li].Emotion, p.Lines[li].Intensity, p.Lines[li].Direction, p.Lines[li].Persona, p.Lines[li].Speed = "", 0, "", "", 0
	p.Lines[li].Repeat, p.Scene = 0, ""
	t, seed, _, err := take(li, sp, line)
	p.Lines[li], p.Scene = saved, scene
	if err != nil {
		return dialogueRef{}, nil, err
	}
	pl := planOf(t, line)
	chunks, err := cr.r.chunks(&RenderParams{Take: t, Workflow: p.Workflow}, &pl, info, modelPath, seed, cr.r.longformChunkDir(), nil)
	if err != nil {
		return dialogueRef{}, nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	if len(chunks) != 1 {
		return dialogueRef{}, nil, &jobs.Error{Code: CodeRenderFailed, Message: "the reference line does not fit one chunk"}
	}
	c := chunks[0]
	return dialogueRef{audio: strings.TrimSuffix(c.path, ".wav") + "-ref.wav", text: line, key: c.key}, &c, nil
}

// dialogueRun makes a segment's lines (or finds them in the cache) with
// the shared runner: the portable references said on the story's model
// first; the segment's progress counts in the chapter's.
func (cr chapterRenderer) dialogueRun(ctx context.Context, st *renderState, i, n int, seg *dialogueSeg, model, modelPath string) error {
	r := cr.r
	j := &seg.job
	j.model, j.modelPath = model, modelPath
	for k, c := range seg.chunks {
		if c != nil {
			j.refs[k].made = func(ctx context.Context) (string, error) {
				_, _, err := st.synthesize(ctx, i, n, *c)
				return c.path, err
			}
		}
	}
	chars := 0
	for k := range j.texts {
		w, c := text.Count(j.texts[k])
		chars += max(c+w*5, 1)
	}
	base := st.seconds()
	j.report = func(pr jobs.Progress) {
		stage, fraction := "generating", -1.0
		if pr.Stage == "loading" {
			stage = "loading"
		}
		if st.total > 0 {
			fraction = min((float64(st.done)+max(pr.Fraction, 0)*float64(chars))/float64(st.total), 1)
		}
		st.report(jobs.Progress{Stage: stage, Fraction: fraction, AudioSeconds: base + pr.AudioSeconds,
			Message: fmt.Sprintf("dialogue, %d lines", len(seg.lines))})
	}
	out, err := r.dialogueRunner(st.job).Run(ctx, j)
	seg.out = out
	if !out.cached {
		st.done += chars
	}
	return err
}

// dialogueRunner is the shared dialogue runner for a render job.
func (r *renderer) dialogueRunner(job jobs.Job) dialogueRunner {
	return dialogueRunner{
		call: func(ctx context.Context, method, model string, params, out any, report func(jobs.Progress)) error {
			return r.callProgress(ctx, job, method, model, params, out, report)
		},
		aligner: r.aligner, recognizer: r.recognizer, tmp: filepath.Join(r.tmpDir(), job.ID), logf: r.logf, label: "chapter " + job.ID,
	}
}
