package dub

// A dub project's methods: creating one, choosing and retargeting
// languages, defaults and Normalize, segments, speakers and their
// voices, the text of each language, cloning, and stages and tracks;
// split from project.go.

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// New makes an empty project with the defaults: dubbed into
// DefaultTarget, which AutoRetarget may still switch.
func New(id string, now time.Time) *Project {
	return &Project{
		Version: Version, ID: id, CreatedAt: now, UpdatedAt: now, NextSeg: 1,
		Targets: []string{DefaultTarget}, TargetsAuto: true,
		Settings: DefaultSettings(),
	}
}

// ChooseTargets sets the targets someone chose: AutoRetarget leaves
// them alone from then on.
func (p *Project) ChooseTargets(targets []string) {
	p.Targets = slices.Clone(targets)
	p.TargetsAuto, p.TargetSwitched = false, ""
}

// AutoRetarget keeps defaulted targets away from the spoken language
// (as chosen, else as detected): a video in the default language is
// dubbed into FallbackTarget (a Chinese video into English), and back to
// the default when the spoken language changes again. It does nothing
// to targets someone chose and reports whether Targets changed.
func (p *Project) AutoRetarget() bool {
	if !p.TargetsAuto || len(p.Targets) != 1 {
		return false
	}
	def := p.TargetSwitched
	if def == "" {
		def = p.Targets[0]
	}
	want, switched := def, ""
	if src := p.SourceLanguage(); src != "" && spokenIn(src, def) {
		want, switched = FallbackTarget, def
		if spokenIn(src, FallbackTarget) {
			want = DefaultTarget
		}
	}
	if p.Targets[0] == want && p.TargetSwitched == switched {
		return false
	}
	p.Targets[0], p.TargetSwitched = want, switched
	return true
}

// spokenIn reports whether speech in src needs no dub into target
// ("zh", "cmn" and "zh-CN" are all Chinese).
func spokenIn(src, target string) bool {
	if sameLanguage(src, target) {
		return true
	}
	c := lang.Canonical(src)
	return c != "" && c == lang.Canonical(target)
}

// RetargetedLang is the language a stage planned for l runs in: the
// replacement when AutoRetarget switched lang away after the run was
// planned (the run's stages were made before transcription found the
// spoken language), else l.
func (p *Project) RetargetedLang(l string) string {
	if l != "" && p.TargetsAuto && l == p.TargetSwitched && !slices.Contains(p.Targets, l) && len(p.Targets) > 0 {
		return p.Targets[0]
	}
	return l
}

// DefaultSettings are a new project's settings.
func DefaultSettings() Settings {
	return Settings{
		Translator: "llm", Quality: translate.Fast, Timing: TimingSmart, KeepBackground: true,
		VoiceMatch: MatchConsistent, Review: true, Dialogue: DialogueAuto,
		Export: ExportPrefs{Bits: 24},
	}
}

// Normalize fills what an older or hand-made document lacks and keeps
// values in range (documents are restored from the store).
func (p *Project) Normalize() {
	if p.Version == 0 {
		p.Version = Version
	}
	if p.NextSeg <= 0 {
		p.NextSeg = 1
	}
	if p.Settings.Timing == "" {
		p.Settings.Timing = TimingLipSync
	}
	if p.Settings.VoiceMatch == "" {
		p.Settings.VoiceMatch = MatchConsistent
	}
	if p.Settings.Translator == "" {
		p.Settings.Translator = "llm"
	}
	if p.Speakers < 0 || p.Speakers > MaxSpeakers {
		p.Speakers = 0
	}
	seen := map[string]bool{}
	for i := range p.Segments {
		s := &p.Segments[i]
		if s.ID == "" || seen[s.ID] {
			s.ID = p.newID()
		}
		seen[s.ID] = true
		if s.Volume < 0 || s.Volume > 2 {
			s.Volume = 1
		}
		if s.Speed != 0 {
			s.Speed = clamp(s.Speed, 0.5, 2)
		}
		if s.End < s.Start {
			s.End = s.Start
		}
	}
	p.Targets = slices.DeleteFunc(slices.Compact(p.Targets), func(s string) bool { return s == "" })
}

