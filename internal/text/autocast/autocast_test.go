package autocast

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func view(r Result) []string {
	var out []string
	for _, l := range r.Lines {
		who := l.Speaker
		if who == "" {
			who = "-"
		}
		out = append(out, fmt.Sprintf("%s|%s", who, l.Text))
	}
	return out
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     []string
		speakers []string
	}{
		{"tagged", "[Mara] Hello there.\n[Cole] Hi, Mara!\n[pause 1s]\n[mara] Again.",
			[]string{"Mara|Hello there.", "Cole|Hi, Mara!\n[pause 1s]", "Mara|Again."}, []string{"Mara", "Cole"}},
		{"one line per line", "林夏: 你好。\n陈默: 嗯。\n雨下大了。\n林夏: 走吧。",
			[]string{"林夏|你好。", "陈默|嗯。", "-|雨下大了。", "林夏|走吧。"}, []string{"林夏", "陈默"}},
		{"paragraph continuation", "MARA: Where were you?\nI waited.\n\nCOLE: Out.",
			[]string{"Mara|Where were you?\nI waited.", "Cole|Out."}, []string{"Mara", "Cole"}},
		{"tag continuation", "[Mara]\nI have\nmany lines.\n\n[laughter] Ha.",
			[]string{"Mara|I have\nmany lines.", "-|[laughter] Ha."}, []string{"Mara"}},
		{"screenplay", "MARA: Where were you?\nCOLE (V.O.): Out.\nNARRATOR: They paused.\nNote: not a speaker.",
			[]string{"Mara|Where were you?", "Cole|Out.", "-|They paused.", "-|Note: not a speaker."}, []string{"Mara", "Cole"}},
		{"screenplay title case", "Old Tom: Evening.\n\nThe Captain: Aye.",
			[]string{"Old Tom|Evening.", "Captain|Aye."}, []string{"Old Tom", "Captain"}},
		{"screenplay zh", "小明：你好！\n小红：你好呀。\n旁白：两人笑了。",
			[]string{"小明|你好！", "小红|你好呀。", "-|两人笑了。"}, []string{"小明", "小红"}},
		{"not screenplay", "At 10:30 we met.\nhttp://example.com is a site.\nthe end: it was.",
			[]string{"-|At 10:30 we met.", "-|http://example.com is a site.", "-|the end: it was."}, nil},
		{"narration before speakers", "It was late.\nMARA: Go home.",
			[]string{"-|It was late.", "Mara|Go home."}, []string{"Mara"}},
		{"prose said X", `"Where were you?" asked Mara. "Out," said the captain.`,
			[]string{"Mara|Where were you?", "-|asked Mara.", "Captain|Out,", "-|said the captain."}, []string{"Mara", "Captain"}},
		{"prose X said", `"Stay," Cole whispered. He left.`,
			[]string{"Cole|Stay,", "-|Cole whispered. He left."}, []string{"Cole"}},
		{"prose before", `Mara said, "Come in." Then she sat.`,
			[]string{"-|Mara said,", "Mara|Come in.", "-|Then she sat."}, []string{"Mara"}},
		{"prose inherit in paragraph", `"Hello," said John. "How are you?"`,
			[]string{"John|Hello,", "-|said John.", "John|How are you?"}, []string{"John"}},
		{"prose pronoun", `“Fine,” she said.`,
			[]string{"-|Fine,", "-|she said."}, nil},
		{"prose unattributed", `"Nobody said who."`,
			[]string{"-|Nobody said who."}, nil},
		{"british single quotes", `‘It’s cold,’ said Ann.`,
			[]string{"Ann|It’s cold,", "-|said Ann."}, []string{"Ann"}},
		{"zh before", "小明说：“你好。”小红笑了。",
			[]string{"-|小明说：", "小明|你好。", "-|小红笑了。"}, []string{"小明"}},
		{"zh after with adverb", "“快走！”小红低声说道。",
			[]string{"小红|快走！", "-|小红低声说道。"}, []string{"小红"}},
		{"zh pronoun and 知道", "他说：“好。”\n\n“我知道，”他知道了。",
			[]string{"-|他说：", "-|好。", "-|我知道，", "-|他知道了。"}, nil},
		{"zh comma before name", "他看着窗外，老王问：“几点了？”",
			[]string{"-|他看着窗外，老王问：", "老王|几点了？"}, []string{"老王"}},
		{"ja after", "「行こう」と太郎は言った。",
			[]string{"太郎|行こう", "-|と太郎は言った。"}, []string{"太郎"}},
		{"heading kept", "# Chapter 1\n\nMARA: Hi.",
			[]string{"-|# Chapter 1", "Mara|Hi."}, []string{"Mara"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Detect(c.in, nil)
			if got := view(r); !slices.Equal(got, c.want) {
				t.Errorf("lines\n got  %q\n want %q", got, c.want)
			}
			if !slices.Equal(r.Speakers, c.speakers) {
				t.Errorf("speakers %q, want %q", r.Speakers, c.speakers)
			}
			for _, l := range r.Lines {
				if l.Span.Of(c.in) != l.Text {
					t.Errorf("span mismatch %q vs %q", l.Span.Of(c.in), l.Text)
				}
			}
		})
	}
}

