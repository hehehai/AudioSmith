package main

// Transcribe (/transcripts, M2): speech to text from a file (any audio or
// video) or a recording, Fast (a smaller model, no word timings) or
// Accurate (the default speech-to-text model, then the forced aligner for
// word timings), in a language picked or detected. Transcripts are kept
// in the library (services.Transcripts over migration 003's table) with
// their 16 kHz audio, so a click on a word seeks the player there. Edit a
// sentence, clean the text up (deterministic polish, or an LLM:
// transcribe_llm.go), export TXT, SRT, VTT or JSON, copy, or reuse it as
// the Studio's script. Dictations (dictation.go) land here too.
//
// Files of the area:
//
//	transcribe.go                this: state, the list, edits of the transcript shown
//	transcribe_models.go         models: speech to text, languages, speakers, the aligner
//	transcribe_run.go            transcribing: files, drops, queueing, job updates, cancel
//	transcribe_export.go         exports (TXT, SRT, VTT, JSON) and reuse in the Studio
//	transcribe_record.go         playback and the recorder
//	transcribe_view.go           the page and its toolbar (model, language, speakers)
//	transcribe_view_history.go   the history list, the status, sources and the recorder
//	transcribe_view_detail.go    the open transcript: exports, player, clean-up
//	transcribe_view_segments.go  the segments and the speakers bar
//	transcribe_llm.go            language models: routes, clean-up
//	transcribe_llm_settings.go   Settings → Writing help

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/translate"
)

// transcribeState is the Transcribe page's view state.
type transcribeState struct {
	trSvc *services.Transcripts

	trList    []core.Transcript // the history, newest first, without word timings
	trLoaded  bool
	trQuery   string
	trFound   []core.Transcript // search results; nil when not searching
	trSearch  int               // bumps with each search: older answers are dropped
	trSel     string            // the transcript shown; "" = the new-transcript panel
	trDetail  *core.Transcript  // the whole transcript shown (word timings)
	trJob     *trJob            // the transcription running
	trErr     string
	trRetry   *services.TranscribeRequest
	trNeed    *catalog.Entry // a model the last attempt found missing
	trWarn    string         // about the transcript shown (word timings missing…)
	trMic     micState
	trMode    int // 0 fast, 1 accurate (bound to the Segmented)
	trLang    trLangPicker
	trEdit    int    // segment being edited; -1 none
	trEditBuf string // its text
	trOrig    bool   // the original shows under the clean-up
	trClean   *trCleaning
	trExport  bool   // the export menu is open
	trConfirm string // id the delete confirmation asks about
	trDevice  string // the recorder's device label (bound to its Select)
	trShown   int    // segments shown (long transcripts show more on demand)
	trPend    string // the composer's file ("" none)
	trNarrow  bool   // the page is narrow: actions show as icons
	trTight   bool   // the header is short of room: its buttons show icons
	trRename  string // the speaker label being renamed ("" none)
	trRenOpen bool   // its popover is open
	trRenBuf  string // the name typed
}

// trPage is how many more segments "Show more" shows.
const trPage = 200

// trJob is the transcription the page follows.
type trJob struct {
	id, owner, trID string
	title           string
	stage           string
	fraction        float64
	started         time.Time
	cancelling      bool
}

// trCleaning is a clean-up of the transcript shown, running.
type trCleaning struct {
	id     string
	cancel context.CancelFunc
}

type trLangPicker struct {
	open  bool
	query string
	list  ui.ListState
}

