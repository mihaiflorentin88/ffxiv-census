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
type sqlitePinger struct {
	driver contract.SQLiteDriver
}

func (s *sqlitePinger) PingContext(ctx context.Context) error {
	if s.driver == nil {
		return fmt.Errorf("sqlite driver uninitialized")
	}
	_, err := s.driver.FetchOne(ctx, "SELECT 1")
	return err
}

func NewRouter() Router {
	reg := container.Load.PrometheusRegistry()
	sqlite := container.Load.SQLite()

	var opts []roothandler.HealthOption
	if sqlite != nil {
		opts = append(opts, roothandler.WithDatabasePinger(&sqlitePinger{driver: sqlite}))
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
