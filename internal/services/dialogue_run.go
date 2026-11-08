package services

// Making a dialogue segment with a dialogue model (the shared layer of
// plan/dialogue-dub-audiobook-plan.md §4.1: Stories now, the dub and the
// audiobook next). The caller groups its lines (internal/dialogue), picks
// each voice's reference and names the files; a dialogueRunner then
//
//   - tidies the references (refcheck.Tidy: edges trimmed, long pauses
//     shortened, −18 LUFS; the models copy a reference's loudness), after
//     making the ones that must be said first (a story's portable line);
//   - speaks the segment in one tts.dialogue: a per-turn model
//     (SoulX-Podcast) writes each line to its own file, a one-track model
//     (MOSS-TTSD, VibeVoice) the segment as one track, which is cut at the lines with
//     the forced aligner (dialogue_run_mixed.go);
//   - checks each line (a plausible length for its text, the recognizer
//     hears it) and makes a line that fails once more by continuation: the
//     same speakers, the lines before it as context (their codes), another
//     seed;
//   - caches the result by the files the caller named: a line's file and
//     its codes (.codes, the context of a later redo), a line that failed
//     twice (.fail: the caller makes it its own way, line by line), a
//     segment the model could not make (.segfail on the first file).
//
// Redo makes one line again by continuation (a dub's edited line), and
// dialogueTwoPass orders a job's work so that the dialogue model stays
// loaded: every segment first, then the regular model's lines.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/dialogue"
	"github.com/hehehai/AudioSmith/internal/dsp/refcheck"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text"
)

// FamilyMossTTSD is MOSS-TTSD's catalogue family: a one-track dialogue model.
const FamilyMossTTSD = dialogue.FamilyMossTTSD

// OneTrackDialogue reports whether a dialogue model family speaks a
// segment as one track (MOSS-TTSD, VibeVoice: dialogue.OneTrack).
func OneTrackDialogue(family string) bool { return dialogue.OneTrack(family) }

// FamilyVibeVoice is VibeVoice 1.5B's catalogue family (thread #166).
const FamilyVibeVoice = dialogue.FamilyVibeVoice

// A line lasts within dialogueMinRatio–dialogueMaxRatio of text.Estimate.
const (
	dialogueMinRatio = 0.3
	dialogueMaxRatio = 3.0
)

// errDialogueLength: a line far shorter or longer than its text.
var errDialogueLength = errors.New("the line is far shorter or longer than its text")

// Why a line was not made by the dialogue model (dialogueReason), as a
// dub's Render.FallbackReason will say it.
const (
	DialogueHeard         = "heard"          // the recognizer did not hear it, twice
	DialogueLength        = "length"         // far shorter or longer than its text, twice
	DialogueSegmentFailed = "segment_failed" // the model could not make the segment
	DialogueUnavailable   = "unavailable"    // no model, no licence or no engine
)

// dialogueReason is the reason code of a line's or segment's error.
func dialogueReason(err error) string {
	switch {
	case err == nil:
		return ""
	case isEngineGone(err):
		return DialogueUnavailable
	case errors.Is(err, errDialogueLength), strings.Contains(err.Error(), errDialogueLength.Error()):
		return DialogueLength
	case errors.Is(err, errCutHeard), strings.Contains(err.Error(), errCutHeard.Error()):
		return DialogueHeard
	}
	return DialogueSegmentFailed
}

// dialogueRef is a speaker's reference: a clip and its exact transcript.
type dialogueRef struct {
	audio, text string
	key         string // identifies the clip for the caller's keys
	// tidy is where the clip is tidied to ("": used as it is); a clip
	// that cannot be tidied is used as it is.
	tidy string
	// made, when set, makes the clip first and returns it; it is tidied
	// to audio, and must be (a story's portable line).
	made func(ctx context.Context) (string, error)
}

// dialogueJob is a segment to make.
type dialogueJob struct {
	model, modelPath string
	oneTrack         bool // the model speaks the segment as one track (MOSS-TTSD, VibeVoice)
	refs             []dialogueRef
	speakers         []int    // per line: index in refs
	texts            []string // per line: what it says (checked against)
	tagged           []string // per line: what the model is given (nil: texts)
	lang             string   // the recognizer's and aligner's language
	seed             int64
	key              string   // names the temporary files
	paths            []string // per line: its file in the cache
	// report follows the segment's call (nil: none).
	report func(jobs.Progress)
	// check checks line k's file (nil: dialogueRunner.check).
	check func(ctx context.Context, k int, path string) error
}

