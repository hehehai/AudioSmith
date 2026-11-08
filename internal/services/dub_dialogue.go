package services

// The dub's dialogue model (plan/dialogue-dub-audiobook-plan.md §2,
// thread #89): in a track whose language the dialogue model speaks
// (SoulX-Podcast: Chinese and English), the conversations
// (dub.Project.DialogueStretches: two voices or more taking turns, no
// silence over 4 s, monologues left out) are spoken by it a stretch at a
// time, each line hearing the lines before it, through the shared runner
// (dialogue_run.go). Every line it makes is then fitted to its own slot
// as any other line (fitLine: dub.Plan, placed at the source's start, so
// the source's gaps and overlaps stay) and levelled.
//
// Each voice of a stretch is cloned from a reference
// (dub_dialogue_refs.go): a speaker's own voice from 10–14 s of their
// lines in the source, with the source's transcript (a1, thread #86); a
// saved cloned voice from its clip; a designed voice from its reference;
// any other voice says a reference text on the dub's model first.
//
// Files: renders/<lang>/dlg/<stretch key>/<line id>.wav (+ .codes, the
// context of a later redo; .txt, the text it says; .fail; .segfail). The
// stretch's key is the model, its revision, the seed, the references and
// the members, not the lines' text: a line edited is made again alone by
// continuation (dialogueRunner.Redo: the lines before it as context, two
// seeds), a voice changed makes the whole stretch again. A line moved is
// only fitted again.
//
// What the dialogue model does not make is made on the dub's model, after
// the stretches (dialogueTwoPass: the dialogue model stays loaded): a
// line that failed the check twice (the stretch's take, then one
// continuation with another seed), a stretch the model could not make,
// all of them when the model is not there (not downloaded: warning
// dub.dialogue_unavailable). Such a line is checked as the dialogue
// model's are; one that fails is kept but flagged (Render.Flag) for the
// user to listen to.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dialogue"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/plan"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// DubDialogueModel is a dialogue model a dub can speak its conversations
// with (DubModels.Dialogue).
type DubDialogueModel struct {
	Repo, Family, Revision string
	// Languages are the base codes it speaks ("zh", "en").
	Languages []string
	// Blocked says why this Mac cannot run it ("" = it can): too little
	// memory.
	Blocked string
}

// Codes of the dub's dialogue model.
const (
	// DubWarnDialogueUnavailable: the dialogue model is not there (not
	// downloaded, no engine, too little memory): every line was made line
	// by line.
	DubWarnDialogueUnavailable = "dub.dialogue_unavailable"
	// DubWarnDialogueFallback: some lines of the conversations were made
	// line by line (Render.FallbackReason).
	DubWarnDialogueFallback = "dub.dialogue_fallback"
	// DubWarnLinesFlagged: some lines failed their check (Render.Flag):
	// made line by line in place of the dialogue model's and not passing
	// its check either, or cloned from a clip in another language and
	// still wrong when made again (dub_check.go).
	DubWarnLinesFlagged = "dub.lines_flagged"
	// DubDialogueMemory is DubDialogueModel.Blocked on a Mac with too
	// little memory.
	DubDialogueMemory = "memory"
)

// dubDialogueVersion starts a stretch's key.
const dubDialogueVersion = "dub-dlg1"

// DialogueFor is the dialogue model a project asks for (its repo; "": the
// first offered), and whether it is offered.
func (m DubModels) DialogueFor(repo string) (DubDialogueModel, bool) {
	for _, d := range m.Dialogue {
		if repo == "" || d.Repo == repo {
			return d, true
		}
	}
	return DubDialogueModel{}, false
}

// DialogueBlocked says why lang's track of p is not spoken by its
// dialogue model ("" when it is, as far as the project and this Mac go;
// the download is checked when it runs): a dub.DialogueWhy* code,
// DubDialogueMemory, or "model" when no dialogue model is offered.
func (m DubModels) DialogueBlocked(p *dub.Project, lang string) string {
	d, ok := m.DialogueFor(p.Settings.DialogueModel)
	if !ok {
		if !p.Settings.DialogueOn() {
			return dub.DialogueWhyOff
		}
		return "model"
	}
	if why := p.DialogueBlocked(lang, d.Languages); why != "" {
		return why
	}
	return d.Blocked
}

