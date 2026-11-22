package gallery

import "testing"

// TestJapanese: every curated voice, use case, language of the facet and
// visible preset has a Japanese name (thread #173), and a Japanese search
// finds voices.
func TestJapanese(t *testing.T) {
	for _, a := range Default().All() {
		if a.Name.Ja == "" || a.Description.Ja == "" {
			t.Errorf("%s: curated.json needs \"ja\" in name and description", a.ID)
		}
	}
	for _, c := range Categories() {
		if c.Name.Ja == "" || c.Description.Ja == "" {
			t.Errorf("use case %s has no Japanese name", c.ID)
		}
	}
	for _, l := range Languages {
		if languageName(l).In("ja") == languageName(l).En {
			t.Errorf("language %s has no Japanese name", l)
		}
	}
	for _, p := range Presets() {
		if p.Name.Ja == "" {
			t.Errorf("preset %s has no Japanese name", p.ID)
		}
	}
	// The search field's placeholder in ja: ナレーター、四川、おばあちゃん.
	for _, q := range []string{"ナレーター", "四川", "おばあちゃん", "日本語"} {
		if Default().Search(Query{Text: q}).Total == 0 {
			t.Errorf("searching %q finds nothing", q)
		}
	}
}
