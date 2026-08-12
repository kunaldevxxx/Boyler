package image

import (
	"boyler/internal/daemon/infrastructure/outbound/layer"
	"boyler/pkg/logger"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	dockerHubAuthURL     = "https://auth.docker.io/token"
	dockerHubRegistryURL = "https://registry-1.docker.io"
)

type dockerHubRegistry struct {
	httpClient *http.Client
}

type manifestDocument struct {
	body        []byte
	contentType string
	digest      string
}

func newDockerHubRegistry(client *http.Client) *dockerHubRegistry {
	return &dockerHubRegistry{httpClient: client}
}

func (r *dockerHubRegistry) token(ctx context.Context, repository string) (string, error) {
	log := logger.FromContext(ctx)
	log.Debug("Start request dockerHub token", "repo", repository)

	url := fmt.Sprintf("%s?service=registry.docker.io&scope=repository:%s:pull", dockerHubAuthURL, repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth token endpoint returned %s", resp.Status)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	log.Debug("Received auth token")
	return body.Token, nil
}

func (r *dockerHubRegistry) manifest(ctx context.Context, repository, reference, token, accept string) (manifestDocument, error) {
	url := fmt.Sprintf("%s/v2/%s/manifests/%s", dockerHubRegistryURL, repository, reference)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return manifestDocument{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return manifestDocument{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return manifestDocument{}, fmt.Errorf("manifest fetch returned %s: %s", resp.Status, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return manifestDocument{}, err
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	actualHash := sha256.Sum256(body)
	actualDigest := "sha256:" + hex.EncodeToString(actualHash[:])
	if digest == "" {
		digest = actualDigest
	} else if normalized, err := layer.ParseDigest(digest); err != nil {
		return manifestDocument{}, fmt.Errorf("invalid manifest digest: %w", err)
	} else if "sha256:"+normalized != actualDigest {
		return manifestDocument{}, fmt.Errorf("manifest digest mismatch: expected %s, got %s", digest, actualDigest)
	}
	return manifestDocument{
		body:        body,
		contentType: resp.Header.Get("Content-Type"),
		digest:      digest,
	}, nil
}

func (r *dockerHubRegistry) blob(ctx context.Context, repository, digest, token string) (*http.Response, error) {
	url := fmt.Sprintf("%s/v2/%s/blobs/%s", dockerHubRegistryURL, repository, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return r.httpClient.Do(req)
}
