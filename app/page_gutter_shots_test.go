package main

// Review images of the page gutter (#130): every page with a header, two
// red lines down the window at the header's content edges (its title's
// left, its last tool's right), Chinese at 1100×720, and three pages dark
// and at 820×520 @125 %. Not compared with anything:
//
//	AUDIOSMITH_SNAPSHOTS=/tmp/c130 go test -run TestSnapshotsGutter ./app

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/i18n"
)

func TestSnapshotsGutter(t *testing.T) { runSnapshots(t, snapshotsGutter) }

func snapshotsGutter(t *testing.T, out string) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	few := []string{"projects", "dub-editor", "discover"}
	for _, cfg := range []gutterCfg{gutterWide, {"1100-zh-dark", 1100, 720, 1, true, false}, gutterMin} {
		d := gutterSeed(t, cfg)
		d.tt.SetScale(2)
		for _, p := range gutterPages {
			if cfg != gutterWide && !slices.Contains(few, p.name) {
				continue
			}
			p.open(d)
			g := measureGutter(d.tt, d.a)
			img := d.tt.Image()
			k := float32(img.Bounds().Dx()) / float32(cfg.w)
			guide(img, int(g.headL*k+0.5), int((g.head.Y+g.head.H)*k))
			guide(img, int(g.headR*k+0.5)-1, int((g.head.Y+g.head.H)*k))
			f, err := os.Create(filepath.Join(out, p.name+"-"+cfg.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// guide draws a red line 2 pixels wide down img at x, from the top.
func guide(img *image.RGBA, x, _ int) {
	red := color.RGBA{230, 30, 30, 255}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		img.SetRGBA(x, y, red)
		img.SetRGBA(x+1, y, red)
	}
}
