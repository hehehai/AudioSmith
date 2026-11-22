package catalog

// Checking a catalogue: Catalog.Validate, and the repos and capabilities it
// knows; split from catalog.go.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/lang"
)

// Validate checks ids, repos and labels are unique and set, tasks,
// capabilities and language codes (internal/lang) are known, and every
// entry has a licence.
func (c *Catalog) Validate() error {
	if c.Version < 1 {
		return fmt.Errorf("version %d", c.Version)
	}
	seen := map[string]bool{}
	for i, e := range append(slices.Clone(c.Entries), c.External...) {
		if external := i >= len(c.Entries); external != e.IsExternal() {
			return fmt.Errorf("entry %q: a runtime belongs to the external entries, and they need one", e.ID)
		}
		if e.ID == "" || e.Repo == "" || e.Label == "" || e.Family == "" || e.Name == "" {
			return fmt.Errorf("entry %q: id, label, name, family and repo are required", e.ID)
		}
		for _, k := range []string{"id:" + e.ID, "repo:" + e.Repo, "label:" + e.Label, "name:" + strings.ToLower(e.Name)} {
			if seen[k] {
				return fmt.Errorf("duplicate %s", k)
			}
			seen[k] = true
		}
		if len(e.Tasks) == 0 {
			return fmt.Errorf("%s: no tasks", e.ID)
		}
		for i, tg := range e.Tags {
			if !slices.Contains(staticTags, tg) || slices.Index(e.Tags, tg) != i {
				return fmt.Errorf("%s: tag %q is unknown, computed or repeated", e.ID, tg)
			}
		}
		for _, t := range e.Tasks {
			if !slices.Contains(knownTasks, t) {
				return fmt.Errorf("%s: unknown task %q", e.ID, t)
			}
		}
		for _, cp := range e.Capabilities {
			if !slices.Contains(knownCaps, cp) {
				return fmt.Errorf("%s: unknown capability %q", e.ID, cp)
			}
		}
		if e.NeedsReference && (!e.Has(CapClone) || !slices.Contains(e.Tasks, TaskTTS)) {
			return fmt.Errorf("%s: needsReference without the clone capability", e.ID)
		}
		if e.Licence.ID == "" || e.Licence.Name == "" {
			return fmt.Errorf("%s: no licence", e.ID)
		}
		if !e.Licence.Commercial && !e.Licence.RequiresAcceptance {
			return fmt.Errorf("%s: a non-commercial licence must require acceptance", e.ID)
		}
		if e.Label == CustomLabel {
			return fmt.Errorf("%s: label %q is reserved", e.ID, CustomLabel)
		}
		for _, a := range e.Aux {
			if a.Repo == "" || a.Repo == e.Repo {
				return fmt.Errorf("%s: bad aux repo %q", e.ID, a.Repo)
			}
		}
		for _, l := range e.Languages {
			if lang.Canonical(l.Code) == "" {
				return fmt.Errorf("%s: unknown language code %q", e.ID, l.Code)
			}
		}
	}
	for _, r := range c.Removed {
		if r.ID == "" || r.Repo == "" || r.Label == "" {
			return fmt.Errorf("removed %q: id, label and repo are required", r.ID)
		}
		for _, k := range []string{"id:" + r.ID, "repo:" + r.Repo, "label:" + r.Label} {
			if seen[k] {
				return fmt.Errorf("removed: duplicate %s", k)
			}
			seen[k] = true
		}
		for i, by := range []string{r.ReplacedBy, r.CloneReplacedBy} {
			if i == 1 && by == "" {
				continue // no other replacement for clones
			}
			if n, ok := c.ByID(by); !ok || n.Hidden || !slices.Contains(n.Tasks, r.Did()) {
				return fmt.Errorf("removed %s: no live %s model replaces it (%q)", r.ID, r.Did(), by)
			}
		}
		if !slices.Contains(knownTasks, r.Did()) {
			return fmt.Errorf("removed %s: unknown task %q", r.ID, r.Task)
		}
		if n, _ := c.ByID(r.CloneReplacedBy); r.CloneReplacedBy != "" && !n.Has(CapClone) {
			return fmt.Errorf("removed %s: %s can't clone", r.ID, r.CloneReplacedBy)
		}
		for _, a := range r.Aux {
			if c.UsesRepo(a) {
				return fmt.Errorf("removed %s: aux repo %s is still used", r.ID, a)
			}
		}
	}
	return nil
}

// UsesRepo reports whether a catalogue entry downloads repo, as its own
// or an auxiliary repo.
func (c *Catalog) UsesRepo(repo string) bool {
	for _, e := range c.Entries {
		if e.Repo == repo || slices.ContainsFunc(e.Aux, func(a Aux) bool { return a.Repo == repo }) {
			return true
		}
	}
	return false
}

var knownCaps = []Capability{CapSpeed, CapSampling, CapClone, CapVoiceStyle, CapLanguage, CapInstruct, CapDesign, CapSeed, CapDuration, CapStream, CapWordTimes,
	CapVoicePrompt, CapSteps, CapGuidance, CapNonVerbal, CapTranslate, CapSpeakers, CapDialogue, CapCloneAudioOnly}
