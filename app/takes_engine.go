package main

// The engine's executors as the library's renders ask for them: each
// chunk's tts.synthesize (useEngine), and the other gpu methods of
// renders that check what they made (useEngineFor: story lead-ins align
// and transcribe); split from takes.go.

import (
	"context"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// useEngine gives renders the engine's tts.synthesize executor; fn is
// asked as each render starts (the engine may connect later).
func (l *library) useEngine(fn func() jobs.Executor) {
	l.synthMu.Lock()
	l.synth = fn
	l.synthMu.Unlock()
}

// useEngineFor gives renders the engine's executors of other gpu methods
// (stt.align, stt.transcribe: story lead-ins).
func (l *library) useEngineFor(fn func(method string) jobs.Executor) {
	l.synthMu.Lock()
	l.engineFn = fn
	l.synthMu.Unlock()
}

func (l *library) engineExecutor(method string) jobs.Executor {
	l.synthMu.Lock()
	fn := l.engineFn
	l.synthMu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(method)
}

// modelRepo is the repo of a catalogue entry ("" when it has none).
func modelRepo(id string) string {
	e, _ := catalog.Builtin().ByID(id)
	return e.Repo
}

func (l *library) synthExecutor() jobs.Executor {
	l.synthMu.Lock()
	fn := l.synth
	l.synthMu.Unlock()
	var eng jobs.Executor
	if fn != nil {
		eng = fn()
	}
	if l.vv == nil {
		return eng
	}
	return externalRouter(eng, l.vv.Executor()) // voicevox_app.go
}

// auxSnapshots are the snapshot directories of the auxiliary repos of a
// model (engine.ExecutorOptions.AuxPaths).
func (l *library) auxSnapshots(ctx context.Context, repo string) map[string]string {
	return hub.AuxSnapshotsInFor(modelCaches(l.settings.Get()), currentOS(), entryFor(repo))
}

// modelReady is services.Config.Ready: the model is downloaded, or its
// outside engine answers (voicevox_app.go).
func (l *library) modelReady(ctx context.Context, repo string) error {
	if entryFor(repo).IsExternal() {
		return l.characterEngineReady(ctx)
	}
	_, err := l.modelSnapshot(ctx, repo)
	return err
}
