// Command thirdparty writes THIRD-PARTY-NOTICES.txt, the third-party
// notices the app bundle ships (mygo.json resources): for every component
// linked into or shipped with AudioSmith, its version or pin, where its
// source is, its licence (SPDX) and the full licence and notice texts.
//
//	scripts/third-party-notices.sh           regenerate the file
//	scripts/third-party-notices.sh --check   exit 1 if it is out of date
//	scripts/third-party-notices.sh --fetch   refresh the Swift packages'
//	                                         texts (network, git), then
//	                                         regenerate
//
// The components:
//
//   - the Go modules linked into the darwin/arm64 app (go list -deps with
//     GOOS=darwin GOARCH=arm64; test-only and tool modules are not linked),
//     with the Go standard library; their texts come from the module
//     cache (go.sum verifies it) and the vendored MyGo (third_party/mygo);
//   - the Swift packages of the engine: engine/Package.resolved, the
//     mlx-audio-swift pin of scripts/fetch-engine-deps.sh and the git
//     submodules of those (mlx and mlx-c in mlx-swift). Their texts are
//     committed under third_party/licenses/swift, one directory per
//     package with the pin they were fetched at (-fetch), so a release
//     build needs no network;
//   - the rest of what the app carries (extra.go): SQLite in MyGo's
//     SQLite plugin, RapiDoc and the libraries in its bundle, the Solar
//     icons, the two Hugeicons and the voice previews.
//
// The output is sorted and has no paths of this machine: the same inputs
// give the same bytes. TestNoticesCurrent (main_test.go) regenerates it in
// memory and fails when the file differs: when go.mod, Package.resolved,
// the mlx-audio-swift pin or MyGo change without a regeneration.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// output is the generated file, at the module root.
const output = "THIRD-PARTY-NOTICES.txt"

// regenerate is the command a failure tells to run.
const regenerate = "scripts/third-party-notices.sh"

func main() {
	check := flag.Bool("check", false, "exit 1 if "+output+" is out of date, without writing it")
	fetch := flag.Bool("fetch", false, "fetch the Swift packages' licence texts at their pins into third_party/licenses/swift (network, git), then write "+output)
	flag.Parse()
	if err := run(*check, *fetch); err != nil {
		fmt.Fprintln(os.Stderr, "thirdparty:", err)
		os.Exit(1)
	}
}

func run(check, fetch bool) error {
	root, err := findRoot()
	if err != nil {
		return err
	}
	if fetch {
		if err := fetchSwift(root); err != nil {
			return err
		}
	}
	data, n, err := generate(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, output)
	if check {
		old, _ := os.ReadFile(path)
		if !bytes.Equal(old, data) {
			return fmt.Errorf("%s is out of date: run %s", output, regenerate)
		}
		fmt.Printf("%s is up to date (%d components)\n", output, n)
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d components, %d bytes\n", output, n, len(data))
	return nil
}

// generate collects every component and renders the file.
func generate(root string) ([]byte, int, error) {
	goComps, linked, err := goComponents(root)
	if err != nil {
		return nil, 0, err
	}
	swiftComps, err := swiftComponents(root)
	if err != nil {
		return nil, 0, err
	}
	extra, err := extraComponents(root, linked)
	if err != nil {
		return nil, 0, err
	}
	sections := []section{
		{title: "The app (Go): the Go standard library and the modules linked into AudioSmith", comps: goComps},
		{title: "The engine (Swift): the packages linked into audiosmith-engine", comps: swiftComps},
		{title: "Other material in the app", comps: extra},
	}
	for _, s := range sections {
		for _, c := range s.comps {
			if err := c.validate(); err != nil {
				return nil, 0, err
			}
		}
	}
	data := render(sections)
	return data, len(goComps) + len(swiftComps) + len(extra), nil
}

// findRoot is AudioSmith's module root: the first directory above the
// working directory with both go.mod and mygo.json (third_party/mygo has
// a go.mod of its own).
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, "go.mod")) && exists(filepath.Join(dir, "mygo.json")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no AudioSmith module root (go.mod and mygo.json) above the working directory")
		}
		dir = parent
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
