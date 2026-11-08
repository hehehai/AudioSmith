package voicedesign

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

func TestTables(t *testing.T) {
	omni := 0
	for _, c := range Categories {
		info, ok := Info(c)
		if !ok || info.Label.En == "" || info.Label.Zh == "" {
			t.Fatalf("category %s: missing info", c)
		}
		for _, op := range info.Options {
			if op.ID == "" || op.Label.En == "" || op.Label.Zh == "" || op.Phrase.En == "" || op.Phrase.Zh == "" {
				t.Errorf("%s/%s: empty label or phrase", c, op.ID)
			}
			if op.Omni.En != "" || op.Omni.Zh != "" {
				omni++
			}
		}
	}
	// OmniVoice's vocabulary: 2 genders, 5 ages, 5 pitches, whisper,
	// 10 English accents, 12 dialects.
	if omni != 35 {
		t.Errorf("OmniVoice tags = %d, want 35", omni)
	}
	if len(ValuesOf(Accent)) < 10 || len(ValuesOf(Dialect)) < 12 {
		t.Errorf("accents %d dialects %d", len(ValuesOf(Accent)), len(ValuesOf(Dialect)))
	}
}

func TestLexicon(t *testing.T) {
	if lex() == nil {
		t.Fatal("no lexicon")
	}
	if _, err := buildLexicon("gender=male: man|man", ""); err == nil {
		t.Error("duplicate phrase accepted")
	}
	if _, err := buildLexicon("gender=robot: bot", ""); err == nil {
		t.Error("unknown value accepted")
	}
	if _, err := buildLexicon("no colon", ""); err == nil {
		t.Error("bad line accepted")
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in        string
		want      Attributes
		unmatched []string
	}{
		{"a warm elderly British storyteller, slightly raspy",
			Attributes{Age: "elderly", Styles: []string{"raspy", "warm"}, Accent: "british"}, []string{"storyteller"}},
		{"A very deep-voiced pirate captain, not too fast, with a Scottish accent",
			Attributes{Pitch: "very_low", Accent: "scottish"}, []string{"pirate captain"}},
		{"young girl, cheerful and bright, high pitched",
			Attributes{Gender: "female", Age: "child", Pitch: "high", Styles: []string{"bright"}, Emotion: "happy"}, nil},
		{"a 45-year-old man with a calm, smooth baritone and a Sichuan accent",
			Attributes{Gender: "male", Age: "middle_aged", Pitch: "low", Styles: []string{"smooth", "calm"}, Dialect: "sichuan"}, nil},
		{"一位温柔的中年女性，声音低沉有磁性，带一点四川口音，语速慢",
			Attributes{Gender: "female", Age: "middle_aged", Pitch: "low", Pace: "slow", Styles: []string{"magnetic"}, Emotion: "tender", Dialect: "sichuan"}, nil},
		{"非常低沉的老爷爷，沙哑，讲鬼故事",
			Attributes{Gender: "male", Age: "elderly", Pitch: "very_low", Styles: []string{"raspy"}}, []string{"鬼故事"}},
		{"35岁的男人，说话很快，东北话",
			Attributes{Gender: "male", Age: "young_adult", Pace: "fast", Dialect: "northeastern"}, nil},
		{"四十多岁的女人", Attributes{Gender: "female", Age: "middle_aged"}, nil},
		{"she sounds like a teenage boy", Attributes{Gender: "male", Age: "teen"}, nil},
		{"in her 70s, whispering", Attributes{Gender: "female", Age: "elderly", Styles: []string{"whisper"}}, nil},
		{"not whispering, energetic", Attributes{Styles: []string{"energetic"}}, nil},
		{"energetic and calm", Attributes{Styles: []string{"calm"}}, nil}, // the later pick wins
		{"Chinese woman speaking with a Beijing accent", Attributes{Gender: "female", Dialect: "beijing"}, nil},
		{"a Chinese-accented English speaker", Attributes{Accent: "chinese"}, nil},
		{"British, no, actually American", Attributes{Accent: "american"}, []string{"actually"}},
		{"high energy hype man for a sports ad", Attributes{Gender: "male", Styles: []string{"energetic"}}, []string{"sports ad"}},
		{"不要太快，温暖的男声", Attributes{Gender: "male", Styles: []string{"warm"}}, nil},
		{"甜美的少女，台湾腔，开心", Attributes{Gender: "female", Age: "teen", Styles: []string{"sweet"}, Emotion: "happy", Dialect: "taiwanese"}, nil},
		{"", Attributes{}, nil},
		{"zxqv blorp", Attributes{}, []string{"zxqv blorp"}},
	}
	for _, tt := range tests {
		r := Parse(tt.in)
		want := tt.want.Canonical()
		if !reflect.DeepEqual(r.Attrs, want) {
			t.Errorf("Parse(%q).Attrs = %+v, want %+v\n%+v", tt.in, r.Attrs, want, r.Matched)
		}
		var un []string
		for _, u := range r.Unmatched {
			un = append(un, u.Text)
		}
		if !slices.Equal(un, tt.unmatched) {
			t.Errorf("Parse(%q).Unmatched = %q, want %q", tt.in, un, tt.unmatched)
		}
		checkSpans(t, tt.in, r)
	}
}

