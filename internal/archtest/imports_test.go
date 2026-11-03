package archtest

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The layering of internal/ (docs/architecture.md §1 and "Code structure
// rules"). Package names here are paths under internal/, "a/**" is a and
// the packages under it, "main" is the app in app/. Imports
// are read from every non-test file whatever its build constraints.
const mygoUI = "github.com/egoist/mygo/ui"

// leaves import no internal package: the bottom layer.
var leaves = []string{"core", "jobs", "settings", "i18n", "lang", "audio", "ffmpeg", "torchpt", "icons",
	"dictation", "engine/proto", "text", "dsp", "doc/textenc", "volume"}

// families import only their own packages: the DSP and text libraries.
var families = []string{"dsp", "text"}

// uiPackages may import MyGo's UI: the widgets and pages of the UI layer,
// and the icons drawn in it. The rest of internal/ is front-end free, for
// the Local API, MCP and tests.
var uiPackages = []string{"icons", "widgets", "pages/**"}

// mygoOther are the internal packages that use one of MyGo's other
// packages, and which.
var mygoOther = map[string]string{
	"audio":             "github.com/egoist/mygo/audio",
	"dictation":         "github.com/egoist/mygo/transfer",
	"store/sqlitestore": "github.com/egoist/mygo/plugins/sqlite",
}

// forbidden are imports that break the layering, and why.
var forbidden = []struct {
	from, to []string
	why      string
}{
	{[]string{"services/**"}, []string{"catalog", "hub", "settings", "engine", "engine/enginetest", "api", "mcp", "appcore", "apibackend"},
		"services get the model catalog, downloads, settings and the engine as functions in services.Config, which the app fills in: keep it so"},
	{[]string{"api/**", "mcp/**", "apibackend/**", "appcore/**", "services/**"}, []string{"widgets", "pages/**"},
		"the API, MCP and the services are front-end free"},
	{[]string{"tools/**"}, []string{"**"},
		"the commands in internal/tools stand alone (standard library only)"},
}

// testHelpers are the fakes for tests, which only tests and other fakes
// may import.
func testHelper(pkg string) bool {
	last := pkg[strings.LastIndexByte(pkg, '/')+1:]
	return strings.HasSuffix(last, "test") && last != "archtest"
}

// match reports whether pkg matches pattern: a/** is a and what is under
// it, ** anything.
func match(pattern, pkg string) bool {
	if pattern == "**" {
		return true
	}
	if base, ok := strings.CutSuffix(pattern, "/**"); ok {
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pkg == pattern
}

func matchAny(patterns []string, pkg string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool { return match(p, pkg) })
}

// edge is a package's import of another, both under internal/ (or "main").
type edge struct{ from, to string }

func (e edge) String() string { return e.from + " " + e.to }

// readEdges reads imports.txt: the imports of an internal package by
// another, a line each.
func readEdges(t *testing.T) map[edge]int {
	t.Helper()
	f, err := os.Open("imports.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	edges := map[edge]int{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		text, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			t.Fatalf("imports.txt:%d: want \"from to\", got %q", n, sc.Text())
		}
		e := edge{fields[0], fields[1]}
		if m, ok := edges[e]; ok {
			t.Fatalf("imports.txt:%d: %s is on line %d already", n, e, m)
		}
		edges[e] = n
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return edges
}

