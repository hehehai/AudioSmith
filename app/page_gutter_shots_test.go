package main

// Review images of the page gutter (#130): every page with a header, two
// red lines down the window at the header's content edges (its title's
// left, its last tool's right; before a tool panel in its lane, the
// second at laneGap before it) and a blue one across at the body's top
// (bodyTop under the header), Chinese at 1100×720, and three pages dark
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
	few := []string{"projects", "new-voice", "story-new"}
	for _, cfg := range []gutterCfg{gutterWide, {"1100-zh-dark", 1100, 720, 1, true, false, false}, gutterMin} {
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
			red, blue := color.RGBA{230, 30, 30, 255}, color.RGBA{30, 110, 230, 255}
			guide(img, int(g.headL*k+0.5), red)
			guide(img, int(g.headR*k+0.5)-1, red)
			if g.panel.W > 0 && !g.over {
				guide(img, int(g.wantR()*k+0.5)-1, red)
			}
			across(img, int(g.head.X*k), int(g.wantT()*k+0.5), blue)
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

// guide draws a line 2 pixels wide down img at x.
func guide(img *image.RGBA, x int, col color.RGBA) {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		img.SetRGBA(x, y, col)
		img.SetRGBA(x+1, y, col)
	}
}

// across draws a dashed line 1 pixel high across img at y, from x0.
func across(img *image.RGBA, x0, y int, col color.RGBA) {
	for x := x0; x < img.Bounds().Max.X; x++ {
		if x/6%2 == 0 {
			img.SetRGBA(x, y, col)
		}
	}
}
