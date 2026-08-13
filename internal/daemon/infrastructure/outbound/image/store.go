package image

import (
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

const layersInfoFileName = "layers.json"

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