// TestImports checks the imports of the module's packages against the
// layering above and imports.txt.
func TestImports(t *testing.T) {
	_, module := moduleRoot(t)
	internal := module + "/internal/"
	listed := readEdges(t)
	found := map[edge]string{} // the edge and a file that makes it
	broken := map[edge]bool{}  // edges a rule forbids: no need to list them
	fail := map[string]bool{}
	failf := func(format string, args ...any) { fail[fmt.Sprintf(format, args...)] = true }
	for _, f := range goFiles(t) {
		if f.test() {
			continue
		}
		var from string
		switch dir := f.dir(); {
		case dir == ".":
			failf("%s is at the module root, which holds no Go files: the app (package main) is in app/, its embedded files in assets/.", f.path)
			continue
		case dir == "app":
			from = "main"
		case strings.HasPrefix(dir, "internal/"):
			from = strings.TrimPrefix(dir, "internal/")
		default:
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f.path, f.data, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if from == "archtest" {
				continue
			}
			if path == mygoUI || strings.HasPrefix(path, mygoUI+"/") {
				if from != "main" && !matchAny(uiPackages, from) {
					failf("%s imports %s (%s): only %s may use MyGo's UI. Move the UI code to the app (package main) or internal/widgets, "+
						"and pass this package what it needs.", from, path, f.path, strings.Join(uiPackages, ", "))
				}
				continue
			}
			if strings.HasPrefix(path, "github.com/egoist/mygo") && from != "main" {
				if want, ok := mygoOther[from]; !ok || want != path {
					failf("%s imports %s (%s): internal packages use MyGo's packages only as listed in mygoOther (internal/archtest/imports_test.go); "+
						"add it there if this one should, in its own commit.", from, path, f.path)
				}
				continue
			}
			to, ok := strings.CutPrefix(path, internal)
			if !ok {
				continue
			}
			if testHelper(to) && !testHelper(from) {
				failf("%s imports the test fake %s (%s): fakes are for tests (_test.go files) and other fakes only.", from, to, f.path)
			}
			if from == "main" {
				continue
			}
			e := edge{from, to}
			if _, ok := found[e]; !ok {
				found[e] = f.path
			}
			for _, r := range forbidden {
				if matchAny(r.from, from) && matchAny(r.to, to) {
					broken[e] = true
					failf("%s imports %s (%s), which it may not: %s.", from, to, f.path, r.why)
				}
			}
			if slices.Contains(leaves, from) {
				broken[e] = true
				failf("%s imports %s (%s): %s is a leaf package, which imports no internal package (internal/archtest/imports_test.go: leaves). "+
					"Move what it needs into it, or pass it in.", from, to, f.path, from)
			}
			for _, fam := range families {
				if match(fam+"/**", from) && !match(fam+"/**", to) {
					broken[e] = true
					failf("%s imports %s (%s): the packages of internal/%s import only one another.", from, to, f.path, fam)
				}
			}
			if match("pages/**", from) && match("pages/**", to) && pageOf(from) != pageOf(to) {
				broken[e] = true
				failf("%s imports %s (%s): a page reaches another only through its Host, which the app implements.", from, to, f.path)
			}
		}
	}
	for e, file := range found {
		if _, ok := listed[e]; !ok && !broken[e] {
			failf("%s imports %s (%s), which internal/archtest/imports.txt does not list: if this dependency is right "+
				"(docs/architecture.md, \"Code structure rules\"), add the line \"%s\" there so that review sees it; "+
				"otherwise pass the package what it needs instead.", e.from, e.to, file, e)
		}
	}
	for e, n := range listed {
		if _, ok := found[e]; !ok {
			failf("%s no longer imports %s: remove line %d of internal/archtest/imports.txt.", e.from, e.to, n)
		}
	}
	msgs := make([]string, 0, len(fail))
	for m := range fail {
		msgs = append(msgs, m)
	}
	slices.Sort(msgs)
	for _, m := range msgs {
		t.Error(m)
	}
}

// pageOf is the page package pkg is in: pages/<page>.
func pageOf(pkg string) string {
	parts := strings.SplitN(pkg, "/", 3)
	if len(parts) < 2 {
		return pkg
	}
	return parts[0] + "/" + parts[1]
}

// TestImportRules checks the rules' own logic.
func TestImportRules(t *testing.T) {
	for _, c := range []struct {
		pattern, pkg string
		want         bool
	}{
		{"pages/**", "pages", true}, {"pages/**", "pages/calls", true}, {"pages/**", "pagesx", false},
		{"dsp", "dsp/wav", false}, {"**", "core", true},
	} {
		if got := match(c.pattern, c.pkg); got != c.want {
			t.Errorf("match(%q, %q) = %v", c.pattern, c.pkg, got)
		}
	}
	for pkg, want := range map[string]bool{"engine/enginetest": true, "dub/dubtest": true, "archtest": false, "core": false} {
		if testHelper(pkg) != want {
			t.Errorf("testHelper(%q) = %v", pkg, !want)
		}
	}
	if pageOf("pages/calls/x") != "pages/calls" {
		t.Error(pageOf("pages/calls/x"))
	}
}
