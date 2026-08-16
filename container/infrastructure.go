package container

import (
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/lodestone"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/queue"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type InfrastructureContainer struct {
	httpClient            contract.HTTPClient
	statsd                contract.StatsdClient
	sqliteDriver          contract.SQLiteDriver
	queue                 contract.Queue
	lodestoneClient       contract.LodestoneClient
	characterRepository   contract.CharacterRepository
	freeCompanyRepository contract.FreeCompanyRepository
	achievementRepository contract.AchievementRepository
	censusRunRepository   contract.CensusRunRepository
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

func (s *ServiceContainer) Queue() contract.Queue {
	if s.infrastructure.queue != nil {
		return s.infrastructure.queue
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.queue", "sqlite driver unavailable, queue disabled")
		return nil
	}
	cfg := s.Config().Queue
	if cfg == nil {
		logging.Warn("container.queue", "queue config missing")
		return nil
	}
	q, err := queue.NewQueue(driver, cfg)
	if err != nil {
		logging.Error("container.queue", fmt.Sprintf("failed to create queue: %v", err))
		return nil
	}
	s.infrastructure.queue = q
	return q
}

func (s *ServiceContainer) LodestoneClient() contract.LodestoneClient {
	if s.infrastructure.lodestoneClient != nil {
		return s.infrastructure.lodestoneClient
	}
	cfg := s.Config().Lodestone
	if cfg == nil {
		logging.Warn("container.lodestone", "lodestone config missing")
		return nil
	}
	client, err := lodestone.NewClient(cfg)
	if err != nil {
		logging.Error("container.lodestone", fmt.Sprintf("failed to create lodestone client: %v", err))
		return nil
	}
	s.infrastructure.lodestoneClient = client
	return client
}

func (s *ServiceContainer) CharacterRepository() contract.CharacterRepository {
	if s.infrastructure.characterRepository != nil {
		return s.infrastructure.characterRepository
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.character_repository", "sqlite driver unavailable")
		return nil
	}
	s.infrastructure.characterRepository = repository.NewCharacterRepository(driver)
	return s.infrastructure.characterRepository
}

func (s *ServiceContainer) FreeCompanyRepository() contract.FreeCompanyRepository {
	if s.infrastructure.freeCompanyRepository != nil {
		return s.infrastructure.freeCompanyRepository
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.free_company_repository", "sqlite driver unavailable")
		return nil
	}
	s.infrastructure.freeCompanyRepository = repository.NewFreeCompanyRepository(driver)
	return s.infrastructure.freeCompanyRepository
}

func (s *ServiceContainer) AchievementRepository() contract.AchievementRepository {
	if s.infrastructure.achievementRepository != nil {
		return s.infrastructure.achievementRepository
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.achievement_repository", "sqlite driver unavailable")
		return nil
	}
	s.infrastructure.achievementRepository = repository.NewAchievementRepository(driver)
	return s.infrastructure.achievementRepository
}

func (s *ServiceContainer) CensusRunRepository() contract.CensusRunRepository {
	if s.infrastructure.censusRunRepository != nil {
		return s.infrastructure.censusRunRepository
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.census_run_repository", "sqlite driver unavailable")
		return nil
	}
	s.infrastructure.censusRunRepository = repository.NewCensusRunRepository(driver)
	return s.infrastructure.censusRunRepository
}
