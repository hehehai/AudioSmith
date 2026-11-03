package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// Error is an API error: an HTTP status and OpenAI's error object
// ({"error":{"message","type","param","code"}}), plus AudioSmith's data
// (the licence to accept, the model to download…). Messages are English.
type Error struct {
	Status  int
	Code    string
	Message string
	Param   string
	Data    map[string]any
	// RetryAfter, in seconds, is sent as Retry-After (503, 429).
	RetryAfter int
}

func (e *Error) Error() string { return e.Message }

// Error codes the API adds to the engine's (proto.Code*) and the
// services' ones.
const (
	CodeInvalid             = "invalid_request"
	CodeNotFound            = "not_found"
	CodeModelNotFound       = "model_not_found"
	CodeVoiceNotFound       = "voice_not_found"
	CodeAmbiguousVoice      = "ambiguous_voice"
	CodeUnsupportedFormat   = "unsupported_format"
	CodeUnsupportedModel    = "unsupported_model" // the model can't do what was asked (a TTS model for STT…)
	CodeEngineUnavailable   = "engine_unavailable"
	CodeQueueTimeout        = "queue_timeout"
	CodeUnauthorized        = "unauthorized"
	CodeForbiddenHost       = "forbidden_host"
	CodeForbiddenOrigin     = "forbidden_origin"
	CodeCSRF                = "csrf_header_missing"
	CodeTooManyAttempts     = "too_many_attempts"
	CodeNotDownloaded       = proto.CodeModelNotDownloaded
	CodeLicenceRequired     = proto.CodeLicenceRequired
	CodeTranslationNotReady = "translation_unavailable" // no language model for /v1/audio/translations
	CodePathNotAllowed      = "path_not_allowed"
	CodeTooLarge            = "too_large"
	// CodeDiskFull: not enough free disk space for the work (507; data
	// need and free, bytes, when a pre-check refused it).
	CodeDiskFull = core.CodeDiskFull
	// hubCodeDiskSpace is a model download's refusal (hub.CodeDiskSpace).
	hubCodeDiskSpace = "disk_space"
	// CodeModelRemoved: the request names a model AudioSmith removed
	// (thread #43) and its replacement can't do what was asked (a
	// built-in speaker of the old model, a download). Requests the
	// replacement can serve are answered by it, with replaced_by.
	CodeModelRemoved = "model_removed"
	// CodeNeedsReference: the model only clones voices and the request
	// gave no reference (ref_audio, or a saved voice
	// cloned from a clip). 400.
	CodeNeedsReference = core.CodeNeedsReference
)

// errNeedsReference is the 400 of a request without a reference for a
// model that only clones voices.
func errNeedsReference(model string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: CodeNeedsReference, Param: "voice",
		Message: model + " only clones voices: give a reference clip (ref_audio) or a saved voice cloned from one (voice).",
		Data:    map[string]any{"model": model}}
}

func errInvalid(param, format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: CodeInvalid, Param: param, Message: fmt.Sprintf(format, args...)}
}

func errNotFound(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusNotFound, Code: code, Message: fmt.Sprintf(format, args...)}
}

func errUnavailable(code string, retry int, format string, args ...any) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: code, RetryAfter: retry, Message: fmt.Sprintf(format, args...)}
}

