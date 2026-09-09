package proxy

import (
	"context"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Service contains the business logic for proxy discovery ingestion. It is
// deliberately insertion-only: discovery performs no health checks of its
// own. Every general check runs in the guarded, lease-bound scan worker
// (domain/proxy/worker), so newly discovered proxies carry no evidence until
// the background queue scans them.
type Service struct {
	providers []contract.ProxyProvider
	repo      contract.ProxyRepository
	logger    contract.Logger
}

// NewService creates a new proxy ingestion service.
func NewService(
	providers []contract.ProxyProvider,
	repo contract.ProxyRepository,
	logger contract.Logger,
) *Service {
	return &Service{
		providers: providers,
		repo:      repo,
		logger:    logger,
	}
}

// Providers returns the configured proxy providers.
func (s *Service) Providers() []contract.ProxyProvider {
	return s.providers
}

// ProcessNewProxy inserts a discovered proxy. It performs no health check:
// the queue delivery is acknowledged once the row is inserted, and the scan
// worker's background queue owns verification from there. Returns nil if the
// proxy already exists (dedup).
func (s *Service) ProcessNewProxy(ctx context.Context, protocol, ip string, port int, country, anonymity *string, source string, uptimePercent *float64) error {
	s.logger.InfoContext(ctx, "proxy.process_new.start", "protocol", protocol, "ip", ip, "port", port, "source", source)

	// Pre-insert existence check (read-only, no database write).
	exists, err := s.repo.Exists(ctx, protocol, ip, port)
	if err != nil {
		s.logger.ErrorContext(ctx, "proxy.process_new.exists_failed", "protocol", protocol, "ip", ip, "port", port, "error", err)
		return err
	}
	if exists {
		s.logger.InfoContext(ctx, "proxy.process_new.skipped_exists", "protocol", protocol, "ip", ip, "port", port)
		return nil
	}

	rec := contract.ProxyRecord{
		Protocol:      protocol,
		IP:            ip,
		Port:          port,
		Country:       country,
		Anonymity:     anonymity,
		Source:        source,
		UptimePercent: uptimePercent,
	}

	id, inserted, err := s.repo.InsertIfAbsent(ctx, rec)
	if err != nil {
		s.logger.ErrorContext(ctx, "proxy.process_new.insert_failed", "ip", ip, "port", port, "error", err)
		return err
	}
	if !inserted {
		// Concurrent delivery race: another process inserted this tuple.
		s.logger.InfoContext(ctx, "proxy.process_new.skipped_exists", "protocol", protocol, "ip", ip, "port", port)
		return nil
	}

	s.logger.InfoContext(ctx, "proxy.process_new.inserted", "proxy_id", id, "ip", ip, "port", port, "background_scan_pending", true)
	return nil
}
