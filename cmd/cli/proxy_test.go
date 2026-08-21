package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// fakeProvider implements contract.ProxyProvider for tests.
type fakeProvider struct {
	name    string
	records []contract.ProxyRecord
	err     error
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) FetchProxies(_ context.Context) ([]contract.ProxyRecord, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.records, nil
}

func TestPublishDiscoveredProxies_EmptyProviderSet(t *testing.T) {
	q := &errorQueue{failOn: 0} // never fails
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	published, err := publishDiscoveredProxies(context.Background(), q, logger, nil)
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

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "bad", err: errors.New("fetch failed")},
		&fakeProvider{name: "good", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, logger, providers)
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

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "bad1", err: errors.New("fail1")},
		&fakeProvider{name: "bad2", err: errors.New("fail2")},
	}

	_, err := publishDiscoveredProxies(context.Background(), q, logger, providers)
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestPublishDiscoveredProxies_QueuePublishFailure(t *testing.T) {
	q := &errorQueue{failOn: 1} // fail on first publish
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	providers := []contract.ProxyProvider{
		&fakeProvider{name: "ok", records: []contract.ProxyRecord{
			{Protocol: "http", IP: "1.2.3.4", Port: 8080},
		}},
	}

	published, err := publishDiscoveredProxies(context.Background(), q, logger, providers)
	// When publish fails, publishDiscoveredProxies reports all providers failed
	// because totalPublished == 0 and totalErrors > 0.
	if err == nil {
		t.Fatal("expected error when queue publish fails")
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}
}
