package main

// actnowkit's reference texts (feedback.Text*) in AudioSmith's catalogue:
// the English is theirs with {placeholders} as format verbs, the
// Chinese is in locales/zh-Hans.json (feedback_test.go compares both).

import (
	"github.com/hehehai/actnowkit/feedback"

	"github.com/hehehai/AudioSmith/internal/i18n"
)

// feedbackKindText names a kind.
func feedbackKindText(k feedback.Kind) string {
	switch k {
	case feedback.KindBug:
		return i18n.T("Problem")
	case feedback.KindIdea:
		return i18n.T("Suggestion")
	case feedback.KindQuestion:
		return i18n.X("feedback kind", "Question")
	}
	return i18n.X("feedback kind", "Other")
}

// feedbackStatusText names a status on the site.
func feedbackStatusText(s feedback.Status) string {
	switch s {
	case feedback.StatusTriaged:
		return i18n.T("Being looked at")
	case feedback.StatusInProgress:
		return i18n.X("feedback status", "In progress")
	case feedback.StatusWaitingUser:
		return i18n.T("Waiting for you")
	case feedback.StatusResolved:
		return i18n.X("feedback status", "Resolved")
	case feedback.StatusClosed:
		return i18n.X("feedback status", "Closed")
	}
	return i18n.X("feedback status", "Received")
}

// feedbackProblemText says what to change in the form.
func feedbackProblemText(p feedback.Problem) string {
	switch p {
	case feedback.ProblemBodyShort, feedback.ProblemKind:
		return i18n.T("Write a few words about it.")
	case feedback.ProblemBodyLong:
		return i18n.T("At most %d characters.", feedback.MaxBodyRunes)
	case feedback.ProblemEmailRequired:
		return i18n.T("Enter an email so we can reply.")
	case feedback.ProblemEmailInvalid:
		return i18n.T("This email doesn't look right.")
	case feedback.ProblemTooManyImages:
		return i18n.T("At most %d images.", feedback.MaxImages)
	case feedback.ProblemImageMissing, feedback.ProblemImageTooLarge, feedback.ProblemImageType:
		return i18n.T("This image cannot be used (missing, too large or not an image).")
	case feedback.ProblemVideoTooLarge, feedback.ProblemVideoMissing, feedback.ProblemVideoType:
		return i18n.T("This video cannot be used: an MP4 or MOV file up to 1 GB.")
	}
	return string(p)
}

// feedbackFailText says why a submission stopped.
func feedbackFailText(sub *feedback.Submission, err error) string {
	if sub != nil {
		switch sub.ErrorCode {
		case "rate_limited":
			return i18n.T("Too much feedback from this Mac for now: it will be sent later.")
		case "attachment_too_large":
			return i18n.T("The video is still over 20 MB after compressing: trim it and try again.")
		case "email_required":
			return i18n.T("Enter an email so we can reply.")
		case "invalid_email":
			return i18n.T("This email doesn't look right.")
		}
	}
	return i18n.T("Your feedback could not be sent. It is kept on this Mac and sent when you're online again.")
}
