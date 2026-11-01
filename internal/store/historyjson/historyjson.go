// Package historyjson reads the MVP's take library: WAVs in <dir>/clips
// and an index, <dir>/history.json. The SQLite store imports old
// histories with Read.
package historyjson

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

// Read returns the takes of dir/history.json whose audio still exists,
// newest first; none when there is no index. The SQLite importer uses it.
func Read(dir string) ([]core.Take, error) {
	data, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var takes []core.Take
	if err := json.Unmarshal(data, &takes); err != nil {
		return nil, err
	}
	kept := takes[:0]
	for _, t := range takes {
		if _, err := os.Stat(filepath.Join(dir, "clips", t.File)); err == nil {
			kept = append(kept, t)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].CreatedAt.After(kept[j].CreatedAt) })
	return kept, nil
}

// NewTakeID is the MVP's take id: sortable, and the audio's file name.
// New code should prefer core.NewID; the two coexist (ids are opaque).
func NewTakeID(now time.Time) string {
	return now.Format("20060102-150405.000")
}
