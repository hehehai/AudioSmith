package main

// The fake actnow.dev of the feedback tests (feedback_test.go): the
// site's feedback API in memory (docs/feedback.md of the site), as
// Dicta's tests have it.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/actnowkit/feedback"
)

// fakeFeedbackSite is actnow.dev's feedback API in memory
// (docs/feedback.md of the site): identity, create with direct uploads
// to a fake bucket, complete, list, get and reply.
type fakeFeedbackSite struct {
	mu     sync.Mutex
	srv    *httptest.Server
	tokens map[string]string // licence token → owner's email
	items  map[string]*fakeFeedback
	n      int
	puts   []string
	calls  []string
}

type fakeFeedback struct {
	item      feedback.Item
	req       feedback.CreateRequest
	body      string
	viewToken string
	messages  []feedback.Message
}

func newFakeFeedbackSite(t *testing.T) *fakeFeedbackSite {
	f := &fakeFeedbackSite{tokens: map[string]string{}, items: map[string]*fakeFeedback{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFeedbackSite) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if strings.HasPrefix(r.URL.Path, "/bucket/") {
		f.puts = append(f.puts, strings.TrimPrefix(r.URL.Path, "/bucket/"))
		w.WriteHeader(200)
		return
	}
	var in struct {
		feedback.CreateRequest
		ID        string         `json:"id"`
		ViewToken string         `json:"viewToken"`
		Items     []feedback.Ref `json:"items"`
	}
	json.Unmarshal(data, &in)
	out := func(v any) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	fail := func(status int, code string) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"error": code, "message": code})
	}
	item := func() *fakeFeedback {
		it := f.items[in.ID]
		if it == nil || it.viewToken != in.ViewToken {
			return nil
		}
		return it
	}
	switch strings.TrimPrefix(r.URL.Path, "/api/feedback/v1/") {
	case "identity":
		if email := f.tokens[in.LicenceToken]; email != "" {
			out(feedback.Identity{Linked: true, EmailHint: email[:1] + "***" + email[strings.IndexByte(email, '@'):]})
			return
		}
		out(feedback.Identity{})
	case "create":
		if in.Email == "" && f.tokens[in.LicenceToken] == "" {
			fail(422, "email_required")
			return
		}
		f.n++
		now := time.Now().UTC()
		it := &fakeFeedback{req: in.CreateRequest, body: in.Body, viewToken: fmt.Sprintf("vt_%d", f.n),
			item: feedback.Item{ID: fmt.Sprintf("fb_%d", f.n), Number: fmt.Sprintf("FB-%d", 1000+f.n), App: in.App, Kind: in.Kind,
				Status: feedback.StatusNew, Summary: feedback.Summary(in.Body, 80), CreatedAt: now, UpdatedAt: now}}
		it.item.WebURL = f.srv.URL + "/feedback/" + it.item.ID
		f.items[it.item.ID] = it
		var ups []feedback.Upload
		for i, a := range in.Attachments {
			ups = append(ups, feedback.Upload{Ref: a.Ref, AttachmentID: fmt.Sprintf("att_%d", i), Method: "PUT",
				URL: f.srv.URL + "/bucket/" + it.item.ID + "/" + a.Name, ExpiresAt: now.Add(time.Hour)})
		}
		out(feedback.CreateAnswer{ID: it.item.ID, Number: it.item.Number, ViewToken: it.viewToken, WebURL: it.item.WebURL, Uploads: ups})
	case "complete":
		it := item()
		if it == nil {
			fail(404, "not_found")
			return
		}
		out(feedback.CompleteAnswer{ID: it.item.ID, Number: it.item.Number, Status: it.item.Status, WebURL: it.item.WebURL})
	case "list":
		var items []feedback.Item
		for _, ref := range in.Items {
			if it := f.items[ref.ID]; it != nil && it.viewToken == ref.ViewToken {
				items = append(items, it.item)
			}
		}
		out(map[string]any{"items": items})
	case "get":
		it := item()
		if it == nil {
			fail(404, "not_found")
			return
		}
		out(feedback.Thread{Item: it.item, Body: it.body, Messages: it.messages})
	case "reply":
		it := item()
		if it == nil {
			fail(404, "not_found")
			return
		}
		var body struct{ Body string }
		json.Unmarshal(data, &body)
		f.reply(it, "user", body.Body)
		out(it.messages[len(it.messages)-1])
	default:
		fail(404, "not_found")
	}
}

// reply adds a message (the caller holds mu).
func (f *fakeFeedbackSite) reply(it *fakeFeedback, author, body string) {
	m := feedback.Message{ID: fmt.Sprintf("m_%d", len(it.messages)+1), Author: author, Body: body, CreatedAt: time.Now().UTC()}
	it.messages = append(it.messages, m)
	it.item.Messages = len(it.messages)
	it.item.LastReplyAt = m.CreatedAt
	if author == "support" {
		it.item.Status = feedback.StatusWaitingUser
	}
}

// supportReplies answers feedback id as support.
func (f *fakeFeedbackSite) supportReplies(id, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply(f.items[id], "support", body)
}

func (f *fakeFeedbackSite) created() []feedback.CreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []feedback.CreateRequest
	for i := 1; i <= f.n; i++ {
		out = append(out, f.items[fmt.Sprintf("fb_%d", i)].req)
	}
	return out
}
