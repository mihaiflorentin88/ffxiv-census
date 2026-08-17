package container

import "testing"

func TestServiceContainer_TomestoneClient(t *testing.T) {
	Load = NewServiceContainer()
	if Load.TomestoneClient() == nil {
		t.Fatal("expected non-nil tomestone client")
	}
}

func TestServiceContainer_TomestoneClientCached(t *testing.T) {
	Load = NewServiceContainer()
	if Load.TomestoneClient() != Load.TomestoneClient() {
		t.Fatal("expected cached tomestone client instance")
	}
}