func checkSpans(t *testing.T, in string, r Result) {
	t.Helper()
	for _, m := range r.Matched {
		if m.Start < 0 || m.End > len(in) || m.Start >= m.End || in[m.Start:m.End] != m.Phrase {
			t.Fatalf("%q: bad span %+v", in, m)
		}
	}
	for _, u := range r.Unmatched {
		if u.Start < 0 || u.End > len(in) || u.Start >= u.End || in[u.Start:u.End] != u.Text {
			t.Fatalf("%q: bad term span %+v", in, u)
		}
	}
}

func TestParseNotes(t *testing.T) {
	r := Parse("not deep, a boy who is actually a teenager")
	notes := map[string]string{}
	for _, m := range r.Matched {
		notes[string(m.Category)+"="+m.Value+":"+m.Phrase] = m.Note
	}
	want := map[string]string{
		"pitch=low:deep":    "negated",
		"gender=male:boy":   "",
		"age=child:boy":     "default",
		"age=teen:teenager": "",
	}
	for k, v := range want {
		if got, ok := notes[k]; !ok || got != v {
			t.Errorf("%s: note %q (present %v), want %q; all %v", k, got, ok, v, notes)
		}
	}
	// overridden values are kept in Matched, not applied
	r = Parse("a high voice ... no wait, a low voice")
	if r.Attrs.Pitch != "low" {
		t.Fatalf("pitch %q", r.Attrs.Pitch)
	}
	if r.Matched[0].Applied || r.Matched[0].Note != "overridden" {
		t.Errorf("first match %+v", r.Matched[0])
	}
}

func TestParseTruncates(t *testing.T) {
	in := strings.Repeat("a ", MaxDescription) + "deep"
	r := Parse(in)
	if !r.Truncated || r.Attrs.Pitch != "" {
		t.Errorf("truncated=%v pitch=%q", r.Truncated, r.Attrs.Pitch)
	}
}

