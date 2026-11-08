// Package dialogue groups lines into the segments a dialogue model
// (SoulX-Podcast, MOSS-TTSD, VibeVoice; threads #80, #82, #166) speaks in one job, each
// line hearing the lines before it: a story's character lines, a dub's
// conversation, an audiobook's dialogue blocks
// (plan/dialogue-dub-audiobook-plan.md §4.1). It is pure: the callers say
// which lines can be turns and which voice each has; the engine side is
// services/dialogue_run.go.
//
// A segment is a run of turns, within the model's limits (Limits: voices,
// lines, text). What ends a run besides the limits is the caller's Rules:
// a line that is neither a turn nor an aside (a long narration line in a
// story), a silence longer than MaxGap (a dub's change of scene), the edge
// of an audiobook's block. A dub also drops what is no conversation: runs
// of fewer than MinVoices voices or MinChanges changes of speaker, and a
// voice's monologue (MonologueLines lines or MonologueSeconds in a row),
// which is cut out, the lines on each side making segments of their own.
package dialogue

import "github.com/hehehai/AudioSmith/internal/text"

// FamilyMossTTSD is MOSS-TTSD's catalogue family: a one-track dialogue
// model (the whole segment as one track, cut at the lines).
const FamilyMossTTSD = "moss_ttsd"

// FamilyVibeVoice is VibeVoice 1.5B's catalogue family (thread #166): a
// one-track dialogue model too, of up to four voices, without codes (a
// line cannot be made again after the lines before it).
const FamilyVibeVoice = "vibevoice"

// OneTrack reports whether a dialogue model family speaks a segment as
// one track, which the app cuts at the lines (MOSS-TTSD, VibeVoice).
func OneTrack(family string) bool {
	return family == FamilyMossTTSD || family == FamilyVibeVoice
}

// Limits bound a segment: voices, lines, unspaced characters (CJK) and
// other text (Chars, 6 a word).
type Limits struct{ Speakers, Turns, CJK, Chars int }

// LimitsFor are a dialogue model family's segment limits: SoulX-Podcast 4
// voices, 16 lines, about 90 s; MOSS-TTSD speaks a segment in one call,
// about 60 s at most (a longer call risks the GPU watchdog and a harder
// cut), up to its five voices; VibeVoice the same, up to its four.
func LimitsFor(family string) Limits {
	switch family {
	case FamilyMossTTSD:
		return Limits{Speakers: 5, Turns: 24, CJK: 240, Chars: 720}
	case FamilyVibeVoice:
		return Limits{Speakers: 4, Turns: 24, CJK: 240, Chars: 720}
	}
	return Limits{Speakers: 4, Turns: 16, CJK: 400, Chars: 1200}
}

// Item is a line to group.
type Item struct {
	// Voice is the line's voice as finally resolved: two lines of one
	// voice are one speaker whatever their labels.
	Voice string
	// Text is what the line says (counted against the limits).
	Text string
	// Turn: the line can be a turn of a segment.
	Turn bool
	// Aside: not a turn, but it may sit inside a segment without ending
	// it (a short narration line in a story, any narration in a block).
	Aside bool
	// Block is the audiobook block the line is in (Rules.Blocks); 0: none.
	Block int
	// Start and End are where the line lies in the source, in seconds
	// (a dub); both 0: untimed.
	Start, End float64
}

// Rules are what ends a segment besides the limits, and what is kept.
type Rules struct {
	// Blocks: only lines in a block group, and a segment never spans two.
	Blocks bool
	// MaxGap ends a segment at a silence longer than it (seconds; 0: none).
	MaxGap float64
	// MinVoices and MinChanges drop a segment with fewer voices or
	// changes of speaker (0 or 1: keep every segment).
	MinVoices, MinChanges int
	// MonologueLines and MonologueSeconds cut a voice's run of at least
	// that many lines or seconds out of a segment (0: no limit).
	MonologueLines   int
	MonologueSeconds float64
}

// The rules of the three callers: a story groups every run of character
// lines (short narration between them does not end it); a dub groups
// conversations (a silence over 4 s ends one; at least two voices taking
// turns at least twice; monologues of 4 lines or 30 s stay line by line);
// an audiobook groups inside its blocks only, a segment of one voice being
// read line by line (a block of one speaker; thread #88).
var (
	StoryRules = Rules{}
	DubRules   = Rules{MaxGap: 4, MinVoices: 2, MinChanges: 2, MonologueLines: 4, MonologueSeconds: 30}
	BookRules  = Rules{Blocks: true, MinVoices: 2}
)

