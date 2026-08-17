package http

import (
	"net/http"

	census "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/census"
	roothandler "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/root/handler"
	ui "github.com/mihaiflorentin88/ffxiv-census/cmd/http/ui"
)

const RouteHealth = "/health"

type Router struct {
	HealthController roothandler.Controller
}

func NewRouter() Router {
	return Router{
		HealthController: roothandler.NewController(),
	}
}

func (r *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(RouteHealth, r.HealthController.Check)
	census.Register(mux)
	ui.Register(mux)
}
