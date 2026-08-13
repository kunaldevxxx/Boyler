package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	service "boyler/internal/daemon/application/container_service"
	imageservice "boyler/internal/daemon/application/image_service"
	networkservice "boyler/internal/daemon/application/network_service"
	systemservice "boyler/internal/daemon/application/system_service"
	image "boyler/internal/daemon/infrastructure/outbound/image"
	layer "boyler/internal/daemon/infrastructure/outbound/layer"
	limits "boyler/internal/daemon/infrastructure/outbound/limits"
	network "boyler/internal/daemon/infrastructure/outbound/network"
	overlay "boyler/internal/daemon/infrastructure/outbound/overlay"
	registry "boyler/internal/daemon/infrastructure/outbound/registry"
	storage "boyler/internal/daemon/infrastructure/outbound/storage/in-memory"
	systeminspector "boyler/internal/daemon/infrastructure/outbound/system"
	runtime "boyler/internal/runtime/myrunc"
	"boyler/pkg/logger"
)

type DaemonConfig struct {
	ImagesPath     string
	ContainersPath string
	RuntimeBinPath string
	ShimBinPath    string
	StatePath      string

	Network        networkservice.NetworkServiceConfig
	NetworkManager network.Config
	Service        service.ServiceConfig
}

type SharedManager struct {
	FS             overlay.VolumeManager
	Image          image.ImageManager
	Layers         layer.Store
	Store          *storage.ContainerRepository
	ImageLifecycle *sync.RWMutex
}

type DaemonFactory struct {
	config DaemonConfig
	shared SharedManager
}

func NewDaemonFactory(config DaemonConfig) *DaemonFactory {
	layers := layer.NewFilesystemStore(config.ImagesPath)
	return &DaemonFactory{
		config: config,
		shared: SharedManager{
			FS:             overlay.NewOverlayManager(config.ImagesPath, config.ContainersPath),
			Image:          image.NewImageManagerWithLayerStore(config.ImagesPath, layers),
			Layers:         layers,
			Store:          storage.NewContainerRepository(),
			ImageLifecycle: &sync.RWMutex{},
		},
	}
}

func NewDaemonFactoryFromEnv() *DaemonFactory {
	root := workingDirectory()
	imagesPath := envOr(root, os.Getenv("IMAGE_PATH"))
	containersPath := envOr(root, os.Getenv("CONTAINER_DIR"))

	return NewDaemonFactory(DaemonConfig{
		ImagesPath:     imagesPath,
		ContainersPath: containersPath,
		RuntimeBinPath: envOr(root, os.Getenv("BIN_MYRUNC")),
		ShimBinPath:    optionalPath(root, os.Getenv("BIN_SHIM")),
		StatePath:      optionalPath(root, os.Getenv("STATE_PATH")),
		NetworkManager: network.Config{
			Eth0:    os.Getenv("DEFAULT_ETH0"),
			Forward: os.Getenv("IP_FORWARDING_PATH"),
		},
		Network: networkservice.NetworkServiceConfig{
			BridgeName:      os.Getenv("BRIDGE_NAME"),
			BridgeIP:        os.Getenv("BRIDGE_IP"),
			InternalNetwork: os.Getenv("CONTAINER_LOCAL_NETWORK"),
		},
		Service: service.ServiceConfig{
			UnpackDir:    envOr(root, os.Getenv("UNPACK_DIR")),
			ContainerDir: containersPath,
			CgroupPath:   os.Getenv("CGROUP_PATH"),
			SystemPath:   os.Getenv("SYSTEM_PATH"),
		},
	})
}

func (d *DaemonFactory) NewSystemService(startedAt time.Time) (systemservice.Service, error) {
	if d == nil {
		return nil, fmt.Errorf("daemon factory is nil")
	}
	inspector := systeminspector.NewInspector(systeminspector.Config{
		RuntimePath: d.config.RuntimeBinPath, ImagesPath: d.config.ImagesPath,
		ContainersPath: d.config.ContainersPath, ShimPath: d.config.ShimBinPath, StatePath: d.config.StatePath,
	})
	return systemservice.New(inspector, startedAt), nil
}

func (d *DaemonFactory) NewContainerService() (service.ContainerService, error) {
	if d == nil {
		return nil, fmt.Errorf("daemon factory is nil")
	}

	networkService, err := networkservice.NewNetworkService(
		network.NewNetworkManager(d.config.NetworkManager),
		d.config.Network,
	)
	if err != nil {
		return nil, fmt.Errorf("create network service: %w", err)
	}

	return service.NewContainerService(service.Deps{
		Runtime:        runtime.NewMyRunc(d.config.RuntimeBinPath),
		FS:             d.shared.FS,
		Images:         d.shared.Image,
		Network:        networkService,
		Reg:            registry.NewRepo(),
		Store:          d.shared.Store,
		Logger:         logger.InitLogger(false),
		CgroupFactory:  limits.NewFactory(),
		Conf:           d.config.Service,
		ImageLifecycle: d.shared.ImageLifecycle,
	}), nil
}

func (d *DaemonFactory) NewImageService() (imageservice.ImageService, error) {
	return imageservice.NewImageService(
		imageservice.ImageServiceConfig{
			ContainersDir: d.config.ContainersPath,
		},
		d.shared.Image,
		d.shared.Store,
		d.shared.ImageLifecycle,
	), nil
}

func envOr(node1 string, node2 string) string { return filepath.Join(node1, node2) }

func optionalPath(root, value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(root, value)
}

func workingDirectory() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func projectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	projectRoot := wd // /home/tema/Boyler
	if filepath.Base(wd) == "bin" {
		projectRoot = filepath.Dir(wd)
	}
	return projectRoot, nil
}
