package imageservice

import (
	"boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/image"
	storage "boyler/internal/daemon/infrastructure/outbound/storage"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Stream interface {
	Send(*core.PullingEvent) error
}

type ImageServiceConfig struct {
	ContainersDir string
}

type ImageService interface {
	Pull(ctx context.Context, name string, stream Stream) error
	Remove(ctx context.Context, cmd RemoveCommand) (*core.ImageRemoveResult, error)
	List(ctx context.Context) ([]*core.Image, error)
	Inspect(ctx context.Context, name string) (*core.Image, error)
	Prune(ctx context.Context, cmd PruneCommand) (*core.ImagePruneResult, error)
}

type imageService struct {
	image     image.ImageManager
	config    ImageServiceConfig
	store     storage.ContainerStorage
	lifecycle *sync.RWMutex
}

func NewImageService(config ImageServiceConfig, im image.ImageManager, store storage.ContainerStorage, lifecycle ...*sync.RWMutex) ImageService {
	service := &imageService{config: config, image: im, store: store}
	if len(lifecycle) > 0 {
		service.lifecycle = lifecycle[0]
	}
	return service
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

func (p *imageService) Remove(ctx context.Context, cmd RemoveCommand) (*core.ImageRemoveResult, error) {
	if p.lifecycle != nil {
		p.lifecycle.Lock()
		defer p.lifecycle.Unlock()
	}
	resolved, err := p.image.Resolve(ctx, cmd.ImageIdentify)
	if err != nil {
		return nil, err
	}
	containers, err := p.store.List(ctx)
	if err != nil {
		return nil, err
	}
	knownContainers := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		knownContainers[container.ID] = struct{}{}
		if container.ImageDigest == resolved.Digest || container.ImageID == resolved.Reference || container.ImageID == cmd.ImageIdentify {
			if !cmd.Force {
				return nil, fmt.Errorf("image %s is used by container %s; use --force to remove only the reference", resolved.Reference, container.ID)
			}
			if container.RootfsDigest == "" {
				return nil, fmt.Errorf("image %s is used by legacy container %s without an immutable rootfs digest; force removal is unsafe", resolved.Reference, container.ID)
			}
		}
	}
	owners, err := p.persistedOwners(resolved.Digest, knownContainers)
	if err != nil {
		return nil, err
	}
	if !cmd.Force && len(owners) > 0 {
		return nil, fmt.Errorf("image %s is used by container %s; use --force to remove only the reference", resolved.Reference, owners[0])
	}
	return p.image.Remove(ctx, cmd.ImageIdentify)
}

func (p *imageService) persistedOwners(digest string, knownContainers map[string]struct{}) ([]string, error) {
	if p.config.ContainersDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(p.config.ContainersDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read containers directory: %w", err)
	}
	var owners []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.config.ContainersDir, entry.Name(), ".boyler-image.json"))
		if os.IsNotExist(err) {
			if _, known := knownContainers[entry.Name()]; known {
				continue
			}
			return nil, fmt.Errorf("container %s has no persisted image digest; refusing unsafe image removal", entry.Name())
		}
		if err != nil {
			return nil, err
		}
		var identity struct {
			ImageDigest string `json:"imageDigest"`
		}
		if err := json.Unmarshal(data, &identity); err != nil {
			return nil, fmt.Errorf("decode image identity for container %s: %w", entry.Name(), err)
		}
		if strings.EqualFold(identity.ImageDigest, digest) {
			owners = append(owners, entry.Name())
		}
	}
	return owners, nil
}

func (p *imageService) List(ctx context.Context) ([]*core.Image, error) {
	return p.image.List(ctx)
}

func (p *imageService) Inspect(ctx context.Context, name string) (*core.Image, error) {
	return p.image.Resolve(ctx, name)
}

func (p *imageService) Prune(ctx context.Context, cmd PruneCommand) (*core.ImagePruneResult, error) {
	if p.lifecycle != nil {
		p.lifecycle.Lock()
		defer p.lifecycle.Unlock()
	}
	containers, err := p.store.List(ctx)
	if err != nil {
		return nil, err
	}
	usage := core.ImageUsage{ManifestDigests: make(map[string]struct{})}
	knownContainers := make(map[string]struct{}, len(containers))
	for _, container := range containers {
		knownContainers[container.ID] = struct{}{}
		if container.ImageDigest != "" {
			usage.ManifestDigests[container.ImageDigest] = struct{}{}
			continue
		}
		resolved, err := p.image.Resolve(ctx, container.ImageID)
		if err != nil {
			return nil, fmt.Errorf("resolve image for legacy container %s: %w", container.ID, err)
		}
		usage.ManifestDigests[resolved.Digest] = struct{}{}
	}
	if err := p.addPersistedUsage(usage.ManifestDigests, knownContainers); err != nil {
		return nil, err
	}
	return p.image.Prune(ctx, usage, core.ImagePruneOptions{All: cmd.All, DryRun: cmd.DryRun})
}

func (p *imageService) addPersistedUsage(used map[string]struct{}, knownContainers map[string]struct{}) error {
	if p.config.ContainersDir == "" {
		return nil
	}
	entries, err := os.ReadDir(p.config.ContainersDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read containers directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.config.ContainersDir, entry.Name(), ".boyler-image.json"))
		if os.IsNotExist(err) {
			if _, known := knownContainers[entry.Name()]; known {
				continue
			}
			return fmt.Errorf("container %s has no persisted image digest; refusing unsafe prune", entry.Name())
		}
		if err != nil {
			return fmt.Errorf("read image identity for container %s: %w", entry.Name(), err)
		}
		var identity struct {
			ImageDigest string `json:"imageDigest"`
		}
		if err := json.Unmarshal(data, &identity); err != nil {
			return fmt.Errorf("decode image identity for container %s: %w", entry.Name(), err)
		}
		if identity.ImageDigest != "" {
			used[identity.ImageDigest] = struct{}{}
		}
	}
	return nil
}
