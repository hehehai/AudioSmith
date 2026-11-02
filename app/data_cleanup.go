package main

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cleanPartFiles removes the temporary files of writes that never
// finished ("."+name+".*.part": a recording or an import cut short by a
// crash or a quit, the engine's atomic writes of a job it was stopped in;
// name+".tmp" and name+".tmp.wav", Go's atomic writes: code review 1102
// M2)
// from the folders given and every folder below them, those older than
// minAge (a write in progress is younger). They were never cleaned
// before (family audit A13); below the first level, not until #101 L3
// (cache chunks, dub renders).
func cleanPartFiles(minAge time.Duration, dirs ...string) int {
	n := 0
	for _, root := range dirs {
		if root == "" {
			continue
		}
		filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: skipped
			}
			name := e.Name()
			if e.IsDir() {
				if path != root && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !e.Type().IsRegular() || !leftover(name) {
				return nil
			}
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > minAge && os.Remove(path) == nil {
				n++
			}
			return nil
		})
	}
	if n > 0 {
		log.Printf("data: removed %d unfinished temporary files", n)
	}
	return n
}

// leftover reports a temporary file's name (cleanPartFiles).
func leftover(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".part") ||
		strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".tmp.wav")
}

// leftoverAge is how old a leftover must be to go: a write in progress
// is younger.
const leftoverAge = time.Hour

// cleanLeftovers removes the leftovers of writes in the data and cache
// folders, and what renders put in the cache's tmp folder (a joined
// chapter, a mastered take: as big as a chapter, deleted by the render
// that made it unless it was cut short), all older than leftoverAge.
func cleanLeftovers(data, cache string) {
	cleanPartFiles(leftoverAge, data, cache)
	tmp := filepath.Join(cache, "tmp")
	ents, _ := os.ReadDir(tmp)
	n := 0
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > leftoverAge && os.RemoveAll(filepath.Join(tmp, e.Name())) == nil {
			n++
		}
	}
	if n > 0 {
		log.Printf("data: removed %d leftovers of renders from %s", n, tmp)
	}
}
