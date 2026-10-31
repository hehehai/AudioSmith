package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// extraComponents is the rest of what the app carries: the native
// libraries of MyGo's plugins (SQLite), RapiDoc and the libraries in its
// bundle, the Solar icons, the two Hugeicons, the voice previews and the dictation sounds of
// official builds, in that order (each group sorted). Each is listed when the package that embeds it is linked.
func extraComponents(root string, linked map[string]bool) ([]component, error) {
	var out []component
	add := func(cs []component, err error) error {
		out = append(out, cs...)
		return err
	}
	for _, step := range []func() ([]component, error){
		func() ([]component, error) { return pluginLibraries(root, linked) },
		func() ([]component, error) { return rapiDoc(root, linked) },
		func() ([]component, error) { return solarIcons(root, linked) },
		func() ([]component, error) { return hugeicons(root, linked) },
		func() ([]component, error) { return voicePreviews(root, linked) },
		func() ([]component, error) { return galleryVoices(root) },
		func() ([]component, error) { return familySounds(root, linked) },
	} {
		if err := add(step()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// pluginLibraries are the native libraries MyGo plugins ship in the app's
// resources (their mygo-plugin.json): today SQLite, in MyGo's SQLite
// plugin.
func pluginLibraries(root string, linked map[string]bool) ([]component, error) {
	const mygo = "github.com/egoist/mygo/"
	var out []component
	for _, pkg := range sortedKeys(linked) {
		rel, ok := strings.CutPrefix(pkg, mygo)
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "third_party", "mygo", filepath.FromSlash(rel), "mygo-plugin.json"))
		if err != nil {
			continue // not a plugin with native libraries
		}
		var plugin struct {
			Libraries []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"libraries"`
		}
		if err := json.Unmarshal(data, &plugin); err != nil {
			return nil, fmt.Errorf("%s/mygo-plugin.json: %v", pkg, err)
		}
		for _, lib := range plugin.Libraries {
			if lib.Name != "mygo-sqlite3" {
				return nil, fmt.Errorf("%s ships the native library %s, which the generator does not know: add it to pluginLibraries (internal/tools/thirdparty/extra.go)", pkg, lib.Name)
			}
			m := regexp.MustCompile(`sqlite-amalgamation-(\d)(\d\d)(\d\d)\d\d\.zip$`).FindStringSubmatch(lib.Source)
			if m == nil {
				return nil, fmt.Errorf("%s: no SQLite version in %q", pkg, lib.Source)
			}
			minor, _ := strconv.Atoi(m[2])
			patch, _ := strconv.Atoi(m[3])
			body, err := readFile(root, "third_party/licenses/texts/SQLite-blessing.txt")
			if err != nil {
				return nil, err
			}
			out = append(out, component{
				name: "SQLite", version: fmt.Sprintf("%s.%d.%d", m[1], minor, patch), url: "https://sqlite.org/copyright.html", spdx: "blessing",
				notes: []string{"In lib" + lib.Name + ".dylib, which MyGo's SQLite plugin (" + pkg + ", MIT) builds from SQLite's amalgamation and a shim of its own and the app carries in its resources."},
				texts: []text{{name: "Public domain dedication", body: body}},
			})
		}
	}
	return out, nil
}

// rapiDoc is the Local API's reference page (internal/api/docs): RapiDoc
// and the libraries its bundle includes (third_party/licenses/rapidoc).
func rapiDoc(root string, linked map[string]bool) ([]component, error) {
	if !linked[mainModule+"/internal/api"] {
		return nil, nil
	}
	js, err := readFile(root, "internal/api/docs/rapidoc-min.js")
	if err != nil {
		return nil, err
	}
	m := regexp.MustCompile(`rapidoc v(\S+)`).FindStringSubmatch(js[:min(len(js), 400)])
	if m == nil {
		return nil, fmt.Errorf("internal/api/docs/rapidoc-min.js: no version in its banner")
	}
	dir := "third_party/licenses/rapidoc"
	pinned, err := readFile(root, dir+"/RAPIDOC-VERSION")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(pinned) != m[1] {
		return nil, fmt.Errorf("RapiDoc is %s but %s has the licences of the libraries in RapiDoc %s's bundle: refresh them (third_party/licenses/README.md)", m[1], dir, strings.TrimSpace(pinned))
	}
	lic, err := readFile(root, "internal/api/docs/RAPIDOC-LICENSE.txt")
	if err != nil {
		return nil, err
	}
	out := []component{{
		name: "RapiDoc", version: m[1], url: "https://github.com/rapi-doc/RapiDoc", spdx: "MIT",
		notes: []string{"The Local API's reference page (internal/api/docs/rapidoc-min.js, unmodified). Its bundle includes the libraries listed right after it."},
		texts: []text{{name: "LICENSE", body: lic}},
	}}
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := describedComponent(root, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		c.notes = append(c.notes, "In RapiDoc's bundle: a dependency of RapiDoc "+m[1]+"; the version is the newest in RapiDoc's range when it was published, as the bundle does not record it.")
		out = append(out, c)
	}
	return out, nil
}

// describedComponent reads a directory of third_party/licenses with a
// COMPONENT file (name, version, url, spdx, note lines) and its licence
// files.
func describedComponent(root, dir string) (component, error) {
	f, err := os.Open(filepath.Join(root, dir, "COMPONENT"))
	if err != nil {
		return component{}, err
	}
	defer f.Close()
	var c component
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "name":
			c.name = v
		case "version":
			c.version = v
		case "url":
			c.url = v
		case "spdx":
			c.spdx = v
		case "note":
			c.notes = append(c.notes, v)
		}
	}
	files, err := licenceFiles(filepath.Join(root, dir))
	if err != nil {
		return c, err
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return c, err
		}
		c.texts = append(c.texts, text{name: filepath.Base(file), body: string(body)})
	}
	return c, sc.Err()
}

// solarIcons are the icons of internal/icons (CC BY 4.0, which asks for
// attribution and the licence's URI).
func solarIcons(root string, linked map[string]bool) ([]component, error) {
	if !linked[mainModule+"/internal/icons"] {
		return nil, nil
	}
	src, err := readFile(root, "internal/icons/solar_gen.go")
	if err != nil {
		return nil, err
	}
	m := regexp.MustCompile(`const SolarVersion = "([^"]+)"`).FindStringSubmatch(src)
	if m == nil {
		return nil, fmt.Errorf("internal/icons/solar_gen.go: no SolarVersion")
	}
	return []component{{
		name: "Solar icon set", version: "@iconify-json/solar " + m[1], url: "https://icon-sets.iconify.design/solar/", spdx: "CC-BY-4.0",
		notes: []string{"Only the icons AudioSmith uses, converted to Go source (internal/icons/solar_gen.go, by internal/icons/gen); their shapes are unchanged."},
		texts: []text{{name: "Attribution", body: "Solar icon set by 480 Design\n(https://www.figma.com/community/file/1166831539721848736),\nlicensed under the Creative Commons Attribution 4.0 International\nlicence (CC BY 4.0): https://creativecommons.org/licenses/by/4.0/\n(the full licence: https://creativecommons.org/licenses/by/4.0/legalcode).\nTaken from the npm package @iconify-json/solar " + m[1] + " (Iconify).\n"}},
	}}, nil
}

// hugeicons are internal/icons' Play and Pause (hugeicons.go), from
// @iconify-json/hugeicons under the MIT licence.
func hugeicons(root string, linked map[string]bool) ([]component, error) {
	if !linked[mainModule+"/internal/icons"] {
		return nil, nil
	}
	src, err := readFile(root, "internal/icons/hugeicons.go")
	if err != nil {
		return nil, err
	}
	m := regexp.MustCompile(`const HugeiconsVersion = "([^"]+)"`).FindStringSubmatch(src)
	if m == nil {
		return nil, fmt.Errorf("internal/icons/hugeicons.go: no HugeiconsVersion")
	}
	lic, err := readFile(root, "third_party/licenses/texts/MIT-Hugeicons.txt")
	if err != nil {
		return nil, err
	}
	return []component{{
		name: "Hugeicons", version: "@iconify-json/hugeicons " + m[1], url: "https://icon-sets.iconify.design/hugeicons/", spdx: "MIT",
		notes: []string{"Only the icons play and pause (stroke-rounded), copied into Go source (internal/icons/hugeicons.go); the Linear variant is unchanged, the Bold one fills the same shape."},
		texts: []text{{name: "LICENSE", body: lic}},
	}}, nil
}

// voiceModels are the models whose speakers the voice previews are, by
// the prefix of their file names (assets/voice-previews/README.md).
var voiceModels = map[string]struct{ name, url string }{
	"kokoro":    {"Kokoro 82M", "https://huggingface.co/hexgrad/Kokoro-82M"},
	"qwen3_tts": {"Qwen3-TTS 12Hz 0.6B CustomVoice", "https://huggingface.co/Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice"},
}

// voicePreviews are the clips of assets/voice-previews: the models' own
// synthetic speakers, Apache-2.0 like the models.
func voicePreviews(root string, linked map[string]bool) ([]component, error) {
	files, err := filepath.Glob(filepath.Join(root, "assets", "voice-previews", "*.ogg"))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	count := map[string]int{}
	for _, f := range files {
		prefix, _, _ := strings.Cut(filepath.Base(f), "-")
		if _, ok := voiceModels[prefix]; !ok {
			return nil, fmt.Errorf("assets/voice-previews/%s: the model %q is not in voiceModels (internal/tools/thirdparty/extra.go): add it with its licence", filepath.Base(f), prefix)
		}
		count[prefix]++
	}
	apache, err := readFile(root, "third_party/licenses/texts/Apache-2.0.txt")
	if err != nil {
		return nil, err
	}
	var out []component
	for _, prefix := range sortedKeys(count) {
		m := voiceModels[prefix]
		out = append(out, component{
			name: "Voice previews: " + m.name, version: fmt.Sprintf("%d clips", count[prefix]), url: m.url, spdx: "Apache-2.0",
			notes: []string{"Short clips of the model's built-in synthetic speakers (assets/voice-previews/" + prefix + "-*.ogg), made with AudioSmith's engine by scripts/make-voice-previews.sh. They are not recordings of people."},
			texts: []text{{name: "The model's licence", body: apache}},
		})
	}
	return out, nil
}

// galleryVoices are the curated voices' clips of assets/gallery-voices
// (thread #94): references and previews generated with Qwen3-TTS
// VoiceDesign and Base, Apache-2.0 like the models.
func galleryVoices(root string) ([]component, error) {
	files, err := filepath.Glob(filepath.Join(root, "assets", "gallery-voices", "*.ogg"))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	apache, err := readFile(root, "third_party/licenses/texts/Apache-2.0.txt")
	if err != nil {
		return nil, err
	}
	return []component{{
		name: "Discover voices' clips: Qwen3-TTS 12Hz 1.7B VoiceDesign and Base", version: fmt.Sprintf("%d clips", len(files)),
		url: "https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign", spdx: "Apache-2.0",
		notes: []string{"The curated voices' references and previews (assets/gallery-voices/*.ogg): voices designed from written descriptions with Qwen3-TTS 1.7B VoiceDesign, previews cloned from them with Qwen3-TTS 1.7B Base (https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-Base), made with AudioSmith's engine by scripts/make-curated-voices.sh. They are synthetic voices, not recordings of people."},
		texts: []text{{name: "The models' licence", body: apache}},
	}}, nil
}

// familySounds are the dictation's sounds of official builds: React Bits
// Sound Lab's, from the ActNow family's private library
// (scripts/sync-sounds.sh), listed when actnowkit's sounds package (their
// player) is linked. They are not in the repository.
func familySounds(root string, linked map[string]bool) ([]component, error) {
	if !linked["github.com/hehehai/actnowkit/sounds"] {
		return nil, nil
	}
	body, err := readFile(root, "third_party/licenses/texts/React-Bits.md")
	if err != nil {
		return nil, err
	}
	return []component{{
		name: "React Bits Sound Lab", version: "sounds exported 2026-10-20", url: "https://reactbits.dev/tools/sound-lab", spdx: "MIT WITH Commons-Clause",
		notes: []string{"The sounds of dictation in official builds (Contents/Resources/sounds), from the ActNow family's private sound library: used inside the app, not redistributed on their own, and not in AudioSmith's source or under its licence. A build from source plays system sounds."},
		texts: []text{{name: "LICENSE.md", body: body}},
	}}, nil
}
