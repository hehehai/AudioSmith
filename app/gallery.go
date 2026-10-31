package main

// Discover voices (/voices/discover; the view is gallery_view.go): the curated voices (internal/gallery,
// thread #94) to search, filter by use case, gender, age and language,
// favorite, preview and use. Previews are bundled: a designed voice's
// clip (gallery_assets.go), a built-in speaker's (voice_preview.go);
// nothing is rendered to listen.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// galleryPageSize is how many voices a page shows (all of them).
const galleryPageSize = gallery.DefaultLimit

// galleryState is the Gallery page's view state.
type galleryState struct {
	text    string
	tab     int // 0 = all use cases, i = gallery.Categories()[i-1]
	filters map[gallery.Facet]string
	favOnly bool
	limit   int

	page    gallery.Page
	pageFor string // the query page answers
	favVer  int    // bumps when favorites change
	// detail is the voice whose detail is open ("" none); try its 「试一句」.
	detail string
	try    voiceTry

	favs  *gallery.Favorites
	using bool
}

// openGallery loads the favorites.
func (a *app) openGallery(lib *library) {
	g := &a.gallery
	g.filters = map[gallery.Facet]string{}
	g.limit = galleryPageSize
	g.favs, _ = gallery.LoadFavorites(context.Background(), nil)
	store := services.GalleryFavorites{Store: lib.store.Favorites()}
	a.background(func() {
		favs, err := gallery.LoadFavorites(context.Background(), store)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the favorites: %v", err))
			}
			g.favs = favs
			g.favVer++
		})
	})
}

// galleryQuery is the page's query.
func (a *app) galleryQuery() gallery.Query {
	g := &a.gallery
	cat := ""
	if cats := gallery.Categories(); g.tab > 0 && g.tab <= len(cats) {
		cat = cats[g.tab-1].ID
	}
	q := galleryFilterQuery(g.text, cat, g.filters)
	q.Limit, q.FavoritesOnly, q.Favorites = g.limit, g.favOnly, g.favs
	return q
}

// galleryFilterQuery is the gallery's query for a search text, a use
// case and facet filters: the page's and the API's search_voices.
func galleryFilterQuery(text, category string, f map[gallery.Facet]string) gallery.Query {
	return gallery.Query{Text: text, Category: category,
		Gender: f[gallery.FacetGender], Age: f[gallery.FacetAge], Language: f[gallery.FacetLanguage]}
}

// galleryResults searches when the query changed (in memory).
func (a *app) galleryResults() gallery.Page {
	g := &a.gallery
	q := a.galleryQuery()
	key := fmt.Sprintf("%q|%s|%d|%v|%v|%d", q.Text, q.Category, q.Limit, g.filters, g.favOnly, g.favVer)
	if key != g.pageFor {
		g.page, g.pageFor = gallery.Default().Search(q), key
	}
	return g.page
}

// toggleFavorite stars or unstars an archetype.
func (a *app) toggleFavorite(id string) {
	g := &a.gallery
	favs := g.favs
	a.background(func() {
		_, err := favs.Toggle(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not save the favorite: %v", err))
			}
			g.favVer++
		})
	})
}

// archetypePreview is what a gallery voice's Preview plays: a designed
// voice's bundled clip, a built-in speaker's; none for the earlier
// gallery's voices.
func archetypePreview(arch gallery.Archetype) voicePreview {
	switch arch.Kind {
	case gallery.KindDesigned:
		if b := galleryClip(arch.ID, "preview"); b != nil {
			return voicePreview{id: "vp:gallery:" + arch.ID, data: b}
		}
	case gallery.KindBuiltin:
		if e, ok := catalog.Builtin().ByID(arch.Model); ok {
			return speakerPreview(e, arch.Speaker)
		}
	}
	return voicePreview{}
}

// previewArchetype plays (or stops) a gallery voice's preview.
func (a *app) previewArchetype(arch gallery.Archetype) { a.togglePreview(archetypePreview(arch)) }

// useArchetype saves a gallery voice to the voices (once: a second use
// finds the voice it saved) and opens it in Design (a designed voice) or
// the Studio (a built-in speaker, or when asked).
func (a *app) useArchetype(arch gallery.Archetype, inStudio bool) {
	done := func(v *core.Voice) {
		if inStudio || v.Kind != core.VoiceDesigned {
			a.useVoiceInStudio(v)
		} else {
			a.openDesignFor(v)
		}
	}
	a.useArchetypeThen(arch, done)
}

