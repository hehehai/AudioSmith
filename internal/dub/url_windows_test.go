package dub

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestYtDlpWindowsDiscoveryAndName(t *testing.T) {
	origOS := ytdlpOS
	t.Cleanup(func() { ytdlpOS = origOS })

	ytdlpOS = "darwin"
	if got := ytDlpBinName(); got != "yt-dlp" {
		t.Errorf("darwin ytDlpBinName() = %q, want yt-dlp", got)
	}

	ytdlpOS = "windows"
	if got := ytDlpBinName(); got != "yt-dlp.exe" {
		t.Errorf("windows ytDlpBinName() = %q, want yt-dlp.exe", got)
	}

	root := t.TempDir()
	winget := filepath.Join(root, "local", "Microsoft", "WinGet", "Links", "yt-dlp.exe")
	scoop := filepath.Join(root, "profile", "scoop", "shims", "yt-dlp.exe")
	for _, p := range []string{winget, scoop} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	t.Setenv("USERPROFILE", filepath.Join(root, "profile"))
	t.Setenv("SCOOP", "")

	found := FindSystemYtDlp()
	if !slices.Contains(found, winget) {
		t.Errorf("expected winget yt-dlp.exe %q in %v", winget, found)
	}
	if !slices.Contains(found, scoop) {
		t.Errorf("expected scoop yt-dlp.exe %q in %v", scoop, found)
	}
}
