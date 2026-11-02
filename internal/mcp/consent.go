package mcp

// Asking the user, and the tools' hints (Mac test #136): GPT-6 Luna read
// "download the MOSS model and delete the isolation" as consent and sent
// confirm: true at once, starting a 24 GB download. confirm: true stays
// the agent's word; a client that can ask its user (MCP elicitation)
// is asked to, and only its user's yes goes on. Tool annotations tell
// clients which tools only read, which delete and which go online, for
// their own approval prompts.

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
)

// CodeDeclined is a tool error: the user said no in their client's
// confirmation.
const CodeDeclined = "declined"

// askUser asks the client's user to confirm message when the client can
// ask (elicitation); nil when they accept or the client cannot ask (then
// confirm: true is the agent's word for it). A refusal, a dismissal or a
// failed question is an error: nothing is done.
func askUser(ctx context.Context, req *sdk.CallToolRequest, message string) error {
	if req == nil || req.Session == nil {
		return nil
	}
	ip := req.Session.InitializeParams()
	if ip == nil || ip.Capabilities == nil || ip.Capabilities.Elicitation == nil {
		return nil
	}
	res, err := req.Session.Elicit(ctx, &sdk.ElicitParams{Message: message,
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}}})
	if err != nil {
		return fmt.Errorf("%s: the confirmation could not be shown (%v): nothing was done; ask the user in the chat", api.CodeConfirmRequired, err)
	}
	if res == nil || res.Action != "accept" {
		return errors.New(CodeDeclined + ": the user did not confirm it in the dialog: nothing was done. Do not try again unless they ask.")
	}
	return nil
}

// annotate gives each tool its hints: read only (list_, get_, check_…),
// destructive (deleting, cancelling, overwriting), going online
// (downloads, links, cloud translation and writing help); the rest add
// to the library.
func annotate(t *sdk.Tool) {
	if t.Annotations != nil {
		return
	}
	no, yes := false, true
	a := &sdk.ToolAnnotations{Title: t.Title, DestructiveHint: &no, OpenWorldHint: &no}
	switch t.Name {
	case "probe_media", "detect_watermark", "direct_performance":
		a.ReadOnlyHint = true
	default:
		a.ReadOnlyHint = readOnlyTool(t.Name)
	}
	// Code review 11-02, N7: what overwrites (files of the same name, a
	// script, a voice's profile) is destructive too, and what may reach a
	// cloud model (translation, the writing help) goes online.
	switch t.Name {
	case "delete_item", "cancel_job", "export_audio", "export_dub", "export_longform", "export_transcript", "export_voice",
		"update_longform", "edit_dub", "update_voice":
		a.DestructiveHint = &yes
	case "download_model", "create_dub", "run_dub", "batch_dub", "write_story", "check_line_fit":
		a.OpenWorldHint = &yes
	}
	if a.ReadOnlyHint {
		a.IdempotentHint = true
	}
	t.Annotations = a
}

// annotateTools adds the hints to tools/list's answer (to copies: the
// server's tools stay as added).
func annotateTools(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		res, err := next(ctx, method, req)
		if l, ok := res.(*sdk.ListToolsResult); ok && err == nil && method == "tools/list" {
			out := *l
			out.Tools = make([]*sdk.Tool, len(l.Tools))
			for i, t := range l.Tools {
				c := *t
				annotate(&c)
				out.Tools[i] = &c
			}
			return &out, nil
		}
		return res, err
	}
}
