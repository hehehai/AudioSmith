package icons

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestGenerated checks that solar_gen.go holds every icon names.txt lists,
// so a name added without go generate fails here.
func TestGenerated(t *testing.T) {
	src, err := os.ReadFile("solar_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("names.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		name, _, _ := strings.Cut(sc.Text(), "#")
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		n++
		if !strings.Contains(string(src), "solar("+strconv.Quote(name)+",") {
			t.Errorf("%s is in names.txt but not generated: run go generate", name)
		}
	}
	if got := strings.Count(string(src), " = solar("); got != n {
		t.Errorf("solar_gen.go has %d icons, names.txt %d: run go generate", got, n)
	}
	if Soundwave.Linear == nil || Soundwave.Bold == nil || Soundwave.Get(true) != Soundwave.Bold {
		t.Error("variants")
	}
}

// TestHugeicons checks that Play and Pause are the Hugeicons ones, not
// Solar's, in both variants.
func TestHugeicons(t *testing.T) {
	for _, ic := range []Icon{Play, Pause} {
		if !strings.HasPrefix(ic.Name, "hugeicons:") || ic.Linear == nil || ic.Bold == nil || ic.Linear == ic.Bold {
			t.Errorf("%q: want a Hugeicon with two variants", ic.Name)
		}
	}
	if Play.Name != "hugeicons:play" || Pause.Name != "hugeicons:pause" {
		t.Errorf("names %q %q", Play.Name, Pause.Name)
	}
}
