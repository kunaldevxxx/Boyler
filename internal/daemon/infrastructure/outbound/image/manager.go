package image

import (
	"boyler/internal/daemon/core"
	domain "boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"boyler/pkg/files"
	"boyler/pkg/logger"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type imageManager struct {
	imageDir        string
	OperationSystem string
	Architecture    string
	layers          layer.Store
	lifecycle       sync.RWMutex
}

func NewImageManager(imageDir string) ImageManager {
	return NewImageManagerWithLayerStore(imageDir, layer.NewFilesystemStore(imageDir))
}

func NewImageManagerWithLayerStore(imageDir string, layers layer.Store) ImageManager {
	return &imageManager{imageDir: imageDir, OperationSystem: "linux", Architecture: "amd64", layers: layers}
}

func (i *imageManager) Extract(ctx context.Context, name, unpackDir string) error {
	i.lifecycle.Lock()
	defer i.lifecycle.Unlock()

	imageDir, storageName, err := i.resolveImageDir(name)
	if err != nil {
		return err
	}
	info, err := readLayersInfo(imageDir)
	if err != nil {
		return fmt.Errorf("read layers info: %w", err)
	}
	if info.SchemaVersion >= 2 {
		return i.extractV2(ctx, unpackDir, storageName, info)
	}
	return i.extractLegacy(ctx, imageDir, filepath.Join(unpackDir, storageName, "rootfs"), info)
}

func (i *imageManager) extractV2(ctx context.Context, unpackDir, storageName string, info layersInfo) error {
	manifestHash, err := layer.ParseDigest(info.ManifestDigest)
	if err != nil {
		return fmt.Errorf("invalid manifest digest: %w", err)
	}
	for _, descriptor := range info.Layers {
		valid, err := i.layers.Has(ctx, descriptor)
		if err != nil {
			return fmt.Errorf("verify layer %s: %w", descriptor.Digest, err)
		}
		if !valid {
			return fmt.Errorf("verify layer %s: local layer is missing or corrupted", descriptor.Digest)
		}
	}

	rootfs := filepath.Join(unpackDir, "rootfs", "sha256", manifestHash)
	if stat, err := os.Stat(rootfs); os.IsNotExist(err) {
		if err := i.extractContentAddressed(ctx, info.Layers, rootfs); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("check rootfs: %w", err)
	} else if !stat.IsDir() {
		return fmt.Errorf("rootfs store entry %s is not a directory", rootfs)
	}
	if err := publishRootfs(filepath.Join(unpackDir, storageName), rootfs); err != nil {
		return err
	}
	logger.FromContext(ctx).Info("image extracted", "layers", len(info.Layers), "manifest", info.ManifestDigest)
	return nil
}

func (i *imageManager) extractContentAddressed(ctx context.Context, descriptors []layer.Descriptor, rootfs string) error {
	parent := filepath.Dir(rootfs)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("create rootfs store: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".rootfs.part-*")
	if err != nil {
		return fmt.Errorf("create temporary rootfs: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	for _, descriptor := range descriptors {
		layerPath, err := i.layers.Path(descriptor.Digest)
		if err != nil {
			return fmt.Errorf("resolve layer %s: %w", descriptor.Digest, err)
		}
		if err := files.ApplyLayer(ctx, layerPath, temporary, descriptor.MediaType); err != nil {
			return fmt.Errorf("extract layer %s: %w", descriptor.Digest, err)
		}
	}
	if err := os.Rename(temporary, rootfs); err != nil {
		return fmt.Errorf("publish rootfs: %w", err)
	}
	committed = true
	return nil
}

func (i *imageManager) extractLegacy(ctx context.Context, imageDir, rootfs string, info layersInfo) error {
	if err := os.MkdirAll(filepath.Dir(rootfs), 0755); err != nil {
		return fmt.Errorf("create image directory: %w", err)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(rootfs), ".rootfs.part-*")
	if err != nil {
		return fmt.Errorf("create temporary rootfs: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	for index := 0; index < info.Num; index++ {
		layerPath := filepath.Join(imageDir, fmt.Sprintf("layer_%d.tar.gz", index))
		if err := files.ApplyLayer(ctx, layerPath, temporary, files.MediaTypeDockerLayerGzip); err != nil {
			return fmt.Errorf("extract layer %s: %w", layerPath, err)
		}
	}
	if err := os.RemoveAll(rootfs); err != nil {
		return fmt.Errorf("replace old rootfs: %w", err)
	}
	if err := os.Rename(temporary, rootfs); err != nil {
		return fmt.Errorf("publish rootfs: %w", err)
	}
	committed = true
	logger.FromContext(ctx).Info("legacy image extracted", "layers", info.Num)
	return nil
}

func publishRootfs(referenceDir, rootfs string) error {
	if err := os.MkdirAll(referenceDir, 0755); err != nil {
		return fmt.Errorf("create image reference directory: %w", err)
	}
	relative, err := filepath.Rel(referenceDir, rootfs)
	if err != nil {
		return fmt.Errorf("resolve rootfs link: %w", err)
	}
	temporary, err := os.CreateTemp(referenceDir, ".rootfs-link-*")
	if err != nil {
		return fmt.Errorf("create rootfs link placeholder: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	if err := os.Symlink(relative, temporaryPath); err != nil {
		return fmt.Errorf("create rootfs link: %w", err)
	}
	defer os.Remove(temporaryPath)
	destination := filepath.Join(referenceDir, "rootfs")
	if info, err := os.Lstat(destination); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("replace legacy rootfs: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect old rootfs: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish rootfs link: %w", err)
	}
	return nil
}

func (i *imageManager) IsExtracted(ctx context.Context, name string) bool {
	i.lifecycle.RLock()
	defer i.lifecycle.RUnlock()
	rootfs := i.getRootfsPath(name)
	info, err := os.Stat(rootfs)
	if err != nil || !info.IsDir() {
		logger.FromContext(ctx).Debug("image is not extracted", "image", name, "error", err)
		return false
	}
	return true
}

func (i *imageManager) GetRootfsPath(name string) string {
	i.lifecycle.RLock()
	defer i.lifecycle.RUnlock()
	return i.getRootfsPath(name)
}

func (i *imageManager) getRootfsPath(name string) string {
	_, storageName, err := i.resolveImageDir(name)
	if err != nil {
		return filepath.Join(i.imageDir, "_invalid_image_", "rootfs")
	}
	return filepath.Join(i.imageDir, storageName, "rootfs")
}

func (i *imageManager) Delete(ctx context.Context, name string) error {
	i.lifecycle.Lock()
	defer i.lifecycle.Unlock()
	path, _, err := i.resolveImageDir(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete image %s: %w", name, err)
	}
	logger.FromContext(ctx).Info("image deleted", "image", name)
	return nil
}

func (i *imageManager) Get(ctx context.Context, name string) (*domain.Image, error) {
	i.lifecycle.RLock()
	defer i.lifecycle.RUnlock()
	path, _, err := i.resolveImageDir(name)
	if err != nil {
		return nil, err
	}
	return readImageMetadata(filepath.Join(path, "meta.json"))
}

func readImageMetadata(path string) (*domain.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var metadata domain.Image
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (i *imageManager) List(ctx context.Context) ([]*domain.Image, error) {
	i.lifecycle.RLock()
	defer i.lifecycle.RUnlock()
	entries, err := os.ReadDir(i.imageDir)
	if err != nil {
		return []*domain.Image{}, err
	}
	images := make([]*domain.Image, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || reservedImageDirectory(entry.Name()) {
			continue
		}
		metadata, err := readImageMetadata(filepath.Join(i.imageDir, entry.Name(), "meta.json"))
		if err == nil {
			images = append(images, metadata)
		}
	}
	return images, nil
}

func (i *imageManager) Pull(ctx context.Context, name string, ch chan *core.PullingEvent) error {
	i.lifecycle.Lock()
	defer i.lifecycle.Unlock()
	defer close(ch)
	puller := NewDockerHubPuller(Platform{OS: i.OperationSystem, Architecture: i.Architecture}, ch, i.layers)
	if _, err := puller.Pull(ctx, name, i.imageDir); err != nil {
		return fmt.Errorf("fetch image: %w", err)
	}
	return nil
}

func (i *imageManager) Prune(ctx context.Context) error {
	i.lifecycle.Lock()
	defer i.lifecycle.Unlock()
	entries, err := os.ReadDir(i.imageDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read images directory: %w", err)
	}
	used := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() || reservedImageDirectory(entry.Name()) {
			continue
		}
		info, err := readLayersInfo(filepath.Join(i.imageDir, entry.Name()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read image %s layers: %w", entry.Name(), err)
		}
		for _, descriptor := range info.Layers {
			used[strings.ToLower(descriptor.Digest)] = struct{}{}
		}
	}
	if err := i.layers.Prune(ctx, used); err != nil {
		return fmt.Errorf("prune layer store: %w", err)
	}
	return nil
}

func (i *imageManager) resolveImageDir(name string) (string, string, error) {
	encoded, err := StorageName(name)
	if err != nil {
		return "", "", err
	}
	encodedPath := filepath.Join(i.imageDir, encoded)
	if _, err := os.Stat(encodedPath); err == nil {
		return encodedPath, encoded, nil
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	legacy, err := LegacyStorageName(name)
	if err == nil && legacy != encoded {
		legacyPath := filepath.Join(i.imageDir, legacy)
		if _, statErr := os.Stat(legacyPath); statErr == nil {
			return legacyPath, legacy, nil
		} else if !os.IsNotExist(statErr) {
			return "", "", statErr
		}
	}
	return encodedPath, encoded, nil
}

func reservedImageDirectory(name string) bool {
	return name == "blobs" || name == "rootfs" || strings.HasPrefix(name, ".")
}

func readLayersInfo(dir string) (layersInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, layersInfoFileName))
	if err != nil {
		return layersInfo{}, fmt.Errorf("read layers info: %w", err)
	}
	var info layersInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return layersInfo{}, fmt.Errorf("unmarshal layers info: %w", err)
	}
	return info, nil
}
