package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// fakeProvider implements contract.ProxyProvider for tests.
type fakeProvider struct {
	name    string
	records []contract.ProxyRecord
	err     error
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) FetchProxies(_ context.Context, emit func(contract.ProxyRecord) error) error {
	if p.err != nil {
		return p.err
	}
	for _, rec := range p.records {
		if err := emit(rec); err != nil {
			return err
		}
	}
	return nil
}

func TestPublishDiscoveredProxies_EmptyProviderSet(t *testing.T) {
	q := &errorQueue{failOn: 0} // never fails
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.NewFakeProxyRepository()

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}

func TestPublishDiscoveredProxies_PartialProviderFailure(t *testing.T) {
	q := &errorQueue{failOn: 0} // never fails
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.NewFakeProxyRepository()

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "bad", err: errors.New("fetch failed")},
		&fakeProvider{name: "good", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d, want 1", published)
	}
}

func TestPublishDiscoveredProxies_AllProvidersFail(t *testing.T) {
	q := &errorQueue{failOn: 0}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.NewFakeProxyRepository()

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "bad1", err: errors.New("fail1")},
		&fakeProvider{name: "bad2", err: errors.New("fail2")},
	}

	_, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestPublishDiscoveredProxies_QueuePublishFailure(t *testing.T) {
	q := &errorQueue{failOn: 1} // fail on first publish
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := repository.NewFakeProxyRepository()

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "ok", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	// When publish fails, publishDiscoveredProxies reports all providers failed
	// because totalPublished == 0 and totalErrors > 0.
	if err == nil {
		t.Fatal("expected error when queue publish fails")
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}

func TestPublishDiscoveredProxies_SkipsExisting(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	// Seed the repo with http/1.2.3.4/8080.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "seed",
	})
	// Reset counters after seeding so we only count the publish call.
	repo.ExistsCalls = 0

	q := &errorQueue{failOn: 0}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "test", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
	if repo.ExistsCalls != 1 {
		t.Errorf("ExistsCalls = %d, want 1", repo.ExistsCalls)
	}
	if q.calls != 0 {
		t.Errorf("queue Publish calls = %d, want 0", q.calls)
	}
}

func TestPublishDiscoveredProxies_PublishesNew(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	// Seed with http/1.2.3.4/8080.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "seed",
	})
	repo.ExistsCalls = 0

	q := &errorQueue{failOn: 0}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Different protocol (socks5) from seeded (http) — distinct tuple.
	providers := []contract.ProxyProvider{
		&fakeProvider{name: "test", records: []contract.ProxyRecord{
			{Protocol: "socks5", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d, want 1", published)
	}
}

func TestPublishDiscoveredProxies_LookupFailureFailsClosed(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.ExistsErr = errors.New("connection refused")

	q := &errorQueue{failOn: 0}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "test", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	if err == nil {
		t.Fatal("expected error when lookup fails")
	}
	if !strings.Contains(err.Error(), "proxy lookup failed") && !strings.Contains(err.Error(), "all providers failed") {
		t.Errorf("error %q does not contain expected substring", err.Error())
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}

func TestPublishDiscoveredProxies_AllExistingSucceeds(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "seed",
	})
	repo.ExistsCalls = 0

	q := &errorQueue{failOn: 0}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Provider has only existing tuples.
	providers := []contract.ProxyProvider{
		&fakeProvider{name: "test", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, repo, logger, providers)
	if err != nil {
		t.Fatalf("unexpected error: %v, want nil (all-existing is a successful provider)", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}
