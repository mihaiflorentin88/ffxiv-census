package container

import (
	"fmt"
	"os"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/lodestone"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres"
	postgresmigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/migration"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/provider"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/queue"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/tomestone"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type InfrastructureContainer struct {
	httpClient            contract.HTTPClient
	statsd                contract.StatsdClient
	prometheusRegistry    *metrics.Registry
	databaseDriver        contract.DatabaseDriver
	queue                 contract.Queue
	lodestoneClient       contract.LodestoneClient
	tomestoneClient       contract.TomestoneClient
	characterRepository   contract.CharacterRepository
	freeCompanyRepository contract.FreeCompanyRepository
	achievementRepository contract.AchievementRepository
	censusRunRepository   contract.CensusRunRepository
	providerRateLimiter   contract.ProviderRateLimiter
}

// Logger returns the process-wide structured logger (infrastructure/logging.Logger)
// as a contract.Logger, for injection into adapters and domain objects.
func (s *ServiceContainer) Logger() contract.Logger {
	return logging.Logger
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.statsd != nil {
		return s.infrastructure.statsd
	}
	cfg := s.configUnlocked().Metrics
	endpoint := ""
	prefix := "ffxiv-census"
	if cfg != nil {
		endpoint = cfg.Endpoint
		if cfg.Prefix != "" {
			prefix = cfg.Prefix
		}
	}
	if envEndpoint := os.Getenv("STATSD_ADDRESS"); envEndpoint != "" {
		endpoint = envEndpoint
	}
	if endpoint == "" {
		endpoint = "127.0.0.1:8125"
	}
	client, err := metrics.New(endpoint, prefix)
	if err != nil {
		logging.Error("container.metrics", fmt.Sprintf("failed to create statsd client: %v", err))
		return nil
	}
	s.infrastructure.statsd = client
	return client
}
func (s *ServiceContainer) PrometheusRegistry() *metrics.Registry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.prometheusRegistry != nil {
		return s.infrastructure.prometheusRegistry
	}
	reg := metrics.NewRegistry()
	s.infrastructure.prometheusRegistry = reg
	return reg
}
func (s *ServiceContainer) ProviderRateLimiter() contract.ProviderRateLimiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerRateLimiterUnlocked()
}

func (s *ServiceContainer) providerRateLimiterUnlocked() contract.ProviderRateLimiter {
	if s.infrastructure.providerRateLimiter != nil {
		return s.infrastructure.providerRateLimiter
	}
	limiter := provider.NewRateLimiter()
	s.infrastructure.providerRateLimiter = limiter
	return limiter
}

func (s *ServiceContainer) Database() contract.DatabaseDriver {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.databaseUnlocked()
}

func (s *ServiceContainer) Postgres() contract.DatabaseDriver {
	return s.Database()
}

func (s *ServiceContainer) SQLite() contract.DatabaseDriver {
	return s.Database()
}

func (s *ServiceContainer) databaseUnlocked() contract.DatabaseDriver {
	if s.infrastructure.databaseDriver != nil {
		return s.infrastructure.databaseDriver
	}
	cfg := s.configUnlocked().Postgres
	if cfg == nil {
		logging.Warn("container.postgres", "postgres config missing")
		return nil
	}
	driver, err := postgres.NewDriver(cfg, postgresmigration.FS())
	if err != nil {
		logging.Error("container.postgres", fmt.Sprintf("failed to create postgres driver: %v", err))
		return nil
	}
	s.infrastructure.databaseDriver = driver
	return driver
}

func (s *ServiceContainer) Queue() contract.Queue {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.queue != nil {
		return s.infrastructure.queue
	}
	driver := s.databaseUnlocked()
	if driver == nil {
		logging.Warn("container.queue", "database driver unavailable, queue disabled")
		return nil
	}
	cfg := s.configUnlocked().Queue
	if cfg == nil {
		logging.Warn("container.queue", "queue config missing")
		return nil
	}
	q, err := queue.NewQueue(driver, cfg, s.Logger())
	if err != nil {
		logging.Error("container.queue", fmt.Sprintf("failed to create queue: %v", err))
		return nil
	}
	s.infrastructure.queue = q
	return q
}

func (s *ServiceContainer) LodestoneClient() contract.LodestoneClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.lodestoneClient != nil {
		return s.infrastructure.lodestoneClient
	}
	cfg := s.configUnlocked().Lodestone
	if cfg == nil {
		logging.Warn("container.lodestone", "lodestone config missing")
		return nil
	}
	client, err := lodestone.NewClient(cfg, s.Logger(), s.providerRateLimiterUnlocked())
	if err != nil {
		logging.Error("container.lodestone", fmt.Sprintf("failed to create lodestone client: %v", err))
		return nil
	}
	s.infrastructure.lodestoneClient = client
	return client
}

func (s *ServiceContainer) TomestoneClient() contract.TomestoneClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.tomestoneClient != nil {
		return s.infrastructure.tomestoneClient
	}
	cfg := s.configUnlocked().Tomestone
	if cfg == nil {
		logging.Warn("container.tomestone", "tomestone config missing")
		return nil
	}
	client, err := tomestone.NewClient(cfg, s.Logger(), s.providerRateLimiterUnlocked())
	if err != nil {
		logging.Error("container.tomestone", fmt.Sprintf("failed to create tomestone client: %v", err))
		return nil
	}
	s.infrastructure.tomestoneClient = client
	return client
}

func (s *ServiceContainer) CharacterRepository() contract.CharacterRepository {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.characterRepository != nil {
		return s.infrastructure.characterRepository
	}
	driver := s.databaseUnlocked()
	if driver == nil {
		logging.Warn("container.character_repository", "database driver unavailable")
		return nil
	}
	s.infrastructure.characterRepository = repository.NewCharacterRepository(driver)
	return s.infrastructure.characterRepository
}

func (s *ServiceContainer) FreeCompanyRepository() contract.FreeCompanyRepository {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.freeCompanyRepository != nil {
		return s.infrastructure.freeCompanyRepository
	}
	driver := s.databaseUnlocked()
	if driver == nil {
		logging.Warn("container.free_company_repository", "database driver unavailable")
		return nil
	}
	s.infrastructure.freeCompanyRepository = repository.NewFreeCompanyRepository(driver)
	return s.infrastructure.freeCompanyRepository
}

func (s *ServiceContainer) AchievementRepository() contract.AchievementRepository {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.achievementRepository != nil {
		return s.infrastructure.achievementRepository
	}
	driver := s.databaseUnlocked()
	if driver == nil {
		logging.Warn("container.achievement_repository", "database driver unavailable")
		return nil
	}
	s.infrastructure.achievementRepository = repository.NewAchievementRepository(driver)
	return s.infrastructure.achievementRepository
}

func (s *ServiceContainer) CensusRunRepository() contract.CensusRunRepository {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.censusRunRepository != nil {
		return s.infrastructure.censusRunRepository
	}
	driver := s.databaseUnlocked()
	if driver == nil {
		logging.Warn("container.census_run_repository", "database driver unavailable")
		return nil
	}
	s.infrastructure.censusRunRepository = repository.NewCensusRunRepository(driver)
	return s.infrastructure.censusRunRepository
}
