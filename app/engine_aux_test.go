package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// The renders' tts.synthesize calls carry the snapshots of the model's
// auxiliary repos (auxPaths), as the engine job kinds do: without them
// mlx-audio-swift resolved Kokoro's G2P by repo id and fetched the repo's
// main revision from huggingface.co into its own folder (thread #77, on
// the Mac). Transcripts, dub and workflow steps pass the same options.
func TestRendersSendAuxPaths(t *testing.T) {
	simulateDarwin(t)
	a, eng, _ := newTestApp(t)
	kokoro, _ := catalog.Builtin().ByID("kokoro-82m-bf16")
	params, _ := json.Marshal(proto.SynthesizeParams{Model: kokoro.Repo, ModelPath: "/snap", Text: "hi"})
	ex := a.lib.synthExecutor()
	if ex == nil {
		t.Fatal("no render executor")
	}
	if _, err := ex.Run(context.Background(), jobs.Job{ID: "j", Params: params}, func(jobs.Progress) {}, nil); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Aux map[string]string `json:"auxPaths"`
	}
	json.Unmarshal(eng.lastOf(t, proto.MethodTTSSynthesize).Params, &p)
	cache := modelCache(a.prefs)
	for _, x := range kokoro.Aux {
		want := filepath.Join(hub.RepoDir(cache, x.Repo), "snapshots", x.Revision)
		if p.Aux[x.Repo] != want {
			t.Errorf("auxPaths[%s] = %q, want %q", x.Repo, p.Aux[x.Repo], want)
		}
	}
}
