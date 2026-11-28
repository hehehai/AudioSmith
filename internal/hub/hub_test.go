package hub

import (
	"path/filepath"
	"testing"
)

func TestCacheDir(t *testing.T) {
	// The family's library (spec v1.0.9), whatever HF_* say.
	t.Setenv("ACTNOW_FAMILY_DIR", "/fam")
	t.Setenv("HF_HOME", "/data/hf")
	t.Setenv("HF_HUB_CACHE", "/cache")
	if got := CacheDir(); got != filepath.Join("/fam", "models", "hub") {
		t.Errorf("CacheDir %q", got)
	}
	if got := RepoDir("/cache", "mlx-community/Kokoro-82M-bf16"); got != filepath.Join("/cache", "models--mlx-community--Kokoro-82M-bf16") {
		t.Errorf("RepoDir %q", got)
	}
}
