package main

// Settings → Pronunciation: the pronunciation dictionary (services.
// Pronunciation, kept in the library). Entries say how a term is read
// (respelling; IPA and CMU are kept and checked for engines with phoneme
// input), for every language or one; "Test a sentence" shows what the
// model will be given and renders it with the Studio's model and voice;
// JSON import (merge or replace) and export.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/text/lexicon"
	"github.com/hehehai/AudioSmith/internal/text/plan"
)

// pronState is the page's view state.
type pronState struct {
	entries []lexicon.Entry
	loaded  bool
	types   []string // per entry: the type's label (the Select's value)
	test    string   // the test sentence
	// The preview render of the test sentence.
	previewJob    string
	previewOutput string
	// Saves run in the background in order: the newest wins.
	gen     int
	saver   *pronSaver
	changed bool
}

// pronSaver saves the dictionary on a goroutine; a save older than one
// already written is skipped.
type pronSaver struct {
	mu    sync.Mutex
	saved int
}

func typeLabels() []string {
	return []string{i18n.T("Respelling"), i18n.T("IPA"), i18n.T("CMU (ARPAbet)")}
}

var entryTypes = []lexicon.Type{lexicon.Respelling, lexicon.IPA, lexicon.CMU}

// loadPronunciation reads the dictionary in the background.
func (a *app) loadPronunciation() {
	svc := a.lib.svc.Pronunciation
	a.background(func() {
		es, err := svc.Entries(context.Background())
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the pronunciation dictionary: %v", err))
				return
			}
			a.pron.entries, a.pron.loaded = es, true
			a.pron.types = nil
		})
	})
}

// savePronunciation saves the entries as they are now.
func (a *app) savePronunciation() {
	p := &a.pron
	if p.saver == nil {
		p.saver = &pronSaver{}
	}
	p.gen++
	gen, entries, saver, svc := p.gen, append([]lexicon.Entry(nil), p.entries...), p.saver, a.lib.svc.Pronunciation
	a.background(func() {
		saver.mu.Lock()
		defer saver.mu.Unlock()
		if gen <= saver.saved {
			return
		}
		if err := svc.Save(context.Background(), entries); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not save the pronunciation dictionary: %v", err)) })
			return
		}
		saver.saved = gen
	})
}

// issueText explains a dictionary entry's problem.
func issueText(is lexicon.Issue) string {
	switch is.Code {
	case lexicon.CodeEmptyTerm:
		return i18n.T("The term is empty.")
	case lexicon.CodeEmptyReplacement:
		return i18n.T("Say how to read it.")
	case lexicon.CodeDuplicate:
		return i18n.T("“%s” is in the dictionary twice for the same languages; the later entry wins.", is.Params["term"])
	case lexicon.CodeBadType:
		return i18n.T("Unknown type %s.", is.Params["type"])
	case lexicon.CodeBadIPA:
		return i18n.T("“%s” is not IPA.", is.Params["char"])
	case lexicon.CodeBadCMU:
		return i18n.T("“%s” is not an ARPAbet phoneme.", is.Params["phoneme"])
	}
	return is.Code
}

// pronLang is the language the test sentence is read in: the Studio's.
func (a *app) pronLang() string {
	return modelInfo(a.preset().Repo).Lang(a.language, a.voice)
}

// testedText is what the model would be given for the test sentence.
func (a *app) testedText() string {
	lang := a.pronLang()
	tr := a.traits()
	pl := plan.Build(a.pron.test, plan.Options{Lang: lang, NonVerbal: tr.NonVerbal, NonVerbalTags: tr.NonVerbalTags, Speed: tr.Speed,
		MaxChars: -1, Lexicon: lexicon.Compile(a.pron.entries, lang), NoNormalize: !a.normalize})
	var parts []string
	for _, s := range pl.Segments {
		switch s.Kind {
		case plan.Speech:
			parts = append(parts, s.Text)
		case plan.Silence:
			parts = append(parts, fmt.Sprintf("[%d ms]", s.Millis))
		}
	}
	return strings.Join(parts, " ")
}

