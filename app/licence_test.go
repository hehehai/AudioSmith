package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/api"
)

// TestProjectLicence keeps the places that state AudioSmith's own licence,
// the GNU AGPL version 3, in step (the models' licences are elsewhere and
// differ): LICENSE, NOTICE, the READMEs, mygo.json (the bundle's copyright
// and the files it ships, with the third-party notices, whose contents
// internal/tools/thirdparty checks), the CLA and its workflow.
func TestProjectLicence(t *testing.T) {
	read := func(name string) string { // name from the module root
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	if lic := read("LICENSE"); !strings.HasPrefix(strings.TrimSpace(lic), "GNU AFFERO GENERAL PUBLIC LICENSE\n                       Version 3, 19 November 2007") {
		t.Errorf("LICENSE is not the GNU AGPL version 3")
	}
	if api.License != "AGPL-3.0-only" || api.SourceURL != "https://github.com/hehehai/AudioSmith" {
		t.Errorf("api: %s %s", api.License, api.SourceURL)
	}
	notice := read("NOTICE")
	for _, s := range []string{"Copyright (C) 2026 hehehai and the AudioSmith contributors", "GNU Affero General Public License", "version 3", api.SourceURL, "CLA.md", thirdPartyNotices} {
		if !strings.Contains(notice, s) {
			t.Errorf("NOTICE lacks %q", s)
		}
	}
	for _, name := range []string{"README.md", "README.zh-CN.md"} {
		r := read(name)
		if !strings.Contains(r, `alt="AGPL-3.0"`) || !strings.Contains(r, "(CLA.md)") || !strings.Contains(r, "(CONTRIBUTING.md)") || !strings.Contains(r, "("+thirdPartyNotices+")") ||
			strings.Contains(r, "Apache-2.0](LICENSE)") || strings.Contains(r, `alt="Apache-2.0"`) {
			t.Errorf("%s: the badge, the licence section or the contributing links", name)
		}
	}
	var cfg struct {
		Copyright string   `json:"copyright"`
		Resources []string `json:"resources"`
	}
	if err := json.Unmarshal([]byte(read("mygo.json")), &cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Copyright, "hehehai and the AudioSmith contributors") || !strings.Contains(cfg.Copyright, "AGPL-3.0") ||
		!slices.Contains(cfg.Resources, "LICENSE") || !slices.Contains(cfg.Resources, "NOTICE") || !slices.Contains(cfg.Resources, thirdPartyNotices) {
		t.Errorf("mygo.json: copyright %q, resources %v", cfg.Copyright, cfg.Resources)
	}
	cla, flow := read("CLA.md"), read(".github/workflows/cla.yml")
	sign := "I have read the CLA Document and I hereby sign the CLA"
	if !strings.Contains(cla, sign) || !strings.Contains(read("CONTRIBUTING.md"), sign) || strings.Count(flow, sign) != 1 ||
		!strings.Contains(flow, "path-to-document: "+api.SourceURL+"/blob/main/CLA.md") {
		t.Errorf("CLA.md, CONTRIBUTING.md and cla.yml disagree on the document or the sentence that signs it")
	}
	// pull_request_target runs with a write token: never the PR's code.
	if strings.Contains(flow, "actions/checkout") || strings.Contains(flow, "run:") {
		t.Errorf("cla.yml checks out or runs code")
	}
}