// openTranscripts makes the transcript service over the library (its
// executor on the scheduler; engine calls through the supervisor that
// connectEngine makes) and follows its events and jobs.
func (a *app) openTranscripts(lib *library) {
	a.trEdit = -1
	a.trMode = 1
	if a.prefs.Transcribe.Mode == settings.TranscribeFast {
		a.trMode = 0
	}
	a.trSvc = services.NewTranscripts(services.TranscriptsConfig{
		Store: lib.store.Transcripts(), Bus: lib.bus, Scheduler: lib.sched, Licences: lib.licences,
		ModelPath: lib.modelSnapshot,
		Engine: func(method string) jobs.Executor {
			if a.eng == nil || a.eng.sup == nil {
				return nil
			}
			return engine.KindExecutor(a.eng.sup, method, engine.ExecutorOptions{AuxPaths: lib.auxSnapshots}) // a method, or "listen:stt.transcribe"
		},
	})
	core.On(lib.bus, func(ev core.TranscriptChanged) { a.post(func() { a.transcriptChanged(ev) }) })
	core.On(lib.bus, func(u jobs.Updated) {
		if u.Chunk == nil && strings.HasPrefix(u.Job.Owner, services.TranscriptOwner) {
			a.post(func() { a.transcriptJobUpdated(u.Job) })
		}
	})
	a.loadTranscripts()
}

// loadTranscripts reads the history in the background.
func (a *app) loadTranscripts() {
	svc := a.trSvc
	a.background(func() {
		l, err := svc.List(context.Background(), core.TranscriptQuery{})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the transcripts: %v", err))
				return
			}
			for _, t := range a.trList { // added by events while reading
				if !slices.ContainsFunc(l, func(x core.Transcript) bool { return x.ID == t.ID }) {
					l = append(l, t)
				}
			}
			sortTranscripts(l)
			a.trList, a.trLoaded = l, true
		})
	})
}

