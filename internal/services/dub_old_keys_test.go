package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// oldDubDigest is what an old project's tracks are: per line its verdict,
// fit and audio (the key checked to be its fingerprint, the file named by
// it), and each mixed track's audio.
func oldDubDigest(t *testing.T, e *dubEnv, p *dub.Project) string {
	t.Helper()
	sum := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:8])
	}
	var b strings.Builder
	langs := slices.Clone(p.Targets)
	slices.Sort(langs)
	for _, l := range langs {
		tr := p.Track(l)
		caps := e.dubs.cfg.Caps(p.Settings.Model)
		cache := map[string]dub.VoiceSpec{}
		fmt.Fprintf(&b, "track %s %s;", l, sum(e.dubs.Path(p.ID, tr.File)))
		for _, seg := range p.Segments {
			r := tr.Lines[seg.ID]
			v, _ := e.dubs.voiceFor(context.Background(), p, seg, l, caps, cache)
			if fp := p.Fingerprint(seg, l, e.dubs.keyVoice(p.ID, v)); r.Key != fp || !strings.HasSuffix(r.File, seg.ID+"-"+fp[:10]+".wav") {
				t.Errorf("%s %s: key %s file %s, fingerprint %s", l, seg.ID, r.Key, r.File, fp)
			}
			fmt.Fprintf(&b, " %s %s %.3f %.3f %.3f %.3f %q %s;", seg.ID, r.Verdict, r.Ratio, r.Raw, r.Length, r.Over, r.Text, sum(e.dubs.Path(p.ID, r.File)))
		}
	}
	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:12])
}

// A project made before the dub's dialogue model (no Dialogue setting)
// renders as it did, the dialogue model offered or not: the digest was
// computed on main 2cad9cc (thread #89) with the same test, less the two
// lines that did not exist there (Dialogue, e.models).
func TestOldDubsRenderAsBefore(t *testing.T) {
	e := newDubEnv(t, 6)
	e.models = dialogueModels()
	p := e.create(t, func(p *dub.Project) {
		p.ChooseTargets([]string{"zh-Hans", "es"})
		p.Settings.Dialogue = ""
	})
	p = e.run(t, p.ID, RunRequest{Through: dub.StageMix})
	if got := oldDubDigest(t, e, p); got != "3734e075e452d9d1e85bd047" {
		t.Errorf("digest %s: an old project renders otherwise", got)
	}
	if e.eng.Count(proto.MethodTTSDialogue) != 0 {
		t.Error("the dialogue model spoke an old project")
	}
}

// dialogueModels are the test's models with SoulX-Podcast offered.
func dialogueModels() *DubModels {
	return &DubModels{STT: "stt", Align: "align", Diarize: "diar", Separate: "sep", Enhance: "dfn",
		Dialogue: []DubDialogueModel{{Repo: "m/soulx", Family: "soulx_podcast", Revision: "r1", Languages: []string{"zh", "en"}}}}
}
