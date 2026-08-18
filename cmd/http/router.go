package http

import (
	"context"
	"fmt"
	"net/http"

	census "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/census"
	roothandler "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/root/handler"
	ui "github.com/mihaiflorentin88/ffxiv-census/cmd/http/ui"
	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const (
	RouteHealth      = "/health"
	RouteHealthLive  = "/health/live"
	RouteHealthReady = "/health/ready"
	RouteMetrics     = "/metrics"
)

type Router struct {
	HealthController  roothandler.Controller
	MetricsController roothandler.MetricsController
}
type databasePinger struct {
	driver contract.DatabaseDriver
}

func (s *databasePinger) PingContext(ctx context.Context) error {
	if s.driver == nil {
		return fmt.Errorf("database driver uninitialized")
	}
	_, err := s.driver.FetchOne(ctx, "SELECT 1")
	return err
}

func NewRouter() Router {
	reg := container.Load.PrometheusRegistry()
	db := container.Load.Database()

	var opts []roothandler.HealthOption
	if db != nil {
		opts = append(opts, roothandler.WithDatabasePinger(&databasePinger{driver: db}))
	}

	return Router{
		HealthController:  roothandler.NewHealthController(opts...),
		MetricsController: roothandler.NewMetricsController(reg),
	}
}

func (r *Router) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(RouteHealth, r.HealthController.Check)
	mux.HandleFunc(RouteHealthLive, r.HealthController.Check)
	mux.HandleFunc(RouteHealthReady, r.HealthController.Ready)
	mux.HandleFunc(RouteMetrics, r.MetricsController.Metrics)
	census.Register(mux)
	ui.Register(mux)
}
