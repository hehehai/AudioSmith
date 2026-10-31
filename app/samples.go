package main

// The sample projects (0.5 F6): the five cards on Home's 示例 tab and the
// Projects page, and ⌘K's 示例 rows. A sample bundled in the binary
// (assets/samples/<id>/, its format in assets/samples/README.md) is
// copied into the library as a normal project and opened
// (samples_copy.go); one not bundled yet keeps the built-in fallback: a
// text-only script for the story, the chapter and the narration, 即将推出
// for the dub and the meeting.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/hehehai/AudioSmith/assets"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/oplog"
)

// Sample kinds (sample.json's "kind").
const (
	sampleStory          = "story"
	sampleAudiobook      = "audiobook"
	sampleNarration      = "narration"
	sampleDub            = "dub"
	sampleTranscriptKind = "transcript"
)

// Sample file roles (sample.json's files[].role).
const (
	roleProject = "project"
	roleScript  = "script"
	roleAudio   = "audio"
	roleMedia   = "media"
)

// sampleBudget is the most the bundled samples may weigh together.
const sampleBudget = 15 << 20

// sampleManifest is a sample's sample.json.
type sampleManifest struct {
	Version  int               `json:"version"`
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Title    map[string]string `json:"title"`
	Subtitle map[string]string `json:"subtitle"`
	Cover    struct {
		Color string `json:"color,omitempty"`
		Icon  string `json:"icon,omitempty"`
	} `json:"cover"`
	Files []sampleFile `json:"files"`
}

// sampleFile is a file of a sample, by its path in the sample's folder.
type sampleFile struct {
	Path string `json:"path"`
	Role string `json:"role"`
}

// file is the first file of role ("" none).
func (m sampleManifest) file(role string) string {
	for _, f := range m.Files {
		if f.Role == role {
			return f.Path
		}
	}
	return ""
}

// text is a localized text of the manifest: the UI's language, else
// English.
func localized(m map[string]string) string {
	if s := m[i18n.Locale()]; s != "" {
		return s
	}
	return m["en"]
}

// sampleCard is a card: its key (the sample's id), texts and icon, and
// what it opens (soon: nothing yet).
type sampleCard struct {
	key, kind, title, about string
	icon                    icons.Icon
	soon                    bool
	bundled                 *sampleManifest
}

// sampleCards are the cards, in order: the bundled samples, or the
// built-in fallbacks.
func (a *app) sampleCards() []sampleCard {
	cards := []sampleCard{
		{"story", sampleStory, i18n.T("A story for two voices"), i18n.T("A short scene, cast and performed"), storiesIcon, false, nil},
		{"chapter", sampleAudiobook, i18n.T("An audiobook chapter"), i18n.T("A chapter narrated with its headings"), audiobookIcon, false, nil},
		{"narration", sampleNarration, i18n.T("A product narration"), i18n.T("Thirty seconds of voice-over"), studioIcon, false, nil},
		{"dub", sampleDub, i18n.T("A dubbed clip"), i18n.T("A short video dubbed into Chinese"), dubIcon, true, nil},
		{"meeting", sampleTranscriptKind, i18n.T("A meeting transcript"), i18n.T("Who said what, with timings"), transcribeIcon, true, nil},
	}
	bundled := a.samples()
	for i := range cards {
		if m, ok := bundled[cards[i].key]; ok && m.Kind == cards[i].kind {
			cards[i].bundled, cards[i].soon = &m, false
			if s := localized(m.Title); s != "" {
				cards[i].title = s
			}
			if s := localized(m.Subtitle); s != "" {
				cards[i].about = s
			}
			if ic, ok := sampleIcon(m.Cover.Icon); ok {
				cards[i].icon = ic
			}
		}
	}
	return cards
}

// sampleCardOf is the card of key.
func (a *app) sampleCardOf(key string) (sampleCard, bool) {
	for _, s := range a.sampleCards() {
		if s.key == key {
			return s, true
		}
	}
	return sampleCard{}, false
}

// samplesFS is where the samples are read from: the bundled ones (tests
// put a fixture here).
var samplesFS fs.FS = mustSub(assets.Samples, "samples")

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}

var (
	samplesMu   sync.Mutex
	samplesRead map[string]sampleManifest // nil: not read (tests that change samplesFS clear it)
)

// samples are the valid samples of samplesFS by id, read once; a broken
// one is logged and left out.
func (a *app) samples() map[string]sampleManifest {
	samplesMu.Lock()
	defer samplesMu.Unlock()
	if samplesRead != nil {
		return samplesRead
	}
	samplesRead = map[string]sampleManifest{}
	entries, _ := fs.ReadDir(samplesFS, ".")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, _, err := readSample(samplesFS, e.Name())
		if err != nil {
			oplog.Warn("sample.invalid", "id", e.Name(), "error", err)
			continue
		}
		samplesRead[m.ID] = m
	}
	return samplesRead
}

// readSample reads and checks the sample in folder dir: its manifest, and
// the size of its files.
func readSample(fsys fs.FS, dir string) (sampleManifest, int64, error) {
	var m sampleManifest
	b, err := fs.ReadFile(fsys, path.Join(dir, "sample.json"))
	if err != nil {
		return m, 0, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, 0, fmt.Errorf("sample.json: %w", err)
	}
	switch {
	case m.Version != 1:
		return m, 0, fmt.Errorf("version %d", m.Version)
	case m.ID != dir:
		return m, 0, fmt.Errorf("id %q is not the folder's name", m.ID)
	case !slices.Contains([]string{sampleStory, sampleAudiobook, sampleNarration, sampleDub, sampleTranscriptKind}, m.Kind):
		return m, 0, fmt.Errorf("kind %q", m.Kind)
	case m.Title["en"] == "" || m.Title["zh-Hans"] == "":
		return m, 0, fmt.Errorf("title needs en and zh-Hans")
	}
	size := int64(len(b))
	for _, f := range m.Files {
		if !fs.ValidPath(f.Path) || f.Path == "." || strings.HasPrefix(f.Path, "sample.json") {
			return m, 0, fmt.Errorf("file path %q", f.Path)
		}
		if !slices.Contains([]string{roleProject, roleScript, roleAudio, roleMedia}, f.Role) {
			return m, 0, fmt.Errorf("file %s: role %q", f.Path, f.Role)
		}
		st, err := fs.Stat(fsys, path.Join(dir, f.Path))
		if err != nil {
			return m, 0, err
		}
		size += st.Size()
	}
	project, script := m.file(roleProject) != "", m.file(roleScript) != ""
	switch m.Kind {
	case sampleNarration:
		if !script {
			return m, 0, fmt.Errorf("a narration needs its script")
		}
	case sampleDub, sampleTranscriptKind:
		if !project {
			return m, 0, fmt.Errorf("a %s needs its project", m.Kind)
		}
	default:
		if !project && !script {
			return m, 0, fmt.Errorf("a %s needs its project or script", m.Kind)
		}
	}
	return m, size, nil
}
