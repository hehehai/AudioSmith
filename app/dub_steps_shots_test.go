package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/i18n"
)

// TestShotsDubSteps pictures the Dub editor's header (thread #133):
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/s133 go test -run TestShotsDubSteps ./app
func TestShotsDubSteps(t *testing.T) {
	runSnapshots(t, func(t *testing.T, out string) {
		t.Cleanup(func() { i18n.SetLocale("en") })
		dark := gutterCfg{"1100-dark-zh", 1100, 720, 1, true, false, false}
		minEn := gutterCfg{"min125-en", 820, 520, 1.25, false, false, true}
		for _, cfg := range []gutterCfg{gutterWide, dark, gutterMin, gutterEn, minEn} {
			d := gutterSeed(t, cfg)
			a, tt := d.a, d.tt
			save := func(name string) {
				tt.Frame()
				tt.Frame()
				f, err := os.Create(filepath.Join(out, cfg.name+"-"+name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
			a.navigate("/dub")
			a.openDub(a.dubList[0].ID)
			tt.Frame()
			a.dubTranslate(false)
			tt.Frame()
			a.dubStep = dubStepCast
			tt.Move(2, float32(cfg.h)-2)
			save("midway")
			if r, ok := tt.Find(i18n.T("Step %d: %s", 2, i18n.T("Transcribe"))); ok && cfg.name == gutterWide.name {
				tt.Move(r.X+r.W/2, r.Y+r.H/2)
				tt.Frame()
				tt.Advance(2e9)
				save("hover")
				tt.Move(2, float32(cfg.h)-2)
			}
			a.dubGenerate()
			tt.Frame()
			tt.Advance(10e9) // the toasts go
			tt.Frame()
			a.dubStep = dubStepExport
			a.dubExport.last = "/tmp/x.mp4"
			save("finished")
		}
	})
}
