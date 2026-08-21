package proxy

import (
	"context"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FakeChecker is an in-memory ProxyChecker for tests.
type FakeChecker struct {
	latency int
	err     error
}

// Ensure FakeChecker implements contract.ProxyChecker at compile time.
var _ contract.ProxyChecker = (*FakeChecker)(nil)

func NewFakeChecker(latency int, err error) *FakeChecker {
	return &FakeChecker{latency: latency, err: err}
}

func (f *FakeChecker) Check(_ context.Context, _, _ string, _ int) (int, error) {
	return f.latency, f.err
}