func TestOptions(t *testing.T) {
	r := Detect(`[Robot] Beep. "Hi," said Ann.`, &Options{Reserved: []string{"robot"}, KeepQuotes: true})
	want := []string{`-|[Robot] Beep.`, `Ann|"Hi,"`, "-|said Ann."}
	if got := view(r); !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestStageDirections(t *testing.T) {
	src := "妈妈（温柔地）：宝贝，过来。\nMOM (softly): Come here.\n[Mia] (pleading) Please?\n孩子：（小声）我可以养它吗？\nJOHN (V.O.): Years later.\n妈妈：（叹气）\n好吧。"
	res := Detect(src, nil)
	want := []struct{ speaker, text, dir string }{
		{"妈妈", "宝贝，过来。", "温柔地"},
		{"Mom", "Come here.", "softly"},
		{"Mia", "Please?", "pleading"},
		{"孩子", "我可以养它吗？", "小声"},
		{"John", "Years later.", ""},
		{"妈妈", "好吧。", "叹气"},
	}
	if len(res.Lines) != len(want) {
		t.Fatalf("lines %+v", res.Lines)
	}
	for i, w := range want {
		l := res.Lines[i]
		if l.Speaker != w.speaker || l.Text != w.text || l.Direction != w.dir || l.Span.Of(src) != l.Text {
			t.Errorf("line %d = %q %q %q, want %+v", i, l.Speaker, l.Text, l.Direction, w)
		}
		if strings.ContainsAny(l.Text, "()（）") {
			t.Errorf("line %d speaks its direction: %q", i, l.Text)
		}
	}
}

// An adverb before the speech verb is no name: "妈妈放下碗，轻声说" does
// not make 轻声 a character.
func TestAdverbIsNoName(t *testing.T) {
	src := "“你来了。”妈妈放下碗，轻声说，“饭在锅里。”"
	r := Detect(src, nil)
	if len(r.Speakers) != 0 || len(r.Lines) != 3 || r.Lines[1].Text != "妈妈放下碗，轻声说，" {
		t.Errorf("speakers %q, lines %+v", r.Speakers, r.Lines)
	}
}

func TestSpeaker(t *testing.T) {
	for _, c := range []struct {
		line, name, dir string
		body            int
		ok              bool
	}{
		{"伊内丝：你好", "伊内丝", "", len("伊内丝："), true},
		{"妈妈（低声）：快睡吧", "妈妈", "低声", len("妈妈（低声）："), true},
		{"旁白：雾散了", "", "", len("旁白："), true},
		{"MARA: Hi", "Mara", "", len("MARA: "), true},
		{"[Mia] (pleading) Please?", "Mia", "pleading", len("[Mia] (pleading) "), true},
		{"Note: no", "", "", 0, false},
		{"他放下碗。", "", "", 0, false},
	} {
		name, dir, body, ok := Speaker(c.line)
		if name != c.name || dir != c.dir || body != c.body || ok != c.ok {
			t.Errorf("%q: %q %q %d %v", c.line, name, dir, body, ok)
		}
	}
}
