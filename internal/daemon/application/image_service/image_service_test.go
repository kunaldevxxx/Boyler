package imageservice

import (
	"boyler/internal/daemon/core"
	"context"
	"errors"
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
func (eventProducingImageManager) Delete(context.Context, string) error          { return nil }
func (eventProducingImageManager) Get(context.Context, string) (*core.Image, error) {
	return nil, nil
}
func (eventProducingImageManager) List(context.Context) ([]*core.Image, error) { return nil, nil }
func (eventProducingImageManager) Prune(context.Context) error                 { return nil }

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
