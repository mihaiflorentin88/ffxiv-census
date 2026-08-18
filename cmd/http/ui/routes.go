package ui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/mihaiflorentin88/ffxiv-census/container"
)

//go:embed templates/* templates/partials/* assets/*
var content embed.FS

// Register mounts the UI routes and static assets on mux.
func Register(mux *http.ServeMux) {
	svc := container.Load.CensusService()
	q := container.Load.Queue()
	ctrl := NewUIController(svc, q)
	ctrl.RegisterRoutes(mux)

	assets, err := fs.Sub(content, "assets")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(assets))
	mux.Handle("GET /ui/assets/", http.StripPrefix("/ui/assets/", fileServer))
}

// RegisterRoutes mounts all controller routes to the mux.
func (c *UIController) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/dashboard", http.StatusFound)
	})
	mux.HandleFunc("GET /ui/dashboard", c.Dashboard)
	mux.HandleFunc("GET /ui/partials/world-breakdown", c.WorldDrilldown)
	mux.HandleFunc("GET /ui/races", c.Races)
	mux.HandleFunc("GET /ui/worlds/{world}", c.WorldDetail)
	mux.HandleFunc("GET /ui/worlds", c.Worlds)
	mux.HandleFunc("GET /ui/expansions", c.Expansions)
	mux.HandleFunc("GET /ui/methodology", c.Methodology)
	// Personal info routes are currently disabled to protect player privacy.
	// mux.HandleFunc("GET /ui/characters/{id}", c.CharacterDetail)
	// mux.HandleFunc("GET /ui/characters", c.CharacterList)
	// mux.HandleFunc("GET /ui/characters/search", c.CharacterSearch)
	// mux.HandleFunc("GET /ui/free-companies/{id}", c.FreeCompanyDetail)
	// mux.HandleFunc("GET /ui/free-companies", c.FreeCompanyList)
}
