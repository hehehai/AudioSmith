package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestNoticesCurrent regenerates THIRD-PARTY-NOTICES.txt in memory and
// compares it with the committed file, which the app bundle ships: it
// fails when a Go module, a Swift pin (engine/Package.resolved, the
// mlx-audio-swift commit), MyGo or third_party/licenses changed without a
// regeneration.
func TestNoticesCurrent(t *testing.T) {
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, n, err := generate(root)
	if err != nil {
		t.Fatalf("%s cannot be regenerated: %v", output, err)
	}
	old, err := os.ReadFile(filepath.Join(root, output))
	if err != nil {
		t.Fatalf("%v: run %s", err, regenerate)
	}
	if !bytes.Equal(old, data) {
		got, want := strings.Split(string(old), "\n"), strings.Split(string(data), "\n")
		i := 0
		for i < len(got) && i < len(want) && got[i] == want[i] {
			i++
		}
		line := func(l []string) string {
			if i < len(l) {
				return l[i]
			}
			return "(end of file)"
		}
		t.Fatalf("%s is out of date: run %s (with --fetch after a Swift pin moved), then commit it.\nFirst difference, line %d:\n  file: %s\n  now:  %s",
			output, regenerate, i+1, line(got), line(want))
	}
	if n < 40 {
		t.Errorf("only %d components", n)
	}
	// The file names every component with its version.
	for _, s := range []string{"github.com/egoist/mygo v0.10.0-actnow.", "mlx-swift 0.32.3", "mlx-audio-swift revision", "swift-crypto", "NOTICE.txt", "SQLite", "RapiDoc", "Solar icon set"} {
		if !bytes.Contains(data, []byte(s)) {
			t.Errorf("the notices lack %q", s)
		}
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"Permission is hereby granted, free of charge, to any person ... The above copyright notice and this permission notice shall be included in all copies", []string{"MIT"}},
		{"Permission is hereby granted, free of charge, to any person obtaining a copy of this\nsoftware ... to permit persons to whom the Software is furnished to do so.", []string{"MIT-0"}},
		{"// Redistribution and use in source and binary forms, with or without\n// modification ... Neither the name of Google Inc. nor ... to endorse or promote products", []string{"BSD-3-Clause"}},
		{" * Redistribution and use in source and binary forms ... disclaimer.", []string{"BSD-2-Clause"}},
		{"                                 Apache License\n                           Version 2.0, January 2004", []string{"Apache-2.0"}},
		{"Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted", []string{"ISC"}},
		{"The origin of this software must not be\n//    misrepresented", []string{"Zlib"}},
		{"Public domain: may you do good and not evil.", nil},
	} {
		if got := classify(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("classify(%.40q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	mit := text{name: "LICENSE", body: "Permission is hereby granted, free of charge ... shall be included in all copies"}
	ok := component{name: "x", version: "v1", url: "https://x", spdx: "Apache-2.0 AND MIT", texts: []text{mit}}
	if err := ok.validate(); err != nil {
		t.Error(err)
	}
	moved := ok
	moved.spdx = "Apache-2.0"
	if err := moved.validate(); err == nil || !strings.Contains(err.Error(), "reads as MIT") {
		t.Errorf("a licence that changed: %v", err)
	}
	if err := (component{name: "x", version: "v1", url: "https://x", spdx: "MIT"}).validate(); err == nil {
		t.Error("a component without a text passes")
	}
}

func TestLicenceFiles(t *testing.T) {
	for name, want := range map[string]bool{
		"LICENSE": true, "LICENSE.txt": true, "LICENSE.md": true, "LICENSE.MIT": true, "LICENCE": true, "COPYING": true,
		"NOTICE": true, "NOTICE.txt": true, "PATENTS": true, "UNLICENSE": true, "LICENSE-APACHE": true,
		"license.go": false, "licenses.swift": false, "README.md": false, "notice_test.go": false,
	} {
		if got := isLicenceFile(name); got != want {
			t.Errorf("isLicenceFile(%q) = %v", name, got)
		}
	}
	for file, want := range map[string]bool{
		"LICENSE": false, "Source/Cmlx/fmt/LICENSE": false, "Tests/Fixtures/LICENSE": true, "docs/LICENSE": true, "Examples/App/LICENSE": true,
	} {
		if got := skippedPath(file); got != want {
			t.Errorf("skippedPath(%q) = %v", file, got)
		}
	}
}

func TestExcerpt(t *testing.T) {
	src := "// Copyright Apple\n#pragma once\n// Original license copied below:\n//  Copyright (c) X\n//  ... SUCH DAMAGE.\nint x;\n"
	got, err := excerpt{"f.h", "Original license", "SUCH DAMAGE."}.cut(src)
	if err != nil || got != "// Original license copied below:\n//  Copyright (c) X\n//  ... SUCH DAMAGE." {
		t.Errorf("cut = %q, %v", got, err)
	}
	if _, err := (excerpt{"f.h", "missing", "x"}).cut(src); err == nil {
		t.Error("a moved licence comment is not reported")
	}
}

func TestSubmoduleURLs(t *testing.T) {
	got := submoduleURLs("[submodule \"submodules/mlx\"]\n\tpath = Source/Cmlx/mlx\n\turl = https://github.com/ml-explore/mlx\n")
	if got["Source/Cmlx/mlx"] != "https://github.com/ml-explore/mlx" {
		t.Errorf("%v", got)
	}
}

func TestRenderDeduplicates(t *testing.T) {
	apache := "Apache License\nVersion 2.0, January 2004\n"
	out := string(render([]section{{title: "S", comps: []component{
		{name: "a", version: "1", url: "u", spdx: "Apache-2.0", texts: []text{{name: "LICENSE", body: apache}}},
		{name: "b", version: "2", url: "u", spdx: "Apache-2.0", texts: []text{{name: "LICENSE.txt", body: "  Apache License\r\n  Version 2.0, January 2004  \r\n"}}},
	}}}))
	if strings.Count(out, "January 2004") != 1 || !strings.Contains(out, "[The same text as LICENSE in [1] a above.]") {
		t.Errorf("the second copy is not replaced by a reference:\n%s", out)
	}
	if !strings.Contains(out, "Components (2)") || !strings.Contains(out, "   [2] b 2 — Apache-2.0") {
		t.Errorf("the list of components:\n%s", out)
	}
}
