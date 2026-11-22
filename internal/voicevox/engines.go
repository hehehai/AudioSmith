package voicevox

// The engines AudioSmith connects to and the voice ids of their styles.

import (
	"strconv"
	"strings"
)

// Kind is an engine's flavour.
type Kind string

const (
	VOICEVOX Kind = "voicevox" // VOICEVOX ENGINE (the VOICEVOX app)
	Aivis    Kind = "aivis"    // AivisSpeech Engine (the AivisSpeech app)
)

// Kinds are the engines, in the order the app shows them.
var Kinds = []Kind{VOICEVOX, Aivis}

// DefaultURL is where kind listens unless the user changed it.
func DefaultURL(k Kind) string {
	if k == Aivis {
		return "http://127.0.0.1:10101"
	}
	return "http://127.0.0.1:50021"
}

// Brand is kind's name as its makers write it.
func (k Kind) Brand() string {
	if k == Aivis {
		return "AivisSpeech"
	}
	return "VOICEVOX"
}

// Site is where the user gets kind.
func (k Kind) Site() string {
	if k == Aivis {
		return "https://aivis-project.com/"
	}
	return "https://voicevox.hiroshiba.jp/"
}

// KindOf tells the flavour of an engine from its manifest (an engine that
// says neither is taken for what its address was set for, want).
func KindOf(m Manifest, want Kind) Kind {
	s := strings.ToLower(m.Name + " " + m.BrandName)
	switch {
	case strings.Contains(s, "aivis"):
		return Aivis
	case strings.Contains(s, "voicevox"):
		return VOICEVOX
	}
	return want
}

// Voice names a style of an engine's character: "voicevox:<speaker
// uuid>/<style id>" (the uuid as well as the id, since style ids are not
// unique across engines and AivisSpeech's may collide).
type Voice struct {
	Kind    Kind
	Speaker string // speaker_uuid
	Style   int64
}

// String is the voice id.
func (v Voice) String() string {
	return string(v.Kind) + ":" + v.Speaker + "/" + strconv.FormatInt(v.Style, 10)
}

// ParseVoice reads a voice id.
func ParseVoice(s string) (Voice, bool) {
	kind, rest, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || (Kind(kind) != VOICEVOX && Kind(kind) != Aivis) {
		return Voice{}, false
	}
	uuid, id, ok := strings.Cut(rest, "/")
	if !ok || uuid == "" || strings.ContainsAny(uuid, "/?&# ") {
		return Voice{}, false
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return Voice{}, false
	}
	return Voice{Kind: Kind(kind), Speaker: uuid, Style: n}, true
}

// IsVoice reports whether s is a voice id of this package.
func IsVoice(s string) bool { _, ok := ParseVoice(s); return ok }