// dialogueRates are the dialogue models' speech rates: how much longer
// than the translation's estimate they speak (raw / predicted), learnt
// from the lines they make (a moving average, as translate.Predictor
// learns the dub's model; from 1.07 for SoulX-Podcast, P0's 1.01–1.12×).
// The translation of a track they speak aims at it (dub.FitRate).
var dialogueRates = &modelRates{start: map[string]float64{"soulx_podcast": 1.07}}

type modelRates struct {
	mu      sync.Mutex
	start   map[string]float64 // by family
	learned map[string]float64 // by repo
}

func (m *modelRates) rate(d DubDialogueModel) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.learned[d.Repo]; ok {
		return r
	}
	if r, ok := m.start[d.Family]; ok {
		return r
	}
	return 1
}

// observe learns from a line of seconds predicted to last predicted.
func (m *modelRates) observe(d DubDialogueModel, seconds, predicted float64) {
	if predicted < 1 || seconds <= 0 {
		return
	}
	obs := min(max(seconds/predicted, 0.6), 1.6)
	r := m.rate(d)
	m.mu.Lock()
	if m.learned == nil {
		m.learned = map[string]float64{}
	}
	m.learned[d.Repo] = 0.7*r + 0.3*obs
	m.mu.Unlock()
}

// dialogueSpeed is the speed a track's translation assumes: the
// project's, slowed by the dialogue model's rate over the share of the
// lines it speaks (unchanged when it does not speak the track).
func (s *Dubs) dialogueSpeed(p *dub.Project, l string) float64 {
	m := s.cfg.Models()
	if m.DialogueBlocked(p, l) != "" {
		return p.Settings.Speed
	}
	d, _ := m.DialogueFor(p.Settings.DialogueModel)
	share := p.DialogueShare(l, dialogue.LimitsFor(d.Family))
	if share == 0 {
		return p.Settings.Speed
	}
	speed := p.Settings.Speed
	if speed <= 0 {
		speed = 1
	}
	return speed / dub.FitRate(dialogueRates.rate(d), share)
}

// dubDialogue is a synthesize stage's dialogue model: its stretches, the
// job of each and what became of their lines.
type dubDialogue struct {
	s         *Dubs
	sc        *stageCtx
	l         string
	model     DubDialogueModel
	dr        dialogueRunner
	stretches []dub.Stretch
	keys      []string // by stretch
	jobs      []*dialogueJob
	byID      map[string]int    // stretch of a line
	reasons   map[string]string // why a line went to the dub's model
	redo      map[string]int    // lines asked for again (Only): made again by continuation, the nth time
	fresh     bool              // Force: every stretch made again
}

