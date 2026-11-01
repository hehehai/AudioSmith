package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// TestShotsDubTransport pictures the Dub editor (thread #134): the
// generate step mid-run, and the export step once done.
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/s134 go test -run TestShotsDubTransport ./app
func TestShotsDubTransport(t *testing.T) {
	runSnapshots(t, func(t *testing.T, out string) {
		t.Cleanup(func() { i18n.SetLocale("en") })
		dark := gutterCfg{"1100-dark-zh", 1100, 720, 1, true, false, false}
		for _, cfg := range []gutterCfg{gutterWide, dark, gutterMin, gutterEn} {
			d := gutterSeed(t, cfg)
			a, tt := d.a, d.tt
			save := func(name string) {
				tt.Move(2, float32(cfg.h)-2)
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
			a.dubSeek(a.dub.Source.Duration * 0.4)
			a.dubStep = dubStepGenerate
			a.dubJobs["shot"] = jobs.Job{ID: "shot", Kind: services.KindDubSynthesize, State: jobs.StateRunning, Created: time.Now(),
				Params: []byte(`{"lang":"` + a.dub.Targets[0] + `"}`), Progress: jobs.Progress{Fraction: 0.5, Message: "3/6"}}
			save("generating")
			delete(a.dubJobs, "shot")
			a.dubGenerate()
			tt.Frame()
			tt.Advance(10e9) // the toasts go
			tt.Frame()
			a.dubStep = dubStepExport
			a.dubExport.last = "/tmp/x.mp4"
			save("finished")
			if cfg.name == gutterWide.name {
				// The step panel hidden: the whole transport row.
				if err := tt.Click(i18n.T("Hide the step settings")); err != nil {
					t.Fatal(err)
				}
				save("finished-nopanel")
			}
		}
	})
}
