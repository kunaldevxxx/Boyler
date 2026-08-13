package imageservice

import (
	"boyler/internal/daemon/core"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var errStreamClosed = errors.New("stream closed")

type failingStream struct{}

func (failingStream) Send(*core.PullingEvent) error { return errStreamClosed }

type eventProducingImageManager struct{}

func (eventProducingImageManager) Pull(ctx context.Context, _ string, events chan *core.PullingEvent) error {
	defer close(events)
	for range 1000 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- &core.PullingEvent{Status: "Downloading"}:
		}
	}
	return nil
}

func (eventProducingImageManager) Extract(context.Context, string, string) error { return nil }
func (eventProducingImageManager) IsExtracted(context.Context, string) bool      { return false }
func (eventProducingImageManager) GetRootfsPath(string) string                   { return "" }
func (eventProducingImageManager) GetRootfsPathByDigest(string) (string, error)  { return "", nil }
func (eventProducingImageManager) Delete(context.Context, string) error          { return nil }
func (eventProducingImageManager) Remove(context.Context, string) (*core.ImageRemoveResult, error) {
	return nil, nil
}
func (eventProducingImageManager) Get(context.Context, string) (*core.Image, error) {
	return nil, nil
}
func (eventProducingImageManager) List(context.Context) ([]*core.Image, error) { return nil, nil }
func (eventProducingImageManager) Resolve(context.Context, string) (*core.Image, error) {
	return nil, nil
}
func (eventProducingImageManager) Prune(context.Context, core.ImageUsage, core.ImagePruneOptions) (*core.ImagePruneResult, error) {
	return nil, nil
}

func TestPullStopsProducerAndReturnsStreamError(t *testing.T) {
	service := &imageService{image: eventProducingImageManager{}}
	done := make(chan error, 1)
	go func() {
		done <- service.Pull(context.Background(), "alpine", failingStream{})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, errStreamClosed) {
			t.Fatalf("Pull error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pull deadlocked after stream failure")
	}
}

func TestPersistedContainerImageIdentityIsIncludedInPruneUsage(t *testing.T) {
	containers := t.TempDir()
	if err := os.Mkdir(filepath.Join(containers, ".state"), 0700); err != nil {
		t.Fatal(err)
	}
	containerDir := filepath.Join(containers, "container-id")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.WriteFile(filepath.Join(containerDir, ".boyler-image.json"), []byte(`{"imageDigest":"`+digest+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	service := &imageService{config: ImageServiceConfig{ContainersDir: containers}}
	used := make(map[string]struct{})
	if err := service.addPersistedUsage(used, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := used[digest]; !ok {
		t.Fatalf("persisted digest was not marked as used: %#v", used)
	}
}
