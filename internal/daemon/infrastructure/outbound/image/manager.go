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
	"sort"
	"strings"
	"sync"
	"time"
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
		if metadata, metadataErr := readImageMetadata(filepath.Join(imageDir, imageMetadataFileName)); metadataErr == nil && metadata.Digest != "" {
			immutableInfo, manifestErr := i.readManifestInfo(metadata.Digest)
			if manifestErr != nil {
				return fmt.Errorf("read immutable manifest %s: %w", metadata.Digest, manifestErr)
			}
			info = immutableInfo
		}
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
	_, err := i.Remove(ctx, name)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, core.ErrImageNotFound) {
		return nil
	}
	return err
}

func (i *imageManager) resolveUnlocked(name string) (*domain.Image, error) {
	path, _, err := i.resolveImageDir(name)
	if err != nil {
		return nil, err
	}
	metadata, err := readImageMetadata(filepath.Join(path, imageMetadataFileName))
	if err == nil {
		metadata.RootfsPath, _ = i.GetRootfsPathByDigest(metadata.RootfsDigest)
		return metadata, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read image metadata: %w", err)
	}
	info, layersErr := readLayersInfo(path)
	if layersErr != nil {
		if errors.Is(layersErr, os.ErrNotExist) {
			canonical, _, _, referenceErr := canonicalDockerHubReference(name)
			if referenceErr != nil {
				return nil, referenceErr
			}
			return nil, fmt.Errorf("%w: %s", core.ErrImageNotFound, canonical)
		}
		return nil, layersErr
	}
	canonical, repository, tag, refErr := canonicalDockerHubReference(name)
	if refErr != nil {
		return nil, refErr
	}
	result := &domain.Image{ID: info.ManifestDigest, Name: strings.TrimPrefix(repository, "library/"), Tag: tag,
		Reference: canonical, Digest: info.ManifestDigest, RootfsDigest: info.ManifestDigest}
	for _, descriptor := range info.Layers {
		result.Size += descriptor.Size
		result.Layers = append(result.Layers, descriptor.Digest)
	}
	if info.ManifestDigest != "" {
		result.RootfsPath, _ = i.GetRootfsPathByDigest(info.ManifestDigest)
	} else {
		result.RootfsPath = filepath.Join(path, "rootfs")
	}
	return result, nil
}

func (i *imageManager) Get(ctx context.Context, name string) (*domain.Image, error) {
	return i.Resolve(ctx, name)
}

func (i *imageManager) Resolve(ctx context.Context, name string) (*domain.Image, error) {
	i.lifecycle.RLock()
	defer i.lifecycle.RUnlock()
	return i.resolveUnlocked(name)
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
	if _, err := layer.ParseDigest(metadata.Digest); err != nil {
		return nil, fmt.Errorf("invalid image digest: %w", err)
	}
	if metadata.RootfsDigest == "" {
		metadata.RootfsDigest = metadata.Digest
	}
	if !strings.EqualFold(metadata.RootfsDigest, metadata.Digest) {
		return nil, fmt.Errorf("rootfs digest %s does not match image digest %s", metadata.RootfsDigest, metadata.Digest)
	}
	canonical, _, _, err := canonicalDockerHubReference(metadata.Reference)
	if err != nil || canonical != metadata.Reference {
		return nil, fmt.Errorf("invalid canonical image reference %q", metadata.Reference)
	}
	if metadata.Size < 0 {
		return nil, fmt.Errorf("invalid negative image size %d", metadata.Size)
	}
	for _, digest := range metadata.Layers {
		if _, err := layer.ParseDigest(digest); err != nil {
			return nil, fmt.Errorf("invalid image layer digest: %w", err)
		}
	}
	return &metadata, nil
}

func (i *imageManager) List(ctx context.Context) ([]*domain.Image, error) {
	i.lifecycle.RLock()
	defer i.lifecycle.RUnlock()
	entries, err := os.ReadDir(i.imageDir)
	if os.IsNotExist(err) {
		return []*domain.Image{}, nil
	}
	if err != nil {
		return []*domain.Image{}, err
	}
	images := make([]*domain.Image, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || reservedImageDirectory(entry.Name()) {
			continue
		}
		metadata, err := readImageMetadata(filepath.Join(i.imageDir, entry.Name(), imageMetadataFileName))
		if err != nil {
			reference, decodeErr := decodeStorageName(entry.Name())
			if decodeErr != nil {
				continue
			}
			metadata, err = i.resolveUnlocked(reference)
			if err != nil {
				continue
			}
		}
		metadata.RootfsPath, _ = i.GetRootfsPathByDigest(metadata.RootfsDigest)
		images = append(images, metadata)
	}
	sort.Slice(images, func(a, b int) bool { return images[a].Reference < images[b].Reference })
	return images, nil
}

