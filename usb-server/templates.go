package main

import (
	"html/template"
	"net/http"
	"sync"
)

var (
	tmplOnce sync.Once
	tmpls    map[string]*template.Template
)

func loadTemplates() map[string]*template.Template {
	tmplOnce.Do(func() {
		tmpls = map[string]*template.Template{}
		base := template.Must(template.ParseFiles("web/base.html"))
		for _, page := range []string{"index", "shell", "opencode", "gpio", "piscope", "tools"} {
			t := template.Must(base.Clone())
			t = template.Must(t.ParseFiles("web/" + page + ".html"))
			tmpls[page] = t
		}
		for _, frag := range []string{"status", "networks", "message", "gpio_pins", "reverse_ssh"} {
			t := template.Must(template.ParseFiles("web/" + frag + ".html"))
			tmpls[frag] = t
		}
	})
	return tmpls
}

func renderPage(w http.ResponseWriter, page string, data any) {
	t, ok := loadTemplates()[page]
	if !ok {
		http.Error(w, "unknown template", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "base.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func renderFragment(w http.ResponseWriter, name string, data any) {
	t, ok := loadTemplates()[name]
	if !ok {
		http.Error(w, "unknown template", http.StatusInternalServerError)
		return
	}
	if err := t.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