// previewPronunciation renders the test sentence with the Studio's model
// and voice, without making a take, and plays it when it is ready.
func (a *app) previewPronunciation() {
	text := strings.TrimSpace(a.pron.test)
	if text == "" || a.engState != engineReady {
		return
	}
	r := a.recipe(time.Now())
	r.Text = text
	svc := a.lib.svc.Render
	a.pron.previewJob, a.pron.previewOutput = "pending", ""
	a.async(func() {
		job, out, err := svc.Preview(context.Background(), r)
		a.post(func() {
			if err != nil {
				a.pron.previewJob = ""
				var lic *core.LicenceRequiredError
				switch {
				case errors.As(err, &lic):
					a.askLicence(entryFor(r.Model), a.previewPronunciation)
				default:
					a.toastWarn(engineErrorText(err))
				}
				return
			}
			a.pron.previewJob, a.pron.previewOutput = job, out
		})
	})
}

// previewDone plays a finished preview of the test sentence.
func (a *app) previewDone(jobID string, ok bool, why string) {
	if jobID != a.pron.previewJob {
		return
	}
	out := a.pron.previewOutput
	a.pron.previewJob = ""
	if !ok {
		if why != "" {
			a.toast(why)
		}
		return
	}
	if err := a.pl.Toggle("preview:"+jobID, out); err != nil {
		a.toastWarn(i18n.T("Could not play: %v", err))
	}
}

// importPronunciation reads a dictionary file and merges it, or replaces
// the dictionary with it.
func (a *app) importPronunciation(mode lexicon.Mode) {
	svc := a.lib.svc.Pronunciation
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   i18n.T("Import a pronunciation dictionary"),
			Filters: []mygo.FileFilter{{Name: i18n.T("JSON"), Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		var rep lexicon.ImportReport
		if err == nil {
			rep, err = svc.Import(context.Background(), data, mode)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not import: %v", err))
				return
			}
			a.toast(i18n.T("Imported: %d added, %d updated, %d skipped", rep.Added, rep.Updated, rep.Skipped))
			a.loadPronunciation()
		})
	}()
}

// exportPronunciation saves the dictionary as JSON.
func (a *app) exportPronunciation() {
	svc := a.lib.svc.Pronunciation
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      a.win,
			Title:       i18n.T("Export the pronunciation dictionary"),
			DefaultPath: "pronunciation.json",
			Filters:     []mygo.FileFilter{{Name: i18n.T("JSON"), Extensions: []string{"json"}}},
		})
		if err != nil || dst == "" {
			return
		}
		data, err := svc.Export(context.Background())
		if err == nil {
			err = os.WriteFile(dst, data, 0o644)
		}
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Export failed: %v", err))
			} else {
				a.toast(i18n.T("Exported %s", dst))
			}
		})
	}()
}

