package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"boyler/internal/daemon/core"
)

func TestContainerRepositoryPersistsAcrossInstances(t *testing.T) {
	root := t.TempDir()
	store, err := NewContainerRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	want := testContainer("container-b")
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewContainerRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("persisted container = %#v, want %#v", *got, want)
	}

	stateInfo, err := os.Stat(filepath.Join(root, stateDirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	if stateInfo.Mode().Perm() != 0700 {
		t.Fatalf("state directory mode = %o, want 700", stateInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(filepath.Join(root, stateDirectoryName, want.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Fatalf("state file mode = %o, want 600", fileInfo.Mode().Perm())
	}
}

func TestContainerRepositoryUpdateListAndDelete(t *testing.T) {
	store, err := NewContainerRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"container-b", "container-a"} {
		if err := store.Save(context.Background(), testContainer(id)); err != nil {
			t.Fatal(err)
		}
	}
	updated := testContainer("container-b")
	updated.PID = 99
	updated.Status = core.StatusRunning
	if err := store.Update(context.Background(), updated); err != nil {
		t.Fatal(err)
	}

	containers, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 || containers[0].ID != "container-a" || containers[1].ID != "container-b" {
		t.Fatalf("containers are not deterministically sorted: %#v", containers)
	}
	if containers[1].PID != 99 || containers[1].Status != core.StatusRunning {
		t.Fatalf("updated container was not persisted: %#v", containers[1])
	}
	if err := store.Delete(context.Background(), "container-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), "container-a"); err != nil {
		t.Fatalf("idempotent delete returned an error: %v", err)
	}
	if _, err := store.Get(context.Background(), "container-a"); !errors.Is(err, core.ErrContainerNotFound) {
		t.Fatalf("Get deleted container error = %v, want ErrContainerNotFound", err)
	}
}

func TestContainerRepositorySupportsConcurrentOperations(t *testing.T) {
	store, err := NewContainerRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	var wait sync.WaitGroup
	errorsChannel := make(chan error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			container := testContainer(fmt.Sprintf("container-%02d", index))
			if err := store.Save(context.Background(), container); err != nil {
				errorsChannel <- err
				return
			}
			if _, err := store.Get(context.Background(), container.ID); err != nil {
				errorsChannel <- err
			}
		}(index)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
	containers, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != count {
		t.Fatalf("List returned %d containers, want %d", len(containers), count)
	}
}

func TestContainerRepositoryRejectsCorruptState(t *testing.T) {
	root := t.TempDir()
	store, err := NewContainerRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, stateDirectoryName, "broken.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"container":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("List corrupt state error = %v, want decode error", err)
	}
}

func TestContainerRepositoryRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "containers")
	if err := os.Symlink(realDirectory, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewContainerRepository(symlink); err == nil {
		t.Fatal("expected symlink-backed storage to be rejected")
	}

	store, err := NewContainerRepository(filepath.Join(root, "safe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), testContainer("../escape")); err == nil {
		t.Fatal("expected unsafe container ID to be rejected")
	}
	target := filepath.Join(root, "outside")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	stateLink := filepath.Join(root, "safe", stateDirectoryName, "linked.json")
	if err := os.Symlink(target, stateLink); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "linked"); err == nil {
		t.Fatal("expected symlink state file to be rejected")
	}
}

func TestContainerRepositoryHonorsCancelledContext(t *testing.T) {
	store, err := NewContainerRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Save(ctx, testContainer("cancelled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v, want context.Canceled", err)
	}
	if _, err := store.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List error = %v, want context.Canceled", err)
	}
}

func testContainer(id string) core.Container {
	memory := int64(64 << 20)
	weight := uint64(100)
	quota := int64(50_000)
	period := uint64(100_000)
	return core.Container{
		ID:           id,
		PID:          0,
		Name:         "test",
		ImageID:      "alpine:latest",
		ImageDigest:  "sha256:" + strings.Repeat("a", 64),
		RootfsDigest: "sha256:" + strings.Repeat("a", 64),
		Status:       core.StatusStopped,
		CreatedAt:    time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC),
		StartedAt:    time.Date(2026, time.August, 13, 12, 0, 1, 0, time.UTC),
		Config: core.ContainerConfig{
			Hostname: "test",
			Env:      []string{"A=B"},
			Args:     []string{"/bin/sh"},
			Resources: core.Restriction{
				Memory: core.MemoryRestriction{Max: &memory},
				CPU: core.CPURestriction{
					Weight: &weight,
					Quota:  &quota,
					Period: &period,
					Cpus:   "0",
					Mems:   "0",
				},
			},
		},
	}
}