// useArchetypeThen saves a gallery voice (once) and hands it to done (M3:
// Use in Stories, Narrate an audiobook).
func (a *app) useArchetypeThen(arch gallery.Archetype, done func(*core.Voice)) {
	g := &a.gallery
	origin := galleryOrigin(arch.ID)
	for i := range a.voices {
		if a.voices[i].Origin == origin {
			done(&a.voices[i])
			return
		}
	}
	if g.using {
		return
	}
	g.using = true
	svc := a.lib.svc.Voices
	tmp := a.lib.store.Dir()
	loc := i18n.Locale()
	a.background(func() {
		saved, err := saveArchetype(context.Background(), svc, arch, loc, tmp)
		a.post(func() {
			g.using = false
			if err != nil {
				a.toastWarn(i18n.T("Could not create that voice: %v", err))
				return
			}
			a.voiceChanged(core.VoiceChanged{Voice: saved})
			a.toast(i18n.T("Saved “%s” to your voices", saved.Name))
			if v := a.findVoice(saved.ID); v != nil {
				done(v)
			}
		})
	})
}

// voiceSaver saves a voice with its files (core.VoiceService's SaveFiles).
type voiceSaver interface {
	SaveFiles(ctx context.Context, v core.Voice, files core.VoiceFiles) (core.Voice, error)
}

// saveArchetype saves gallery voice arch as "Use voice" does: a curated
// designed voice with its bundled reference and preview, decoded into
// tmpDir first; the others as archetypeVoice makes them. The page's and
// the API's use_gallery_voice.
func saveArchetype(ctx context.Context, svc voiceSaver, arch gallery.Archetype, loc, tmpDir string) (core.Voice, error) {
	v := archetypeVoice(arch, loc)
	var files core.VoiceFiles
	if arch.Kind == gallery.KindDesigned {
		dir, err := os.MkdirTemp(tmpDir, "gallery-*")
		if err != nil {
			return core.Voice{}, err
		}
		defer os.RemoveAll(dir)
		for _, f := range []struct {
			kind string
			dst  *string
		}{{"ref", &files.Reference}, {"preview", &files.Sample}} {
			b := galleryClip(arch.ID, f.kind)
			if b == nil {
				return core.Voice{}, fmt.Errorf("no bundled %s for %s", f.kind, arch.ID)
			}
			p := filepath.Join(dir, f.kind+".wav")
			secs, err := decodeBundled(b, p)
			if err != nil {
				return core.Voice{}, err
			}
			if f.kind == "ref" {
				v.RefSeconds = secs
			}
			*f.dst = p
		}
	}
	return svc.SaveFiles(ctx, v, files)
}

// archetypeVoice is the voice "Use voice" saves for a gallery voice, in
// the UI's language: a built-in speaker's profile (its model and speaker);
// a curated designed voice with its persona, seed and the bundled
// reference's text (heard saying it when it was made, so it is not heard
// again), made with 1.7B VoiceDesign; an earlier gallery voice with its
// attributes and seed (its reference is made on first use).
func archetypeVoice(arch gallery.Archetype, loc string) core.Voice {
	v := core.Voice{Name: arch.Name.In(loc), Language: arch.Languages[0], Origin: galleryOrigin(arch.ID), Tags: arch.Tags}
	if arch.Kind == gallery.KindBuiltin {
		e, _ := catalog.Builtin().ByID(arch.Model)
		v.Kind, v.Model, v.Speaker = core.VoiceBuiltin, e.Repo, arch.Speaker
		return v
	}
	r := arch.Recipe()
	attrs, _ := json.Marshal(r.Attrs)
	v.Kind, v.Design, v.DesignAttrs, v.Seed = core.VoiceDesigned, r.Description, attrs, r.Seed
	if arch.Kind == gallery.KindDesigned {
		e, _ := catalog.Builtin().ByID(galleryDesignModel)
		v.CreatedWith = e.Repo
		v.RefText = arch.RefText
		v.RefCheck = &core.RefQuality{Grade: arch.RefGrade, Cleaned: []string{"tidied"}, TextChecked: true}
		v.SampleText, _ = arch.PreviewLine()
	}
	return v
}

// galleryDesignModel made the curated voices' references (and makes them
// again when asked).
const galleryDesignModel = "qwen3-tts-1.7b-voicedesign-4bit"

// galleryOrigin is the origin of a voice saved from a gallery voice.
func galleryOrigin(id string) string { return "gallery:" + id }