func TestWith(t *testing.T) {
	a := Attributes{Accent: "british", Styles: []string{"energetic"}}
	b, cleared, err := a.With(Dialect, "sichuan")
	if err != nil || b.Accent != "" || b.Dialect != "sichuan" || len(cleared) != 1 || cleared[0] != (Cleared{Accent, "british"}) {
		t.Errorf("dialect pick: %+v %v %v", b, cleared, err)
	}
	b, cleared, _ = b.With(Style, "calm")
	if !slices.Equal(b.Styles, []string{"calm"}) || len(cleared) != 1 || cleared[0].ID != "energetic" {
		t.Errorf("calm pick: %+v %v", b, cleared)
	}
	b, _, _ = b.With(Style, "warm")
	b, _, _ = b.With(Style, "raspy")
	b, cleared, _ = b.With(Style, "nasal")
	if len(b.Styles) != MaxStyles || !b.HasStyle("nasal") || len(cleared) != 1 {
		t.Errorf("max styles: %+v %v", b, cleared)
	}
	if _, _, err := b.With(Age, "ancient"); err == nil {
		t.Error("unknown value accepted")
	}
	if c := b.Without(Style, "nasal"); c.HasStyle("nasal") {
		t.Error("Without kept the style")
	}
	if c := b.Without(Dialect, ""); c.Dialect != "" {
		t.Error("Without kept the dialect")
	}
	if c, _, _ := b.With(Age, "child"); c.HasStyle("raspy") {
		t.Errorf("child kept raspy: %+v", c)
	}
}

func TestConflicts(t *testing.T) {
	a := Attributes{Accent: "british", Dialect: "sichuan", Age: "child", Pitch: "very_low", Styles: []string{"whisper", "energetic", "calm", "warm"}}
	cs := a.Conflicts()
	if len(cs) < 5 || a.Validate() == nil {
		t.Errorf("conflicts %+v", cs)
	}
	if (Attributes{Gender: "male", Pitch: "very_high", Emotion: "angry", Styles: []string{"whisper"}}).Validate() != nil {
		t.Error("unusual-but-possible combination rejected")
	}
}

func TestSummary(t *testing.T) {
	a := Attributes{Gender: "female", Age: "middle_aged", Styles: []string{"warm"}, Accent: "british"}
	if got := a.Summary("en"); got != "Female · Middle-aged · Warm · British" {
		t.Errorf("en %q", got)
	}
	if got := a.Summary("zh-Hans"); got != "女 · 中年 · 温暖 · 英式" {
		t.Errorf("zh %q", got)
	}
	if got := (Attributes{}).Summary("en"); !strings.HasPrefix(got, "Auto") {
		t.Errorf("auto %q", got)
	}
}

func TestLower(t *testing.T) {
	full := Recipe{Attrs: Attributes{Gender: "female", Age: "middle_aged", Pitch: "low", Styles: []string{"warm", "whisper"}, Accent: "british", Pace: "slow", Emotion: "tender"}, Seed: 7}
	dialect := Recipe{Attrs: Attributes{Gender: "male", Age: "young_adult", Dialect: "sichuan", Styles: []string{"energetic"}, Emotion: "happy"}}
	tests := []struct {
		target Target
		r      Recipe
		lang   string
		voice  string
		drop   int
	}{
		// A slow pace is left out of design prompts (#93).
		{TargetQwen3Design, full, "en", "A warm middle-aged woman with a deep, low-pitched voice and a British accent. She speaks in a whisper, sounding tender.", 0},
		{TargetQwen3Design, full, "zh", "一位中年女性，声音低沉，音色温暖，带有英式口音，用耳语般的声音轻声说话，语气温柔。", 0},
		{TargetOmniVoice, full, "", "female, middle-aged, low pitch, whisper, british accent", 3},
		{TargetQwen3CustomVoice, full, "en", "Serena, Speak slowly, in a whisper, in a warm voice, sounding tender.", 3},
		{TargetQwen3Design, dialect, "en", "An energetic young man, speaking Mandarin with a Sichuan accent. He sounds happy.", 0},
		{TargetQwen3Design, dialect, "", "一位青年男性，音色充满活力，说四川话，语气开心。", 0},
		{TargetOmniVoice, dialect, "en", "男，青年，四川话", 2},
		{TargetQwen3CustomVoice, dialect, "zh", "Eric, 用充满活力的声音说话，语气开心。", 0},
		{TargetOmniVoice, Recipe{}, "", "", 0},
		{TargetQwen3Design, Recipe{Attrs: Attributes{Pitch: "high", Styles: []string{"bright"}}}, "en", "A high-pitched, bright voice.", 0},
		{TargetOmniVoice, Recipe{Attrs: Attributes{Gender: "female", Dialect: "beijing"}}, "", "female", 1},
	}
	for _, tt := range tests {
		l, err := Lower(tt.target, tt.r, Options{Lang: tt.lang})
		if err != nil {
			t.Fatal(err)
		}
		if l.Voice != tt.voice || len(l.Dropped) != tt.drop {
			t.Errorf("%s %s: %q dropped %v\n want %q (%d dropped)", tt.target, tt.lang, l.Voice, l.Dropped, tt.voice, tt.drop)
		}
		if l.Seed != tt.r.Seed || l.Target != tt.target {
			t.Errorf("seed/target %+v", l)
		}
	}
	if _, err := Lower("nope", full, Options{}); err == nil {
		t.Error("unknown target accepted")
	}
}

