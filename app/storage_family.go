package main

// Settings → Storage's part of the ActNow family (spec §7.6 steps 4, 6;
// internal/family storage.go): the space of AudioSmith's models only it
// uses and shared with other apps (named only as spec §7.10 allows), and
// the models only uninstalled apps used, with their clean-up.

import (
	"context"
	"log"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/actnowkit/models"
)

// measureFamily fills r's family part for the model folder dir (on
// the measuring goroutine).
func (a *app) measureFamily(r *storageReport, dir string) {
	fam := a.fam
	if fam == nil {
		return
	}
	var err error
	if r.usage, err = fam.Usage(dir); err != nil {
		log.Printf("storage: family usage: %v", err)
	}
	orphans, err := fam.Orphans(dir)
	if err != nil {
		log.Printf("storage: orphans: %v", err)
	}
	r.orphans, r.orphanBytes = orphanCount(orphans)
}

// orphanCount counts the models of orphans (each once) and what cleaning
// them up frees.
func orphanCount(orphans []models.Orphan) (n int, bytes int64) {
	seen := map[models.Ref]bool{}
	for _, o := range orphans {
		bytes += o.Bytes
		for _, c := range o.Claims {
			if !seen[c.Ref()] {
				seen[c.Ref()] = true
				n++
			}
		}
	}
	return n, bytes
}

// familyStorage shows r's family part under the model folder's.
func (a *app) familyStorage(c *ui.Context, r *storageReport) {
	if u := r.usage; len(a.famOthers) > 0 && u.Shared > 0 && len(u.SharedWith) > 0 {
		settingNote(c, "st-shared", i18n.T("Only AudioSmith: %s · Shared with %s: %s", humanBytes(u.Mine), listJoin(u.SharedWith), humanBytes(u.Shared)))
	}
	if r.orphans == 0 {
		return
	}
	settingRow(c, "st-orphans", i18n.N(r.orphans, "%d model (%s) is only used by apps that are no longer installed.",
		"%d models (%s) are only used by apps that are no longer installed.", r.orphans, humanBytes(r.orphanBytes)), "", func() {
		if secondaryButton(c.Key("st-orphans-clean"), i18n.T("Clean up")).Disabled(a.stOrphansBusy).Clicked() {
			a.cleanOrphans()
		}
	})
}

// cleanOrphans deletes what only uninstalled apps used, then measures
// again.
func (a *app) cleanOrphans() {
	fam, dir := a.fam, modelCache(a.prefs)
	if fam == nil || a.stOrphansBusy {
		return
	}
	a.stOrphansBusy = true
	a.background(func() {
		freed, err := fam.CleanOrphans(context.Background(), dir)
		a.post(func() {
			a.stOrphansBusy = false
			if err != nil {
				log.Printf("storage: clean up orphans: %v", err)
				if name, busy := hub.Holder(err); busy {
					a.toastWarn(busyText(name))
				}
			}
			a.toast(i18n.T("Freed %s", mb(freed)))
			a.stReport = nil
			a.refreshModels()
		})
	})
}
