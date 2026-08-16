package container

import (
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type InfrastructureContainer struct {
	httpClient   contract.HTTPClient
	statsd       contract.StatsdClient
	sqliteDriver contract.SQLiteDriver
}

func (s *ServiceContainer) HTTPClient() contract.HTTPClient {
	if s.infrastructure.httpClient != nil {
		return s.infrastructure.httpClient
	}
	client := httpclient.New(nil)
	s.infrastructure.httpClient = client
	return client
}

func (s *ServiceContainer) Statsd() contract.StatsdClient {
	if s.infrastructure.statsd != nil {
		return s.infrastructure.statsd
	}
	cfg := s.Config().Metrics
	if cfg == nil {
		logging.Warn("container.metrics", "metrics config missing")
		return nil
	}
	client, err := metrics.New(cfg.Endpoint, cfg.Prefix)
	if err != nil {
		logging.Error("container.metrics", fmt.Sprintf("failed to create statsd client: %v", err))
		return nil
	}
	s.infrastructure.statsd = client
	return client
}

func (s *ServiceContainer) SQLite() contract.SQLiteDriver {
	if s.infrastructure.sqliteDriver != nil {
		return s.infrastructure.sqliteDriver
	}
	cfg := s.Config().SQLite
	if cfg == nil {
		logging.Warn("container.sqlite", "sqlite config missing")
		return nil
	}
	driver, err := sqlite.NewDriver(cfg, sqlitemigration.FS())
	if err != nil {
		logging.Error("container.sqlite", fmt.Sprintf("failed to create sqlite driver: %v", err))
		return nil
	}
	s.infrastructure.sqliteDriver = driver
	return driver
}