func TestLowerFreeformDescription(t *testing.T) {
	desc := "a warm elderly British storyteller, slightly raspy"
	parsed := Parse(desc).Attrs

	// no picks: verbatim
	l, _ := Lower(TargetQwen3Design, Recipe{Description: desc, Attrs: parsed}, Options{})
	if l.Voice != desc {
		t.Errorf("verbatim: %q", l.Voice)
	}
	// an added pick is appended
	a, _, _ := parsed.With(Gender, "female")
	l, _ = Lower(TargetQwen3Design, Recipe{Description: desc, Attrs: a}, Options{})
	if l.Voice != desc+". A woman." {
		t.Errorf("addition: %q", l.Voice)
	}
	// a contradicting pick: written out in full, unmapped words as context
	a, _, _ = parsed.With(Accent, "american")
	l, _ = Lower(TargetQwen3Design, Recipe{Description: desc, Attrs: a}, Options{})
	if strings.Contains(l.Voice, "British") || !strings.Contains(l.Voice, "American") || !strings.HasSuffix(l.Voice, "Context: storyteller.") {
		t.Errorf("contradiction: %q", l.Voice)
	}
	// OmniVoice drops the free text
	l, _ = Lower(TargetOmniVoice, Recipe{Description: desc, Attrs: parsed}, Options{})
	if !l.DescriptionDropped || l.Voice != "elderly, british accent" {
		t.Errorf("omni: %+v", l)
	}
}

// TestSentenceSlowPace (#93, after #91): design prompts leave a slow pace
// out (VoiceDesign said references past its length limit); a fast one
// stays; the gallery's description keeps it; a description the user
// wrote with "slowly" keeps it, even when a pick rewrites it in full.
func TestSentenceSlowPace(t *testing.T) {
	elder := Attributes{Gender: "male", Age: "elderly", Pitch: "low", Styles: []string{"warm"}, Pace: "slow"}
	if got := Sentence(elder, "zh"); got != "一位老年男性，声音低沉，音色温暖。" {
		t.Errorf("zh %q", got)
	}
	if got := Sentence(elder, "en"); got != "A warm elderly man with a deep, low-pitched voice." {
		t.Errorf("en %q", got)
	}
	if got := Describe(elder, "zh"); got != "一位老年男性，声音低沉，音色温暖，语速偏慢。" {
		t.Errorf("describe %q", got)
	}
	if Sentence(Attributes{Pace: "very_slow"}, "en") != "" {
		t.Error("only a slow pace")
	}
	if got := Sentence(Attributes{Age: "young_adult", Pace: "fast"}, "zh"); !strings.Contains(got, "语速偏快") {
		t.Errorf("fast %q", got)
	}
	// The user's own words.
	desc := "an elderly man who speaks slowly"
	parsed := Parse(desc).Attrs
	if parsed.Pace != "slow" {
		t.Fatalf("parsed %+v", parsed)
	}
	if l, _ := Lower(TargetQwen3Design, Recipe{Description: desc, Attrs: parsed}, Options{}); l.Voice != desc {
		t.Errorf("verbatim %q", l.Voice)
	}
	a, _, _ := parsed.With(Age, "middle_aged")
	if l, _ := Lower(TargetQwen3Design, Recipe{Description: desc, Attrs: a}, Options{}); !strings.Contains(l.Voice, "slowly") || !strings.Contains(l.Voice, "middle-aged") {
		t.Errorf("rewritten %q", l.Voice)
	}
	// Picked, not written: left out.
	b, _, _ := (Attributes{Age: "elderly"}).With(Pace, "slow")
	if l, _ := Lower(TargetQwen3Design, Recipe{Attrs: b}, Options{Lang: "en"}); strings.Contains(l.Voice, "slow") {
		t.Errorf("picked %q", l.Voice)
	}
}

