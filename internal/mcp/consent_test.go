package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
)

// TestConsentElicit: a client that can ask its user gets AudioSmith's
// question before delete_item and download_model go on with confirm:
// true; a no stops them (Mac test #136); clients that cannot ask go by
// confirm alone (TestKitIsolator).
func TestConsentElicit(t *testing.T) {
	k := newKitFake(t)
	url := serveKit(t, k).URL
	var mu sync.Mutex
	var asked []string
	answer := "decline"
	c := sdk.NewClient(&sdk.Implementation{Name: "asker", Version: "1"}, &sdk.ClientOptions{
		ElicitationHandler: func(_ context.Context, r *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, r.Params.Message)
			return &sdk.ElicitResult{Action: answer}, nil
		}})
	cs, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	var x api.IsolationJSON
	succeeds(t, cs, "isolate_voice", map[string]any{"path": writeTone(t, filepath.Join(t.TempDir(), "a.wav"))}, &x)
	failsWith(t, cs, "delete_item", map[string]any{"kind": "isolation", "id": x.ID}, api.CodeConfirmRequired) // not asked without confirm
	failsWith(t, cs, "delete_item", map[string]any{"kind": "isolation", "id": x.ID, "confirm": true}, CodeDeclined)
	failsWith(t, cs, "download_model", map[string]any{"model": "kokoro-82m-bf16", "confirm": true}, CodeDeclined)
	if len(k.isos) != 1 {
		t.Fatal("deleted after a no")
	}
	mu.Lock()
	answer = "accept"
	mu.Unlock()
	succeeds(t, cs, "delete_item", map[string]any{"kind": "isolation", "id": x.ID, "confirm": true}, nil)
	succeeds(t, cs, "download_model", map[string]any{"model": "kokoro-82m-bf16", "confirm": true}, nil)
	mu.Lock()
	defer mu.Unlock()
	if len(k.isos) != 0 || len(asked) != 4 || !strings.Contains(asked[0], x.ID) || !strings.Contains(asked[0], "“"+x.Title+"”") || !strings.Contains(asked[1], "download") {
		t.Fatalf("isolations %d, asked %q", len(k.isos), asked)
	}
}

// TestToolAnnotations: tools/list says which tools only read, which
// delete and which go online.
func TestToolAnnotations(t *testing.T) {
	cs := connect(t, serveKit(t, newKitFake(t)).URL, "claude-code", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*sdk.ToolAnnotations{}
	for _, tl := range res.Tools {
		if tl.Annotations == nil {
			t.Fatalf("%s has no annotations", tl.Name)
		}
		by[tl.Name] = tl.Annotations
	}
	if a := by["list_voices"]; !a.ReadOnlyHint {
		t.Fatalf("list_voices %+v", a)
	}
	if a := by["delete_item"]; a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Fatalf("delete_item %+v", a)
	}
	if a := by["download_model"]; a.OpenWorldHint == nil || !*a.OpenWorldHint || *a.DestructiveHint {
		t.Fatalf("download_model %+v", a)
	}
	if a := by["generate_speech"]; a.ReadOnlyHint || *a.DestructiveHint {
		t.Fatalf("generate_speech %+v", a)
	}
}
