package proxy

import (
	"context"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Service contains the business logic for proxy discovery, scanning, and management.
type Service struct {
	providers          []contract.ProxyProvider
	repo               contract.ProxyRepository
	checker            *proxy.Checker
	logger             contract.Logger
	deadThreshold      time.Duration
	failCountThreshold int
}

// NewService creates a new proxy service.
func NewService(
	providers []contract.ProxyProvider,
	repo contract.ProxyRepository,
	checker *proxy.Checker,
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
	rec := contract.ProxyRecord{
		Protocol:      protocol,
		IP:            ip,
		Port:          port,
		Country:       country,
		Anonymity:     anonymity,
		Source:        source,
		UptimePercent: uptimePercent,
	}

	id, exists, err := s.repo.Upsert(ctx, rec)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

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
func (s *Service) ProcessScanProxy(ctx context.Context, proxyID int64) error {
	p, err := s.repo.Get(ctx, proxyID)
	if err != nil {
		return err
	}
	if p == nil {
		return nil // proxy deleted between queue and processing
	}

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
		s.logger.InfoContext(ctx, "proxy.check_failed",
			"proxy_id", p.ID, "ip", p.IP, "port", p.Port,
			"new_status", newStatus, "fail_count", newFailCount, "error", err)
		return nil
	}

	// Proxy check succeeded.
	if err := s.repo.UpdateStatus(ctx, p.ID, contract.ProxyStatusActive, &latency, 0, &now); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "proxy.check_passed",
		"proxy_id", p.ID, "ip", p.IP, "port", p.Port,
		"latency_ms", latency)
	return nil
}
