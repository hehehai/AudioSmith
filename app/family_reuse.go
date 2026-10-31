package main

// Reusing what other ActNow apps put in the model folder (family spec
// §7.3, §7.9, §7.10.1, §7.10.2; internal/family reuse.go, licences.go):
// the first run's reuse card, the Models page's "Shared with Dicta",
// "Installed · from Dicta" with [Use] and "Also loaded in Dicta", the
// claim of a model AudioSmith loads without claiming it, and licences
// accepted in another app. Other apps are named only when a release
// build of one is installed (famOthers, spec §7.10); the claims are
// written either way. The card's view is in onboarding_view.go.
//
// A model another app installed that AudioSmith does not claim (it was
// never chosen, or Models → Delete removed it from AudioSmith while
// Dicta keeps the files) stays usable: it is on disk. AudioSmith claims
// it (origin reused) when the user chooses [Use] or "Use them", and when
// the engine loads it: using a model is reusing it (§7.3 "when to add").

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// familyReuse is the app's P3 state (UI thread).
type familyReuse struct {
	// famShares: a catalogue repo (lower case) → whether AudioSmith claims
	// it and which named apps do; famLoaded: → the named apps holding it
	// loaded. Both nil unless another release app is installed.
	famShares map[string]family.Share
	famLoaded map[string][]string

	// The first run's reuse card: the survey of obSurveyFor (a pack id),
	// and what the user chose (reuseDefault: "Use them" when the setup
	// finishes).
	obSurvey    family.Survey
	obSurveyFor string
	obReuse     reuseChoice
	obReuseFor  string // the pack obReuse is about

	// licenceFrom names the app in which the user accepted the licence of
	// the dialog's model already, at licenceAt: the dialog says so and
	// asks to Continue.
	licenceFrom string
	licenceAt   time.Time
}

type reuseChoice int

const (
	reuseDefault reuseChoice = iota // nothing chosen: the models are used when the setup finishes
	reuseUsed                       // "Use them": claimed
	reuseChoose                     // "Choose models…": the Models page after the setup
)

// readShares fills v's per-model part for the model folder hub (on a
// goroutine).
func readShares(v *familyView, fam *family.Family, rt *family.Runtime, hub string) {
	entries := catalog.Builtin().Entries
	v.shares = fam.Shares(hub, entries)
	if rt == nil {
		return
	}
	v.loaded = map[string][]string{}
	for _, e := range entries {
		if names := rt.LoadedIn(e.Repo, e.Revision); len(names) > 0 {
			v.loaded[strings.ToLower(e.Repo)] = names
		}
	}
}

// familyShown runs after showFamily: the reuse card looks again (unless
// its models are claimed already: the card keeps saying so).
func (a *app) familyShown() {
	if a.obOpen && a.obReuse != reuseUsed {
		a.obSurveyFor = ""
	}
}

// refreshFamily reads the family's view again in the background (after
// AudioSmith claimed or deleted something).
func (a *app) refreshFamily() {
	fam, rt, hub := a.fam, a.famRT, modelCache(a.prefs)
	if fam == nil {
		return
	}
	a.async(func() {
		v := readFamilyView(fam, rt, hub)
		a.post(func() { a.showFamily(v) })
	})
}

// modelShare is what the Models page may say of e and the other apps.
func (a *app) modelShare(e catalog.Entry) (family.Share, []string) {
	key := strings.ToLower(e.Repo)
	return a.famShares[key], a.famLoaded[key]
}

// installedBy names the app that installed e when AudioSmith does not
// claim it ("Installed · from Dicta" with [Use]); "" otherwise.
func (a *app) installedBy(e catalog.Entry, m *modelState) string {
	sh, _ := a.modelShare(e)
	if m == nil || sh.Mine || len(sh.With) == 0 || m.downloading || m.local.State != hub.Downloaded {
		return ""
	}
	return sh.With[0]
}

// familyLine is the Models page's line under a model's facts: "Shared
// with Dicta", "Also loaded in Dicta"; "" when there is nothing to say.
func (a *app) familyLine(e catalog.Entry, m *modelState) string {
	sh, loaded := a.modelShare(e)
	var parts []string
	if sh.Mine && len(sh.With) > 0 && m.local.State == hub.Downloaded {
		parts = append(parts, i18n.T("Shared with %s", listJoin(sh.With)))
	}
	if len(loaded) > 0 {
		parts = append(parts, i18n.T("Also loaded in %s", listJoin(loaded)))
	}
	return strings.Join(parts, " · ")
}

// useModel claims e, which another app installed, for AudioSmith ([Use]
// on the Models page).
func (a *app) useModel(e catalog.Entry) {
	fam, hub := a.fam, modelCache(a.prefs)
	if fam == nil {
		return
	}
	a.async(func() {
		_, err := fam.Reuse(context.Background(), hub, family.EntryClaims(hub, e))
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not use %s: %v", modelRef(e), err))
				return
			}
			a.logs.Append(core.LogEntry{Source: "hub", Message: "using " + e.Repo + " (installed by another app)"})
			a.refreshFamily()
		})
	})
}

// claimInUse claims, for AudioSmith, the models the engine holds that it
// does not claim (family.RuntimeOptions.Loaded; any goroutine).
func (a *app) claimInUse(repos []string) {
	fam, lib := a.fam, a.lib
	if fam == nil {
		return
	}
	hub := modelCache(lib.settings.Get())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	claimed := 0
	for _, repo := range repos {
		e, ok := catalog.Builtin().ByRepo(repo)
		if !ok {
			e = catalog.Custom(repo)
		}
		n, err := fam.Reuse(ctx, hub, family.EntryClaims(hub, e))
		if err != nil {
			log.Printf("family: claim %s in use: %v", repo, err)
		}
		if n > 0 {
			log.Printf("family: claimed %s, which AudioSmith uses (origin reused)", repo)
		}
		claimed += n
	}
	if claimed > 0 {
		a.post(a.refreshFamily)
	}
}

