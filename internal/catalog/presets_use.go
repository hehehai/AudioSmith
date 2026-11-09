package catalog

// Recommendations by use (thread #167): models recommended on top of a
// language's list when the content calls for them — VoxCPM2 (Apache,
// commercial) before OmniVoice (non-commercial) for languages the
// language's models don't speak (Vietnamese, Thai, Arabic…), VoxCPM2 for
// a clone directed line by line (Stories) and for Chinese dialect design.
// They only mark models 推荐 / Recommended: defaults never come from them
// and no choice is switched by them.

import "slices"

// Use is a reason, beyond the content language, to recommend a model.
type Use string

const (
	// UseOtherLanguages: content in a language the recommended models
	// can't clone or design in.
	UseOtherLanguages Use = "otherLanguages"
	// UseDirectedClone: cloned voices that should follow a delivery
	// (Stories' scene, personas, directions and emotions).
	UseDirectedClone Use = "directedClone"
	// UseDialect: a designed voice in a Chinese dialect.
	UseDialect Use = "dialect"
)

// ForUse are the models presets recommend for use u in workflow w, best
// first (hidden ones left out).
func (c *Catalog) ForUse(u Use, w Workflow) []Entry {
	return c.entries(builtinPresets.RecommendByUse[string(u)][string(w)])
}

// RecommendedFor are workflow w's recommended models for the interface's
// language ui when the content is in langs ("" or "auto": unknown) and
// calls for uses: the language's list, made to speak every content
// language (presets_use: other languages), with the uses' models first;
// at most three.
func (c *Catalog) RecommendedFor(w Workflow, ui string, langs []string, uses ...Use) []Entry {
	out := c.coverLanguages(w, c.Recommended(w, ui), langs)
	var first []Entry
	for _, u := range uses {
		for _, e := range c.ForUse(u, w) {
			if speaksAll(e, langs) && !hasEntry(first, e.ID) {
				first = append(first, e)
			}
		}
	}
	out = append(first, slices.DeleteFunc(out, func(e Entry) bool { return hasEntry(first, e.ID) })...)
	return out[:min(len(out), 3)]
}

// coverLanguages is base when one of its models that clones or designs
// speaks each of langs; else base's models that speak them all followed
// by the models recommended for other languages that do.
func (c *Catalog) coverLanguages(w Workflow, base []Entry, langs []string) []Entry {
	covered := true
	for _, l := range langs {
		if !slices.ContainsFunc(base, func(e Entry) bool { return (e.Has(CapClone) || e.Has(CapDesign)) && e.Knows(l) }) {
			covered = false
		}
	}
	if covered {
		return base
	}
	var out []Entry
	for _, e := range append(slices.Clone(base), c.ForUse(UseOtherLanguages, w)...) {
		if speaksAll(e, langs) && !hasEntry(out, e.ID) {
			out = append(out, e)
		}
	}
	return out[:min(len(out), 3)]
}

func speaksAll(e Entry, langs []string) bool {
	for _, l := range langs {
		if !e.Knows(l) {
			return false
		}
	}
	return true
}

func hasEntry(es []Entry, id string) bool {
	return slices.ContainsFunc(es, func(e Entry) bool { return e.ID == id })
}
