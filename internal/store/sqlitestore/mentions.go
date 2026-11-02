package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Mentioned returns the names of the files directly in dir that the
// library names anywhere in its text columns (as dir/<name>, as plain
// text or inside JSON strings): takes' recipes, voices, projects,
// renders, dubs, drafts, jobs' params. What it does not name, nothing in
// the library uses (code review 1102 M3: the clone flow's working clips,
// replaced covers).
func (s *Store) Mentioned(ctx context.Context, dir string) (map[string]bool, error) {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return nil, fmt.Errorf("sqlitestore: Mentioned needs a folder")
	}
	tables, err := s.db.Query(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND sql NOT LIKE 'CREATE VIRTUAL%'
		AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts%'`)
	if err != nil {
		return nil, wrap("mentioned", err)
	}
	prefixes := []string{dir + "/"}
	if b, _ := json.Marshal(dir); string(b[1:len(b)-1]) != dir {
		prefixes = append(prefixes, string(b[1:len(b)-1])+"/")
	}
	out := map[string]bool{}
	for _, t := range tables.Values {
		table := asString(t[0])
		cols, err := s.db.Query(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
		if err != nil {
			return nil, wrap("mentioned", err)
		}
		for _, c := range cols.Values {
			col, typ := asString(c[1]), strings.ToUpper(asString(c[2]))
			if typ != "TEXT" && typ != "" {
				continue
			}
			for _, prefix := range prefixes {
				rows, err := s.db.Query(ctx, fmt.Sprintf("SELECT %q FROM %q WHERE instr(%q, ?) > 0", col, table, col), prefix)
				if err != nil {
					return nil, wrap("mentioned", err)
				}
				for _, r := range rows.Values {
					namesAfter(asString(r[0]), prefix, out)
				}
			}
		}
	}
	return out, nil
}

// namesAfter adds to out the file names that follow prefix in v.
func namesAfter(v, prefix string, out map[string]bool) {
	for {
		i := strings.Index(v, prefix)
		if i < 0 {
			return
		}
		v = v[i+len(prefix):]
		end := strings.IndexAny(v, "\"\\'/\n\r\t")
		name := v
		if end >= 0 {
			name = v[:end]
		}
		if name != "" {
			out[name] = true
		}
	}
}
