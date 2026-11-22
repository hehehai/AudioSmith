package main

// The family apps' models in the app (models_family.go): Settings →
// Storage → Erase everything with the models keeps what Dicta uses, and
// Models → Clean up lists the removed models it keeps.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/actnowkit/models"
)

// fakeHubRepo lays out a repo in a Hugging Face cache: a snapshot with
// one file linked to a blob of size bytes.
func fakeHubRepo(t *testing.T, cache, repo string, size int) {
	t.Helper()
	dir := hub.RepoDir(cache, repo)
	snap := filepath.Join(dir, "snapshots", strings.Repeat("1", 40))
	for _, d := range []string{filepath.Join(dir, "blobs"), snap} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", "b10b"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../blobs/b10b", filepath.Join(snap, "model.safetensors")); err != nil {
		t.Fatal(err)
	}
}

// TestRemovedKeptListed: Clean up's confirmation lists a removed model
// Dicta uses as kept (when it may name it), and does not count it in what
// it frees.
func TestRemovedKeptListed(t *testing.T) {
	i18n.SetLocale("en")
	a := &app{}
	a.removedLeft = []hub.Leftover{
		{Repo: "mlx-community/gemma-4-e4b-it-4bit", Bytes: 5000, InUse: true, UsedBy: []string{"Dicta"}, Users: []models.User{{BundleID: dictaID}}},
		{Repo: "mlx-community/parakeet-tdt-0.6b-v3", Bytes: 300},
	}
	// Dicta is not named while no release build of another app is.
	if _, _, kept := a.removedOnDisk(); len(kept) != 0 {
		t.Errorf("kept named without an installed app: %q", kept)
	}
	a.famOthers = []string{"Dicta"}
	if n := a.removedBytes(); n != 300 {
		t.Errorf("bytes %d", n)
	}
	names, bytes, kept := a.removedOnDisk()
	if len(names) != 1 || bytes[0] != 300 || !strings.Contains(names[0], "Parakeet") {
		t.Errorf("to clean %v %v", names, bytes)
	}
	if !slices.Equal(kept, []string{"Gemma 4 E4B · Kept: shared across ActNow"}) {
		t.Errorf("kept %q", kept)
	}
	i18n.SetLocale("zh-Hans")
	defer i18n.SetLocale("en")
	if _, _, kept := a.removedOnDisk(); !slices.Equal(kept, []string{"Gemma 4 E4B · 已保留：ActNow 共用"}) {
		t.Errorf("zh kept %q", kept)
	}
}

// TestSharedText: why the files of a model Dicta uses stay.
func TestSharedText(t *testing.T) {
	i18n.SetLocale("en")
	if got := sharedText([]string{"Dicta"}, 712_000_000); got != "Dicta also uses this model, so its files (712 MB) stay on this Mac." {
		t.Errorf("en %q", got)
	}
	i18n.SetLocale("zh-Hans")
	defer i18n.SetLocale("en")
	if got := sharedText([]string{"Dicta", "VoxtNew"}, 712_000_000); got != "Dicta、VoxtNew 也在使用这个模型，文件（712 MB）会保留在这台 Mac 上。" {
		t.Errorf("zh %q", got)
	}
}