// Story asides: a narration line of at most this many unspaced
// characters and this much other text (6 a word) does not end a story's
// segment.
const (
	AsideCJK   = 24
	AsideChars = 80
)

// ShortAside reports whether a narration line saying texts is short
// enough to sit inside a story's segment.
func ShortAside(texts ...string) bool {
	w, c := 0, 0
	for _, s := range texts {
		sw, sc := text.Count(s)
		w, c = w+sw, c+sc
	}
	return c <= AsideCJK && w*6 <= AsideChars
}

// Seconds is how long an item lasts: its time in the source, else its
// text's estimate.
func Seconds(it Item) float64 {
	if it.End > it.Start {
		return it.End - it.Start
	}
	w, c := text.Count(it.Text)
	return text.Estimate(w, c).Seconds()
}

// Group are the segments of items (indices, in order) by the limits and
// rules.
func Group(items []Item, lim Limits, r Rules) [][]int {
	var out [][]int
	for _, run := range runs(items, r) {
		for _, part := range r.monologues(items, run) {
			for _, g := range limit(items, part, lim) {
				if r.keeps(items, g) {
					out = append(out, g)
				}
			}
		}
	}
	return out
}

// runs are the turns between the lines, gaps and block edges that end a
// segment.
func runs(items []Item, r Rules) [][]int {
	var out [][]int
	var cur []int
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
		}
		cur = nil
	}
	block := 0
	for i, it := range items {
		if r.Blocks && (it.Block == 0 || it.Block != block) {
			flush()
			block = it.Block
			if it.Block == 0 {
				continue
			}
		}
		if !it.Turn {
			if !it.Aside {
				flush()
			}
			continue
		}
		if len(cur) > 0 && r.MaxGap > 0 {
			if prev := items[cur[len(cur)-1]]; prev.End > 0 && it.Start-prev.End > r.MaxGap {
				flush()
			}
		}
		cur = append(cur, i)
	}
	flush()
	return out
}

// monologues cut a run's monologues out: the parts before, between and
// after them.
func (r Rules) monologues(items []Item, run []int) [][]int {
	if r.MonologueLines <= 0 && r.MonologueSeconds <= 0 {
		return [][]int{run}
	}
	var out [][]int
	from := 0
	for i := 0; i < len(run); {
		j := i + 1
		for j < len(run) && items[run[j]].Voice == items[run[i]].Voice {
			j++
		}
		if r.monologue(items, run[i:j]) {
			if i > from {
				out = append(out, run[from:i])
			}
			from = j
		}
		i = j
	}
	if from < len(run) {
		out = append(out, run[from:])
	}
	return out
}

// monologue reports whether one voice's lines in a row are a monologue.
func (r Rules) monologue(items []Item, lines []int) bool {
	if r.MonologueLines > 0 && len(lines) >= r.MonologueLines {
		return true
	}
	if r.MonologueSeconds <= 0 {
		return false
	}
	first, last := items[lines[0]], items[lines[len(lines)-1]]
	secs := 0.0
	if first.End > first.Start && last.End > last.Start {
		secs = last.End - first.Start
	} else {
		for _, l := range lines {
			secs += Seconds(items[l])
		}
	}
	return secs >= r.MonologueSeconds
}

// limit splits a run where it would pass the limits.
func limit(items []Item, run []int, lim Limits) [][]int {
	var out [][]int
	var cur []int
	voices := map[string]bool{}
	cjk, rest := 0, 0
	for _, i := range run {
		it := items[i]
		w, c := text.Count(it.Text)
		if len(cur) > 0 && (len(cur) >= lim.Turns || cjk+c > lim.CJK || rest+w*6 > lim.Chars ||
			!voices[it.Voice] && len(voices) >= lim.Speakers) {
			out = append(out, cur)
			cur, voices, cjk, rest = nil, map[string]bool{}, 0, 0
		}
		cur = append(cur, i)
		voices[it.Voice] = true
		cjk, rest = cjk+c, rest+w*6
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// keeps reports whether a segment has the voices and turns the rules ask.
func (r Rules) keeps(items []Item, g []int) bool {
	voices := map[string]bool{}
	changes := 0
	for k, i := range g {
		voices[items[i].Voice] = true
		if k > 0 && items[i].Voice != items[g[k-1]].Voice {
			changes++
		}
	}
	return len(voices) >= r.MinVoices && changes >= r.MinChanges
}
