package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
	famset "github.com/hehehai/actnowkit/settings"
)

// TestSnapshotsFamilyShared renders the shared settings with Dicta's
// release build installed (thread #76, family spec §9.3, §9.4, §15.2
// step 5): Settings → General's language following the family ("Applies
// to all ActNow apps"), Settings → Network with Dicta's other proxy found
// at the migration ("Dicta uses a different proxy. Use theirs"), Settings
// → Models with the model folder kept for AudioSmith ("Only for this
// app"), the Models page's "Move models" banner, its confirmation and
// the move's progress; English and Chinese at 1100×720:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/shots go test -run TestSnapshotsFamilyShared .
func TestSnapshotsFamilyShared(t *testing.T) { runSnapshots(t, snapshotsFamilyShared) }

func snapshotsFamilyShared(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	useMemKeys(t)
	stubMachine(t, 16, true)
	for _, cfg := range []b2Config{
		{"1100-en", 1100, 720, 1, false, "en"},
		{"1100-zh", 1100, 720, 1, false, "zh-Hans"},
	} {
		a, _, tt := newTestApp(t)
		acceptLicences(t, a)
		i18n.SetLocale("en")
		a.updateSettings(func(s *settings.Settings) {
			s.General.Language = cfg.lang
			s.Models.Proxy = "socks5://127.0.0.1:1080"
		})
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
		tf := useFamily(t, a, dictaID)
		// Dicta set the language AudioSmith has, its own proxy and another
		// model folder before AudioSmith's first start with the spec.
		tf.familySet(t, famset.KeyLanguage, cfg.lang)
		tf.familySet(t, famset.KeyProxy, famset.ProxyDirect)
		tf.familySet(t, famset.KeyModelsRoot, "/Users/me/Models")
		useShared(t, tf)
		a.toasts = nil
		shoot("s01-general-scope", func() { a.openSettings("/general") })
		shoot("s02-network-other-value", func() { a.openSettings("/network") })
		shoot("s03-models-folder-own", func() { a.openSettings("/models") })
		a.settingsOpen = false
		tt.Frame()
		a.shared.Wait()
		shoot("s04-models-move-banner", func() { a.navigate("/models") })
		shoot("s05-move-confirm", func() {
			a.modelsMovePlan = modelsMove{From: modelCache(a.prefs), To: "/Users/me/Models", Value: "/Users/me/Models", Models: 9, Bytes: 7_400_000_000}
			a.modelsMoveAsk = true
		})
		a.modelsMoveAsk = false
		shoot("s06-moving", func() {
			a.modelsMoveJob = &jobs.Job{ID: "move", Spec: jobs.Spec{Kind: kindMoveModels, Title: i18n.T("Move models")}, State: jobs.StateRunning,
				Progress: jobs.Progress{Stage: "moving", Fraction: 0.42, Bytes: 3_100_000_000, TotalBytes: 7_400_000_000}}
		})
		a.modelsMoveJob = nil
	}
}
