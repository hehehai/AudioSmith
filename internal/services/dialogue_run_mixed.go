package services

// One-track dialogue (MOSS-TTSD, thread #82; VibeVoice, #166, whose track
// has no codes: its failing lines are left to the caller): the model speaks a
// segment's lines as one track (proto.DialogueResult.Mixed), which is cut
// back into the lines as character tracks are (story_track.go): the forced
// aligner times the segment's text, each line must be the text's words at
// a plausible share of the time, and the cut falls at the quietest point
// between two lines. A track the aligner cannot cut (a line skipped,
// repeated or garbled) is made once more with another seed, else the
// segment fails. Each cut line then gets the per-line checks (its length
// for its text, the recognizer hears it); a line that fails is made again
// by continuation: the same speakers, the lines before it as context
// (their codec frames, cut from the track's), the line itself to speak,
// another seed. Failing twice, it is left to the caller. The caller
// decides about the model's own gaps (a story trims each line's edges and
// puts its pacing's gaps back).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// mossFrameSamples is a MOSS-TTSD codec frame at 24 kHz (12.5 a second).
const mossFrameSamples = 1920

// mixed cuts a one-track result into the segment's lines, checks them
// and makes failing lines again (the first call's params and result).
func (dr dialogueRunner) mixed(ctx context.Context, j *dialogueJob, out *dialogueOutcome, params proto.DialogueParams, res proto.DialogueResult) error {
	files, cuts, err := dr.cutMixed(ctx, j, res.Mixed.Output)
	if err != nil && ctx.Err() == nil && !isEngineGone(err) {
		// Once more with another seed.
		dr.log("dialogue track: %v; making it again", err)
		redo := params
		redo.OutputDir = params.OutputDir + "-again"
		defer os.RemoveAll(redo.OutputDir)
		s2 := dialogueRedoSeed(j.seed, len(j.texts), 0)
		redo.Seed = &s2
		var res2 proto.DialogueResult
		if err = dr.call(ctx, proto.MethodTTSDialogue, j.model, &redo, &res2, nil); err == nil {
			if res2.Mixed == nil {
				err = fmt.Errorf("the dialogue model returned no track")
			} else {
				out.redone += len(j.texts)
				params, res = redo, res2
				files, cuts, err = dr.cutMixed(ctx, j, res.Mixed.Output)
			}
		}
	}
	cleanup := slices.Clone(files)
	defer func() {
		for _, f := range cleanup {
			os.Remove(f)
		}
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if !isEngineGone(err) {
			os.WriteFile(j.paths[0]+".segfail", []byte(err.Error()), 0o644)
		}
		return err
	}
	m := res.Mixed
	frames := func(k int) []int {
		if m.Codebooks <= 0 || len(m.Codes) == 0 {
			return nil
		}
		return mixedFrames(m, cuts, k)
	}
	before := make([][]int, len(j.texts)) // each line's frames in the track
	for k := range j.texts {
		before[k] = frames(k)
	}
	for k := range j.texts {
		codes := before[k]
		err := dr.checkLine(ctx, j, k, files[k])
		if err != nil && ctx.Err() == nil && m.Codebooks > 0 && len(m.Codes) > 0 {
			dr.log("dialogue line %d: %v; making it again after the lines before it", k+1, err)
			out.redone++
			var again string
			again, codes, err = dr.continueLine(ctx, j, params.Speakers, k, before[:k], dialogueRedoSeed(j.seed, k, 0),
				fmt.Sprintf("%s/line-%d", params.OutputDir, k))
			if err == nil {
				files[k] = again
				err = dr.checkLine(ctx, j, k, again)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := dr.keep(j, k, files[k], codes, err, out); err != nil {
			return err
		}
	}
	return nil
}

// isEngineGone reports errors a second try cannot help: no model, no
// licence, no engine.
func isEngineGone(err error) bool {
	var je *jobs.Error
	if !errors.As(err, &je) {
		return false
	}
	return je.Code == proto.CodeModelNotDownloaded || je.Code == proto.CodeLicenceRequired || je.Code == CodeEngineUnavailable
}

// cutMixed cuts the track at path into the segment's lines (temporary
// files next to their cache paths) and returns them with the cuts: the
// sample where each line starts, then the track's end.
func (dr dialogueRunner) cutMixed(ctx context.Context, j *dialogueJob, path string) ([]string, []int, error) {
	if dr.aligner == "" {
		return nil, nil, fmt.Errorf("no forced aligner to cut the dialogue track")
	}
	var al proto.AlignResult
	if err := dr.call(ctx, proto.MethodSTTAlign, dr.aligner, &proto.AlignParams{Audio: path, Text: joinTurn(j.texts), Language: j.lang}, &al, nil); err != nil {
		return nil, nil, err
	}
	rg, err := trackRanges(al.Words, j.texts)
	if err != nil {
		return nil, nil, err
	}
	buf, _, err := wav.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	cuts := []int{0}
	for k := 1; k < len(rg); k++ {
		cuts = append(cuts, quietSplit(buf, rg[k-1][1], rg[k][0]))
	}
	cuts = append(cuts, buf.Frames())
	var files []string
	for k := range j.texts {
		clip := buf.Slice(cuts[k], cuts[k+1]).Clone()
		fadeIn(clip)
		fadeOut(clip)
		f := j.paths[k] + ".tmp.wav"
		if err := wav.WriteFile(f, clip, wav.Float32, wav.Options{}); err != nil {
			for _, g := range files {
				os.Remove(g)
			}
			return nil, nil, err
		}
		files = append(files, f)
	}
	return files, cuts, nil
}

// mixedFrames are line j's codec frames in a track's codes (cuts: each
// line's first sample): from the frame its cut falls in (the track's
// leading frame for the first line) to the next line's.
func mixedFrames(m *proto.DialogueMixed, cuts []int, j int) []int {
	n := m.Codebooks
	frames := len(m.Codes) / n
	at := func(k int) int {
		if k == 0 {
			return 0
		}
		return min(1+cuts[k]/mossFrameSamples, frames)
	}
	return m.Codes[at(j)*n : at(j+1)*n]
}
