package translate

import (
	"bytes"
	"embed"
	"encoding/json"
	"strings"
	"sync"
	"text/template"

	"github.com/hehehai/AudioSmith/internal/llm"
)

// The prompts are Go templates in prompts/*.tmpl, written for AudioSmith.
// Each skill has "<name>.system" and "<name>.user"; per-language notes are
// "notes:<variant>" / "notes:<code>" and "cleanup:<code>".

//go:embed prompts/*.tmpl
var promptFS embed.FS

var prompts = sync.OnceValue(func() *template.Template {
	funcs := template.FuncMap{
		"json": func(v any) (string, error) {
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", " ")
			err := enc.Encode(v)
			return strings.TrimSpace(b.String()), err
		},
		"join": strings.Join,
	}
	return template.Must(template.New("prompts").Funcs(funcs).ParseFS(promptFS, "prompts/*.tmpl"))
})

// render executes template name with data.
func render(name string, data any) (string, error) {
	var b strings.Builder
	if err := prompts().ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.ReplaceAll(b.String(), "\r\n", "\n")), nil
}

// has reports whether a template is defined.
func has(name string) bool { return prompts().Lookup(name) != nil }

// messages renders "<name>.system" and "<name>.user" into a chat.
func messages(name string, data any) ([]llm.Message, error) {
	sys, err := render(name+".system", data)
	if err != nil {
		return nil, err
	}
	user, err := render(name+".user", data)
	if err != nil {
		return nil, err
	}
	return []llm.Message{{Role: llm.System, Content: sys}, {Role: llm.User, Content: user}}, nil
}

// langNotes renders the most specific "<prefix><variant>" or "<prefix><code>" template for l.
func langNotes(prefix string, l Lang) string {
	for _, k := range []string{prefix + l.Variant(), prefix + l.Code} {
		if has(k) {
			s, err := render(k, l)
			if err == nil {
				return s
			}
		}
	}
	return ""
}
