package layer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrLayerNotFound     = errors.New("layer not found")
	ErrInvalidDigest     = errors.New("invalid layer digest")
	ErrDigestMismatch    = errors.New("layer digest mismatch")
	ErrLayerSizeMismatch = errors.New("layer size mismatch")
)

type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type FetchFunc func() (io.ReadCloser, error)

type ProgressFunc func(downloaded int64)

func ValidateDescriptor(descriptor Descriptor) error {
	if _, err := ParseDigest(descriptor.Digest); err != nil {
		return err
	}
	if descriptor.Size < 0 {
		return fmt.Errorf("%w: negative size %d", ErrLayerSizeMismatch, descriptor.Size)
	}
	return nil
}

func ParseDigest(digest string) (string, error) {
	algorithm, encoded, ok := strings.Cut(digest, ":")
	if !ok || strings.ToLower(algorithm) != "sha256" || len(encoded) != sha256.Size*2 {
		return "", fmt.Errorf("%w: %q", ErrInvalidDigest, digest)
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("%w: %q", ErrInvalidDigest, digest)
	}
	return strings.ToLower(encoded), nil
}

// Store keeps compressed image layers by their content digest.
type Store interface {
	Has(ctx context.Context, descriptor Descriptor) (bool, error)
	Ensure(ctx context.Context, descriptor Descriptor, fetch FetchFunc, progress ProgressFunc) (cached bool, err error)
	Open(ctx context.Context, digest string) (io.ReadCloser, error)
	Path(digest string) (string, error)
	Prune(ctx context.Context, usedDigests map[string]struct{}) error
}