func (a *app) pronunciationSettings(c *ui.Context) {
	a.revealAnchor(c, "pronunciation")
	t := c.Theme()
	p := &a.pron
	if !p.loaded {
		a.loadPronunciation()
	}
	subHeading(c, i18n.T("Pronunciation"))
	caption(c, i18n.T("Say how names, brands and abbreviations are read. In a script, [[word|say it like this]] changes one place only."))

	settingGroupBlock(c, i18n.T("Test a sentence"), "pron-test-block", func() {
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("pron-test"), &p.test).Placeholder(i18n.T("A sentence with your terms")).Label(i18n.T("Test sentence")).Grow(1)
			label := i18n.T("Listen")
			if p.previewJob != "" {
				label = i18n.T("Rendering…")
			}
			if secondaryButton(c.Key("pron-listen"), label).Disabled(strings.TrimSpace(p.test) == "" || p.previewJob != "" || a.engState != engineReady).
				Tooltip(i18n.T("Render it with the Studio's model and voice")).Clicked() {
				a.previewPronunciation()
			}
		})
		if strings.TrimSpace(p.test) != "" {
			ui.Text(c, i18n.T("The model reads: %s", a.testedText())).FontSize(px(t, 12)).Selectable()
		}

	})
	settingGroupBlock(c, i18n.T("Entries"), "pron-entries", func() {
		issues := map[int][]lexicon.Issue{}
		for _, is := range lexicon.Validate(p.entries) {
			issues[is.Index] = append(issues[is.Index], is)
		}
		labels := typeLabels()
		if len(p.types) != len(p.entries) {
			p.types = make([]string, len(p.entries))
			for i, e := range p.entries {
				p.types[i] = labels[0]
				for k, ty := range entryTypes {
					if e.Type == ty {
						p.types[i] = labels[k]
					}
				}
			}
		}
		changed := false
		if len(p.entries) == 0 {
			ui.Text(c, i18n.T("No entries yet.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
		} else {
			ui.Row(c).Gap(px(t, 8)).Children(func() {
				for _, h := range []struct {
					label string
					w     float32
				}{{i18n.T("Term"), 0}, {i18n.T("Say it as"), 0}, {i18n.T("Type"), 130}, {i18n.T("Languages"), 90}, {i18n.T("On"), 44}, {"", 40}} {
					txt := ui.Text(c, h.label).FontSize(px(t, 11)).Bold().TextColor(t.TextMuted)
					if h.w > 0 {
						txt.Width(px(t, h.w)).Shrink(0)
					} else {
						txt.Grow(1).MinWidth(0).Width(0)
					}
				}
			})
		}
		remove := -1
		for i := range p.entries {
			e := &p.entries[i]
			ui.Column(c.Key(fmt.Sprintf("pron-%d", i))).Gap(px(t, 2)).Children(func() {
				ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
					if ui.TextInput(c.Key("term"), &e.Term).Placeholder(i18n.T("Term")).Label(i18n.T("Term")).Grow(1).MinWidth(0).Width(0).Changed() {
						changed = true
					}
					if ui.TextInput(c.Key("say"), &e.Replacement).Placeholder(i18n.T("Say it as")).Label(i18n.T("Say it as")).Grow(1).MinWidth(0).Width(0).Changed() {
						changed = true
					}
					if ui.Select(c.Key("type"), &p.types[i], labels).Label(i18n.T("Type")).Width(px(t, 130)).Shrink(0).Changed() {
						for k, l := range labels {
							if p.types[i] == l {
								e.Type = entryTypes[k]
							}
						}
						changed = true
					}
					if ui.TextInput(c.Key("scope"), &e.Scope).Placeholder(lexicon.Global).Label(i18n.T("Languages")).Width(px(t, 90)).Shrink(0).
						Tooltip(i18n.T("“global”, or a language code such as en or zh")).Changed() {
						changed = true
					}
					if inkSwitch(c.Key("on"), &e.Enabled).Label(i18n.T("On")).Changed() {
						changed = true
					}
					if iconButton(c.Key("remove"), icons.Close, i18n.T("Remove")).Tooltip(i18n.T("Remove")).Shrink(0).Clicked() {
						remove = i
					}
				})
				for _, is := range issues[i] {
					ui.Text(c, issueText(is)).FontSize(px(t, 11)).TextColor(t.Warning)
				}
			})
		}
		if remove >= 0 {
			p.entries = append(p.entries[:remove:remove], p.entries[remove+1:]...)
			p.types = nil
			changed = true
		}
		ui.Row(c).Gap(px(t, 8)).Children(func() {
			if secondaryButton(c.Key("pron-add"), i18n.T("Add an entry")).Clicked() {
				p.entries = append(p.entries, lexicon.Entry{Type: lexicon.Respelling, Scope: lexicon.Global, Enabled: true})
				p.types = nil
				changed = true
			}
			ui.MenuButton(c.Key("pron-import"), i18n.T("Import…"), func(m *ui.Menu) {
				if m.Item(i18n.T("Add to the dictionary…")).Chosen() {
					a.importPronunciation(lexicon.Merge)
				}
				if m.Item(i18n.T("Replace the dictionary…")).Chosen() {
					a.importPronunciation(lexicon.Replace)
				}
			})
			if secondaryButton(c.Key("pron-export"), i18n.T("Export…")).Disabled(len(p.entries) == 0).Clicked() {
				a.exportPronunciation()
			}
		})
		if changed {
			a.savePronunciation()
		}
	})
}
