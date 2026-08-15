package http

import (
    "net/http"

    examplehandler "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/example/handler"
    roothandler "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/root/handler"
    ui "github.com/mihaiflorentin88/ffxiv-census/cmd/http/ui"
)

const RouteHealth = "/health"
const RouteExample = "/example"

type Router struct {
    HealthController  roothandler.Controller
    ExampleHandler    examplehandler.Handler
}

func NewRouter() Router {
    return Router{
        HealthController: roothandler.NewController(),
        ExampleHandler:   examplehandler.New(),
    }
}

func (r *Router) RegisterRoutes(mux *http.ServeMux) {
    mux.HandleFunc(RouteHealth, r.HealthController.Check)
    mux.Handle(RouteExample, r.ExampleHandler)
    ui.Register(mux)
}
