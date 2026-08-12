package layer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const digestAlgorithm = "sha256"

type FilesystemStore struct {
	root       string
	locks      *digestLocks
	verifiedMu sync.RWMutex
	verified   map[string]fileFingerprint
}

type fileFingerprint struct {
	size    int64
	modTime time.Time
}

func NewFilesystemStore(imagesRoot string) *FilesystemStore {
	return &FilesystemStore{
		root:     filepath.Join(imagesRoot, "blobs"),
		locks:    newDigestLocks(),
		verified: make(map[string]fileFingerprint),
	}
}

func (s *FilesystemStore) Has(ctx context.Context, descriptor Descriptor) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ValidateDescriptor(descriptor); err != nil {
		return false, err
	}
	path, expected, err := s.resolve(descriptor.Digest)
	if err != nil {
		return false, err
	}
	release := s.locks.acquire(digestAlgorithm + ":" + expected)
	defer release()
	return s.verifyFull(path, expected, descriptor.Size)
}

func (s *FilesystemStore) Ensure(ctx context.Context, descriptor Descriptor, fetch FetchFunc, progress ProgressFunc) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ValidateDescriptor(descriptor); err != nil {
		return false, err
	}
	path, expected, err := s.resolve(descriptor.Digest)
	if err != nil {
		return false, err
	}

	normalizedDigest := digestAlgorithm + ":" + expected
	release := s.locks.acquire(normalizedDigest)
	defer release()
	if err := ctx.Err(); err != nil {
		return false, err
	}

	valid, err := s.verifyCached(path, expected, descriptor.Size)
	if err != nil {
		return false, fmt.Errorf("check layer %s: %w", descriptor.Digest, err)
	}
	if valid {
		return true, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("remove invalid layer %s: %w", descriptor.Digest, err)
	}
	s.forget(normalizedDigest)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if fetch == nil {
		return false, fmt.Errorf("fetch layer %s: nil fetch function", descriptor.Digest)
	}

	reader, err := fetch()
	if err != nil {
		return false, fmt.Errorf("fetch layer %s: %w", descriptor.Digest, err)
	}
	defer reader.Close()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("create layer directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+expected+".part-*")
	if err != nil {
		return false, fmt.Errorf("create temporary layer: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	var source io.Reader = reader
	if descriptor.Size >= 0 {
		source = io.LimitReader(reader, descriptor.Size+1)
	}
	written, actual, err := copyLayer(ctx, temporary, source, progress)
	if err != nil {
		return false, fmt.Errorf("store layer %s: %w", descriptor.Digest, err)
	}
	if descriptor.Size >= 0 && written != descriptor.Size {
		return false, fmt.Errorf("%w: layer %s: expected %d bytes, got %d", ErrLayerSizeMismatch, descriptor.Digest, descriptor.Size, written)
	}
	if actual != expected {
		return false, fmt.Errorf("%w: layer %s: expected %s, got %s", ErrDigestMismatch, descriptor.Digest, expected, actual)
	}
	if err := temporary.Sync(); err != nil {
		return false, fmt.Errorf("sync layer %s: %w", descriptor.Digest, err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close layer %s: %w", descriptor.Digest, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return false, fmt.Errorf("commit layer %s: %w", descriptor.Digest, err)
	}
	committed = true
	if info, err := os.Stat(path); err == nil {
		s.remember(normalizedDigest, info)
	}
	return false, nil
}

func (s *FilesystemStore) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.Path(digest)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrLayerNotFound, digest)
	}
	if err != nil {
		return nil, fmt.Errorf("open layer %s: %w", digest, err)
	}
	return file, nil
}

func (s *FilesystemStore) Path(digest string) (string, error) {
	path, _, err := s.resolve(digest)
	return path, err
}

func (s *FilesystemStore) Prune(ctx context.Context, usedDigests map[string]struct{}) error {
	normalizedUsed := make(map[string]struct{}, len(usedDigests))
	for digest := range usedDigests {
		normalizedUsed[strings.ToLower(digest)] = struct{}{}
	}
	directory := filepath.Join(s.root, digestAlgorithm)
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read layer store: %w", err)
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if strings.Contains(entry.Name(), ".part-") {
				info, infoErr := entry.Info()
				if infoErr != nil {
					return fmt.Errorf("inspect partial layer %s: %w", entry.Name(), infoErr)
				}
				if time.Since(info.ModTime()) >= 24*time.Hour {
					if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
						return fmt.Errorf("prune partial layer %s: %w", entry.Name(), err)
					}
				}
			}
			continue
		}
		digest := digestAlgorithm + ":" + entry.Name()
		if _, used := normalizedUsed[digest]; used {
			continue
		}
		release := s.locks.acquire(digest)
		err := os.Remove(filepath.Join(directory, entry.Name()))
		s.forget(digest)
		release()
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prune layer %s: %w", digest, err)
		}
	}
	return nil
}

func (s *FilesystemStore) resolve(digest string) (path string, encoded string, err error) {
	encoded, err = ParseDigest(digest)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(s.root, digestAlgorithm, encoded), encoded, nil
}

func (s *FilesystemStore) verifyCached(path, expectedDigest string, expectedSize int64) (bool, error) {
	return s.verify(path, expectedDigest, expectedSize, true)
}

func (s *FilesystemStore) verifyFull(path, expectedDigest string, expectedSize int64) (bool, error) {
	return s.verify(path, expectedDigest, expectedSize, false)
}

func (s *FilesystemStore) verify(path, expectedDigest string, expectedSize int64, allowCache bool) (bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || expectedSize >= 0 && info.Size() != expectedSize {
		return false, nil
	}
	digest := digestAlgorithm + ":" + expectedDigest
	if allowCache && s.known(digest, info) {
		return true, nil
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return false, err
	}
	valid := hex.EncodeToString(hasher.Sum(nil)) == expectedDigest
	if valid {
		s.remember(digest, info)
	}
	return valid, nil
}

func (s *FilesystemStore) known(digest string, info os.FileInfo) bool {
	s.verifiedMu.RLock()
	fingerprint, ok := s.verified[digest]
	s.verifiedMu.RUnlock()
	return ok && fingerprint.size == info.Size() && fingerprint.modTime.Equal(info.ModTime())
}

func (s *FilesystemStore) remember(digest string, info os.FileInfo) {
	s.verifiedMu.Lock()
	s.verified[digest] = fileFingerprint{size: info.Size(), modTime: info.ModTime()}
	s.verifiedMu.Unlock()
}

func (s *FilesystemStore) forget(digest string) {
	s.verifiedMu.Lock()
	delete(s.verified, digest)
	s.verifiedMu.Unlock()
}

func copyLayer(ctx context.Context, destination io.Writer, source io.Reader, progress ProgressFunc) (int64, string, error) {
	hasher := sha256.New()
	written, err := copyWithContext(ctx, io.MultiWriter(destination, hasher), source, progress)
	return written, encodeHash(hasher), err
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader, progress ProgressFunc) (int64, error) {
	buffer := make([]byte, 1024*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			n, writeErr := destination.Write(buffer[:read])
			written += int64(n)
			if progress != nil {
				progress(written)
			}
			if writeErr != nil {
				return written, writeErr
			}
			if n != read {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func encodeHash(hasher hash.Hash) string {
	return hex.EncodeToString(hasher.Sum(nil))
}
