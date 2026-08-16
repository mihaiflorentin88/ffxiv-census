package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_Queue(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "queue.db"))
	Load = NewServiceContainer()
	defer Load.SQLite().Close()

	q := Load.Queue()
	if q == nil {
		t.Fatal("expected non-nil queue")
	}
}

func TestServiceContainer_QueueCached(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "queue.db"))
	Load = NewServiceContainer()
	defer Load.SQLite().Close()

	first := Load.Queue()
	second := Load.Queue()
	if first != second {
		t.Fatal("expected cached queue instance")
	}
}
