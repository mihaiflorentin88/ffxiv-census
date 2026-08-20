package proxy

import (
	"context"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FakeProvider is an in-memory ProxyProvider for tests.
type FakeProvider struct {
	name     string
	proxies  []contract.ProxyRecord
	fetchErr error
}

func NewFakeProvider(name string, proxies []contract.ProxyRecord) *FakeProvider {
	return &FakeProvider{name: name, proxies: proxies}
}

func (f *FakeProvider) SetError(err error) {
	f.fetchErr = err
}

func (f *FakeProvider) Name() string {
	return f.name
}

func (f *FakeProvider) FetchProxies(_ context.Context) ([]contract.ProxyRecord, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.proxies, nil
}