// MaxSpeakers is the most speakers a project may say it has.
const MaxSpeakers = 20

// newID numbers a new segment.
func (p *Project) newID() string {
	for {
		id := "s" + strconv.Itoa(p.NextSeg)
		p.NextSeg++
		if p.Index(id) < 0 {
			return id
		}
	}
}

// NewSegment makes a segment with a new id and the defaults.
func (p *Project) NewSegment(start, end float64, speaker, text string) Segment {
	return Segment{ID: p.newID(), Start: start, End: end, Speaker: speaker, Text: text, Volume: 1}
}

// Index is the position of segment id, or -1.
func (p *Project) Index(id string) int {
	return slices.IndexFunc(p.Segments, func(s Segment) bool { return s.ID == id })
}

// Segment returns segment id.
func (p *Project) Segment(id string) (*Segment, bool) {
	i := p.Index(id)
	if i < 0 {
		return nil, false
	}
	return &p.Segments[i], true
}

// Primary is the primary target language ("" when there is none).
func (p *Project) Primary() string {
	if len(p.Targets) == 0 {
		return ""
	}
	return p.Targets[0]
}

// SourceLanguage is the spoken language: as chosen, else as detected.
func (p *Project) SourceLanguage() string {
	if p.SourceLang != "" {
		return p.SourceLang
	}
	return p.Detected
}

// SpeakerLabels are the speakers of the segments, in order of first line.
func (p *Project) SpeakerLabels() []string {
	var out []string
	for _, s := range p.Segments {
		for _, sp := range s.speakers() {
			if sp != "" && !slices.Contains(out, sp) {
				out = append(out, sp)
			}
		}
	}
	return out
}

func (s Segment) speakers() []string {
	out := []string{s.Speaker}
	for _, pt := range s.Parts {
		out = append(out, pt.Speaker)
	}
	return out
}

// SpeakerName is a speaker's display name: its name, else its label.
func (p *Project) SpeakerName(label string) string {
	if n := p.Names[label]; n != "" {
		return n
	}
	return label
}

// VoiceOf is the VoiceRef a segment speaks with: its own, else its
// speaker's cast, else the cast of every speaker (CastAll), else ""
// (the speaker's automatic clone).
func (p *Project) VoiceOf(s Segment) string {
	if s.Voice != "" {
		return s.Voice
	}
	if v, ok := p.Cast[s.Speaker]; ok {
		return v
	}
	return p.Cast[CastAll]
}

// CastAll in Cast casts every speaker without a voice of their own (the
// batch queue's one voice).
const CastAll = "*"

// LangFor is the language segment s speaks in track lang.
func LangFor(s Segment, lang string) string {
	if s.Lang != "" {
		return s.Lang
	}
	return lang
}

// TextFor is what segment s says in track lang: its translation, the
// source when the line keeps the source language, or "".
func (p *Project) TextFor(s Segment, lang string) string {
	l := LangFor(s, lang)
	if src := p.SourceLanguage(); l != "" && src != "" && sameLanguage(l, src) {
		return s.Text
	}
	return s.Lines[l].Text
}

// CrossLingual reports whether segment s speaks another language in
// track lang than the source it was cut from (its speaker's clips): the
// source language is known (not "auto") and its base language differs
// from the line's (zh-Hant → zh-Hans is the same speech).
func (p *Project) CrossLingual(s Segment, lang string) bool {
	src, l := p.SourceLanguage(), LangFor(s, lang)
	if src == "" || l == "" || strings.EqualFold(src, "auto") {
		return false
	}
	return base(src) != base(l)
}

// sameLanguage reports whether two BCP-47 tags name the same language
// variety (es = es-ES differ only by region; zh-Hans ≠ zh-Hant).
func sameLanguage(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	if base(a) != base(b) {
		return false
	}
	sa, sb := script(a), script(b)
	return sa == "" || sb == "" || sa == sb
}