// dubDialogue plans lang's dialogue (nil: every line line by line, with
// a warning when the project asked for it and the model is not there).
// voices are the lines' voices (by id).
func (s *Dubs) dubDialogue(ctx context.Context, sc *stageCtx, voices map[string]dub.VoiceSpec, caps DubModelCaps) (*dubDialogue, error) {
	p, l := sc.p, sc.params.Lang
	m := s.cfg.Models()
	why := m.DialogueBlocked(p, l)
	if why != "" && why != DubDialogueMemory {
		return nil, nil
	}
	d, _ := m.DialogueFor(p.Settings.DialogueModel)
	sts := p.DialogueStretches(l, dialogue.LimitsFor(d.Family))
	if len(sts) == 0 {
		return nil, nil
	}
	if why == DubDialogueMemory {
		sc.warn(DubWarnDialogueUnavailable)
		return nil, nil
	}
	modelPath := ""
	if s.cfg.ModelPath != nil {
		dir, err := s.cfg.ModelPath(ctx, d.Repo)
		if err != nil {
			if errors.Is(err, core.ErrNotDownloaded) {
				sc.warn(DubWarnDialogueUnavailable)
				return nil, nil
			}
			return nil, err
		}
		modelPath = dir
	}
	recognizer, _ := m.STTFor(l)
	dd := &dubDialogue{s: s, sc: sc, l: l, model: d, stretches: sts, byID: map[string]int{}, reasons: map[string]string{}, redo: map[string]int{},
		fresh: sc.params.Force}
	dd.dr = s.dialogueRunner(sc, recognizer, m.Align)
	takes := p.Track(l).DialogueTakes
	for si, st := range sts {
		j := &dialogueJob{model: d.Repo, modelPath: modelPath, oneTrack: OneTrackDialogue(d.Family), lang: baseLang(l),
			seed: dub.DialogueSeed(st.IDs[0], takes[st.IDs[0]])}
		h := sha256.New()
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%s\x00", dubDialogueVersion, d.Repo, d.Revision, j.seed, l)
		refs := map[string]int{}
		for _, id := range st.IDs {
			seg, _ := p.Segment(id)
			voice := p.DialogueVoice(*seg)
			k, ok := refs[voice]
			if !ok {
				ref, err := s.dialogueRef(sc, *seg, voices[id], caps)
				if err != nil {
					return nil, err
				}
				k = len(j.refs)
				refs[voice] = k
				j.refs = append(j.refs, ref)
				fmt.Fprintf(h, "ref %s\x00%s\x00", ref.key, ref.text)
			}
			say := s.dialogueSay(ctx, d.Repo, p.TextFor(*seg, l), j.lang)
			j.speakers = append(j.speakers, k)
			j.texts = append(j.texts, say)
			fmt.Fprintf(h, "turn %s %d\x00", id, k)
			dd.byID[id] = si
		}
		key := hex.EncodeToString(h.Sum(nil))[:32]
		j.key = key
		for _, id := range st.IDs {
			j.paths = append(j.paths, sc.path(filepath.Join(dd.dir(key), dub.SafeName(id)+".wav")))
		}
		dd.keys = append(dd.keys, key)
		dd.jobs = append(dd.jobs, j)
	}
	// A line asked for again is made again by continuation, with other
	// seeds each time (counted in the track).
	for _, id := range sc.params.Only {
		if _, ok := dd.byID[id]; ok {
			dd.redo[id] = takes["line:"+id] + 1
		}
	}
	if len(dd.redo) > 0 {
		err := sc.save(ctx, func(q *dub.Project) {
			t := q.Track(l)
			t.Lang = l
			if t.DialogueTakes == nil {
				t.DialogueTakes = map[string]int{}
			}
			for id, n := range dd.redo {
				t.DialogueTakes["line:"+id] = n
			}
			q.PutTrack(t)
		})
		if err != nil {
			return nil, err
		}
	}
	return dd, nil
}

// dir is a stretch's directory in the project.
func (dd *dubDialogue) dir(key string) string {
	return filepath.Join("renders", dub.SafeName(dd.l), "dlg", key)
}

// key is line id's render key: its fingerprint fp, and its stretch's key.
func (dd *dubDialogue) key(id, fp string) string {
	if dd == nil {
		return fp
	}
	if si, ok := dd.byID[id]; ok {
		return dub.DialogueKey(fp, dd.keys[si])
	}
	return fp
}

// groups are the stale lines (indices of work, whose ids ids gives) by
// stretch; the stretches without any are left out.
func (dd *dubDialogue) groups(ids []string) (segs [][]int, which []int) {
	at := map[int]int{}
	for wi, id := range ids {
		si, ok := dd.byID[id]
		if !ok {
			continue
		}
		g, seen := at[si]
		if !seen {
			g = len(segs)
			at[si] = g
			segs = append(segs, nil)
			which = append(which, si)
		}
		segs[g] = append(segs[g], wi)
	}
	return segs, which
}

// dialogueRunner is the shared runner on the dub's engine calls: the
// model's local copy filled in (a tts.dialogue's, when set, kept).
func (s *Dubs) dialogueRunner(sc *stageCtx, recognizer, aligner string) dialogueRunner {
	return dialogueRunner{
		call: func(ctx context.Context, method, model string, params, out any, report func(jobs.Progress)) error {
			var mp *string
			switch q := params.(type) {
			case *proto.DialogueParams:
				q.Model = model
				if q.ModelPath == "" {
					mp = &q.ModelPath
				}
			case *proto.TranscribeParams:
				q.Model, mp = model, &q.ModelPath
			case *proto.AlignParams:
				q.Model, mp = model, &q.ModelPath
			}
			return sc.call(ctx, method, model, params, mp, out, report)
		},
		aligner: aligner, recognizer: recognizer, tmp: sc.path(filepath.Join("renders", "tmp", "dlg")), logf: s.cfg.Logf,
		label: "dub " + sc.job.ID,
	}
}