func TestOmniVoiceTagsRoundTrip(t *testing.T) {
	for _, a := range []Attributes{
		{Gender: "female", Age: "young_adult", Pitch: "high", Accent: "british"},
		{Gender: "male", Age: "elderly", Pitch: "very_low", Styles: []string{"whisper"}, Dialect: "henan"},
		{Age: "teen"},
	} {
		for _, lang := range []string{"en", "zh"} {
			l, _ := Lower(TargetOmniVoice, Recipe{Attrs: a}, Options{Lang: lang})
			back, unknown := ParseOmniVoiceTags(l.Voice)
			if !reflect.DeepEqual(back, a.Canonical()) || len(unknown) > 0 {
				t.Errorf("%q → %+v %v, want %+v", l.Voice, back, unknown, a)
			}
		}
	}
	_, unknown := ParseOmniVoiceTags("female, sparkly, None")
	if !slices.Equal(unknown, []string{"sparkly"}) {
		t.Errorf("unknown %v", unknown)
	}
}

func TestEngineFor(t *testing.T) {
	cat := catalog.Builtin()
	want := map[string]Target{
		"omnivoice-bf16":                  TargetOmniVoice,
		"qwen3-tts-0.6b-customvoice-8bit": TargetQwen3CustomVoice,
		"qwen3-tts-1.7b-voicedesign-4bit": TargetQwen3Design, // catalogue entry added by the M1 engine thread
		"voxcpm2-8bit":                    TargetVoxCPM,
	}
	for _, e := range cat.Entries {
		got, ok := EngineFor(e.Family, e.Repo)
		if w, has := want[e.ID]; has {
			if !ok || got.Target != w {
				t.Errorf("%s: %v %v", e.ID, got.Target, ok)
			}
		} else if ok {
			t.Errorf("%s can't design but got %s", e.ID, got.Target)
		}
	}
	if e, ok := EngineFor("qwen3_tts", "mlx-community/Qwen3-TTS-12Hz-1.7B-VoiceDesign-4bit"); !ok || e.Target != TargetQwen3Design || e.Vocabulary != VocabFreeform {
		t.Errorf("voicedesign: %+v %v", e, ok)
	}
	// the table is extensible
	Register(Engine{Target: "test-engine", Vocabulary: VocabFreeform, Match: func(f, _ string) bool { return f == "test" },
		Lower: func(r Recipe, _ Options) Lowered { return Lowered{Voice: r.Description} }})
	defer func() {
		engMu.Lock()
		engines = slices.DeleteFunc(engines, func(e Engine) bool { return e.Target == "test-engine" })
		engMu.Unlock()
	}()
	if e, ok := EngineFor("test", ""); !ok || e.Target != "test-engine" {
		t.Error("registered engine not found")
	}
	if l, _ := Lower("test-engine", Recipe{Description: " hi  there "}, Options{}); l.Voice != "hi there" {
		t.Errorf("normalised description %q", l.Voice)
	}
}

