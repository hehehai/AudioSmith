package ffmpeg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFindSystemWindowsDiscovery(t *testing.T) {
	origOS := findOS
	t.Cleanup(func() { findOS = origOS })
	findOS = "windows"

	root := t.TempDir()
	wingetLink := filepath.Join(root, "local", "Microsoft", "WinGet", "Links", "ffmpeg.exe")
	scoopShim := filepath.Join(root, "scoop", "shims", "ffmpeg.exe")
	for _, p := range []string{wingetLink, scoopShim} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	t.Setenv("SCOOP", filepath.Join(root, "scoop"))
	t.Setenv("USERPROFILE", filepath.Join(root, "none"))
	t.Setenv("ProgramFiles", filepath.Join(root, "none"))

	found := FindSystem()
	if !slices.Contains(found, wingetLink) {
		t.Errorf("expected winget ffmpeg.exe %q in %v", wingetLink, found)
	}
	if !slices.Contains(found, scoopShim) {
		t.Errorf("expected scoop ffmpeg.exe %q in %v", scoopShim, found)
	}
}

func TestDefaultFontsPlatform(t *testing.T) {
	origOS := fontOS
	t.Cleanup(func() { fontOS = origOS })

	fontOS = "darwin"
	if got := DefaultFonts(); !slices.Equal(got, MacFonts) {
		t.Errorf("darwin DefaultFonts() = %v, want MacFonts", got)
	}

	fontOS = "windows"
	t.Setenv("WINDIR", `D:\Windows`)
	got := DefaultFonts()
	if len(got) != len(WinFonts) {
		t.Fatalf("windows DefaultFonts() len = %d, want %d", len(got), len(WinFonts))
	}
	if got[0] != `D:\Windows\Fonts\segoeuib.ttf` {
		t.Errorf("windows DefaultFonts()[0] = %q, want D:\\Windows\\Fonts\\segoeuib.ttf", got[0])
	}
	if !slices.Contains(got, `D:\Windows\Fonts\msyhbd.ttc`) || !slices.Contains(got, `D:\Windows\Fonts\YuGothB.ttc`) {
		t.Errorf("expected YaHei and Yu Gothic in Windows fonts: %v", got)
	}
}
