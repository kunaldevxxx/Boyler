package filesystem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"boyler/internal/daemon/core"
	storage "boyler/internal/daemon/infrastructure/outbound/storage"
)

const (
	stateDirectoryName = ".state"
	stateSchemaVersion = 1
	maxStateFileSize   = 1 << 20
)

type persistedContainer struct {
	SchemaVersion int            `json:"schemaVersion"`
	Container     core.Container `json:"container"`
}

// ContainerRepository persists one JSON state file per container. Runtime,
// network and cgroup reconciliation deliberately remain outside this adapter.
type ContainerRepository struct {
	stateDir string
	mu       sync.RWMutex
}

var _ storage.ContainerStorage = (*ContainerRepository)(nil)

func NewContainerRepository(containersDir string) (*ContainerRepository, error) {
	if strings.TrimSpace(containersDir) == "" {
		return nil, fmt.Errorf("containers directory is required")
	}
	absolute, err := filepath.Abs(containersDir)
	if err != nil {
		return nil, fmt.Errorf("resolve containers directory: %w", err)
	}
	stateDir := filepath.Join(filepath.Clean(absolute), stateDirectoryName)
	if err := rejectSymlinkPath(stateDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create container state directory: %w", err)
	}
	if err := rejectSymlinkPath(stateDir); err != nil {
		return nil, err
	}
	info, err := os.Lstat(stateDir)
	if err != nil {
		return nil, fmt.Errorf("inspect container state directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("container state path %s is not a directory", stateDir)
	}
	if err := os.Chmod(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("secure container state directory: %w", err)
	}
	return &ContainerRepository{stateDir: stateDir}, nil
}

func (r *ContainerRepository) Save(ctx context.Context, container core.Container) error {
	return r.write(ctx, container)
}

func (r *ContainerRepository) Update(ctx context.Context, container core.Container) error {
	return r.write(ctx, container)
}

func (r *ContainerRepository) write(ctx context.Context, container core.Container) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateContainer(container); err != nil {
		return err
	}
	data, err := json.Marshal(persistedContainer{SchemaVersion: stateSchemaVersion, Container: container})
	if err != nil {
		return fmt.Errorf("encode container %s state: %w", container.ID, err)
	}
	data = append(data, '\n')
	if len(data) > maxStateFileSize {
		return fmt.Errorf("container %s state exceeds %d bytes", container.ID, maxStateFileSize)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	temporary, err := os.CreateTemp(r.stateDir, "."+container.ID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary container state: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		return fmt.Errorf("secure temporary container state: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary container state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary container state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary container state: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target := r.path(container.ID)
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to replace symlink container state %s", target)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing container state: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("commit container %s state: %w", container.ID, err)
	}
	committed = true
	if err := syncDirectory(r.stateDir); err != nil {
		return fmt.Errorf("sync container state directory: %w", err)
	}
	return nil
}

func (r *ContainerRepository) Get(ctx context.Context, id string) (*core.Container, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateContainerID(id); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.read(id)
}

func (r *ContainerRepository) List(ctx context.Context) ([]*core.Container, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries, err := os.ReadDir(r.stateDir)
	if err != nil {
		return nil, fmt.Errorf("read container state directory: %w", err)
	}
	containers := make([]*core.Container, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		container, err := r.read(id)
		if err != nil {
			return nil, fmt.Errorf("load container state %s: %w", entry.Name(), err)
		}
		containers = append(containers, container)
	}
	sort.Slice(containers, func(a, b int) bool { return containers[a].ID < containers[b].ID })
	return containers, nil
}

func (r *ContainerRepository) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateContainerID(id); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	path := r.path(id)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to delete symlink container state %s", path)
	} else if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect container %s state: %w", id, err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete container %s state: %w", id, err)
	}
	if err := syncDirectory(r.stateDir); err != nil {
		return fmt.Errorf("sync container state directory: %w", err)
	}
	return nil
}

func (r *ContainerRepository) read(id string) (*core.Container, error) {
	path := r.path(id)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", core.ErrContainerNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect container %s state: %w", id, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("container %s state is not a regular file", id)
	}
	if info.Size() > maxStateFileSize {
		return nil, fmt.Errorf("container %s state exceeds %d bytes", id, maxStateFileSize)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open container %s state: %w", id, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStateFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read container %s state: %w", id, err)
	}
	if len(data) > maxStateFileSize {
		return nil, fmt.Errorf("container %s state exceeds %d bytes", id, maxStateFileSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var persisted persistedContainer
	if err := decoder.Decode(&persisted); err != nil {
		return nil, fmt.Errorf("decode container %s state: %w", id, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, fmt.Errorf("decode container %s state: %w", id, err)
	}
	if persisted.SchemaVersion != stateSchemaVersion {
		return nil, fmt.Errorf("unsupported container %s state schema version %d", id, persisted.SchemaVersion)
	}
	if persisted.Container.ID != id {
		return nil, fmt.Errorf("container state ID %q does not match filename ID %q", persisted.Container.ID, id)
	}
	if err := validateContainer(persisted.Container); err != nil {
		return nil, fmt.Errorf("validate container %s state: %w", id, err)
	}
	return &persisted.Container, nil
}

func (r *ContainerRepository) path(id string) string {
	return filepath.Join(r.stateDir, id+".json")
}

func validateContainer(container core.Container) error {
	if err := validateContainerID(container.ID); err != nil {
		return err
	}
	if container.PID < 0 {
		return fmt.Errorf("container %s has negative PID", container.ID)
	}
	switch container.Status {
	case core.StatusRunning, core.StatusStopped, core.StatusFreeze, core.StatusDeleted:
	default:
		return fmt.Errorf("container %s has invalid status %q", container.ID, container.Status)
	}
	return nil
}

func validateContainerID(id string) error {
	if id == "" || len(id) > 128 || strings.HasPrefix(id, ".") {
		return fmt.Errorf("invalid container ID %q", id)
	}
	for _, char := range id {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return fmt.Errorf("invalid container ID %q", id)
	}
	return nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected data after container state")
}

func rejectSymlinkPath(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("container state path must not contain symlinks: %s", current)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect container state path %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
