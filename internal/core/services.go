package core

import (
	"context"
	"fmt"
)

// The service layer: what the UI and the Local API (M5) may do with the
// domain. Thread A implements these in internal/services over a
// store.Store and a jobs.Scheduler; until then the UI still drives the
// history.json store and the v1 engine itself (see docs/architecture.md).
//
// Every method is goroutine-safe, may block on I/O, and publishes the
// matching event on the Bus once its change is durable.

// TakeService is the take library and TTS generation.
type TakeService interface {
	// List returns takes matching q, newest first.
	List(ctx context.Context, q TakeQuery) ([]Take, error)
	// Get returns one take, or an error wrapping ErrNotFound.
	Get(ctx context.Context, id string) (Take, error)
	// Generate checks the request (text, model downloaded, licence
	// accepted), submits a "tts.synthesize" job and returns its id at
	// once. The take is saved, and TakeCreated published, when the job
	// succeeds; progress and failure arrive as jobs.Updated events.
	Generate(ctx context.Context, req TakeRequest) (jobID string, err error)
	// Delete removes the take and its audio.
	Delete(ctx context.Context, id string) error
	// SetStarred stars or unstars a take.
	SetStarred(ctx context.Context, id string, starred bool) error
	// Add saves a take made outside Generate whose audio is already in
	// AudioDir (the protocol-1 engine link until Generate runs on jobs;
	// later imports), filling ID and CreatedAt when empty, and publishes
	// TakeCreated. It returns the take as stored.
	Add(ctx context.Context, t Take) (Take, error)
	// AudioDir is the directory take audio lives in. Pure: no I/O.
	AudioDir() string
	// AudioPath is the absolute path of the take's audio file. Pure: no I/O.
	AudioPath(t Take) string
	// Export copies the take's audio to dst (streamed, not read into memory).
	Export(ctx context.Context, id, dst string) error
}

// TakeRequest asks for a new take.
type TakeRequest struct {
	// Recipe holds the text, model, voice and parameters; its File,
	// CreatedAt and result fields are ignored and filled in by the service.
	// Zero parameters are not sent, so the model's defaults apply. Its ID
	// may be set (a new ULID, core.NewID) so the caller can follow the job
	// by its owner, "take:<id>", before Generate returns; "" lets the
	// service choose one.
	Recipe Take
	// Stream asks for audio chunks as they are generated (M1 streaming
	// preview): the job's events then carry tts.chunk payloads.
	Stream bool
	// Background queues behind interactive work (batch, longform). The
	// Studio and API requests leave it false.
	Background bool
}

// VoiceService is the voice library (M1).
type VoiceService interface {
	List(ctx context.Context, q VoiceQuery) ([]Voice, error)
	Get(ctx context.Context, id string) (Voice, error)
	// Save creates (empty ID) or updates a profile and returns it as stored.
	// Blob fields (RefAudio, Sample, Portrait, Consent.Recording, Prompts)
	// keep their stored values on update: files change through SaveFiles,
	// Lock, SetConsent.
	Save(ctx context.Context, v Voice) (Voice, error)
	// SaveFiles is Save that first copies files into the voice's directory.
	// A new reference clears the lock and a verified consent (the new clip
	// may be another speaker), except a designed voice's (rendered from
	// the voice itself).
	SaveFiles(ctx context.Context, v Voice, f VoiceFiles) (Voice, error)
	// Delete deletes a profile and its files. Trash deletes it too, but
	// its files wait in the library's trash until Purge (or the next
	// start), so that Restore can undo it.
	Delete(ctx context.Context, id string) error
	Trash(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) (Voice, error)
	Purge(id string)
	// PurgeTrash empties the trash (at start).
	PurgeTrash(ctx context.Context)
	// Import reads a .asvoice (or VoiceStudio .ovsvoice) package into a new
	// profile. Imported consent is ConsentIncluded at most.
	Import(ctx context.Context, path string) (VoiceImport, error)
	// Export writes a .asvoice package.
	Export(ctx context.Context, id, dst string, o VoiceExport) error
	// HasPreview reports whether Export finds a preview by itself (the
	// voice's sample or reference is a WAV): asked before a save dialog.
	HasPreview(v Voice) bool
	// Usage says where the voice was used: takes, project segments.
	Usage(ctx context.Context, id string) (VoiceUsage, error)
	// Lock ties the voice's identity to one of its takes: its audio becomes
	// the voice's sample, its seed the voice's seed.
	Lock(ctx context.Context, id, takeID string) (Voice, error)
	Unlock(ctx context.Context, id string) (Voice, error)
	// CleanReference makes a cleaned copy of the voice's reference (the
	// file at path, seconds long) its reference, keeping the current one
	// as RefOriginal; RevertReference undoes it. Neither touches the lock
	// or the consent: the speaker is the same.
	CleanReference(ctx context.Context, id, path string, seconds float64, q *RefQuality) (Voice, error)
	RevertReference(ctx context.Context, id string) (Voice, error)
	// ReferenceTextChecked records that the recognizer heard the reference
	// was say text: text becomes RefText and RefCheck says it is checked
	// (ErrInvalid when the reference is no longer was).
	ReferenceTextChecked(ctx context.Context, id string, was RefHeard, text string) (Voice, error)
	// SetConsent stores a recording of the user reading the consent
	// statement: the voice becomes Verified. RevokeConsent removes it.
	SetConsent(ctx context.Context, id string, c ConsentRecording) (Voice, error)
	RevokeConsent(ctx context.Context, id string) (Voice, error)
	// FilePath is the absolute path of one of the voice's files ("" for
	// an empty name). Pure: no I/O.
	FilePath(v Voice, name string) string
}