func base(tag string) string {
	b, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return b
}

func script(tag string) string {
	for _, part := range strings.Split(tag, "-")[1:] {
		if len(part) == 4 {
			return strings.ToLower(part)
		}
	}
	return ""
}

// Translated counts the segments with text in lang (and those to translate).
func (p *Project) Translated(lang string) (done, total int) {
	for _, s := range p.Segments {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		total++
		if strings.TrimSpace(p.TextFor(s, lang)) != "" {
			done++
		}
	}
	return done, total
}

// Clone is a deep copy (the UI keeps one; the pipeline works on another).
func (p *Project) Clone() *Project {
	q := *p
	q.Targets = slices.Clone(p.Targets)
	q.Segments = CloneSegments(p.Segments)
	q.Names = maps.Clone(p.Names)
	q.Cast = maps.Clone(p.Cast)
	if p.References != nil {
		q.References = make(map[string]Reference, len(p.References))
		for k, r := range p.References {
			r.Hints = slices.Clone(r.Hints)
			q.References[k] = r
		}
	}
	q.RefTried = slices.Clone(p.RefTried)
	q.Glossary = slices.Clone(p.Glossary)
	q.Settings.Export.Tracks = slices.Clone(p.Settings.Export.Tracks)
	q.Media.Onsets = slices.Clone(p.Media.Onsets)
	if p.Source.Video != nil {
		v := *p.Source.Video
		q.Source.Video = &v
	}
	if p.Source.Meta != nil {
		m := *p.Source.Meta
		m.Captions = slices.Clone(m.Captions)
		q.Source.Meta = &m
	}
	if p.Stages != nil {
		q.Stages = make(map[string]StageState, len(p.Stages))
		for k, st := range p.Stages {
			st.Warnings = slices.Clone(st.Warnings)
			q.Stages[k] = st
		}
	}
	if p.Run != nil {
		r := *p.Run
		r.Langs = slices.Clone(p.Run.Langs)
		q.Run = &r
	}
	if p.Tracks != nil {
		q.Tracks = make(map[string]Track, len(p.Tracks))
		for k, t := range p.Tracks {
			t.Lines = maps.Clone(t.Lines)
			t.DialogueTakes = maps.Clone(t.DialogueTakes)
			if t.QC != nil {
				qc := *t.QC
				qc.Lines = slices.Clone(qc.Lines)
				qc.Overlap = slices.Clone(qc.Overlap)
				qc.Keys = maps.Clone(qc.Keys)
				t.QC = &qc
			}
			q.Tracks[k] = t
		}
	}
	return &q
}

// CloneSegments deep-copies segments (undo snapshots).
func CloneSegments(in []Segment) []Segment {
	if in == nil {
		return nil
	}
	out := make([]Segment, len(in))
	for i, s := range in {
		s.Words = slices.Clone(s.Words)
		s.Parts = slices.Clone(s.Parts)
		if s.Lines != nil {
			lines := make(map[string]Line, len(s.Lines))
			for k, l := range s.Lines {
				l.Warnings = slices.Clone(l.Warnings)
				if l.Fit != nil {
					f := *l.Fit
					l.Fit = &f
				}
				lines[k] = l
			}
			s.Lines = lines
		}
		out[i] = s
	}
	return out
}

// Stage returns a stage's state.
func (p *Project) Stage(s Stage, lang string) StageState {
	return p.Stages[StageKey(s, lang)]
}

// SetStage records a stage's state.
func (p *Project) SetStage(s Stage, lang string, st StageState) {
	if p.Stages == nil {
		p.Stages = map[string]StageState{}
	}
	p.Stages[StageKey(s, lang)] = st
}

// Track returns lang's track (zero when there is none).
func (p *Project) Track(lang string) Track { return p.Tracks[lang] }

// PutTrack stores a track.
func (p *Project) PutTrack(t Track) {
	if p.Tracks == nil {
		p.Tracks = map[string]Track{}
	}
	p.Tracks[t.Lang] = t
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
