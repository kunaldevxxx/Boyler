package imageservice

import (
	"boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/image"
	overlay "boyler/internal/daemon/infrastructure/outbound/overlay"
	"context"
	"sync"
)

type Stream interface {
	Send(*core.PullingEvent) error
}

type ImageSerivceConfig struct {
	UnpackDir string
}

type ImageService interface {
	Pull(ctx context.Context, name string, stream Stream) error
	Remove(ctx context.Context, cmd RemoveCommand) error
	List(ctx context.Context) ([]*core.Image, error)
}

type imageService struct {
	fs     overlay.VolumeManager
	image  image.ImageManager
	config ImageSerivceConfig
}

func NewImageService(config ImageSerivceConfig, im image.ImageManager, fs overlay.VolumeManager) ImageService {
	return &imageService{config: config, image: im, fs: fs}
}

func (p *imageService) Pull(ctx context.Context, name string, stream Stream) error {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan *core.PullingEvent, 150)
	defer cancel()
	streamResult := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go sendToStream(ctx, cancel, stream, ch, streamResult, &wg)
	pullErr := p.image.Pull(ctx, name, ch)
	wg.Wait()
	streamErr := <-streamResult
	if streamErr != nil {
		return streamErr
	}
	if pullErr != nil {
		return &core.ImageError{Image: name, Err: pullErr}
	}
	return nil
}

func sendToStream(ctx context.Context, cancel context.CancelFunc, stream Stream, ch <-chan *core.PullingEvent, result chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(result)
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case val, ok := <-ch:
			if !ok {
				result <- nil
				return
			}
			if err := stream.Send(val); err != nil {
				result <- err
				cancel()
				return
			}
		}
	}
}

func (p *imageService) Remove(ctx context.Context, cmd RemoveCommand) error {
	return nil
}

func (p *imageService) List(ctx context.Context) ([]*core.Image, error) {
	return []*core.Image{}, nil
}
