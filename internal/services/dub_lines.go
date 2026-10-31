package services

// The dub's synthesize stage: each line's voice (cast, Auto, the line's
// own clip), its recipe (LineTake, LineRecipe), and rendering the stale
// lines through the take pipeline, each fitted to its slot and recorded
// in the language's track; split from dub_render.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/edit"
	"github.com/hehehai/AudioSmith/internal/dsp/loudness"
	"github.com/hehehai/AudioSmith/internal/dsp/stretch"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// langValue is the model's language value for a BCP-47 code.
func langValue(caps DubModelCaps, l string) string {
	if len(caps.Languages) == 0 {
		return l
	}
	if v, ok := caps.Languages[l]; ok {
		return v
	}
	codes := make([]string, 0, len(caps.Languages))
	for c := range caps.Languages {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	if m, ok := lang.Match(codes, l); ok {
		return caps.Languages[m]
	}
	return ""
}

// voiceFor resolves the voice segment i speaks with in lang, and why it
// fell back to the default voice ("" when it did not).
func (s *Dubs) voiceFor(ctx context.Context, p *dub.Project, seg dub.Segment, l string, caps DubModelCaps, cache map[string]dub.VoiceSpec) (dub.VoiceSpec, string) {
	v, warn := s.castVoice(ctx, p, seg, l, caps, cache)
	if caps.NeedsReference && caps.Clone && v.RefAudio == "" && v.VoicePrompt == "" && warn != DubWarnNoReference {
		// A model that only clones cannot speak with a voice without a
		// reference: the line clones its speaker (or fails, saying why).
		auto, w := s.autoVoice(p, seg)
		if auto.RefAudio != "" {
			return crossLingualClone(p, seg, l, caps, auto), warn
		}
		return auto, w
	}
	return crossLingualClone(p, seg, l, caps, v), warn
}

// crossLingualClone is v for a line in another language than its
// speaker's clip (thread #90): given the clip's transcript, Qwen3-TTS
// Base goes on in the clip's language (a Japanese clip had 9 of 14
// Chinese lines spoken in Japanese or garbled); from the clip alone (its
// speaker embedding) it speaks the line's language, as like the speaker.
// So the speaker's own clip is sent without its transcript to a model
// that clones that way.
func crossLingualClone(p *dub.Project, seg dub.Segment, l string, caps DubModelCaps, v dub.VoiceSpec) dub.VoiceSpec {
	if v.Auto && v.RefText != "" && caps.CloneAudioOnly && p.CrossLingual(seg, l) {
		v.RefText = ""
	}
	return v
}

// autoVoice is the clone of segment seg's own speaker (Auto).
func (s *Dubs) autoVoice(p *dub.Project, seg dub.Segment) (dub.VoiceSpec, string) {
	if p.Settings.VoiceMatch == dub.MatchLine {
		name := filepath.Join("refs", "line-"+seg.ID+".wav")
		return dub.VoiceSpec{RefAudio: s.Path(p.ID, name), RefText: seg.Text, Auto: true}, ""
	}
	r, ok := p.References[seg.Speaker]
	if !ok {
		return dub.VoiceSpec{}, DubWarnNoReference
	}
	return dub.VoiceSpec{RefAudio: s.Path(p.ID, r.File), RefText: r.Text, Auto: true}, ""
}

// castVoice is the voice segment seg is cast with.
func (s *Dubs) castVoice(ctx context.Context, p *dub.Project, seg dub.Segment, l string, caps DubModelCaps, cache map[string]dub.VoiceSpec) (dub.VoiceSpec, string) {
	ref := p.VoiceOf(seg)
	switch ref {
	case "", dub.VoiceAuto:
		if !caps.Clone {
			return dub.VoiceSpec{}, ""
		}
		return s.autoVoice(p, seg)
	case dub.VoiceDefault:
		return dub.VoiceSpec{}, ""
	}
	if v, ok := cache[ref]; ok {
		return v, ""
	}
	if s.cfg.Voice == nil {
		return dub.VoiceSpec{}, DubWarnVoice
	}
	v, err := s.cfg.Voice(ctx, ref, p.Settings.Model, l)
	if err != nil {
		return dub.VoiceSpec{}, DubWarnVoice
	}
	cache[ref] = v
	return v, ""
}

// LineTake is the recipe of a dubbed line: what the synthesize stage
// renders (and the workspace's audition plays).
func LineTake(p *dub.Project, seg dub.Segment, l string, v dub.VoiceSpec, caps DubModelCaps) core.Take {
	st := p.Settings
	t := core.Take{
		Text: p.TextFor(seg, l), Model: dubFirst(v.Model, st.Model), Voice: v.Voice, RefAudio: v.RefAudio, RefText: v.RefText,
		VoicePrompt: v.VoicePrompt, Seed: v.Seed, Language: dubFirst(v.Language, langValue(caps, dub.LangFor(seg, l))),
		Render: &core.TakeRender{Bits: 32, SeedPinned: v.Seed != 0},
	}
	if caps.Steps {
		t.Steps = st.Steps
	}
	if caps.Guidance {
		t.Guidance = st.Guidance
	}
	speed := st.Speed
	if seg.Speed > 0 {
		speed = seg.Speed
	}
	if speed <= 0 {
		speed = 1
	}
	if v.Speed > 0 {
		speed *= v.Speed
	}
	if caps.Speed && speed > 0 && speed != 1 {
		t.Speed = speed
	}
	instruct := strings.Trim(strings.Join(slices.DeleteFunc([]string{v.Instruct, st.Instruct, seg.Direction}, func(s string) bool { return strings.TrimSpace(s) == "" }), ", "), ", ")
	if caps.Instruct && instruct != "" && t.RefAudio == "" {
		if t.Voice != "" {
			t.Voice += ", " + instruct
		} else {
			t.Voice = instruct
		}
	}
	if caps.Duration && (st.Timing == dub.TimingLipSync || st.Timing == "") && seg.Duration() > 0.3 {
		t.Duration = math.Round(seg.Duration()*100) / 100
	}
	return t
}

// LineRecipe is the recipe of segment id in lang as the pipeline would
// render it, with the reason its voice fell back to the default ("").
func (s *Dubs) LineRecipe(ctx context.Context, p *dub.Project, id, l string) (core.Take, string, error) {
	seg, ok := p.Segment(id)
	if !ok {
		return core.Take{}, "", fmt.Errorf("dub: no line %s: %w", id, core.ErrNotFound)
	}
	if p.Settings.Model == "" {
		return core.Take{}, "", stageErr(DubCodeNoModel, "Choose a voice model.")
	}
	caps := s.cfg.Caps(p.Settings.Model)
	v, warn := s.voiceFor(ctx, p, *seg, l, caps, map[string]dub.VoiceSpec{})
	if v.Auto && p.Settings.VoiceMatch == dub.MatchLine {
		if err := s.lineClip(p, *seg); err != nil {
			return core.Take{}, "", err
		}
	}
	return LineTake(p, *seg, l, v, caps), warn, nil
}

// lineClip cuts a line's own source clip (Voice match: per line), at
// least 3 s around it.
func (s *Dubs) lineClip(p *dub.Project, seg dub.Segment) error {
	name := filepath.Join("refs", "line-"+seg.ID+".wav")
	if _, err := os.Stat(s.Path(p.ID, name)); err == nil {
		return nil
	}
	src := dubAudio
	if p.Media.Vocals != "" {
		src = dubVocals
	}
	start, end := seg.Start, seg.End
	if d := end - start; d < 3 {
		start, end = max(start-(3-d)/2, 0), end+(3-d)/2
	}
	if err := os.MkdirAll(s.Path(p.ID, "refs"), 0o755); err != nil {
		return err
	}
	return cutClip(s.Path(p.ID, src), s.Path(p.ID, name), start, end)
}

// fatal reports render errors that stop the whole stage (the next line
// would fail the same way).
func fatal(err error) bool {
	switch code(err) {
	case proto.CodeModelNotDownloaded, proto.CodeLicenceRequired, CodeEngineUnavailable, proto.CodeOOM,
		jobs.CodeInterrupted, jobs.CodeShutdown, proto.CodeCancelled, proto.CodeModelLoadFailed, DubCodeNoModel:
		return true
	}
	return !errors.As(err, new(*jobs.Error))
}

// synthesize renders the lines of one language that are stale (or the
// ones asked for), fits each to its slot, and records them in the track;
// the conversations of a track the dialogue model speaks first
// (dub_dialogue.go).
func (s *Dubs) synthesize(ctx context.Context, sc *stageCtx) error {
	p := sc.p
	l := sc.params.Lang
	if p.Settings.Model == "" {
		return stageErr(DubCodeNoModel, "Choose a voice model for the dub.")
	}
	if s.cfg.Render == nil || s.cfg.Render.render == nil {
		return stageErr(CodeEngineUnavailable, "The render pipeline is not connected.")
	}
	caps := s.cfg.Caps(p.Settings.Model)
	cache := map[string]dub.VoiceSpec{}
	dir := filepath.Join("renders", dub.SafeName(l))
	if err := os.MkdirAll(sc.path(dir), 0o755); err != nil {
		return err
	}
	voices := map[string]dub.VoiceSpec{}
	for _, seg := range p.Segments {
		if strings.TrimSpace(p.TextFor(seg, l)) == "" {
			continue
		}
		v, warn := s.voiceFor(ctx, p, seg, l, caps, cache)
		if warn != "" {
			sc.warn(warn)
		}
		voices[seg.ID] = v
	}
	dd, err := s.dubDialogue(ctx, sc, voices, caps)
	if err != nil {
		return err
	}
	var work []dubWork
	keys := map[string]string{}
	track := p.Track(l)
	track.Lang = l
	for i, seg := range p.Segments {
		v, ok := voices[seg.ID]
		if !ok {
			continue
		}
		key := dd.key(seg.ID, p.Fingerprint(seg, l, s.keyVoice(p.ID, v)))
		keys[seg.ID] = key
		r, ok := track.Lines[seg.ID]
		fresh := ok && r.Key == key && r.Err == "" && r.File != "" && fileExists(sc.path(r.File))
		if len(sc.params.Only) > 0 {
			fresh = fresh && !slices.Contains(sc.params.Only, seg.ID)
		} else if sc.params.Force {
			fresh = false
		}
		if fresh {
			continue
		}
		if v.Auto && p.Settings.VoiceMatch == dub.MatchLine {
			if err := s.lineClip(p, seg); err != nil {
				return err
			}
		}
		work = append(work, dubWork{i, key, LineTake(p, seg, l, v, caps)})
	}
	if len(work) == 0 {
		return sc.save(ctx, func(q *dub.Project) { q.PutTrack(mergeTrack(q.Track(l), track, keys, l)) })
	}
	return s.renderWork(ctx, sc, work, dd, dir)
}

// keyVoice is v as a line's render key sees it: the project's files
// relative to its folder (keyPath), so a copied or moved project keeps
// its keys.
func (s *Dubs) keyVoice(id string, v dub.VoiceSpec) dub.VoiceSpec {
	v.RefAudio, v.VoicePrompt = s.keyPath(id, v.RefAudio), s.keyPath(id, v.VoicePrompt)
	return v
}

// dubWork is a line to render: its index, key and recipe.
type dubWork struct {
	i    int
	key  string
	take core.Take
}

// renderWork renders the work of a synthesize stage, saving every ten
// lines: line by line, or with a dialogue model its stretches first.
func (s *Dubs) renderWork(ctx context.Context, sc *stageCtx, work []dubWork, dd *dubDialogue, dir string) error {
	p, l := sc.p, sc.params.Lang
	rendered := map[string]dub.Render{}
	// The lines checked once all are made (dub_check.go): kept out of
	// the flushed batches until then.
	var checks []checkedLine
	checked := map[string]dub.Render{}
	failed := 0
	flush := func() error {
		if len(rendered) == 0 {
			return nil
		}
		batch := rendered
		rendered = map[string]dub.Render{}
		return sc.save(context.WithoutCancel(ctx), func(q *dub.Project) {
			t := q.Track(l)
			t.Lang = l
			if t.Lines == nil {
				t.Lines = map[string]dub.Render{}
			}
			for id, r := range batch {
				t.Lines[id] = r
			}
			t.QC = nil
			q.PutTrack(t)
		})
	}
	put := func(id string, r dub.Render) error {
		rendered[id] = r
		if len(rendered) >= 10 {
			return flush()
		}
		return nil
	}
	// one renders work line w on the dub's model.
	one := func(ctx context.Context, w dubWork) error {
		seg := p.Segments[w.i]
		r, err := s.renderLine(ctx, sc, p, w.i, l, w.key, w.take, dir)
		if err != nil {
			if ctx.Err() != nil {
				flush()
				return ctx.Err()
			}
			if fatal(err) {
				flush()
				return err
			}
			failed++
			r = dub.Render{Key: w.key, Start: seg.Start, Err: err.Error(), Text: w.take.Text}
		} else if needsCheck(p, seg, l, w.take) && !dd.has(seg.ID) {
			// Checked once all are made (a conversation's line is
			// checked as its fallback, dd.mark).
			checks = append(checks, checkedLine{w.i, w.key, w.take})
			checked[seg.ID] = r
			return nil
		}
		if dd != nil {
			dd.mark(ctx, &r, seg.ID)
		}
		return put(seg.ID, r)
	}
	ids := make([]string, len(work))
	for wi, w := range work {
		ids[wi] = p.Segments[w.i].ID
	}
	var groups [][]int
	var which []int
	if dd != nil {
		groups, which = dd.groups(ids)
	}
	if len(groups) == 0 {
		for n, w := range work {
			if err := ctx.Err(); err != nil {
				flush()
				return err
			}
			sc.progress("generating", float64(n)/float64(len(work)), fmt.Sprintf("%d/%d", n+1, len(work)))
			if err := one(ctx, w); err != nil {
				return err
			}
		}
	} else {
		done := 0
		seg := func(ctx context.Context, g int) ([]int, error) {
			// The stretch's request reports within the progress.
			dd.jobs[which[g]].report = func(pr jobs.Progress) {
				stage := "generating"
				if pr.Stage == "loading" {
					stage = "loading"
				}
				f := (float64(done) + max(pr.Fraction, 0)*float64(len(groups[g]))) / float64(len(work))
				sc.progress(stage, min(f, 1), fmt.Sprintf("%d/%d", done+1, len(work)))
			}
			return dd.make(ctx, which[g], groups[g], ids, func(wi int, path string) error {
				w := work[wi]
				r, made, err := fitLine(sc, p, w.i, w.key, w.take.Text, path, dir)
				if err != nil {
					return err
				}
				dd.made(&r, ids[wi], made)
				return put(ids[wi], r)
			})
		}
		line := func(ctx context.Context, wi int) error { return one(ctx, work[wi]) }
		progress := func(n, total int) {
			done = n
			sc.progress("generating", float64(n)/float64(total), fmt.Sprintf("%d/%d", min(n+1, total), total))
		}
		if err := dialogueTwoPass(ctx, len(work), groups, seg, line, progress); err != nil {
			flush()
			return err
		}
		dd.prune()
	}
	if err := flush(); err != nil {
		return err
	}
	if len(checks) > 0 {
		err := s.checkLines(ctx, sc, l, dir, s.cfg.Caps(p.Settings.Model), checks, checked)
		rendered = checked
		if ferr := flush(); err == nil {
			err = ferr
		}
		if err != nil {
			return err
		}
	}
	if failed > 0 {
		sc.warn(DubWarnLinesFailed)
	}
	return nil
}

// mergeTrack keeps t's lines, dropping those of segments gone.
func mergeTrack(cur, _ dub.Track, keys map[string]string, l string) dub.Track {
	cur.Lang = l
	for id := range cur.Lines {
		if _, ok := keys[id]; !ok {
			delete(cur.Lines, id)
		}
	}
	return cur
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// renderLine renders one line through the take pipeline and fits it.
func (s *Dubs) renderLine(ctx context.Context, sc *stageCtx, p *dub.Project, i int, l, key string, t core.Take, dir string) (dub.Render, error) {
	seg := p.Segments[i]
	raw := sc.path(filepath.Join(dir, seg.ID+".raw.wav"))
	defer os.Remove(raw)
	// Not watermarked here: fitting stretches the line; the final track
	// is marked instead (mix, watermark.go).
	params, err := json.Marshal(RenderParams{Take: t, Lang: dub.LangFor(seg, l), Output: raw, NoWatermark: true, Workflow: "dub"})
	if err != nil {
		return dub.Render{}, err
	}
	sub := jobs.Job{ID: sc.job.ID + "." + seg.ID, Spec: jobs.Spec{Kind: KindRender, Lane: jobs.LaneGPU, Params: params, Owner: sc.job.Owner}}
	if _, err := s.cfg.Render.render.Run(ctx, sub, func(jobs.Progress) {}, nil); err != nil {
		return dub.Render{}, err
	}
	r, made, err := fitLine(sc, p, i, key, t.Text, raw, dir)
	if err != nil {
		return dub.Render{}, err
	}
	speed := t.Speed
	if speed <= 0 {
		speed = 1
	}
	if t.Duration == 0 {
		translate.DefaultPredictor.Calibrate(t.Text, dub.LangFor(seg, l), made, speed)
	}
	return r, nil
}

// fitLine fits the audio a model made for line i (raw) to its slot,
// brings it to LineLoudness and writes it in dir as said (text); it
// returns its render and the seconds of speech the model made.
func fitLine(sc *stageCtx, p *dub.Project, i int, key, text, raw, dir string) (dub.Render, float64, error) {
	seg := p.Segments[i]
	buf, _, err := wav.ReadFile(raw)
	if err != nil {
		return dub.Render{}, 0, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	buf = dsp.Remix(buf, 1)
	trimmed := edit.Trim(buf, edit.TrimOptions{})
	if trimmed.Frames() == 0 {
		return dub.Render{}, 0, &jobs.Error{Code: NoticeChunkSilent, Message: "The model made no sound for this line."}
	}
	plan := dub.Plan(dub.FitIn{Rendered: trimmed.Seconds(), Slot: seg.Duration(), Room: dub.Room(p.Segments, i, p.Source.Duration), Timing: p.Settings.Timing})
	out := trimmed
	if plan.Ratio != 1 {
		out = stretch.Stretch(out, plan.Ratio, stretch.Options{})
	}
	if plan.Cut {
		n := min(dsp.FramesAt(plan.Length, out.Rate), out.Frames())
		out = out.Slice(0, n).Clone()
		dsp.FadeOut(out, dsp.FramesAt(0.05, out.Rate), dsp.EqualPower)
	}
	if lufs := loudness.Integrated(out); !math.IsInf(lufs, 0) && !math.IsNaN(lufs) && lufs > -70 {
		dsp.Gain(out.Samples, float32(dsp.Amp(min(max(LineLoudness-lufs, -12), 12))))
	}
	name := filepath.Join(dir, seg.ID+"-"+key[:10]+".wav")
	if err := writeWAVFloat(sc.path(name), out); err != nil {
		return dub.Render{}, 0, err
	}
	os.WriteFile(sc.path(name)+".txt", []byte(text), 0o644)
	// Older renders of the line go.
	if old, _ := filepath.Glob(sc.path(filepath.Join(dir, seg.ID+"-*.wav"))); len(old) > 1 {
		for _, f := range old {
			if filepath.Base(f) != filepath.Base(name) {
				os.Remove(f)
				os.Remove(f + ".txt")
			}
		}
	}
	return dub.Render{Key: key, File: name, Raw: round3(trimmed.Seconds()), Length: round3(out.Seconds()), Ratio: plan.Ratio,
		Start: seg.Start, Verdict: plan.Verdict, Over: plan.Over, Text: text}, trimmed.Seconds(), nil
}

// writeWAVFloat writes b as 32-bit float through a temporary file.
func writeWAVFloat(path string, b dsp.Buffer) error {
	tmp := path + ".tmp"
	if err := wav.WriteFile(tmp, b, wav.Float32, wav.Options{}); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
