package application

import (
	image "boyler/internal/daemon/application/image_service"
	net "boyler/internal/daemon/application/network_service"
	layer "boyler/internal/daemon/infrastructure/outbound/image"
	limits "boyler/internal/daemon/infrastructure/outbound/limits"
	overlay "boyler/internal/daemon/infrastructure/outbound/overlay"
	registry "boyler/internal/daemon/infrastructure/outbound/registry"
	storage "boyler/internal/daemon/infrastructure/outbound/storage"
	run "boyler/internal/runtime"
	"log/slog"
	"sync"
)

type Deps struct {
	Runtime        run.Runtime
	FS             overlay.VolumeManager
	Images         layer.ImageManager
	Network        net.NetworkService
	Reg            registry.ResourcesRegistry
	Store          storage.ContainerStorage
	Logger         *slog.Logger
	Conf           ServiceConfig
	CgroupFactory  limits.Factory
	Pull           image.ImageService
	ImageLifecycle *sync.RWMutex
}

type ServiceConfig struct {
	UnpackDir    string
	ContainerDir string
	CgroupPath   string
	SystemPath   string
}
