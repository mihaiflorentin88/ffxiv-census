package container
import (
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/httpclient"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/metrics"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/mysql"
	mysqlfixtures "github.com/mihaiflorentin88/ffxiv-census/infrastructure/mysql/fixtures"
	mysqlmigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/mysql/migration"
	mysqlrepository "github.com/mihaiflorentin88/ffxiv-census/infrastructure/mysql/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type InfrastructureContainer struct {
	httpClient contract.HTTPClient
	statsd contract.StatsdClient
	mysqlDriver          contract.MySQLDriver
	mysqlMigrations      contract.MigrationRunner
	mysqlFixtureGenerator contract.FixtureGenerator
	mysqlFixtureLoader    contract.FixtureLoader
	exampleRepository    contract.ExampleRepository
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
func (s *ServiceContainer) MySQL() contract.MySQLDriver {
	if s.infrastructure.mysqlDriver != nil {
		return s.infrastructure.mysqlDriver
	}
	cfg := s.Config().MySQL
	if cfg == nil {
		logging.Warn("container.mysql", "mysql config missing")
		return nil
	}
	driver, err := mysql.NewDriver(cfg)
	if err != nil {
		logging.Error("container.mysql", fmt.Sprintf("failed to create mysql driver: %v", err))
		return nil
	}
	s.infrastructure.mysqlDriver = driver
	return driver
}

func (s *ServiceContainer) MySQLMigrations() contract.MigrationRunner {
	if s.infrastructure.mysqlMigrations != nil {
		return s.infrastructure.mysqlMigrations
	}
	driver := s.MySQL()
	if driver == nil {
		return nil
	}
	runner := mysqlmigration.NewRunner(driver)
	s.infrastructure.mysqlMigrations = runner
	return runner
}

func (s *ServiceContainer) MySQLFixturesGenerator() contract.FixtureGenerator {
	if s.infrastructure.mysqlFixtureGenerator != nil {
		return s.infrastructure.mysqlFixtureGenerator
	}
	gen := mysqlfixtures.NewGenerator()
	s.infrastructure.mysqlFixtureGenerator = gen
	return gen
}

func (s *ServiceContainer) MySQLFixturesLoader() contract.FixtureLoader {
	if s.infrastructure.mysqlFixtureLoader != nil {
		return s.infrastructure.mysqlFixtureLoader
	}
	driver := s.MySQL()
	if driver == nil {
		return nil
	}
	loader := mysqlfixtures.NewLoader(driver)
	s.infrastructure.mysqlFixtureLoader = loader
	return loader
}

func (s *ServiceContainer) ExampleRepository() contract.ExampleRepository {
	if s.infrastructure.exampleRepository != nil {
		return s.infrastructure.exampleRepository
	}
	driver := s.MySQL()
	if driver == nil {
		return nil
	}
	repo := mysqlrepository.NewExampleRepository(driver)
	s.infrastructure.exampleRepository = repo
	return repo
}