func sortTranscripts(l []core.Transcript) {
	slices.SortStableFunc(l, func(x, y core.Transcript) int {
		if c := y.CreatedAt.Compare(x.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(y.ID, x.ID)
	})
}

// briefTranscript is t without word timings, as the list keeps it.
func briefTranscript(t core.Transcript) core.Transcript {
	segs := slices.Clone(t.Segments)
	for i := range segs {
		segs[i].Words = nil
	}
	t.Segments = segs
	return t
}

// transcriptChanged applies a saved or deleted transcript.
func (a *app) transcriptChanged(ev core.TranscriptChanged) {
	t := ev.Transcript
	i := slices.IndexFunc(a.trList, func(x core.Transcript) bool { return x.ID == t.ID })
	switch {
	case ev.Deleted:
		if i >= 0 {
			a.trList = slices.Delete(a.trList, i, i+1)
		}
		a.trFound = slices.DeleteFunc(a.trFound, func(x core.Transcript) bool { return x.ID == t.ID })
		if a.trSel == t.ID {
			a.trSel, a.trDetail, a.trEdit = "", nil, -1
		}
		if a.router.Path() == "/transcripts/"+t.ID {
			a.navigate("/transcripts")
		}
	case i >= 0:
		a.trList[i] = briefTranscript(t)
	default:
		a.trList = append(a.trList, briefTranscript(t))
		sortTranscripts(a.trList)
	}
	if !ev.Deleted {
		if j := slices.IndexFunc(a.trFound, func(x core.Transcript) bool { return x.ID == t.ID }); j >= 0 {
			a.trFound[j] = briefTranscript(t)
		}
		if a.trSel == t.ID {
			full := t
			a.trDetail = &full
		}
	}
	a.dictTranscriptChanged(ev) // dictation.go
}

// shownTranscripts is the history list: search results while searching.
func (a *app) shownTranscripts() []core.Transcript {
	if strings.TrimSpace(a.trQuery) != "" && a.trFound != nil {
		return a.trFound
	}
	return a.trList
}

// searchTranscripts runs the history search in the background.
func (a *app) searchTranscripts() {
	q := strings.TrimSpace(a.trQuery)
	a.trSearch++
	gen := a.trSearch
	if q == "" {
		a.trFound = nil
		return
	}
	svc := a.trSvc
	a.background(func() {
		l, err := svc.List(context.Background(), core.TranscriptQuery{Search: q})
		a.post(func() {
			if gen != a.trSearch || err != nil {
				return
			}
			if l == nil {
				l = []core.Transcript{}
			}
			a.trFound = l
		})
	})
}

// selectTranscript shows a transcript at /transcripts/{id} ("" = none),
// reading the whole of it (word timings) in the background.
func (a *app) selectTranscript(id string) {
	if id != "" {
		a.navigate("/transcripts/" + id) // transcribe_view.go: the transcript's page
	}
	if a.trSel == id && (id == "" || a.trDetail != nil) {
		return
	}
	a.stopTranscriptPlayback()
	a.trSel, a.trDetail, a.trEdit, a.trOrig, a.trExport, a.trWarn = id, nil, -1, false, false, ""
	a.trShown = trPage
	if id == "" {
		return
	}
	svc := a.trSvc
	a.background(func() {
		t, err := svc.Get(context.Background(), id)
		a.post(func() {
			if a.trSel != id {
				return
			}
			if err != nil {
				a.toastWarn(i18n.T("Could not read the transcript: %v", err))
				return
			}
			a.trDetail = &t
		})
	})
}

// --- the transcript shown ----------------------------------------------------

// updateTranscript changes the transcript shown and saves it.
func (a *app) updateTranscript(fn func(*core.Transcript)) {
	t := a.trDetail
	if t == nil {
		return
	}
	fn(t)
	id := t.ID
	svc := a.trSvc
	a.async(func() {
		if _, err := svc.Update(context.Background(), id, fn); err != nil {
			a.post(func() { a.toastWarn(i18n.T("Could not save the transcript: %v", err)) })
		}
	})
}

// editSegment opens segment i for editing.
func (a *app) editSegment(i int) {
	if a.trDetail == nil || i < 0 || i >= len(a.trDetail.Segments) {
		return
	}
	a.saveSegmentEdit()
	a.trEdit, a.trEditBuf = i, a.trDetail.Segments[i].Text
}

// saveSegmentEdit keeps the segment being edited: its words lose their
// timings when its text changed (exports estimate them).
func (a *app) saveSegmentEdit() {
	i := a.trEdit
	a.trEdit = -1
	t := a.trDetail
	if t == nil || i < 0 || i >= len(t.Segments) {
		return
	}
	text := strings.TrimSpace(a.trEditBuf)
	if text == t.Segments[i].Text {
		return
	}
	a.updateTranscript(func(t *core.Transcript) {
		if i < len(t.Segments) {
			t.Segments[i].Text, t.Segments[i].Edited = text, true
		}
	})
}

// renameSpeaker names a speaker label of the transcript shown ("" = the
// label again): the segments, exports and API show the name.
func (a *app) renameSpeaker(label, name string) {
	name = strings.TrimSpace(name)
	a.trRename, a.trRenOpen = "", false
	if t := a.trDetail; t == nil || t.Speakers[label] == name {
		return
	}
	a.updateTranscript(func(t *core.Transcript) {
		if name == "" || name == label {
			delete(t.Speakers, label)
			return
		}
		if t.Speakers == nil {
			t.Speakers = map[string]string{}
		}
		t.Speakers[label] = name
	})
}

// toggleTranscribeSpeakers turns the Speakers option on or off.
func (a *app) toggleTranscribeSpeakers() {
	on := !a.prefs.Transcribe.Speakers
	a.updateSettings(func(s *settings.Settings) { s.Transcribe.Speakers = on })
}

// deleteTranscript deletes a transcript and its audio.
func (a *app) deleteTranscript(id string) {
	a.trConfirm = ""
	if a.trSel == id {
		a.stopTranscriptPlayback()
	}
	svc := a.trSvc
	a.async(func() {
		err := svc.Delete(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not delete the transcript: %v", err))
				return
			}
			a.toast(i18n.T("Transcript deleted"))
		})
	})
}

// polishTranscript cleans the text without a language model.
func (a *app) polishTranscript() {
	t := a.trDetail
	if t == nil {
		return
	}
	clean := translate.Polish(t.Text(), t.Language, translate.PolishOptions{})
	a.updateTranscript(func(t *core.Transcript) { t.Clean, t.CleanBy = clean, "polish" })
	a.trOrig = false
}

// dropClean removes the clean-up: the transcript reads as its segments.
func (a *app) dropClean() {
	a.updateTranscript(func(t *core.Transcript) { t.Clean, t.CleanBy = "", "" })
}