// dialogueOutcome is what became of a segment's lines.
type dialogueOutcome struct {
	failed map[int]error // lines for the caller to make its own way
	redone int           // lines made twice
	cached bool          // all from the cache: the model was not called
}

// dialogueRunner makes segments. call runs an engine request with the
// model's local snapshot filled in (a tts.dialogue's modelPath, when set,
// kept); tmp prefixes the temporary directories.
type dialogueRunner struct {
	call                func(ctx context.Context, method, model string, params, out any, report func(jobs.Progress)) error
	aligner, recognizer string
	tmp                 string
	logf                func(string, ...any)
	label               string // what the logs name ("chapter <job>")
}

func (dr dialogueRunner) log(format string, args ...any) {
	if dr.logf != nil {
		dr.logf(dr.label+": "+format, args...)
	}
}

// turnText is what the model is given for line k.
func (j *dialogueJob) turnText(k int) string {
	if j.tagged != nil {
		return j.tagged[k]
	}
	return j.texts[k]
}

// Run makes a segment's lines (or finds them in the cache). An error
// means the segment could not be made: none of its lines are; ctx's
// error, and an engine without the model or its licence, pass through
// (isEngineGone: nothing is cached).
func (dr dialogueRunner) Run(ctx context.Context, j *dialogueJob) (dialogueOutcome, error) {
	out := dialogueOutcome{failed: map[int]error{}, cached: true}
	for k, p := range j.paths {
		if b, err := os.ReadFile(p + ".fail"); err == nil {
			out.failed[k] = errors.New(strings.TrimSpace(string(b)))
			continue
		}
		if fi, err := os.Stat(p); err != nil || fi.Size() <= 44 {
			out.cached = false
		}
	}
	if out.cached {
		return out, nil
	}
	if b, err := os.ReadFile(j.paths[0] + ".segfail"); err == nil {
		return out, errors.New(strings.TrimSpace(string(b)))
	}
	out.cached = false
	clear(out.failed)
	speakers, err := dr.references(ctx, j)
	if err != nil {
		return out, err
	}
	turns := make([]proto.DialogueTurn, len(j.texts))
	for k := range j.texts {
		turns[k] = proto.DialogueTurn{Speaker: speakers[j.speakers[k]].ID, Text: j.turnText(k)}
	}
	dir := dr.tmp + "-" + j.key
	defer os.RemoveAll(dir)
	seed := j.seed
	params := proto.DialogueParams{Model: j.model, ModelPath: j.modelPath, Speakers: speakers, Turns: turns, OutputDir: dir, Bits: 32, Seed: &seed}
	var res proto.DialogueResult
	if err := dr.call(ctx, proto.MethodTTSDialogue, j.model, &params, &res, j.report); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if !isEngineGone(err) {
			os.WriteFile(j.paths[0]+".segfail", []byte(err.Error()), 0o644)
		}
		return out, err
	}
	if res.Mixed != nil {
		err := dr.mixed(ctx, j, &out, params, res)
		return out, err
	}
	codes := make([][]int, len(j.texts))
	files := make([]string, len(j.texts))
	for _, t := range res.Turns {
		if t.Index >= 0 && t.Index < len(j.texts) {
			codes[t.Index], files[t.Index] = t.Codes, t.Output
		}
	}
	for k := range j.texts {
		if files[k] == "" {
			return out, &jobs.Error{Code: CodeRenderFailed, Message: fmt.Sprintf("the dialogue model returned no line %d", k+1)}
		}
		err := dr.checkLine(ctx, j, k, files[k])
		if err != nil && ctx.Err() == nil {
			// Once more with the same context and another seed.
			dr.log("dialogue line %d: %v; making it again", k+1, err)
			out.redone++
			f, c, err2 := dr.continueLine(ctx, j, speakers, k, codes[:k], dialogueRedoSeed(j.seed, k, 0), fmt.Sprintf("%s/line-%d", dir, k))
			if err2 == nil {
				files[k], codes[k] = f, c
				err = dr.checkLine(ctx, j, k, f)
			} else if !errors.Is(err2, errNoLine) {
				err = err2
			}
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if err := dr.keep(j, k, files[k], codes[k], err, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}

// keep caches line k: its file and codes, else (failed) why it failed.
func (dr dialogueRunner) keep(j *dialogueJob, k int, file string, codes []int, failed error, out *dialogueOutcome) error {
	p := j.paths[k]
	if failed != nil {
		dr.log("dialogue line %d: %v; made line by line", k+1, failed)
		out.failed[k] = failed
		os.WriteFile(p+".fail", []byte(failed.Error()), 0o644)
		return nil
	}
	if err := moveFile(file, p); err != nil {
		return &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	os.Remove(p + ".fail")
	if len(codes) > 0 {
		if b, err := json.Marshal(codes); err == nil {
			writeFileAtomic(p+".codes", b)
		}
	}
	return nil
}

// dialogueCodes are a cached line's codes (nil: none).
func dialogueCodes(path string) []int {
	var c []int
	if b, err := os.ReadFile(path + ".codes"); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

// dialogueRedoSeed is the seed of line k's redo: attempt 0 is the one a
// segment makes itself (as stories always have), later ones others.
func dialogueRedoSeed(seed int64, k, attempt int) int64 {
	return seed + 7919*int64(k+1) + 104729*int64(attempt)
}

// errNoLine: the model answered a redo without the line.
var errNoLine = errors.New("the dialogue model returned no line")

// continueLine makes line k after the lines before it (ctxCodes: their
// codes, the context) with seed in dir; returns its file and codes.
func (dr dialogueRunner) continueLine(ctx context.Context, j *dialogueJob, speakers []proto.DialogueSpeaker, k int, ctxCodes [][]int,
	seed int64, dir string) (string, []int, error) {
	turns := make([]proto.DialogueTurn, k+1)
	for i := range turns {
		turns[i] = proto.DialogueTurn{Speaker: speakers[j.speakers[i]].ID, Text: j.turnText(i)}
		if i < k {
			if len(ctxCodes) <= i || len(ctxCodes[i]) == 0 {
				return "", nil, fmt.Errorf("line %d has no codes to hear", i+1)
			}
			turns[i].Codes = ctxCodes[i]
		}
	}
	params := proto.DialogueParams{Model: j.model, ModelPath: j.modelPath, Speakers: speakers, Turns: turns, OutputDir: dir, Bits: 32, Seed: &seed}
	var res proto.DialogueResult
	if err := dr.call(ctx, proto.MethodTTSDialogue, j.model, &params, &res, nil); err != nil {
		return "", nil, err
	}
	if res.Mixed != nil {
		codes := res.Mixed.Codes
		if n := res.Mixed.Codebooks; k > 0 && n > 0 && len(codes) >= n {
			codes = codes[n:] // a later line's frames: without the track's leading frame
		}
		return res.Mixed.Output, codes, nil
	}
	if len(res.Turns) != 1 || res.Turns[0].Output == "" {
		return "", nil, errNoLine
	}
	return res.Turns[0].Output, res.Turns[0].Codes, nil
}

// Redo makes line k of a made segment again by continuation, the lines
// before it (ctxCodes: their codes, dialogueCodes of their files) as
// context: with another seed, checked, once more if it fails. The line
// is cached as Run caches it; an error means it is to be made otherwise
// (its reason: dialogueReason).
func (dr dialogueRunner) Redo(ctx context.Context, j *dialogueJob, k int, ctxCodes [][]int) error {
	speakers, err := dr.references(ctx, j)
	if err != nil {
		return err
	}
	dir := fmt.Sprintf("%s-%s-redo-%d", dr.tmp, j.key, k)
	defer os.RemoveAll(dir)
	var failed error
	for attempt := range 2 {
		f, codes, err := dr.continueLine(ctx, j, speakers, k, ctxCodes, dialogueRedoSeed(j.seed, k, attempt), fmt.Sprintf("%s/%d", dir, attempt))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if failed = dr.checkLine(ctx, j, k, f); failed == nil {
			out := dialogueOutcome{failed: map[int]error{}}
			return dr.keep(j, k, f, codes, nil, &out)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dr.log("dialogue line %d: %v", k+1, failed)
	}
	out := dialogueOutcome{failed: map[int]error{}}
	dr.keep(j, k, "", nil, failed, &out)
	return failed
}

// references are the segment's speakers: each reference made (when it
// must be) and tidied.
func (dr dialogueRunner) references(ctx context.Context, j *dialogueJob) ([]proto.DialogueSpeaker, error) {
	speakers := make([]proto.DialogueSpeaker, len(j.refs))
	for k, ref := range j.refs {
		audio := ref.audio
		if ref.made != nil {
			src, err := ref.made(ctx)
			if err != nil {
				return nil, err
			}
			if err := tidyReference(src, ref.audio); err != nil {
				return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
			}
		} else if ref.tidy != "" {
			if err := tidyReference(ref.audio, ref.tidy); err == nil {
				audio = ref.tidy
			} else {
				dr.log("reference %s used as it is: %v", ref.audio, err)
			}
		}
		speakers[k] = proto.DialogueSpeaker{ID: fmt.Sprintf("s%d", k), RefAudio: audio, RefText: ref.text}
	}
	return speakers, nil
}

// checkLine checks line k's file with the job's check, else the runner's.
func (dr dialogueRunner) checkLine(ctx context.Context, j *dialogueJob, k int, path string) error {
	if j.check != nil {
		return j.check(ctx, k, path)
	}
	return dr.check(ctx, j, k, path)
}

// check checks line k's file: a plausible length for its text, and the
// recognizer (when there is one) hears it.
func (dr dialogueRunner) check(ctx context.Context, j *dialogueJob, k int, path string) error {
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		return err
	}
	w, c := text.Count(j.texts[k])
	want := text.Estimate(w, c).Seconds()
	if got := buf.Seconds(); want > 0 && (got < dialogueMinRatio*want || got > dialogueMaxRatio*want) {
		return fmt.Errorf("%.1f s for %.1f s of text: %w", got, want, errDialogueLength)
	}
	if dr.recognizer == "" {
		return nil
	}
	var tr proto.TranscribeResult
	if err := dr.call(ctx, proto.MethodSTTTranscribe, dr.recognizer, &proto.TranscribeParams{Audio: path, Language: j.lang}, &tr, nil); err != nil {
		return err
	}
	if d := dub.Drift(foldPronouns(j.texts[k]), foldPronouns(tr.Text)); d > cutMaxCER {
		return fmt.Errorf("heard %q (error rate %.2f): %w", tr.Text, d, errCutHeard)
	}
	return nil
}

// dialogueTwoPass runs a job's lines so that the dialogue model stays
// loaded (plan §2.6): every segment first (seg makes segment i and gives
// back the lines it did not make), then on the regular model, in line
// order, the lines in no segment and those given back. progress counts
// the lines made of n. An error from seg or line ends the run.
func dialogueTwoPass(ctx context.Context, n int, segs [][]int, seg func(ctx context.Context, i int) ([]int, error),
	line func(ctx context.Context, li int) error, progress func(done, total int)) error {
	inSeg := make([]bool, n)
	done := 0
	report := func() {
		if progress != nil {
			progress(done, n)
		}
	}
	var rest []int
	for i, g := range segs {
		if err := ctx.Err(); err != nil {
			return err
		}
		back, err := seg(ctx, i)
		if err != nil {
			return err
		}
		for _, li := range g {
			inSeg[li] = true
		}
		rest = append(rest, back...)
		done += len(g) - len(back)
		report()
	}
	for li := range n {
		if !inSeg[li] {
			rest = append(rest, li)
		}
	}
	slices.Sort(rest)
	rest = slices.Compact(rest)
	for _, li := range rest {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := line(ctx, li); err != nil {
			return err
		}
		done++
		report()
	}
	return nil
}

// tidyReference writes src tidied (refcheck.Tidy: edges trimmed, long
// pauses shortened, −18 LUFS) as dst (once). Every reference of a scene
// is then as loud as the others: the models copy a reference's loudness.
func tidyReference(src, dst string) error {
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 44 {
		return nil
	}
	b, _, err := wav.ReadFile(src)
	if err != nil {
		return err
	}
	out, _ := refcheck.Tidy(b, nil)
	tmp := dst + ".tmp.wav"
	if err := wav.WriteFile(tmp, out, wav.Float32, wav.Options{}); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// moveFile renames src to dst, copying across file systems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(dst, data); err != nil {
		return err
	}
	return os.Remove(src)
}
