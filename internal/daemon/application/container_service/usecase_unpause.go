package application

import (
	"boyler/internal/daemon/core"
	registry "boyler/internal/daemon/infrastructure/outbound/registry"
	storage "boyler/internal/daemon/infrastructure/outbound/storage"
	"boyler/pkg/logger"
	"context"
)

type Unpauser struct {
	reg   registry.ResourcesRegistry
	store storage.ContainerStorage
}

func NewUnpauser(d Deps) *Unpauser {
	return &Unpauser{
		reg:   d.Reg,
		store: d.Store,
	}
}

func (u *Unpauser) Execute(ctx context.Context, cmd UnpauseContainerCommand) (*UnpauseContainerResponse, error) {
	ctx = logger.WithFields(ctx, "id", cmd.ID)
	if err := u.pauseSignalToCgroup(ctx, cmd.ID); err != nil {
		return nil, err
	}
	container, err := u.store.Get(ctx, cmd.ID)
	if err != nil {
		return nil, &core.InternalDaemonError{Op: "get container for unpause", Err: err}
	}
	container.Status = core.StatusRunning
	if err := u.store.Update(ctx, *container); err != nil {
		return nil, &core.InternalDaemonError{Op: "save unpaused container state", Err: err}
	}
	return &UnpauseContainerResponse{
		ContainerContext: ContainerContext{ID: cmd.ID},
	}, nil
}

func (u *Unpauser) pauseSignalToCgroup(ctx context.Context, id string) error {
	manager, err := u.reg.Get(id)
	if err != nil {
		return &core.CgroupsError{Op: "pause", Err: err}
	}
	if err := manager.Unfreeze(ctx); err != nil {
		return &core.CgroupsError{Op: "unfreeze", Err: err}
	}
	return nil
}
