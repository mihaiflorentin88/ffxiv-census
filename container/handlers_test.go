package container

import (
	"path/filepath"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
)

func TestServiceContainer_Handlers(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	reg := Load.Handlers()
	if _, ok := reg.Get(handler.EventIDSweep); !ok {
		t.Fatal("id-sweep handler not registered")
	}
}