func TestBestSpeaker(t *testing.T) {
	cases := []struct {
		a       Attributes
		lang    string
		allowed []string
		want    string
	}{
		{Attributes{Gender: "male", Dialect: "beijing"}, "", nil, "Dylan"},
		{Attributes{Gender: "male", Age: "elderly"}, "zh", nil, "Uncle_Fu"},
		{Attributes{Gender: "male", Accent: "american"}, "", nil, "Aiden"},
		{Attributes{Gender: "female", Styles: []string{"warm"}}, "ko", nil, "Sohee"},
		{Attributes{Gender: "female", Styles: []string{"warm"}}, "ko", []string{"Vivian", "Serena"}, "Serena"},
	}
	for _, c := range cases {
		if got := BestSpeaker(c.a, c.lang, c.allowed); got.ID != c.want {
			t.Errorf("%+v %s: %s, want %s", c.a, c.lang, got.ID, c.want)
		}
	}
}

func TestRecipeKey(t *testing.T) {
	r := Recipe{Description: "a  warm\tvoice ", Attrs: Attributes{Styles: []string{"warm", "raspy"}, Gender: "male"}, Seed: 42}
	r2 := Recipe{Description: "a warm voice", Attrs: Attributes{Styles: []string{"raspy", "warm", "warm"}, Gender: "male"}, Seed: 42}
	if r.Key() != r2.Key() {
		t.Error("equivalent recipes have different keys")
	}
	// golden: the key must not change between releases (preview caches)
	if got := r.Key(); got != goldenRecipeKey {
		t.Errorf("Key() = %s, want %s (bump keyVersion if the layout changed on purpose)", got, goldenRecipeKey)
	}
	r2.Seed = 43
	if r.Key() == r2.Key() {
		t.Error("seed not in key")
	}
	if err := (Recipe{Seed: -1}).Validate(); err == nil {
		t.Error("negative seed valid")
	}
	if err := (Recipe{Seed: MaxSeed, Attrs: Attributes{Accent: "british", Dialect: "henan"}}).Validate(); err == nil {
		t.Error("accent+dialect valid")
	}
	if n := (Recipe{Seed: -5}).Normalize().Seed; n < 0 || n > MaxSeed {
		t.Errorf("fold %d", n)
	}
}

const goldenRecipeKey = "b46a48c5b7c343937d872e09bcff28601dde8dcc2bedd69282d6d4ff22fdecb0"

func TestSeeds(t *testing.T) {
	for range 100 {
		if s := NewSeed(); s < 0 || s > MaxSeed {
			t.Fatalf("NewSeed %d", s)
		}
	}
	if SeedFrom("x") != SeedFrom("x") || SeedFrom("x") == SeedFrom("y") {
		t.Error("SeedFrom not stable")
	}
	if v, err := ParseSeed(" 2147483647 "); err != nil || v != MaxSeed {
		t.Errorf("%d %v", v, err)
	}
	for _, bad := range []string{"-1", "2147483648", "4x", ""} {
		if _, err := ParseSeed(bad); err == nil {
			t.Errorf("ParseSeed(%q) accepted", bad)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"a warm elderly British storyteller", "非常低沉的老爷爷，沙哑", "not too fast", "35岁", "in her 40s", "x'y’z--", "十五岁的少女"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			return
		}
		r := Parse(s)
		checkSpans(t, s, r)
		if cs := r.Attrs.Conflicts(); len(cs) > 0 {
			t.Fatalf("%q: conflicts %+v", s, cs)
		}
		for _, tg := range []Target{TargetQwen3Design, TargetOmniVoice, TargetQwen3CustomVoice, TargetVoxCPM} {
			if _, err := Lower(tg, Recipe{Description: s, Attrs: r.Attrs}, Options{}); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func BenchmarkParse(b *testing.B) {
	in := "A warm, slightly raspy elderly British storyteller who speaks slowly and calmly; 一位温柔的中年女性，声音低沉有磁性，带一点四川口音"
	for b.Loop() {
		Parse(in)
	}
}