func (i *imageManager) GetRootfsPathByDigest(digest string) (string, error) {
	hash, err := layer.ParseDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(i.imageDir, "rootfs", "sha256", hash), nil
}

func (i *imageManager) Remove(ctx context.Context, name string) (*core.ImageRemoveResult, error) {
	i.lifecycle.Lock()
	defer i.lifecycle.Unlock()
	metadata, err := i.resolveUnlocked(name)
	if err != nil {
		return nil, err
	}
	path, _, err := i.resolveImageDir(name)
	if err != nil {
		return nil, err
	}
	if metadata.Digest != "" {
		if _, err := i.readManifestInfo(metadata.Digest); errors.Is(err, os.ErrNotExist) {
			info, layersErr := readLayersInfo(path)
			if layersErr != nil {
				return nil, layersErr
			}
			hash, digestErr := layer.ParseDigest(metadata.Digest)
			if digestErr != nil {
				return nil, digestErr
			}
			if err := newImageStore(i.imageDir).writeManifestInfo(hash, info); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	}
	if err := i.trashReference(path); err != nil {
		return nil, fmt.Errorf("remove image reference %s: %w", name, err)
	}
	logger.FromContext(ctx).Info("image reference removed", "image", metadata.Reference, "digest", metadata.Digest)
	return &core.ImageRemoveResult{Reference: metadata.Reference, Digest: metadata.Digest}, nil
}

func (i *imageManager) trashReference(path string) error {
	trash := filepath.Join(i.imageDir, ".trash")
	if err := os.MkdirAll(trash, 0700); err != nil {
		return err
	}
	target := filepath.Join(trash, filepath.Base(path)+"-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.Rename(path, target); err != nil {
		return err
	}
	return os.RemoveAll(target)
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

func (i *imageManager) Prune(ctx context.Context, usage core.ImageUsage, options core.ImagePruneOptions) (*core.ImagePruneResult, error) {
	i.lifecycle.Lock()
	defer i.lifecycle.Unlock()
	result := &core.ImagePruneResult{}
	if !options.DryRun {
		if err := os.RemoveAll(filepath.Join(i.imageDir, ".trash")); err != nil {
			return nil, fmt.Errorf("clean image trash: %w", err)
		}
	}
	entries, err := os.ReadDir(i.imageDir)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read images directory: %w", err)
	}
	liveManifests := make(map[string]struct{}, len(usage.ManifestDigests))
	manifestFallback := make(map[string]layersInfo)
	legacyLayers := make(map[string]struct{})
	for digest := range usage.ManifestDigests {
		liveManifests[strings.ToLower(digest)] = struct{}{}
	}
	for _, entry := range entries {
		if !entry.IsDir() || reservedImageDirectory(entry.Name()) {
			continue
		}
		path := filepath.Join(i.imageDir, entry.Name())
		metadata, metadataErr := readImageMetadata(filepath.Join(path, imageMetadataFileName))
		if metadataErr != nil {
			info, infoErr := readLayersInfo(path)
			if infoErr != nil {
				result.QuarantinedReferences = append(result.QuarantinedReferences, entry.Name())
				if !options.DryRun {
					if err := i.quarantineReference(path); err != nil {
						return nil, fmt.Errorf("quarantine corrupt image reference %s: %w", entry.Name(), err)
					}
				}
				continue
			}
			if info.ManifestDigest == "" {
				for _, descriptor := range info.Layers {
					legacyLayers[strings.ToLower(descriptor.Digest)] = struct{}{}
				}
				continue
			}
			digest := strings.ToLower(info.ManifestDigest)
			_, usedByContainer := liveManifests[digest]
			if options.All && !usedByContainer {
				reference, decodeErr := decodeStorageName(entry.Name())
				if decodeErr != nil {
					reference = entry.Name()
				}
				size, sizeErr := directorySize(path)
				if sizeErr != nil {
					return nil, fmt.Errorf("measure reference %s: %w", reference, sizeErr)
				}
				result.ReclaimedBytes += size
				result.DeletedReferences = append(result.DeletedReferences, reference)
				if !options.DryRun {
					if err := i.trashReference(path); err != nil {
						return nil, fmt.Errorf("prune legacy reference %s: %w", reference, err)
					}
				}
				continue
			}
			liveManifests[digest] = struct{}{}
			manifestFallback[digest] = info
			continue
		}
		digest := strings.ToLower(metadata.Digest)
		_, usedByContainer := liveManifests[digest]
		if options.All && !usedByContainer {
			size, sizeErr := directorySize(path)
			if sizeErr != nil {
				return nil, fmt.Errorf("measure reference %s: %w", metadata.Reference, sizeErr)
			}
			result.ReclaimedBytes += size
			result.DeletedReferences = append(result.DeletedReferences, metadata.Reference)
			if !options.DryRun {
				if err := i.trashReference(path); err != nil {
					return nil, fmt.Errorf("prune reference %s: %w", metadata.Reference, err)
				}
			}
			continue
		}
		liveManifests[digest] = struct{}{}
	}

	usedLayers := make(map[string]struct{})
	for digest := range liveManifests {
		info, err := i.readManifestInfo(digest)
		if err != nil {
			var ok bool
			info, ok = manifestFallback[digest]
			if !ok {
				return nil, fmt.Errorf("read live manifest %s: %w", digest, err)
			}
		}
		for _, descriptor := range info.Layers {
			usedLayers[strings.ToLower(descriptor.Digest)] = struct{}{}
		}
	}
	for digest := range legacyLayers {
		usedLayers[digest] = struct{}{}
	}
	if err := i.collectAndPruneManifests(liveManifests, options.DryRun, result); err != nil {
		return nil, err
	}
	if err := i.collectAndPruneRootfs(liveManifests, options.DryRun, result); err != nil {
		return nil, err
	}
	if err := i.collectUnusedLayers(usedLayers, result); err != nil {
		return nil, err
	}
	if !options.DryRun {
		if err := i.layers.Prune(ctx, usedLayers); err != nil {
			return nil, fmt.Errorf("prune layer store: %w", err)
		}
	}
	sort.Strings(result.DeletedReferences)
	sort.Strings(result.DeletedManifests)
	sort.Strings(result.DeletedRootfs)
	sort.Strings(result.DeletedLayers)
	sort.Strings(result.QuarantinedReferences)
	return result, nil
}

func (i *imageManager) quarantineReference(path string) error {
	directory := filepath.Join(i.imageDir, ".quarantine")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	target := filepath.Join(directory, filepath.Base(path)+"-"+fmt.Sprint(time.Now().UnixNano()))
	return os.Rename(path, target)
}

func (i *imageManager) readManifestInfo(digest string) (layersInfo, error) {
	hash, err := layer.ParseDigest(digest)
	if err != nil {
		return layersInfo{}, err
	}
	data, err := os.ReadFile(filepath.Join(i.imageDir, "manifests", "sha256", hash+".json"))
	if err != nil {
		return layersInfo{}, err
	}
	var info layersInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return layersInfo{}, err
	}
	return info, nil
}

