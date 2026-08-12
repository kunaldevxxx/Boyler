package image

import (
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const manifestAccept = "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json"

type manifestResolver struct {
	registry *dockerHubRegistry
	platform Platform
}

func (r manifestResolver) resolve(ctx context.Context, ref dockerHubReference, token string) (ociManifest, string, error) {
	document, err := r.registry.manifest(ctx, ref.repository, ref.tag, token, manifestAccept)
	if err != nil {
		return ociManifest{}, "", err
	}

	if !isManifestIndex(document.contentType) {
		var manifest ociManifest
		if err := json.Unmarshal(document.body, &manifest); err != nil {
			return ociManifest{}, "", err
		}
		if err := validateManifest(manifest); err != nil {
			return ociManifest{}, "", err
		}
		return manifest, document.digest, nil
	}

	var index ociIndex
	if err := json.Unmarshal(document.body, &index); err != nil {
		return ociManifest{}, "", err
	}
	selected, err := selectPlatformManifest(index, r.platform)
	if err != nil {
		return ociManifest{}, "", err
	}

	platformDocument, err := r.registry.manifest(
		ctx,
		ref.repository,
		selected.Digest,
		token,
		selected.MediaType+", application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json",
	)
	if err != nil {
		return ociManifest{}, "", err
	}
	selectedHash, _ := layer.ParseDigest(selected.Digest)
	returnedHash, err := layer.ParseDigest(platformDocument.digest)
	if err != nil || returnedHash != selectedHash {
		return ociManifest{}, "", fmt.Errorf("platform manifest digest mismatch: expected %s, got %s", selected.Digest, platformDocument.digest)
	}
	if selected.Size != int64(len(platformDocument.body)) {
		return ociManifest{}, "", fmt.Errorf("platform manifest size mismatch: expected %d, got %d", selected.Size, len(platformDocument.body))
	}
	var manifest ociManifest
	if err := json.Unmarshal(platformDocument.body, &manifest); err != nil {
		return ociManifest{}, "", err
	}
	if err := validateManifest(manifest); err != nil {
		return ociManifest{}, "", err
	}
	return manifest, platformDocument.digest, nil
}

func validateManifest(manifest ociManifest) error {
	if manifest.SchemaVersion != 2 {
		return fmt.Errorf("unsupported manifest schema version %d", manifest.SchemaVersion)
	}
	for index, descriptor := range manifest.Layers {
		if err := layer.ValidateDescriptor(descriptor); err != nil {
			return fmt.Errorf("invalid layer %d: %w", index, err)
		}
		if !supportedLayerMediaType(descriptor.MediaType) {
			return fmt.Errorf("unsupported layer media type %q", descriptor.MediaType)
		}
	}
	return nil
}

func supportedLayerMediaType(mediaType string) bool {
	switch mediaType {
	case "application/vnd.docker.image.rootfs.diff.tar",
		"application/vnd.docker.image.rootfs.diff.tar.gzip",
		"application/vnd.oci.image.layer.v1.tar",
		"application/vnd.oci.image.layer.v1.tar+gzip",
		"application/vnd.oci.image.layer.v1.tar+zstd":
		return true
	default:
		return false
	}
}

func isManifestIndex(contentType string) bool {
	return strings.Contains(contentType, "manifest.list.v2+json") || strings.Contains(contentType, "image.index.v1+json")
}

func selectPlatformManifest(index ociIndex, platform Platform) (ociIndexEntry, error) {
	if index.SchemaVersion != 2 {
		return ociIndexEntry{}, fmt.Errorf("unsupported image index schema version %d", index.SchemaVersion)
	}
	for _, candidate := range index.Manifests {
		if candidate.Platform.OS == platform.OS && candidate.Platform.Architecture == platform.Architecture {
			if err := layer.ValidateDescriptor(candidate.ociDescriptor); err != nil {
				return ociIndexEntry{}, fmt.Errorf("invalid platform manifest descriptor: %w", err)
			}
			if candidate.MediaType != "application/vnd.docker.distribution.manifest.v2+json" && candidate.MediaType != "application/vnd.oci.image.manifest.v1+json" {
				return ociIndexEntry{}, fmt.Errorf("unsupported platform manifest media type %q", candidate.MediaType)
			}
			return candidate, nil
		}
	}
	return ociIndexEntry{}, fmt.Errorf("no manifest for platform %s/%s", platform.OS, platform.Architecture)
}

type ociDescriptor = layer.Descriptor

type ociManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Layers        []ociDescriptor `json:"layers"`
}

type ociIndexEntry struct {
	ociDescriptor
	Platform struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	Manifests     []ociIndexEntry `json:"manifests"`
}
