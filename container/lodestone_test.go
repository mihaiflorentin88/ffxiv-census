package container

import "testing"

func TestServiceContainer_LodestoneClient(t *testing.T) {
	Load = NewServiceContainer()
	if Load.LodestoneClient() == nil {
		t.Fatal("expected non-nil lodestone client")
	}
}

func TestServiceContainer_LodestoneClientCached(t *testing.T) {
	Load = NewServiceContainer()
	if Load.LodestoneClient() != Load.LodestoneClient() {
		t.Fatal("expected cached client instance")
	}
}
