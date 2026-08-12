package image

import (
	"boyler/internal/daemon/core"
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestParseDockerHubReference(t *testing.T) {
	tests := []struct {
		input      string
		repository string
		tag        string
		storage    string
	}{
		{input: "alpine", repository: "library/alpine", tag: "latest", storage: "alpine%3Alatest"},
		{input: "alpine:3.20", repository: "library/alpine", tag: "3.20", storage: "alpine%3A3.20"},
		{input: "docker.io/acme/api:v2", repository: "acme/api", tag: "v2", storage: "acme%2Fapi%3Av2"},
	}

	for _, test := range tests {
		actual, err := parseDockerHubReference(test.input)
		if err != nil {
			t.Fatalf("parseDockerHubReference(%q): %v", test.input, err)
		}
		if actual.repository != test.repository || actual.tag != test.tag || actual.storageName != test.storage {
			t.Fatalf("parseDockerHubReference(%q) = %#v", test.input, actual)
		}
	}
}

func TestSelectPlatformManifest(t *testing.T) {
	var index ociIndex
	index.SchemaVersion = 2
	index.Manifests = []ociIndexEntry{{}}
	index.Manifests[0].Digest = "sha256:" + strings.Repeat("a", 64)
	index.Manifests[0].Size = 1
	index.Manifests[0].MediaType = "application/vnd.oci.image.manifest.v1+json"
	index.Manifests[0].Platform.OS = "linux"
	index.Manifests[0].Platform.Architecture = "amd64"

	descriptor, err := selectPlatformManifest(index, Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatalf("selectPlatformManifest returned an error: %v", err)
	}
	if descriptor.Digest != index.Manifests[0].Digest {
		t.Fatalf("digest = %q", descriptor.Digest)
	}

	if _, err := selectPlatformManifest(index, Platform{OS: "linux", Architecture: "arm64"}); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestValidateManifestRejectsInvalidDescriptors(t *testing.T) {
	tests := []ociManifest{
		{SchemaVersion: 1},
		{SchemaVersion: 2, Layers: []ociDescriptor{{Digest: "bad", Size: 1}}},
		{SchemaVersion: 2, Layers: []ociDescriptor{{Digest: strings.Repeat("a", 64), Size: -1}}},
		{SchemaVersion: 2, Layers: []ociDescriptor{{Digest: "sha256:" + strings.Repeat("a", 64), Size: 1, MediaType: "application/unknown"}}},
	}
	for _, manifest := range tests {
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("expected manifest validation error for %#v", manifest)
		}
	}
}

func TestRegistryRejectsManifestDigestMismatch(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		headers := http.Header{
			"Content-Type":          []string{"application/vnd.oci.image.manifest.v1+json"},
			"Docker-Content-Digest": []string{"sha256:" + strings.Repeat("a", 64)},
		}
		return response(http.StatusOK, `{"schemaVersion":2,"layers":[]}`, "", headers), nil
	})}
	_, err := newDockerHubRegistry(client).manifest(context.Background(), "library/test", "latest", "token", manifestAccept)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("manifest error = %v", err)
	}
}

