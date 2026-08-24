package census

import (
	"net/http"

	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/cmd/http/middleware"
	"github.com/mihaiflorentin88/ffxiv-census/container"
)

// Register mounts the versioned census REST API on mux. Dependencies are
// resolved from the global container at registration time, keeping the
// controllers free of infrastructure imports.
func Register(mux *http.ServeMux) {
	cfg := container.Load.Config()
	var env, token string
	if cfg != nil {
		env = cfg.App.Env
		if cfg.Auth != nil {
			token = cfg.Auth.Token
		}
	}

	auth := middleware.APIAuth(env, token)
	handle := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(h))
	}

	svc := container.Load.CensusService()
	c := handler.NewCensusController(svc, container.Load.UIStatsService())

	handle("GET /api/v1/census/latest", c.Latest)
	handle("GET /api/v1/census/characters", c.List)
	handle("GET /api/v1/census/export", c.Export)
	handle("GET /api/v1/census/characters/{id}", c.Get)
	handle("GET /api/v1/stats/breakdown", c.Breakdown)
	handle("GET /api/v1/stats/new-characters", c.NewCharacters)
	handle("GET /api/v1/stats/expansion", c.Expansion)
}
