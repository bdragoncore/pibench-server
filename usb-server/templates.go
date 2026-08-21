package main

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"sync"
)

//go:embed web/*.html
var webFS embed.FS

var (
	tmplOnce  sync.Once
	pagesTmpl map[string]*template.Template
	fragsTmpl map[string]*template.Template
)

// loadTemplates parses and caches HTML page templates and HTMX fragment partials once on startup.
func loadTemplates() {
	tmplOnce.Do(func() {
		pagesTmpl = map[string]*template.Template{}
		fragsTmpl = map[string]*template.Template{}

		base := template.Must(template.ParseFS(webFS, "web/base.html"))

		pages := []string{"index", "shell", "opencode", "gpio", "piscope", "tools"}
		for _, p := range pages {
			t := template.Must(base.Clone())
			t = template.Must(t.ParseFS(webFS, "web/"+p+".html"))
			pagesTmpl[p] = t
			pagesTmpl[p+".html"] = t
		}

		frags := []string{"status", "networks", "message", "gpio_pins", "reverse_ssh"}
		for _, f := range frags {
			t := template.Must(template.ParseFS(webFS, "web/"+f+".html"))
			fragsTmpl[f] = t
			fragsTmpl[f+".html"] = t
		}
	})
}

// renderPage executes a full HTML layout template extending web/base.html.
func renderPage(w http.ResponseWriter, page string, data any) {
	loadTemplates()
	t, ok := pagesTmpl[page]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown page template %q", page), http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "base.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderFragment executes an isolated HTMX HTML fragment snippet without base layout wrappers.
func renderFragment(w http.ResponseWriter, name string, data any) {
	loadTemplates()
	t, ok := fragsTmpl[name]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown fragment template %q", name), http.StatusInternalServerError)
		return
	}
	if err := t.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
