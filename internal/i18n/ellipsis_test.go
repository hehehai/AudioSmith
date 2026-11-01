package i18n

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// stateMessage is a message about work under way ("Loading…",
// "Waiting for the engine…"): its first word ends in -ing.
var stateMessage = regexp.MustCompile(`^\PL*\pL+ing\b`)

// ellipsisAllowed are the other messages that may end in "…": none is a
// button, menu item or option label (user, 11-01: labels have no "…").
var ellipsisAllowed = map[string]bool{
	// Work under way that does not start with an -ing word.
	"The call gateway is starting…":                                true,
	"The engine is starting…":                                      true,
	"The speech engine is starting…":                               true,
	"The reference of “%s” doesn't say its text: making it again…": true,
	"Press keys…": true, // the shortcut recorder, waiting for keys
	// Placeholders: hints inside empty fields.
	"Search: narrator, 东北, grandma…":                                  true,
	"Search pages, actions, voices, projects…":                        true,
	"Tone, audience, how freely to adapt…":                            true,
	"Type or paste the text to speak…":                                true,
	"Add a note, or send the file as it is…":                          true,
	"Click here, then press the shortcut and speak…":                  true,
	"Drop a recording to turn into text…":                             true,
	"Dub this video into Chinese: drop it here or paste its link…":    true,
	"Narrate a product introduction…":                                 true,
	"Paste a chapter to read aloud…":                                  true,
	"Describe a story in a sentence, or paste a script…":              true,
	"Paste a manuscript, or drop a TXT, Markdown, EPUB or DOCX file…": true,
	"Say it as…": true, // a pronunciation's "say" field
	// A folder panel's title that reads on into the chosen folder.
	"Move the data folder to…": true,
}

// TestNoEllipsisLabels: action labels (buttons, menu items, options) end
// without "…" in both languages; only work under way and placeholders
// keep it.
func TestNoEllipsisLabels(t *testing.T) {
	b, err := os.ReadFile("locales/zh-Hans.json")
	if err != nil {
		t.Fatal(err)
	}
	var cat map[string]string
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatal(err)
	}
	for k, v := range cat {
		dots := strings.HasSuffix(k, "…") || strings.HasSuffix(k, "...")
		if dots && !stateMessage.MatchString(k) && !ellipsisAllowed[k] {
			t.Errorf("%q ends in an ellipsis: drop it, or allow it here if it is not a label", k)
		}
		if !dots && (strings.HasSuffix(v, "…") || strings.HasSuffix(v, "...")) {
			t.Errorf("%q: its translation %q ends in an ellipsis the English has not", k, v)
		}
	}
	for k := range ellipsisAllowed {
		if _, ok := cat[k]; !ok {
			t.Errorf("ellipsisAllowed has %q, which the catalogue no longer has", k)
		}
	}
}
