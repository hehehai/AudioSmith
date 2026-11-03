package main

// Holds on the engine's models (#109): idle models are unloaded after the
// limit in Settings (30 minutes by default), on every worker; while a
// dictation records and transcribes, or a call is live, the models it
// works with stay resident (engine.Supervisor.Hold, HoldModels) even when
// it pauses longer than that between jobs. A dictation holds only its
// recogniser and the clean-up language model, so the gpu worker's other
// models still go for the idle limit, the budget or memory pressure.

import "github.com/hehehai/AudioSmith/internal/engine/proto"

// holdModels keeps the models of roles resident until release is called
// (once is enough; more calls do nothing).
func (a *app) holdModels(roles ...proto.Role) (release func()) {
	if a.eng == nil || a.eng.sup == nil {
		return func() {}
	}
	return a.eng.sup.Hold(roles...)
}

// holdModelsOf keeps models of role resident until release is called
// (once is enough), and none of role's other models (code review 1102 M1).
func (a *app) holdModelsOf(role proto.Role, models ...string) (release func()) {
	if a.eng == nil || a.eng.sup == nil {
		return func() {}
	}
	return a.eng.sup.HoldModels(role, models...)
}

// holdForCall holds the gpu (language and voice models) and listen
// (speech recognition) workers' models while call id is live. Main thread.
func (a *app) holdForCall(id string, live bool) {
	release, held := a.callHolds[id]
	switch {
	case live && !held:
		if a.callHolds == nil {
			a.callHolds = map[string]func(){}
		}
		a.callHolds[id] = a.holdModels(proto.RoleGPU, proto.RoleListen)
	case !live && held:
		release()
		delete(a.callHolds, id)
	}
}
