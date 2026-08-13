package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"boyler/internal/daemon/core"
)

func TestDaemonFactoryUsesPersistentContainerStorage(t *testing.T) {
	root := t.TempDir()
	config := DaemonConfig{
		ImagesPath:     filepath.Join(root, "images"),
		ContainersPath: filepath.Join(root, "containers"),
	}
	first, err := NewDaemonFactory(config)
	if err != nil {
		t.Fatal(err)
	}
	container := core.Container{ID: "container-id", Status: core.StatusStopped}
	if err := first.shared.Store.Save(context.Background(), container); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config.ContainersPath, ".state", container.ID+".json")); err != nil {
		t.Fatalf("container state was not written to disk: %v", err)
	}

	second, err := NewDaemonFactory(config)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := second.shared.Store.Get(context.Background(), container.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != container.ID || loaded.Status != core.StatusStopped {
		t.Fatalf("loaded container = %#v", loaded)
	}
}