func (i *imageManager) collectAndPruneManifests(live map[string]struct{}, dryRun bool, result *core.ImagePruneResult) error {
	dir := filepath.Join(i.imageDir, "manifests", "sha256")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read manifest store: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		digest := "sha256:" + strings.TrimSuffix(entry.Name(), ".json")
		if _, ok := live[digest]; ok {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if info, statErr := entry.Info(); statErr == nil {
			result.ReclaimedBytes += info.Size()
		}
		result.DeletedManifests = append(result.DeletedManifests, digest)
		if !dryRun {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove manifest %s: %w", digest, err)
			}
		}
	}
	return nil
}

func (i *imageManager) collectAndPruneRootfs(live map[string]struct{}, dryRun bool, result *core.ImagePruneResult) error {
	dir := filepath.Join(i.imageDir, "rootfs", "sha256")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read rootfs store: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		digest := "sha256:" + entry.Name()
		if _, ok := live[digest]; ok {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		size, sizeErr := directorySize(path)
		if sizeErr != nil {
			return sizeErr
		}
		result.ReclaimedBytes += size
		result.DeletedRootfs = append(result.DeletedRootfs, digest)
		if !dryRun {
			trash := filepath.Join(i.imageDir, ".trash")
			if err := os.MkdirAll(trash, 0700); err != nil {
				return err
			}
			target := filepath.Join(trash, "rootfs-"+entry.Name()+"-"+fmt.Sprint(time.Now().UnixNano()))
			if err := os.Rename(path, target); err != nil {
				return err
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i *imageManager) collectUnusedLayers(used map[string]struct{}, result *core.ImagePruneResult) error {
	dir := filepath.Join(i.imageDir, "blobs", "sha256")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read layer store: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		digest := "sha256:" + entry.Name()
		if _, ok := used[digest]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result.ReclaimedBytes += info.Size()
		result.DeletedLayers = append(result.DeletedLayers, digest)
	}
	return nil
}

func directorySize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
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
	return name == "blobs" || name == "rootfs" || name == "manifests" || strings.HasPrefix(name, ".")
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
