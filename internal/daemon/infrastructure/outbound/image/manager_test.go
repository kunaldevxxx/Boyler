package image

import (
	"archive/tar"
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"boyler/pkg/files"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageManagerExtractsContentAddressedLayers(t *testing.T) {
	imagesRoot := t.TempDir()
	layerStore := layer.NewFilesystemStore(imagesRoot)
	manager := NewImageManagerWithLayerStore(imagesRoot, layerStore)
	archive := tarGzipLayer(t, "etc/boyler.conf", "enabled=true")
	descriptor := imageLayerDescriptor(archive)
	if _, err := layerStore.Ensure(context.Background(), descriptor, bytesFetch(archive), nil); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(imagesRoot, "alpine")
	if err := os.MkdirAll(imagePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := (imageStore{}).writeLayersInfo(imagePath, layersInfo{
		SchemaVersion:  2,
		ManifestDigest: "sha256:" + strings.Repeat("f", 64),
		Layers:         []layer.Descriptor{descriptor},
	}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Extract(context.Background(), "alpine", imagesRoot); err != nil {
		t.Fatalf("Extract returned an error: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(imagesRoot, "alpine", "rootfs", "etc", "boyler.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "enabled=true" {
		t.Fatalf("extracted content = %q", content)
	}
}

func TestImageManagerRefusesCorruptedContentAddressedLayer(t *testing.T) {
	imagesRoot := t.TempDir()
	layerStore := layer.NewFilesystemStore(imagesRoot)
	manager := NewImageManagerWithLayerStore(imagesRoot, layerStore)
	content := []byte("valid")
	descriptor := imageLayerDescriptor(content)
	if _, err := layerStore.Ensure(context.Background(), descriptor, bytesFetch(content), nil); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(imagesRoot, "corrupted")
	if err := os.MkdirAll(imagePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := (imageStore{}).writeLayersInfo(imagePath, layersInfo{SchemaVersion: 2, Layers: []layer.Descriptor{descriptor}}); err != nil {
		t.Fatal(err)
	}
	layerPath, err := layerStore.Path(descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layerPath, []byte("broke"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := manager.Extract(context.Background(), "corrupted", imagesRoot); err == nil {
		t.Fatal("expected corrupted layer error")
	}
}

func TestImageManagerDoesNotPublishPartialRootfs(t *testing.T) {
	imagesRoot := t.TempDir()
	layerStore := layer.NewFilesystemStore(imagesRoot)
	manager := NewImageManagerWithLayerStore(imagesRoot, layerStore)
	validArchive := tarGzipLayer(t, "created-before-error", "temporary")
	invalidArchive := []byte("not a gzip archive")
	descriptors := []layer.Descriptor{imageLayerDescriptor(validArchive), imageLayerDescriptor(invalidArchive)}
	for index, descriptor := range descriptors {
		contents := [][]byte{validArchive, invalidArchive}[index]
		if _, err := layerStore.Ensure(context.Background(), descriptor, bytesFetch(contents), nil); err != nil {
			t.Fatal(err)
		}
	}
	storageName, err := StorageName("atomic:test")
	if err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(imagesRoot, storageName)
	if err := os.MkdirAll(imagePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := (imageStore{}).writeLayersInfo(imagePath, layersInfo{
		SchemaVersion: 2, ManifestDigest: "sha256:" + strings.Repeat("a", 64), Layers: descriptors,
	}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Extract(context.Background(), "atomic:test", imagesRoot); err == nil {
		t.Fatal("expected extraction error")
	}
	if _, err := os.Lstat(filepath.Join(imagePath, "rootfs")); !os.IsNotExist(err) {
		t.Fatalf("partial rootfs was published: %v", err)
	}
	immutable := filepath.Join(imagesRoot, "rootfs", "sha256", strings.Repeat("a", 64))
	if _, err := os.Stat(immutable); !os.IsNotExist(err) {
		t.Fatalf("partial immutable rootfs remains: %v", err)
	}
}

func TestStorageNamesDoNotCollide(t *testing.T) {
	first, err := StorageName("team/api:v1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := StorageName("team_api:v1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("distinct references share storage name %q", first)
	}
	latest, _ := StorageName("alpine")
	explicitLatest, _ := StorageName("alpine:latest")
	if latest != explicitLatest {
		t.Fatalf("equivalent references differ: %q != %q", latest, explicitLatest)
	}
}

func TestImageManagerExtractSupportsLegacyLayersInfo(t *testing.T) {
	imagesRoot := t.TempDir()
	manager := NewImageManager(imagesRoot)
	imagePath := filepath.Join(imagesRoot, "legacy")
	if err := os.MkdirAll(imagePath, 0755); err != nil {
		t.Fatal(err)
	}
	archive := tarGzipLayer(t, "legacy.txt", "legacy")
	if err := os.WriteFile(filepath.Join(imagePath, "layer_0.tar.gz"), archive, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imagePath, layersInfoFileName), []byte(`{"num":1}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := manager.Extract(context.Background(), "legacy", imagesRoot); err != nil {
		t.Fatalf("Extract returned an error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(imagePath, "rootfs", "legacy.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestImageManagerPruneUsesImageMetadata(t *testing.T) {
	imagesRoot := t.TempDir()
	layerStore := layer.NewFilesystemStore(imagesRoot)
	manager := NewImageManagerWithLayerStore(imagesRoot, layerStore)
	usedContent, unusedContent := []byte("used"), []byte("unused")
	used, unused := imageLayerDescriptor(usedContent), imageLayerDescriptor(unusedContent)
	for _, item := range []struct {
		descriptor layer.Descriptor
		content    []byte
	}{{used, usedContent}, {unused, unusedContent}} {
		if _, err := layerStore.Ensure(context.Background(), item.descriptor, bytesFetch(item.content), nil); err != nil {
			t.Fatal(err)
		}
	}
	imagePath := filepath.Join(imagesRoot, "kept")
	if err := os.MkdirAll(imagePath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := (imageStore{}).writeLayersInfo(imagePath, layersInfo{SchemaVersion: 2, Layers: []layer.Descriptor{used}}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if valid, _ := layerStore.Has(context.Background(), used); !valid {
		t.Fatal("referenced layer was pruned")
	}
	if valid, _ := layerStore.Has(context.Background(), unused); valid {
		t.Fatal("unreferenced layer was not pruned")
	}
}

func TestImageStoreInvalidatesRootfsWhenManifestChanges(t *testing.T) {
	imagePath := t.TempDir()
	store := imageStore{}
	if err := store.writeLayersInfo(imagePath, layersInfo{SchemaVersion: 2, ManifestDigest: "sha256:old"}); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(imagePath, "rootfs")
	if err := os.MkdirAll(rootfs, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "stale"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := store.writeLayersInfo(imagePath, layersInfo{SchemaVersion: 2, ManifestDigest: "sha256:new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rootfs); !os.IsNotExist(err) {
		t.Fatalf("rootfs was not invalidated: %v", err)
	}
}

func TestImageStoreKeepsImmutableRootfsWhenTagChanges(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "reference")
	immutable := filepath.Join(root, "rootfs", "sha256", strings.Repeat("a", 64))
	if err := os.MkdirAll(immutable, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imagePath, 0755); err != nil {
		t.Fatal(err)
	}
	store := imageStore{}
	oldInfo := layersInfo{SchemaVersion: 2, ManifestDigest: "sha256:" + strings.Repeat("a", 64)}
	if err := store.writeLayersInfo(imagePath, oldInfo); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(immutable, filepath.Join(imagePath, "rootfs")); err != nil {
		t.Fatal(err)
	}
	newInfo := layersInfo{SchemaVersion: 2, ManifestDigest: "sha256:" + strings.Repeat("b", 64)}
	if err := store.writeLayersInfo(imagePath, newInfo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(imagePath, "rootfs")); !os.IsNotExist(err) {
		t.Fatalf("stale reference was not invalidated: %v", err)
	}
	if info, err := os.Stat(immutable); err != nil || !info.IsDir() {
		t.Fatalf("immutable rootfs was removed: %v", err)
	}
}

func TestImageManagerDeleteRejectsUnsafeName(t *testing.T) {
	imagesRoot := t.TempDir()
	outside := filepath.Join(filepath.Dir(imagesRoot), "must-not-delete")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	manager := NewImageManager(imagesRoot)
	if err := manager.Delete(context.Background(), "../must-not-delete"); err == nil {
		t.Fatal("expected unsafe image name error")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was affected: %v", err)
	}
}

func tarGzipLayer(t *testing.T, name, content string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tarWriter, content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func imageLayerDescriptor(content []byte) layer.Descriptor {
	digest := sha256.Sum256(content)
	return layer.Descriptor{
		MediaType: files.MediaTypeOCILayerGzip,
		Digest:    "sha256:" + hex.EncodeToString(digest[:]),
		Size:      int64(len(content)),
	}
}

func bytesFetch(content []byte) layer.FetchFunc {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}
}