// surveyPack looks, in the background, for the models of the pack id
// already in the model folder (the reuse card). The same pack is looked
// at again when the family changed (showFamily): the first look may come
// before discovery named the apps.
func (a *app) surveyPack(id string) {
	fam, hub := a.fam, modelCache(a.prefs)
	if id != a.obSurveyFor {
		a.obSurvey = family.Survey{}
	}
	a.obSurveyFor = id
	if id != a.obReuseFor {
		a.obReuse, a.obReuseFor = reuseDefault, id
	}
	if fam == nil || id == "" {
		return
	}
	entries := packEntries(id)
	a.async(func() {
		s := fam.Survey(hub, entries)
		a.post(func() {
			if a.obSurveyFor == id {
				a.obSurvey = s
			}
		})
	})
}

// reusePack claims for AudioSmith the models of the pack id already in
// the model folder (origin reused), then runs then (may be nil) on the
// main thread: "Use them", or the setup finishing with it as the default.
func (a *app) reusePack(id string, then func()) {
	fam, hub := a.fam, modelCache(a.prefs)
	if fam == nil {
		if then != nil {
			then()
		}
		return
	}
	entries := packEntries(id)
	a.async(func() {
		n := 0
		var errs []string
		for _, e := range entries {
			got, err := fam.Reuse(context.Background(), hub, family.EntryClaims(hub, e))
			n += got
			if err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			log.Printf("family: reuse: %s", strings.Join(errs, "; "))
		}
		if n > 0 {
			log.Printf("family: reusing %d repos of the %s pack already on this Mac", n, id)
		}
		a.post(func() {
			if then != nil {
				then()
			}
			a.refreshFamily()
		})
	})
}

// reuseCard is the first run's card over the models step (spec §7.10.1):
// the chosen pack's models already on this Mac, by whom, their size, what
// is still to download; "Use them" or "Choose models…". Only when
// another family app's release build is installed.
func (a *app) reuseCard(c *ui.Context) {
	s := a.obSurvey
	if len(a.famOthers) == 0 || a.obPack == "" || a.obSurveyFor != a.obPack || len(s.Models) == 0 {
		return
	}
	t := c.Theme()
	k := tok(t)
	_, missing, _ := a.packSizes(a.obPack)
	fillCard(c.Key("ob-reuse")).Padding(px(t, sp3), px(t, sp4)).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icons.CheckCircle.Bold).Size(px(t, 20), px(t, 20)).TextColor(k.Accent).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, sp1)).Children(func() {
				n := len(s.Models)
				ui.Text(c, i18n.N(n, "%d model is already on this Mac", "%d models are already on this Mac", n)).FontWeight(600)
				by := i18n.T("Already on this Mac · %s · no download needed", humanBytes(s.Bytes))
				if name := s.By(); name != "" {
					by = i18n.T("Installed by %s · %s · no download needed", name, humanBytes(s.Bytes))
				}
				ui.Text(c, by).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				var names []string
				for _, m := range s.Models {
					names = append(names, m.Entry.DisplayName())
				}
				ui.Text(c, joinNames(names)).FontSize(px(t, fsMicro)).TextColor(k.Text2)
				if missing > 0 {
					ui.Text(c, i18n.T("%s still to download", humanBytes(missing))).FontSize(px(t, fsMicro)).TextColor(k.Text2)
				}
			})
		})
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(0, 0, 0, px(t, 20+sp3)).Wrap().Children(func() {
			switch a.obReuse {
			case reuseUsed:
				statusMark(c, icons.CheckCircle, t.Success, i18n.T("In use: nothing to download for them"))
				return
			case reuseChoose:
				caption(c, i18n.T("Models lists them after the setup: use the ones you want."))
			}
			if secondaryButton(c.Key("ob-reuse-use"), i18n.T("Use them")).Clicked() {
				pack := a.obPack
				a.reusePack(pack, func() { a.obReuse = reuseUsed })
			}
			if a.obReuse != reuseChoose && ghostButton(c.Key("ob-reuse-choose"), i18n.T("Choose models…")).Clicked() {
				a.obReuse = reuseChoose
			}
		})
	})
}

// licenceFromFamily finds, in the background, whether the user accepted
// e's licence in another family app, then opens the dialog.
func (a *app) licenceFromFamily(e catalog.Entry, open func()) {
	fam := a.fam
	a.licenceFrom, a.licenceAt = "", time.Time{}
	if fam == nil || len(a.famOthers) == 0 {
		open()
		return
	}
	repo, id := e.Repo, e.Licence.ID
	a.async(func() {
		name, at := fam.LicenceAcceptedIn(repo, id)
		a.post(func() {
			a.licenceFrom, a.licenceAt = name, at
			open()
		})
	})
}

// recordLicence records the acceptance of e's licence for the family too
// (any goroutine).
func (a *app) recordLicence(e catalog.Entry) {
	if a.fam == nil {
		return
	}
	if err := a.fam.AcceptLicence(context.Background(), e.Repo, e.Licence.ID); err != nil {
		log.Printf("family: licence of %s: %v", e.Repo, err)
	}
}
