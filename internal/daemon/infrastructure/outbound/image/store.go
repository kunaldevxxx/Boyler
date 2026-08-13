package image

import (
	"boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const layersInfoFileName = "layers.json"
const imageMetadataFileName = "image.json"

type imageStore struct {
	root string
}

type layersInfo struct {
	Num            int                `json:"num,omitempty"`
	SchemaVersion  int                `json:"schemaVersion,omitempty"`
	ManifestDigest string             `json:"manifestDigest,omitempty"`
	Layers         []layer.Descriptor `json:"layers,omitempty"`
}

func newImageStore(root string) imageStore {
	return imageStore{root: root}
}

func (s imageStore) prepare(name string) (string, error) {
	if err := os.MkdirAll(s.root, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(s.root, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("failed to create image directory: %w", err)
	}
	return path, nil
}

func (imageStore) createAt(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("image has already been downloaded")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

func (imageStore) writeLayersInfo(saveDir string, info layersInfo) error {
	current, err := readLayersInfo(saveDir)
	invalidateRootfs := err == nil && !reflect.DeepEqual(current, info)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read current layers info: %w", err)
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal layers info: %w", err)
	}
	temporary, err := os.CreateTemp(saveDir, ".layers-*.json")
	if err != nil {
		return fmt.Errorf("create temporary layers info: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary layers info: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync layers info: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close layers info: %w", err)
	}
	if invalidateRootfs {
		if err := os.RemoveAll(filepath.Join(saveDir, "rootfs")); err != nil {
			return fmt.Errorf("invalidate image rootfs: %w", err)
		}
	}
	path := filepath.Join(saveDir, layersInfoFileName)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit layers info: %w", err)
	}
	committed = true
	return nil
}

func (s imageStore) writeImageMetadata(saveDir, reference string, info layersInfo) error {
	manifestHash, err := layer.ParseDigest(info.ManifestDigest)
	if err != nil {
		return fmt.Errorf("invalid manifest digest: %w", err)
	}
	if err := s.writeManifestInfo(manifestHash, info); err != nil {
		return err
	}

	_, name, tag, err := canonicalDockerHubReference(reference)
	if err != nil {
		return err
	}
	name = strings.TrimPrefix(name, "library/")
	createdAt := time.Now().UTC()
	if existing, readErr := readImageMetadata(filepath.Join(saveDir, imageMetadataFileName)); readErr == nil && existing.Digest == info.ManifestDigest {
		createdAt = existing.CreatedAt
	}
	metadata := core.Image{
		ID: info.ManifestDigest, Name: name, Tag: tag, Reference: name + ":" + tag,
		Digest: info.ManifestDigest, RootfsDigest: info.ManifestDigest,
		CreatedAt: createdAt,
	}
	for _, descriptor := range info.Layers {
		metadata.Size += descriptor.Size
		metadata.Layers = append(metadata.Layers, descriptor.Digest)
	}
	return writeJSONAtomic(saveDir, imageMetadataFileName, metadata)
}

func (s imageStore) writeManifestInfo(manifestHash string, info layersInfo) error {
	dir := filepath.Join(s.root, "manifests", "sha256")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create manifest store: %w", err)
	}
	return writeJSONAtomic(dir, manifestHash+".json", info)
}

func writeJSONAtomic(dir, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	temporary, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", name, err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary %s: %w", name, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", name, err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	committed = true
	return nil
}
