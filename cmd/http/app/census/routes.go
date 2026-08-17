package census

import (
	"net/http"

	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/container"
)

// Register mounts the versioned census REST API on mux. Dependencies are
// resolved from the global container at registration time, keeping the
// controllers free of infrastructure imports.
func Register(mux *http.ServeMux) {
	svc := container.Load.CensusService()
	q := container.Load.Queue()
	c := handler.NewCensusController(svc)
	qc := handler.NewQueueController(q)
	mux.HandleFunc("GET /api/v1/census/latest", c.Latest)
	mux.HandleFunc("GET /api/v1/census/characters", c.List)
	mux.HandleFunc("GET /api/v1/census/characters/{id}", c.Get)
	mux.HandleFunc("GET /api/v1/stats/breakdown", c.Breakdown)
	mux.HandleFunc("GET /api/v1/stats/new-characters", c.NewCharacters)
	mux.HandleFunc("GET /api/v1/stats/expansion", c.Expansion)
	mux.HandleFunc("GET /api/v1/queue", qc.Depth)
}
