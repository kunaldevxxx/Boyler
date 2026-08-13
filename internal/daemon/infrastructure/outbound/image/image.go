package image

import (
	"boyler/internal/daemon/core"
	domain "boyler/internal/daemon/core"
	"context"
)

type ImageManager interface {
	// Extract unpack .tar.gz archive
	Extract(ctx context.Context, name string, unpackDir string) error

	// IsExtracted check if image is extracted
	IsExtracted(ctx context.Context, name string) bool

	// GetRootfsPath return directory of rootfs
	GetRootfsPath(name string) string

	// Delete remove image
	Delete(ctx context.Context, name string) error
	Remove(ctx context.Context, name string) (*core.ImageRemoveResult, error)

	// Get return mage using name
	Get(ctx context.Context, name string) (*domain.Image, error)

	// List return list of images
	List(ctx context.Context) ([]*domain.Image, error)

	// Resolve returns the immutable identity currently assigned to a reference.
	Resolve(ctx context.Context, name string) (*domain.Image, error)

	// GetRootfsPathByDigest resolves an immutable rootfs without using a mutable tag.
	GetRootfsPathByDigest(digest string) (string, error)

	// Pull download images from dockerHub
	Pull(ctx context.Context, name string, ch chan *core.PullingEvent) error

	// Prune performs mark-and-sweep while preserving container-owned manifests.
	Prune(ctx context.Context, usage core.ImageUsage, options core.ImagePruneOptions) (*core.ImagePruneResult, error)
}
