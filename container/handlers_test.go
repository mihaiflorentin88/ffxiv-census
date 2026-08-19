package container

import (
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
)

func TestServiceContainer_Handlers(t *testing.T) {
	Load = NewServiceContainer()
	if Load.Database() == nil {
		t.Skip("postgres not available")
	}

	reg := Load.Handlers()
	if _, ok := reg.Get(handler.EventIDSweep); !ok {
		t.Fatal("id-sweep handler not registered")
	}
}
