package ui

import (
    "embed"
    "html/template"
    "io/fs"
    "net/http"

    "github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
    "github.com/mihaiflorentin88/ffxiv-census/container"
)

//go:embed templates/*.html assets/*
var content embed.FS

func Register(mux *http.ServeMux) {
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        http.Redirect(w, r, "/ui/dashboard", http.StatusFound)
    })
    mux.HandleFunc("/ui/dashboard", renderTemplate("templates/dashboard.html"))
    assets, err := fs.Sub(content, "assets")
    if err != nil {
        panic(err)
    }
    fileServer := http.FileServer(http.FS(assets))
    mux.Handle("/ui/assets/", http.StripPrefix("/ui/assets/", fileServer))
}

func renderTemplate(path string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        tmpl, err := template.ParseFS(content, path)
        if err != nil {
            logging.Error("ui.render", err.Error())
            http.Error(w, "template error", http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        if err := tmpl.Execute(w, map[string]any{"App": container.Load.Config().App.Name}); err != nil {
            logging.Error("ui.render", err.Error())
        }
    }
}