// VoiceQuery selects voices. The zero value is every voice, by name.
type VoiceQuery struct {
	Search string // name, style, transcript, description, tags
	Kind   VoiceKind
	Model  string
	Origin string // exact Voice.Origin
}

// VoiceFiles are files to copy into a voice's directory. Empty fields
// leave the voice's files as they are.
type VoiceFiles struct {
	Reference string // audio file (absolute path): the new clone reference
	// Original is the new reference before it was cleaned (Reference is
	// the cleaned one); RemoveReference removes the reference (a designed
	// voice whose design changed).
	Original        string
	RemoveReference bool
	Sample          string // audio file: a rendered sample (designed voices)
	// Portrait is the new portrait, a PNG (already cropped to 256×256);
	// RemovePortrait deletes the current one.
	Portrait       []byte
	RemovePortrait bool
	// Prompt is a cached voice prompt for model PromptModel (tts.encodeVoice).
	Prompt      string
	PromptModel string
}

// ConsentRecording is the consent statement as read by the user.
type ConsentRecording struct {
	Audio     string // the recording (absolute path), copied into the voice's directory
	Statement string // the text read
	Locale    string
}

// VoiceImport is an imported voice and what the importer could not carry over.
type VoiceImport struct {
	Voice    Voice
	Format   string   // "asvoice" or "ovsvoice"
	Warnings []string // English, technical
}

// VoiceExport configures Export.
type VoiceExport struct {
	// IncludeReference adds the clone reference; off shares only the preview.
	IncludeReference bool
	Licence          string // SPDX id; "" = personal use
	// Preview is a WAV to ship as the preview; "" picks the voice's sample,
	// or its reference (WAV): never its takes (private).
	Preview    string
	App        string // creator software, "AudioSmith"
	AppVersion string
}

// VoiceUsage is where a voice was used.
type VoiceUsage struct {
	Takes      int
	LatestTake string // id of the newest take
	Segments   int
	Projects   []VoiceProjectUsage
}

// VoiceProjectUsage is one project whose segments use a voice.
type VoiceProjectUsage struct {
	ID       string
	Title    string
	Kind     ProjectKind
	Segments int
}

// ProjectService is the project library (M3+).
type ProjectService interface {
	List(ctx context.Context, kind ProjectKind, search string) ([]Project, error)
	Get(ctx context.Context, id string) (Project, error)
	Save(ctx context.Context, p Project) (Project, error)
	Delete(ctx context.Context, id string) error
}

// LicenceService is the model licence gate. The UI asks before loading or
// generating; the API and the job executors check again, so nothing runs a
// gated model without an acceptance on record.
type LicenceService interface {
	// Check returns nil when model may be used, or a *LicenceRequiredError
	// (which wraps ErrLicenceRequired) naming the licence to show.
	Check(ctx context.Context, model string) error
	Accept(ctx context.Context, a LicenceAcceptance) error
	Revoke(ctx context.Context, licence, model string) error
	List(ctx context.Context) ([]LicenceAcceptance, error)
}

// LicenceRequiredError says which licence must be accepted for a model.
type LicenceRequiredError struct {
	Model   string
	Licence string // catalog.Licence.ID
	Name    string // human name, e.g. "CC BY-NC 4.0"
	URL     string
}

func (e *LicenceRequiredError) Error() string {
	return fmt.Sprintf("%s: the %s licence must be accepted first", e.Model, e.Name)
}

func (e *LicenceRequiredError) Unwrap() error { return ErrLicenceRequired }

// NeedsReferenceError: the model only clones voices (catalog
// Entry.NeedsReference) and the recipe has no reference
// clip. It wraps ErrInvalid (the Local API answers 400).
type NeedsReferenceError struct {
	Model string // the model's name as the UI shows it, or its repo
}

func (e *NeedsReferenceError) Error() string {
	return fmt.Sprintf("%s only clones voices: give it a reference clip, or a saved voice cloned from one", e.Model)
}

func (e *NeedsReferenceError) Unwrap() error { return ErrInvalid }

// CodeNeedsReference is the jobs.Error code of a render refused for the
// same reason (Data: {"model": repo}).
const CodeNeedsReference = "needs_reference"

// NotificationService is the notification centre. Post never blocks.
type NotificationService interface {
	Post(n Notification)
	List(ctx context.Context) ([]Notification, error)
	MarkRead(ctx context.Context, id string) error
	Clear(ctx context.Context) error
}

// LogService is the in-memory ring of log lines behind the logs panel,
// also written to rotated files in mygo.PathLogs. Append never blocks.
type LogService interface {
	Append(e LogEntry)
	// Tail returns up to n newest entries from source ("" = all), oldest first.
	Tail(n int, source string) []LogEntry
}

// DraftService autosaves editor state (scripts, forms) so it survives a
// restart. Save is debounced and asynchronous; Flush writes pending drafts
// (call it on quit).
type DraftService interface {
	// Load decodes the draft stored under key into v (JSON) and reports
	// whether there was one.
	Load(ctx context.Context, key string, v any) (bool, error)
	Save(key string, v any)
	Delete(ctx context.Context, key string) error
	Flush(ctx context.Context) error
}

// Services bundles the domain services for the UI and the Local API.
// Nil fields are services not implemented yet.
type Services struct {
	Bus           *Bus
	Takes         TakeService
	Voices        VoiceService
	Projects      ProjectService
	Licences      LicenceService
	Notifications NotificationService
	Logs          LogService
	Drafts        DraftService
}
