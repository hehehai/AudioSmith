package core

import (
	"strings"
	"testing"
	"time"
)

// textQuadratic is Text as it was before the perf review of 11-02 (C1):
// the reference the linear Text must equal.
func textQuadratic(t Transcript) string {
	var b strings.Builder
	for _, s := range t.Segments {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		if b.Len() > 0 {
			bf, af := []rune(b.String()), []rune(txt)
			if !(isUnspaced(bf[len(bf)-1]) || isUnspaced(af[0])) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(txt)
	}
	return b.String()
}

func segs(texts ...string) Transcript {
	var t Transcript
	for _, x := range texts {
		t.Segments = append(t.Segments, TranscriptSegment{Text: x})
	}
	return t
}

func TestTranscriptTextJoins(t *testing.T) {
	for _, tr := range []Transcript{
		segs(),
		segs("  ", ""),
		segs("Hello there.", "How are you?"),
		segs("他也说过，", "这个城市。", "Hello", "米粉", "end."),
		segs(" a ", "", "b", "「引号」", "c", "ｆｕｌｌ", "d"),
	} {
		if got, want := tr.Text(), textQuadratic(tr); got != want {
			t.Errorf("Text() = %q, want %q", got, want)
		}
	}
}

// TestTranscriptTextPrefix: the prefix is Text's start, whole segments,
// at least the bytes asked for when there are that many.
func TestTranscriptTextPrefix(t *testing.T) {
	var tr Transcript
	for range 600 {
		tr.Segments = append(tr.Segments, TranscriptSegment{Text: "他也说过，这个城市里有很多米粉店。"}, TranscriptSegment{Text: "Hello there."})
	}
	full := tr.Text()
	for _, n := range []int{0, 1, 50, 300, len(full) + 10} {
		p := tr.TextPrefix(n)
		if !strings.HasPrefix(full, p) || len(p) < min(n, len(full)) || len(p) > n+60 && n < len(full) {
			t.Errorf("TextPrefix(%d) = %d bytes, not Text's start of about that length", n, len(p))
		}
	}
}

// TestTranscriptTextLinear: an hour (600 segments) joins in well under a
// millisecond, four hours too (perf review 11-02 C1: 9–16 ms and
// 130–237 ms before; 0.04 and 0.06 ms after). Wide margins: the host is
// shared.
func TestTranscriptTextLinear(t *testing.T) {
	for _, n := range []int{600, 2400} {
		var tr Transcript
		for j := range n {
			tx := "他也说过，这个城市里有很多米粉店，西边那家最好吃。"
			if j%3 == 0 {
				tx = "Hello there."
			}
			tr.Segments = append(tr.Segments, TranscriptSegment{Text: tx})
		}
		best := time.Hour
		for range 3 {
			t0 := time.Now()
			_ = tr.Text()
			best = min(best, time.Since(t0))
		}
		if best > 5*time.Millisecond {
			t.Errorf("Text() of %d segments took %v, want < 5 ms", n, best)
		}
	}
}
