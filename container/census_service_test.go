package container

import (
	"testing"
)

func TestServiceContainer_CensusService(t *testing.T) {
	Load = NewServiceContainer()
	if Load.Database() == nil {
		t.Skip("postgres not available")
	}

	svc := Load.CensusService()
	if svc == nil {
		t.Fatal("CensusService nil")
	}
	if Load.CensusService() != svc {
		t.Fatal("expected cached CensusService instance")
	}
}
