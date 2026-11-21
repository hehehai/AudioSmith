package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The app is built for darwin/arm64 only (mygo build -platform darwin/arm64).
var goEnv = []string{"GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0"}

// goPackage is a package linked into the app, outside the standard library.
type goPackage struct {
	importPath, dir            string
	module, version, moduleDir string
	// replacement is the module go.mod's replace puts in its place,
	// "path version" ("" without one): MyGo is ActNow's fork.
	replacement string
}

// mainPackage is the app's main package, mygo.json's "main" (MyGo's
// default ".").
func mainPackage(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "mygo.json"))
	if err != nil {
		return "", err
	}
	var cfg struct{ Main string }
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("mygo.json: %v", err)
	}
	if cfg.Main == "" {
		return ".", nil
	}
	return cfg.Main, nil
}

// linkedPackages lists the packages of the app's binary: go list -deps of
// the main package for darwin/arm64. Test files and tools are not built
// into it, so their modules are not listed.
func linkedPackages(root string) ([]goPackage, error) {
	const format = `{{if not .Standard}}{{.ImportPath}}|{{.Dir}}|{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}|{{with .Replace}}{{.Path}} {{.Version}}{{end}}{{end}}{{end}}`
	main, err := mainPackage(root)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "list", "-deps", "-f", format, main)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), goEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps: %v\n%s", err, stderr.String())
	}
	var pkgs []goPackage
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "|")
		if len(f) != 6 {
			continue // the standard library: an empty line
		}
		pkgs = append(pkgs, goPackage{importPath: f[0], dir: f[1], module: f[2], version: f[3], moduleDir: f[4], replacement: f[5]})
	}
	return pkgs, nil
}

// mainModule is AudioSmith's own module path.
const mainModule = "github.com/hehehai/AudioSmith"

// goComponents are the Go standard library and the modules of the linked
// packages, sorted by path, and the set of linked package paths (for the
// material some of them carry: extra.go).
func goComponents(root string) ([]component, map[string]bool, error) {
	pkgs, err := linkedPackages(root)
	if err != nil {
		return nil, nil, err
	}
	linked := map[string]bool{}
	modules := map[string]*component{}
	dirs := map[string][]string{} // module → its linked packages' directories
	moduleDirs := map[string]string{}
	for _, p := range pkgs {
		linked[p.importPath] = true
		if p.module == mainModule || p.module == "" {
			continue
		}
		if modules[p.module] == nil {
			c := &component{name: p.module, version: p.version, url: goModuleURL(p.module)}
			// A module replaced by another module (MyGo by ActNow's fork):
			// its source and version are the replacement's.
			if rpath, rversion, ok := strings.Cut(p.replacement, " "); ok && rversion != "" {
				c.version, c.url = rversion, goModuleURL(rpath)
			}
			modules[p.module] = c
			moduleDirs[p.module] = p.moduleDir
		}
		dirs[p.module] = append(dirs[p.module], p.dir)
	}
	std, err := goStdlib(root)
	if err != nil {
		return nil, nil, err
	}
	out := []component{std}
	for _, path := range sortedKeys(modules) {
		c := modules[path]
		spdx, ok := goLicences[path]
		if !ok {
			return nil, nil, fmt.Errorf("%s is linked into the app but has no licence in goLicences (internal/tools/thirdparty/tables.go): read its licence and add it", path)
		}
		c.spdx = spdx
		c.notes = goNotes(root, path)
		if c.texts, err = moduleTexts(moduleDirs[path], dirs[path]); err != nil {
			return nil, nil, fmt.Errorf("%s: %v", path, err)
		}
		if strings.HasPrefix(moduleDirs[path], root+string(filepath.Separator)) {
			rel, _ := filepath.Rel(root, moduleDirs[path])
			c.url += " (vendored: " + filepath.ToSlash(rel) + ")"
		}
		out = append(out, *c)
	}
	return out, linked, nil
}

// goStdlib is the Go standard library and runtime, which every Go program
// links, at the version go.mod asks for.
func goStdlib(root string) (component, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return component{}, err
	}
	m := regexp.MustCompile(`(?m)^go (\S+)`).FindSubmatch(data)
	if m == nil {
		return component{}, fmt.Errorf("go.mod has no go line")
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return component{}, fmt.Errorf("go env GOROOT: %v", err)
	}
	goroot := strings.TrimSpace(string(out))
	c := component{name: "Go standard library and runtime", version: "go" + string(m[1]), url: "https://go.googlesource.com/go", spdx: goLicences["std"]}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		body, err := os.ReadFile(filepath.Join(goroot, name))
		if err != nil {
			return component{}, err
		}
		c.texts = append(c.texts, text{name: name, body: string(body)})
	}
	return c, nil
}

// goModuleURL is where a module's source is.
func goModuleURL(path string) string {
	parts := strings.Split(path, "/")
	switch {
	case parts[0] == "github.com" && len(parts) >= 3:
		return "https://github.com/" + parts[1] + "/" + parts[2]
	case parts[0] == "golang.org" && len(parts) >= 3 && parts[1] == "x":
		return "https://go.googlesource.com/" + parts[2]
	}
	return "https://pkg.go.dev/" + path
}

// moduleTexts are a module's licence files: those at its root, then those
// in the directories of its linked packages and between them and the root
// (a package that carries code under another licence), sorted.
func moduleTexts(moduleDir string, pkgDirs []string) ([]text, error) {
	var top, sub []string
	files, err := licenceFiles(moduleDir)
	if err != nil {
		return nil, err
	}
	top = files
	seen := map[string]bool{moduleDir: true}
	for _, d := range pkgDirs {
		for ; d != moduleDir && strings.HasPrefix(d, moduleDir+string(filepath.Separator)) && !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			files, err := licenceFiles(d)
			if err != nil {
				return nil, err
			}
			sub = append(sub, files...)
		}
	}
	if len(top) == 0 {
		return nil, fmt.Errorf("no licence file in %s", moduleDir)
	}
	slices.Sort(sub)
	var texts []text
	for _, f := range append(top, sub...) {
		body, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(moduleDir, f)
		texts = append(texts, text{name: filepath.ToSlash(rel), body: string(body)})
	}
	return texts, nil
}

// licenceFiles are the licence and notice files in dir, sorted, LICENSE
// first.
func licenceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && isLicenceFile(e.Name()) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		return cmp.Or(cmp.Compare(licenceOrder(a), licenceOrder(b)), strings.Compare(a, b))
	})
	return out, nil
}

var licenceName = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|unlicense|patents)([-_.][a-z0-9.-]*)?$`)

// isLicenceFile tells a licence or notice file by its name (not a source
// file named license.go).
func isLicenceFile(name string) bool {
	if !licenceName.MatchString(name) {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case "", ".txt", ".md", ".markdown", ".rst", ".mit", ".bsd", ".apache", ".apache2", ".0":
		return true
	}
	return false
}

// licenceOrder puts the licence before the notices.
func licenceOrder(path string) int {
	n := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasPrefix(n, "licen"), strings.HasPrefix(n, "copying"), strings.HasPrefix(n, "unlicense"):
		return 0
	case strings.HasPrefix(n, "notice"):
		return 1
	}
	return 2
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
