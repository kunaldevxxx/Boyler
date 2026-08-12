package image

import (
	"boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"boyler/pkg/logger"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DockerHubPuller coordinates the components involved in pulling an image.
// HTTP transport, manifest resolution, layer transfer and disk layout live in
// dedicated components; this type only describes the workflow.
type DockerHubPuller struct {
	HTTPClient *http.Client
	Platform   Platform
	Progress   chan *core.PullingEvent
	LayerStore layer.Store
}

type Platform struct {
	OS           string
	Architecture string
}

func NewDockerHubPuller(osSettings Platform, ch chan *core.PullingEvent, stores ...layer.Store) *DockerHubPuller {
	puller := &DockerHubPuller{
		HTTPClient: &http.Client{Timeout: 5 * time.Minute},
		Platform: Platform{
			OS:           osSettings.OS,
			Architecture: osSettings.Architecture,
		},
		Progress: ch,
	}
	if len(stores) > 0 {
		puller.LayerStore = stores[0]
	}
	return puller
}

func (p *DockerHubPuller) Supports(ref string) bool {
	return !strings.Contains(ref, "/") || strings.HasPrefix(ref, "docker.io/") || !strings.Contains(strings.Split(ref, "/")[0], ".")
}

func (p *DockerHubPuller) Pull(ctx context.Context, ref string, destDir string) (string, error) {
	log := logger.FromContext(ctx)
	log.Debug("Start pulling image", "image", ref)

	reference, err := parseDockerHubReference(ref)
	if err != nil {
		return "", err
	}
	registry := newDockerHubRegistry(p.HTTPClient)

	token, err := registry.token(ctx, reference.repository)
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}

	resolver := manifestResolver{
		registry: registry,
		platform: p.Platform,
	}
	manifest, digest, err := resolver.resolve(ctx, reference, token)
	if err != nil {
		return "", err
	}

	store := newImageStore(destDir)
	imagePath, err := store.prepare(reference.storageName)
	if err != nil {
		return "", err
	}

	layerStore := p.LayerStore
	if layerStore == nil {
		layerStore = layer.NewFilesystemStore(destDir)
	}
	downloader := layerDownloader{
		registry: registry,
		progress: p.Progress,
		store:    layerStore,
	}
	if err := downloader.download(ctx, reference.repository, token, manifest.Layers); err != nil {
		return "", err
	}
	if err := store.writeLayersInfo(imagePath, layersInfo{
		SchemaVersion:  2,
		ManifestDigest: digest,
		Layers:         manifest.Layers,
	}); err != nil {
		return "", err
	}

	return digest, nil
}

// dockerHubToken is kept as a small compatibility seam for package callers;
// the HTTP exchange itself belongs to dockerHubRegistry.
func (p *DockerHubPuller) dockerHubToken(ctx context.Context, repository string) (string, error) {
	if !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	return newDockerHubRegistry(p.HTTPClient).token(ctx, repository)
}

func (p *DockerHubPuller) isExistOr(path string) error {
	return imageStore{}.createAt(path)
}
