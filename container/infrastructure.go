package container

import (
	"fmt"
	"os"
	"strings"
	"time"

	proxydomain "github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/geonode"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/lodestone"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres"
	postgresmigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/migration"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/provider"
	proxyinfra "github.com/mihaiflorentin88/ffxiv-census/infrastructure/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/proxyscrape"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/pubproxy"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/queue"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/textproxy"
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
	proxyRepository       contract.ProxyRepository
	proxyChecker          *proxyinfra.Checker
	proxyScrapeProvider   contract.ProxyProvider
	geonodeProvider       contract.ProxyProvider
	pubProxyProvider      contract.ProxyProvider
	proxiflyProvider      contract.ProxyProvider
	theSpeedXProvider     contract.ProxyProvider
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

func (s *ServiceContainer) ProxyRepository() contract.ProxyRepository {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.proxyRepository != nil {
		return s.infrastructure.proxyRepository
	}
	driver := s.databaseUnlocked()
	if driver == nil {
		logging.Warn("container.proxy_repository", "database driver unavailable")
		return nil
	}
	s.infrastructure.proxyRepository = repository.NewProxyRepository(driver)
	return s.infrastructure.proxyRepository
}

func (s *ServiceContainer) ProxyChecker() *proxyinfra.Checker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.proxyChecker != nil {
		return s.infrastructure.proxyChecker
	}
	cfg := s.configUnlocked().Proxy
	testURL := "https://na.finalfantasyxiv.com/lodestone/"
	timeout := 15 * time.Second
	if cfg != nil {
		if cfg.TestURL != "" {
			testURL = cfg.TestURL
		}
		if cfg.TestTimeout != "" {
			if d, err := time.ParseDuration(cfg.TestTimeout); err == nil {
				timeout = d
			}
		}
	}
	s.infrastructure.proxyChecker = proxyinfra.NewChecker(testURL, timeout, s.Logger())
	return s.infrastructure.proxyChecker
}

func (s *ServiceContainer) ProxyScrapeProvider() contract.ProxyProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.proxyScrapeProvider != nil {
		return s.infrastructure.proxyScrapeProvider
	}
	cfg := s.configUnlocked().Proxy
	if cfg == nil || !cfg.Providers.ProxyScrape {
		return nil
	}
	s.infrastructure.proxyScrapeProvider = proxyscrape.New(s.HTTPClient(), cfg.Providers.ProxyScrapeURL)
	return s.infrastructure.proxyScrapeProvider
}

func (s *ServiceContainer) GeonodeProvider() contract.ProxyProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.geonodeProvider != nil {
		return s.infrastructure.geonodeProvider
	}
	cfg := s.configUnlocked().Proxy
	if cfg == nil || !cfg.Providers.Geonode {
		return nil
	}
	s.infrastructure.geonodeProvider = geonode.New(s.HTTPClient(), cfg.Providers.GeonodeURL)
	return s.infrastructure.geonodeProvider
}

func (s *ServiceContainer) PubProxyProvider() contract.ProxyProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.pubProxyProvider != nil {
		return s.infrastructure.pubProxyProvider
	}
	cfg := s.configUnlocked().Proxy
	if cfg == nil || !cfg.Providers.PubProxy {
		return nil
	}
	s.infrastructure.pubProxyProvider = pubproxy.New(s.HTTPClient(), cfg.Providers.PubProxyURL)
	return s.infrastructure.pubProxyProvider
}

func (s *ServiceContainer) ProxiflyProvider() contract.ProxyProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.proxiflyProvider != nil {
		return s.infrastructure.proxiflyProvider
	}
	cfg := s.configUnlocked().Proxy
	if cfg == nil || !cfg.Providers.Proxifly {
		return nil
	}
	base := strings.TrimRight(cfg.Providers.ProxiflyURL, "/")
	s.infrastructure.proxiflyProvider = textproxy.New(s.HTTPClient(), "proxifly", map[string]string{
		"http":   base + "/http/data.txt",
		"socks4": base + "/socks4/data.txt",
		"socks5": base + "/socks5/data.txt",
	})
	return s.infrastructure.proxiflyProvider
}

func (s *ServiceContainer) TheSpeedXProvider() contract.ProxyProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infrastructure.theSpeedXProvider != nil {
		return s.infrastructure.theSpeedXProvider
	}
	cfg := s.configUnlocked().Proxy
	if cfg == nil || !cfg.Providers.TheSpeedX {
		return nil
	}
	base := strings.TrimRight(cfg.Providers.TheSpeedXURL, "/")
	s.infrastructure.theSpeedXProvider = textproxy.New(s.HTTPClient(), "thespeedx", map[string]string{
		"http":   base + "/http.txt",
		"socks4": base + "/socks4.txt",
		"socks5": base + "/socks5.txt",
	})
	return s.infrastructure.theSpeedXProvider
}

// ProxyHub creates a ProxyHub for the given owner (process name + goroutine ID).
// The lock TTL is read from [proxy.consumer] config.
func (s *ServiceContainer) ProxyHub(owner string) *proxydomain.ProxyHub {
	repo := s.ProxyRepository()
	if repo == nil {
		return nil
	}
	lockTTL := 5 * time.Minute
	if cfg := s.Config().Proxy; cfg != nil && cfg.Consumer.LockTTL != "" {
		if d, err := time.ParseDuration(cfg.Consumer.LockTTL); err == nil && d > 0 {
			lockTTL = d
		}
	}
	return proxydomain.NewProxyHub(repo, lockTTL)
}