func TestParseDockerHubReferenceRejectsUnsafeName(t *testing.T) {
	for _, value := range []string{"", "..", ".", "bad name"} {
		if _, err := parseDockerHubReference(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestDockerHubPullerComposesRegistryManifestDownloaderAndStore(t *testing.T) {
	const layerBody = "compressed layer bytes"
	layerHash := sha256.Sum256([]byte(layerBody))
	layerDigest := "sha256:" + hex.EncodeToString(layerHash[:])
	manifestBody := fmt.Sprintf(`{"schemaVersion":2,"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":%d}]}`, layerDigest, len(layerBody))
	var blobRequests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "auth.docker.io":
			return response(http.StatusOK, `{"token":"test-token"}`, "application/json", nil), nil
		case strings.HasSuffix(req.URL.Path, "/manifests/latest"):
			headers := http.Header{"Content-Type": []string{"application/vnd.docker.distribution.manifest.v2+json"}}
			return response(http.StatusOK, manifestBody, "", headers), nil
		case strings.HasSuffix(req.URL.Path, "/blobs/"+layerDigest):
			blobRequests.Add(1)
			return response(http.StatusOK, layerBody, "application/octet-stream", nil), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}
	progress := make(chan *core.PullingEvent, 4)
	puller := &DockerHubPuller{
		HTTPClient: client,
		Platform:   Platform{OS: "linux", Architecture: "amd64"},
		Progress:   progress,
	}

	destination := t.TempDir()
	puller.LayerStore = layer.NewFilesystemStore(destination)
	digest, err := puller.Pull(context.Background(), "alpine", destination)
	if err != nil {
		t.Fatalf("Pull returned an error: %v", err)
	}
	if digest != contentDigest(manifestBody) {
		t.Fatalf("digest = %q", digest)
	}

	imagePath := filepath.Join(destination, "alpine%3Alatest")
	layerPath, err := puller.LayerStore.Path(layerDigest)
	if err != nil {
		t.Fatalf("resolve downloaded layer: %v", err)
	}
	downloadedLayer, err := os.ReadFile(layerPath)
	if err != nil {
		t.Fatalf("read downloaded layer: %v", err)
	}
	if string(downloadedLayer) != layerBody {
		t.Fatalf("layer contents = %q", downloadedLayer)
	}
	metadata, err := os.ReadFile(filepath.Join(imagePath, layersInfoFileName))
	if err != nil {
		t.Fatalf("read layers metadata: %v", err)
	}
	if !strings.Contains(string(metadata), `"schemaVersion": 2`) || !strings.Contains(string(metadata), layerDigest) {
		t.Fatalf("layers metadata = %s", metadata)
	}

	first, second := <-progress, <-progress
	if first.Status != "Downloading" || first.Progress != int64(len(layerBody)) {
		t.Fatalf("download event = %#v", first)
	}
	if second.Status != "Pull complete" || second.LayId != layerDigest[:12] {
		t.Fatalf("completion event = %#v", second)
	}

	if _, err := puller.Pull(context.Background(), "alpine", destination); err != nil {
		t.Fatalf("repeat Pull returned an error: %v", err)
	}
	cacheHit := <-progress
	if cacheHit.Status != "Already exists" || cacheHit.Progress != int64(len(layerBody)) {
		t.Fatalf("cache hit event = %#v", cacheHit)
	}
	if _, err := puller.Pull(context.Background(), "busybox", destination); err != nil {
		t.Fatalf("shared-layer Pull returned an error: %v", err)
	}
	sharedHit := <-progress
	if sharedHit.Status != "Already exists" {
		t.Fatalf("shared layer event = %#v", sharedHit)
	}
	if blobRequests.Load() != 1 {
		t.Fatalf("blob endpoint called %d times", blobRequests.Load())
	}
	if _, err := os.Stat(filepath.Join(destination, "busybox%3Alatest", layersInfoFileName)); err != nil {
		t.Fatalf("shared image metadata: %v", err)
	}
}

func TestDockerHubPullerDownloadsOnlyNewLayersWhenTagChanges(t *testing.T) {
	firstBody, secondBody := []byte("first layer"), []byte("second layer")
	firstHash, secondHash := sha256.Sum256(firstBody), sha256.Sum256(secondBody)
	firstDigest := "sha256:" + hex.EncodeToString(firstHash[:])
	secondDigest := "sha256:" + hex.EncodeToString(secondHash[:])
	var manifestRequests atomic.Int32
	var firstBlobRequests atomic.Int32
	var secondBlobRequests atomic.Int32

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Host == "auth.docker.io":
			return response(http.StatusOK, `{"token":"test-token"}`, "application/json", nil), nil
		case strings.HasSuffix(req.URL.Path, "/manifests/latest"):
			request := manifestRequests.Add(1)
			layers := fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":%d}`, firstDigest, len(firstBody))
			if request > 1 {
				layers += "," + fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":%d}`, secondDigest, len(secondBody))
			}
			headers := http.Header{"Content-Type": []string{"application/vnd.docker.distribution.manifest.v2+json"}}
			return response(http.StatusOK, `{"schemaVersion":2,"layers":[`+layers+`]}`, "", headers), nil
		case strings.HasSuffix(req.URL.Path, "/blobs/"+firstDigest):
			firstBlobRequests.Add(1)
			return response(http.StatusOK, string(firstBody), "application/octet-stream", nil), nil
		case strings.HasSuffix(req.URL.Path, "/blobs/"+secondDigest):
			secondBlobRequests.Add(1)
			return response(http.StatusOK, string(secondBody), "application/octet-stream", nil), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}
	destination := t.TempDir()
	progress := make(chan *core.PullingEvent, 10)
	puller := &DockerHubPuller{
		HTTPClient: client,
		Platform:   Platform{OS: "linux", Architecture: "amd64"},
		Progress:   progress,
		LayerStore: layer.NewFilesystemStore(destination),
	}

	if _, err := puller.Pull(context.Background(), "moving-tag", destination); err != nil {
		t.Fatal(err)
	}
	<-progress
	<-progress
	if _, err := puller.Pull(context.Background(), "moving-tag", destination); err != nil {
		t.Fatal(err)
	}
	events := []*core.PullingEvent{<-progress, <-progress, <-progress}
	if events[0].Status != "Already exists" || events[1].Status != "Downloading" || events[2].Status != "Pull complete" {
		t.Fatalf("updated pull events = %#v", events)
	}
	if firstBlobRequests.Load() != 1 || secondBlobRequests.Load() != 1 {
		t.Fatalf("blob requests: first=%d second=%d", firstBlobRequests.Load(), secondBlobRequests.Load())
	}
	info, err := readLayersInfo(filepath.Join(destination, "moving-tag%3Alatest"))
	if err != nil {
		t.Fatal(err)
	}
	secondManifest := fmt.Sprintf(`{"schemaVersion":2,"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":%d},{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":%d}]}`,
		firstDigest, len(firstBody), secondDigest, len(secondBody))
	if info.ManifestDigest != contentDigest(secondManifest) || len(info.Layers) != 2 {
		t.Fatalf("updated layers info = %#v", info)
	}
}

func contentDigest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func response(status int, body, contentType string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        headers,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}