// dialogueSay is what the dialogue model is given for a line: its text
// as the render pipeline would say it (numbers, dates and the
// pronunciation dictionary written out), without markup.
func (s *Dubs) dialogueSay(ctx context.Context, repo, txt, lang string) string {
	var info ModelInfo
	var dict = s.cfg.Render.render.dictionary(ctx, lang)
	info = s.cfg.Render.render.model(repo)
	pl := plan.Build(txt, plan.Options{Lang: lang, NonVerbal: info.NonVerbal, NonVerbalTags: info.NonVerbalTags, Speed: info.Speed, Voices: true,
		MaxChars: info.MaxChars, MaxCharsCJK: info.MaxCharsCJK, Lexicon: dict})
	if say := spokenText(pl); say != "" {
		return say
	}
	return strings.TrimSpace(stripTags(txt))
}

// make makes stretch si's lines wanted (indices of ids): their files from
// the cache, the stretch made whole when none of it is there, a line
// whose text changed (or asked for again) by continuation. fit fits a
// made line; the lines it gives back are for the dub's model
// (dd.reasons says why).
func (dd *dubDialogue) make(ctx context.Context, si int, wanted []int, ids []string, fit func(wi int, path string) error) ([]int, error) {
	st, j := dd.stretches[si], dd.jobs[si]
	p := dd.sc.p
	if err := os.MkdirAll(dd.sc.path(dd.dir(dd.keys[si])), 0o755); err != nil {
		return nil, err
	}
	os.MkdirAll(dd.sc.path(filepath.Join("renders", "tmp")), 0o755)
	texts := make([]string, len(st.IDs))
	have := 0
	for k, id := range st.IDs {
		seg, _ := p.Segment(id)
		texts[k] = p.TextFor(*seg, dd.l)
		path := j.paths[k]
		if said, err := os.ReadFile(path + ".txt"); err != nil || string(said) != texts[k] || dd.fresh {
			// Made for another text (or never): it goes.
			for _, ext := range []string{"", ".codes", ".fail", ".txt"} {
				os.Remove(path + ext)
			}
		}
		if fi, err := os.Stat(path); err == nil && fi.Size() > 44 {
			have++
		}
	}
	if dd.fresh {
		os.Remove(j.paths[0] + ".segfail")
	}
	var back []int
	giveBack := func(wi int, err error) {
		dd.reasons[ids[wi]] = dialogueReason(err)
		back = append(back, wi)
	}
	if have == 0 {
		out, err := dd.dr.Run(ctx, j)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			if isEngineGone(err) {
				dd.sc.warn(DubWarnDialogueUnavailable)
			}
			for _, wi := range wanted {
				giveBack(wi, err)
			}
			return back, nil
		}
		for k := range st.IDs {
			if _, failed := out.failed[k]; !failed {
				os.WriteFile(j.paths[k]+".txt", []byte(texts[k]), 0o644)
			}
		}
	}
	for _, wi := range wanted {
		k := st.Index(ids[wi])
		path := j.paths[k]
		if dd.redo[ids[wi]] > 0 && have > 0 {
			os.Remove(path)
		}
		if _, err := os.Stat(path); err != nil {
			if b, err := os.ReadFile(path + ".fail"); err == nil {
				giveBack(wi, errors.New(strings.TrimSpace(string(b))))
				continue
			}
			if err := dd.redoLine(ctx, si, k); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				giveBack(wi, err)
				continue
			}
			os.WriteFile(path+".txt", []byte(texts[k]), 0o644)
		}
		if err := fit(wi, path); err != nil {
			return nil, err
		}
	}
	return back, nil
}

