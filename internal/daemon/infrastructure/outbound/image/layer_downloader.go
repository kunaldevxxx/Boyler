package image

import (
	"boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"context"
	"fmt"
	"io"
	"net/http"
)

type layerDownloader struct {
	registry *dockerHubRegistry
	store    layer.Store
	progress chan *core.PullingEvent
}

func (d layerDownloader) download(ctx context.Context, repository, token string, layers []ociDescriptor) error {
	if d.store == nil {
		return fmt.Errorf("layer store is not configured")
	}
	for _, descriptor := range layers {
		if err := d.downloadLayer(ctx, repository, token, descriptor); err != nil {
			return err
		}
	}
	return nil
}

func (d layerDownloader) downloadLayer(ctx context.Context, repository, token string, descriptor layer.Descriptor) error {
	cached, err := d.store.Ensure(
		ctx,
		descriptor,
		func() (io.ReadCloser, error) {
			resp, err := d.registry.blob(ctx, repository, descriptor.Digest, token)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return nil, fmt.Errorf("download layer %s failed: %s", descriptor.Digest, resp.Status)
			}
			return resp.Body, nil
		},
		func(downloaded int64) {
			d.sendProgress(ctx, &core.PullingEvent{
				LayId:    shortDigest(descriptor.Digest),
				Status:   "Downloading",
				Progress: downloaded,
				Total:    descriptor.Size,
			})
		},
	)
	if err != nil {
		return err
	}
	status := "Pull complete"
	if cached {
		status = "Already exists"
	}
	if err := d.sendProgress(ctx, &core.PullingEvent{
		LayId:    shortDigest(descriptor.Digest),
		Status:   status,
		Progress: descriptor.Size,
		Total:    descriptor.Size,
	}); err != nil {
		return err
	}
	return nil
}

func (d layerDownloader) sendProgress(ctx context.Context, event *core.PullingEvent) error {
	if d.progress == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case d.progress <- event:
		return nil
	}
}

func shortDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}