// errorOf maps an error of the services, the queue or the engine to an
// API error.
func errorOf(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var lic *core.LicenceRequiredError
	if errors.As(err, &lic) {
		return &Error{Status: http.StatusForbidden, Code: CodeLicenceRequired,
			Message: fmt.Sprintf("The licence of %s (%s) must be accepted first: open AudioSmith → Models.", lic.Model, lic.Name),
			Data:    map[string]any{"model": lic.Model, "licence": lic.Licence, "licenceName": lic.Name, "licenceURL": lic.URL}}
	}
	var nr *core.NeedsReferenceError
	if errors.As(err, &nr) {
		return errNeedsReference(nr.Model)
	}
	var je *jobs.Error
	if errors.As(err, &je) {
		if d := services.DiskFullJob(je); d != nil && je.Code != hubCodeDiskSpace {
			je = d
		}
		return jobErrorOf(je)
	}
	var pe *proto.Error
	if errors.As(err, &pe) {
		return jobErrorOf(&jobs.Error{Code: pe.Code, Message: pe.Message, Data: pe.Data, Retryable: proto.Retryable(pe.Code)})
	}
	// A full disk (a pre-check's refusal, a write's ENOSPC) says so,
	// with the bytes needed and free (code review 1102 M4).
	if d := services.DiskFullJob(err); d != nil {
		return jobErrorOf(d)
	}
	switch {
	case errors.Is(err, core.ErrLicenceRequired):
		return &Error{Status: http.StatusForbidden, Code: CodeLicenceRequired, Message: err.Error()}
	case errors.Is(err, core.ErrNotDownloaded):
		return &Error{Status: http.StatusConflict, Code: CodeNotDownloaded, Message: err.Error() + ": download it in AudioSmith → Models, or POST /api/models/{id}/download."}
	case errors.Is(err, core.ErrNotFound), errors.Is(err, jobs.ErrNotFound):
		return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: err.Error()}
	case errors.Is(err, core.ErrInvalid), errors.Is(err, jobs.ErrInvalid):
		return &Error{Status: http.StatusBadRequest, Code: CodeInvalid, Message: err.Error()}
	case errors.Is(err, core.ErrExists):
		return &Error{Status: http.StatusConflict, Code: "exists", Message: err.Error()}
	case errors.Is(err, core.ErrUnsupported):
		return &Error{Status: http.StatusNotImplemented, Code: proto.CodeUnsupported, Message: err.Error()}
	case errors.Is(err, core.ErrEngine):
		return &Error{Status: http.StatusServiceUnavailable, Code: CodeEngineUnavailable, RetryAfter: 5, Message: err.Error()}
	case errors.Is(err, core.ErrBusy):
		return &Error{Status: http.StatusServiceUnavailable, Code: proto.CodeBusy, RetryAfter: 10, Message: err.Error()}
	case errors.Is(err, core.ErrCancelled), errors.Is(err, context.Canceled):
		return &Error{Status: 499, Code: proto.CodeCancelled, Message: "cancelled"}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Status: http.StatusGatewayTimeout, Code: "timeout", Message: err.Error()}
	}
	return &Error{Status: http.StatusInternalServerError, Code: proto.CodeInternal, Message: err.Error()}
}

// ErrorOf is errorOf for the app (an API error for any error).
func ErrorOf(err error) *Error { return errorOf(err) }

// jobErrorOf maps a failed job's error.
func jobErrorOf(je *jobs.Error) *Error {
	e := &Error{Code: je.Code, Message: je.Message}
	if len(je.Data) > 0 {
		json.Unmarshal(je.Data, &e.Data)
	}
	switch je.Code {
	case proto.CodeLicenceRequired:
		e.Status = http.StatusForbidden
	case proto.CodeModelNotDownloaded:
		e.Status = http.StatusConflict
	case CodeEngineUnavailable, proto.CodeOOM, proto.CodeBusy, proto.CodeNotInitialized, proto.CodeProtocolMismatch:
		e.Status, e.RetryAfter = http.StatusServiceUnavailable, 10
	case proto.CodeCancelled:
		e.Status = http.StatusConflict
		if e.Message == "" {
			e.Message = "The job was cancelled."
		}
	case proto.CodeBadRequest, proto.CodeUnsupported, proto.CodeInputTooLong, proto.CodeFileNotFound,
		proto.CodeUnsupportedMedia, services.CodeNothingToSay, CodeNeedsReference:
		e.Status = http.StatusBadRequest
	case proto.CodeModelLoadFailed:
		e.Status = http.StatusUnprocessableEntity
	case core.CodeDiskFull, hubCodeDiskSpace:
		e.Status = http.StatusInsufficientStorage
	default:
		e.Status = http.StatusInternalServerError
		if je.Retryable {
			e.Status, e.RetryAfter = http.StatusServiceUnavailable, 10
		}
	}
	return e
}

// errorType is OpenAI's error type for a status.
func errorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	}
	return "invalid_request_error"
}

// errorBody is the JSON of an error.
func errorBody(e *Error) map[string]any {
	obj := map[string]any{"message": e.Message, "type": errorType(e.Status), "code": e.Code, "param": nil}
	if e.Param != "" {
		obj["param"] = e.Param
	}
	for k, v := range e.Data {
		if _, taken := obj[k]; !taken {
			obj[k] = v
		}
	}
	return map[string]any{"error": obj}
}

// writeError answers err.
func writeError(w http.ResponseWriter, err error) {
	e := errorOf(err)
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	if e.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="AudioSmith"`)
	}
	status := e.Status
	if status == 499 {
		status = http.StatusConflict // nobody reads it: the client went away
	}
	writeJSON(w, status, errorBody(e))
}

// writeJSON answers v as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// writeResult answers err when there is one, else v.
func writeResult(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, v)
}

// writeList answers err when there is one, else data as a list object.
func writeList(w http.ResponseWriter, data any, err error) {
	writeResult(w, http.StatusOK, map[string]any{"object": "list", "data": data}, err)
}
