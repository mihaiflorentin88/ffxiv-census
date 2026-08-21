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

func (f *FakeProvider) FetchProxies(_ context.Context, emit func(contract.ProxyRecord) error) error {
	if f.fetchErr != nil {
		return f.fetchErr
	}
	for _, p := range f.proxies {
		if err := emit(p); err != nil {
			return err
		}
	}
	return nil
}
