package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/models"
	famruntime "github.com/hehehai/actnowkit/runtime"
)

// TestSnapshotsFamilyReuse renders the reuse of Dicta's models (Dicta, a
// release build, installed): the first run's reuse card, the Models
// page's family lines ("Shared across ActNow · In use by another app",
// "Installed · Shared across ActNow" with Use), the licence dialog of a licence
// accepted in Dicta; English and Chinese at 1100×720 (the storage page's
// folder row is in TestSnapshotsFamily's f05):
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsFamilyReuse .
func TestSnapshotsFamilyReuse(t *testing.T) { runSnapshots(t, snapshotsFamilyReuse) }

func snapshotsFamilyReuse(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	useMemKeys(t)
	stubMachine(t, 8, true) // the fast pack
	for _, cfg := range []b2Config{
		{"1100-en", 1100, 720, 1, false, "en"},
		{"1100-zh", 1100, 720, 1, false, "zh-Hans"},
	} {
		a, eng, tt := newTestApp(t)
		i18n.SetLocale("en")
		a.updateSettings(func(s *settings.Settings) { s.General.Language = cfg.lang })
		tt.SetSize(cfg.w, cfg.h)
		tt.SetScale(2)
		shoot := func(name string, fn func()) {
			fn()
			tt.Frame()
			tt.Frame()
			tt.Frame()
			f, err := os.Create(filepath.Join(out, name+"-"+cfg.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
		}
		ctx := context.Background()
		tf := startFamily(t, a, dictaID) // a first run: nothing migrated
		// Dicta installed Qwen3-ASR, speech detection and the aligner,
		// holds Qwen3-ASR loaded, and accepted OmniVoice's licence.
		asr := catalogEntry(t, catalog.DefaultDictation)
		for _, id := range []string{catalog.DefaultDictation, catalog.DefaultVAD, catalog.DefaultAlign} {
			tf.claim(t, dictaID, catalogEntry(t, id))
		}
		if err := famruntime.Write(tf.Family, famruntime.File{BundleID: dictaID, Loaded: []famruntime.Loaded{
			{Repo: asr.Repo, Revision: asr.Revision, Worker: "listen", Bytes: 1 << 29}}}); err != nil {
			t.Fatal(err)
		}
		omni := catalogEntry(t, idOmniVoice)
		if err := models.AcceptLicence(ctx, tf.Family, omni.Repo, "CC-BY-NC-4.0", dictaID); err != nil {
			t.Fatal(err)
		}
		a.famRT = tf.fam.Runtime(family.RuntimeOptions{})
		a.refreshModels()
		eng.sent = nil
		shoot("f06-reuse-card", func() {
			a.openOnboarding()
			a.obStep = 1
		})
		a.obOpen = false
		// Qwen3-ASR used ([Use]); the aligner not.
		a.useModel(asr)
		shoot("f07-models-family", func() {
			a.navigate("/models")
			a.modelsKind = 2 // Transcription
			a.refreshFamily()
		})
		shoot("f08-licence-accepted", func() { a.askLicence(omni, nil) })
		a.licenceOpen = false
	}
}