// redoLine makes line k of stretch si again by continuation: the lines
// before it that the model made (their codes) as context, two seeds
// (dialogueRunner.Redo), another pair each time it is asked for again.
func (dd *dubDialogue) redoLine(ctx context.Context, si, k int) error {
	j := dd.jobs[si]
	sub := &dialogueJob{model: j.model, modelPath: j.modelPath, oneTrack: j.oneTrack, refs: j.refs, lang: j.lang, key: fmt.Sprintf("%s-%d", j.key, k),
		seed: j.seed + 104729*int64(dd.lineTake(dd.stretches[si].IDs[k]))}
	var ctxCodes [][]int
	for i := range k {
		c := dialogueCodes(j.paths[i])
		if _, err := os.Stat(j.paths[i]); err != nil || len(c) == 0 {
			continue // made otherwise: not heard
		}
		sub.speakers, sub.texts, sub.paths = append(sub.speakers, j.speakers[i]), append(sub.texts, j.texts[i]), append(sub.paths, j.paths[i])
		ctxCodes = append(ctxCodes, c)
	}
	sub.speakers, sub.texts, sub.paths = append(sub.speakers, j.speakers[k]), append(sub.texts, j.texts[k]), append(sub.paths, j.paths[k])
	return dd.dr.Redo(ctx, sub, len(sub.texts)-1, ctxCodes)
}

// lineTake is how many times line id was asked for again.
func (dd *dubDialogue) lineTake(id string) int {
	if n, ok := dd.redo[id]; ok {
		return n
	}
	return dd.sc.p.Track(dd.l).DialogueTakes["line:"+id]
}

// mark notes on a line made on the dub's model how it relates to the
// dialogue: its stretch, and when it was given back, why, and whether it
// passes the dialogue model's check (a flag when it does not).
func (dd *dubDialogue) mark(ctx context.Context, r *dub.Render, id string) {
	si, ok := dd.byID[id]
	if !ok {
		return
	}
	r.Dialogue = dd.stretches[si].N
	reason, back := dd.reasons[id]
	if !back || r.Err != "" || r.File == "" {
		return
	}
	r.Via, r.FallbackReason = dub.ViaFallback, reason
	dd.sc.warn(DubWarnDialogueFallback)
	if reason == DialogueUnavailable {
		return // the model is not there: nothing to compare with
	}
	if flag, heard := dd.check(ctx, r.File, r.Text); flag != "" {
		r.Flag, r.Heard = flag, heard
		dd.sc.warn(DubWarnLinesFlagged)
	}
}

// check checks a line made on the dub's model as the dialogue model's
// lines are (a plausible length for its text, the recognizer hears it,
// in its language's script: dub.ForeignScript): the flag ("" when it
// passes) and what was heard.
func (dd *dubDialogue) check(ctx context.Context, file, say string) (string, string) {
	path := dd.sc.path(file)
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		return "", ""
	}
	w, c := text.Count(stripTags(say))
	if want := text.Estimate(w, c).Seconds(); want > 0 && (buf.Seconds() < dialogueMinRatio*want || buf.Seconds() > dialogueMaxRatio*want) {
		return dub.FlagLength, ""
	}
	if dd.dr.recognizer == "" {
		return "", ""
	}
	var tr proto.TranscribeResult
	if err := dd.dr.call(ctx, proto.MethodSTTTranscribe, dd.dr.recognizer, &proto.TranscribeParams{Audio: path, Language: baseLang(dd.l)}, &tr, nil); err != nil {
		return "", ""
	}
	if dub.ForeignScript(dd.l, tr.Text) != "" {
		return dub.FlagLanguage, strings.TrimSpace(tr.Text) // another language's script (thread #90)
	}
	if dub.Drift(foldPronouns(say), foldPronouns(tr.Text)) > cutMaxCER {
		return dub.FlagHeard, strings.TrimSpace(tr.Text)
	}
	return "", ""
}

// made notes a line the dialogue model made: its stretch and the rate it
// spoke at.
func (dd *dubDialogue) made(r *dub.Render, id string, seconds float64) {
	si := dd.byID[id]
	r.Dialogue, r.Via = dd.stretches[si].N, dub.ViaDialogue
	seg, _ := dd.sc.p.Segment(id)
	dialogueRates.observe(dd.model, seconds, translate.DefaultPredictor.Duration(r.Text, dub.LangFor(*seg, dd.l), 1))
}

// prune deletes the track's stretch directories no stretch uses now.
func (dd *dubDialogue) prune() {
	dirs, _ := filepath.Glob(dd.sc.path(filepath.Join("renders", dub.SafeName(dd.l), "dlg", "*")))
	for _, d := range dirs {
		if !slices.Contains(dd.keys, filepath.Base(d)) {
			os.RemoveAll(d)
		}
	}
}
