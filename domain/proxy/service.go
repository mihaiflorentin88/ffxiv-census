package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Service contains the business logic for proxy discovery, scanning, and management.
type Service struct {
	providers          []contract.ProxyProvider
	repo               contract.ProxyRepository
	checker            contract.ProxyChecker
	logger             contract.Logger
	deadThreshold      time.Duration
	failCountThreshold int
}

// NewService creates a new proxy service.
func NewService(
	providers []contract.ProxyProvider,
	repo contract.ProxyRepository,
	checker contract.ProxyChecker,
	logger contract.Logger,
	deadThreshold time.Duration,
	failCountThreshold int,
) *Service {
	return &Service{
		providers:          providers,
		repo:               repo,
		checker:            checker,
		logger:             logger,
		deadThreshold:      deadThreshold,
		failCountThreshold: failCountThreshold,
	}
}

// Providers returns the configured proxy providers.
func (s *Service) Providers() []contract.ProxyProvider {
	return s.providers
}

// ProcessNewProxy inserts a discovered proxy and tests it.
// Returns nil if the proxy already exists (dedup).
func (s *Service) ProcessNewProxy(ctx context.Context, protocol, ip string, port int, country, anonymity *string, source string, uptimePercent *float64) error {
	s.logger.InfoContext(ctx, "Processing new proxy", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", protocol, ip, port)))

	// Pre-insert existence check (read-only, no database write).
	exists, err := s.repo.Exists(ctx, protocol, ip, port)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to check if proxy exists", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", protocol, ip, port)), slog.Any("error", err))
		return err
	}
	if exists {
		s.logger.InfoContext(ctx, "Proxy already exists, skipping", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", protocol, ip, port)))
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
		s.logger.ErrorContext(ctx, "Failed to insert new proxy", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", protocol, ip, port)), slog.Any("error", err))
		return err
	}
	if !inserted {
		// Concurrent delivery race: another process inserted this tuple.
		s.logger.InfoContext(ctx, "Proxy already exists, skipping", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", protocol, ip, port)))
		return nil
	}

	s.logger.InfoContext(ctx, "New proxy added", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", protocol, ip, port)))

	// Fetch the newly inserted proxy by ID.
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}

	return s.processProxyCheck(ctx, p)
}

// ProcessScanProxy tests an existing proxy and updates its status.
// The caller provides the already-selected ProxyRecord directly, avoiding
// a redundant repository Get round trip.
func (s *Service) ProcessScanProxy(ctx context.Context, p *contract.ProxyRecord) error {
	s.logger.InfoContext(ctx, "Starting proxy scan", slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", p.Protocol, p.IP, p.Port)))

	return s.processProxyCheck(ctx, p)
}

// processProxyCheck tests a proxy against the test URL and updates its status.
func (s *Service) processProxyCheck(ctx context.Context, p *contract.ProxyRecord) error {
	latency, err := s.checker.Check(ctx, p.Protocol, p.IP, p.Port)

	now := time.Now().UTC()
	if err != nil {
		// Proxy check failed.
		newFailCount := p.FailCount + 1
		var newStatus string

		isDead := false
		if p.LastAliveAt != nil {
			isDead = now.Sub(*p.LastAliveAt) > s.deadThreshold
		} else {
			isDead = now.Sub(p.FirstSeenAt) > s.deadThreshold
		}

		if newFailCount >= s.failCountThreshold || isDead {
			newStatus = contract.ProxyStatusDead
		} else {
			newStatus = contract.ProxyStatusInactive
		}

		if err := s.repo.UpdateStatus(ctx, p.ID, newStatus, nil, newFailCount, p.LastAliveAt); err != nil {
			return err
		}
		s.logger.InfoContext(ctx, "Proxy check failed",
			slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", p.Protocol, p.IP, p.Port)),
			slog.Any("error", err))
		return nil
	}

	// Proxy check succeeded.
	if err := s.repo.UpdateStatus(ctx, p.ID, contract.ProxyStatusActive, &latency, 0, &now); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "Proxy check passed",
		slog.String("proxy_address", fmt.Sprintf("%s://%s:%d", p.Protocol, p.IP, p.Port)),
		slog.Int("duration_ms", latency))
	return nil
}
