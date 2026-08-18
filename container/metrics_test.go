package container

import (
	"testing"
)

func TestServiceContainer_PrometheusRegistry(t *testing.T) {
	Load = NewServiceContainer()
	reg := Load.PrometheusRegistry()
	if reg == nil {
		t.Fatal("expected prometheus registry to be initialized, got nil")
	}
}

func TestServiceContainer_Statsd(t *testing.T) {
	t.Setenv("STATSD_ADDRESS", "127.0.0.1:8125")
	Load = NewServiceContainer()
	statsd := Load.Statsd()
	if statsd == nil {
		t.Fatal("expected statsd client to be initialized, got nil")
	}
}
