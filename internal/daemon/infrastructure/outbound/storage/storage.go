package storage

import (
	"context"

	"boyler/internal/daemon/core"
)

// ContainerStorage is the outbound port used by application services to
// persist container metadata. Implementations must be safe for concurrent use.
type ContainerStorage interface {
	Save(ctx context.Context, container core.Container) error
	Get(ctx context.Context, id string) (*core.Container, error)
	List(ctx context.Context) ([]*core.Container, error)
	Delete(ctx context.Context, id string) error
	Update(ctx context.Context, container core.Container) error
}
