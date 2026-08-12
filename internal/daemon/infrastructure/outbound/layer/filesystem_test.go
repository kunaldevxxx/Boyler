package layer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFilesystemStoreEnsureAndCacheHit(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	content := []byte("shared image layer")
	descriptor := descriptorFor(content)
	var fetches atomic.Int32
	fetch := func() (io.ReadCloser, error) {
		fetches.Add(1)
		return io.NopCloser(bytes.NewReader(content)), nil
	}

	cached, err := store.Ensure(context.Background(), descriptor, fetch, nil)
	if err != nil || cached {
		t.Fatalf("first Ensure cached=%v err=%v", cached, err)
	}
	cached, err = store.Ensure(context.Background(), descriptor, fetch, nil)
	if err != nil || !cached {
		t.Fatalf("second Ensure cached=%v err=%v", cached, err)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetch called %d times", fetches.Load())
	}

	valid, err := store.Has(context.Background(), descriptor)
	if err != nil || !valid {
		t.Fatalf("Has valid=%v err=%v", valid, err)
	}
}

func TestFilesystemStoreRejectsDigestMismatchAndCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	store := NewFilesystemStore(root)
	expected := descriptorFor([]byte("expected"))
	_, err := store.Ensure(context.Background(), expected, func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("differen")), nil
	}, nil)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Ensure error = %v", err)
	}

	directory := filepath.Join(root, "blobs", "sha256")
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files remain: %#v", entries)
	}
}

func TestFilesystemStoreRejectsSizeMismatch(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	content := []byte("layer")
	descriptor := descriptorFor(content)
	descriptor.Size++
	_, err := store.Ensure(context.Background(), descriptor, readerFetch(content), nil)
	if !errors.Is(err, ErrLayerSizeMismatch) {
		t.Fatalf("Ensure error = %v", err)
	}
}

func TestFilesystemStoreCancellationCleansPartialLayer(t *testing.T) {
	root := t.TempDir()
	store := NewFilesystemStore(root)
	content := bytes.Repeat([]byte("x"), 2*1024*1024)
	descriptor := descriptorFor(content)
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstRead{reader: bytes.NewReader(content), cancel: cancel}
	_, err := store.Ensure(ctx, descriptor, func() (io.ReadCloser, error) {
		return io.NopCloser(reader), nil
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ensure error = %v", err)
	}
	directory := filepath.Join(root, "blobs", "sha256")
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("partial files remain: %#v", entries)
	}
}

func TestFilesystemStoreDetectsCorruptedCachedLayer(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	content := []byte("valid!")
	descriptor := descriptorFor(content)
	if _, err := store.Ensure(context.Background(), descriptor, readerFetch(content), nil); err != nil {
		t.Fatal(err)
	}
	path, err := store.Path(descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	valid, err := store.Has(context.Background(), descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("corrupted layer reported as valid")
	}
}

func TestFilesystemStoreConcurrentEnsureFetchesOnce(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	content := []byte("one physical layer")
	descriptor := descriptorFor(content)
	var fetches atomic.Int32
	fetch := func() (io.ReadCloser, error) {
		fetches.Add(1)
		return io.NopCloser(bytes.NewReader(content)), nil
	}

	const callers = 8
	var wg sync.WaitGroup
	errorsChannel := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Ensure(context.Background(), descriptor, fetch, nil)
			errorsChannel <- err
		}()
	}
	wg.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("Ensure: %v", err)
		}
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetch called %d times", fetches.Load())
	}
}

func TestFilesystemStorePruneKeepsUsedLayers(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	used := descriptorFor([]byte("used"))
	unused := descriptorFor([]byte("unused"))
	for _, item := range []struct {
		descriptor Descriptor
		content    []byte
	}{
		{used, []byte("used")},
		{unused, []byte("unused")},
	} {
		if _, err := store.Ensure(context.Background(), item.descriptor, readerFetch(item.content), nil); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Prune(context.Background(), map[string]struct{}{used.Digest: {}}); err != nil {
		t.Fatal(err)
	}
	if valid, _ := store.Has(context.Background(), used); !valid {
		t.Fatal("used layer was pruned")
	}
	if valid, _ := store.Has(context.Background(), unused); valid {
		t.Fatal("unused layer was not pruned")
	}
}

func TestFilesystemStoreRejectsInvalidDigest(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	_, err := store.Has(context.Background(), Descriptor{Digest: "sha256:../../etc/passwd", Size: 1})
	if !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("Has error = %v", err)
	}
}

func descriptorFor(content []byte) Descriptor {
	digest := sha256.Sum256(content)
	return Descriptor{
		Digest: "sha256:" + hex.EncodeToString(digest[:]),
		Size:   int64(len(content)),
	}
}

func readerFetch(content []byte) FetchFunc {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(content)), nil
	}
}

type cancelAfterFirstRead struct {
	reader *bytes.Reader
	cancel context.CancelFunc
	done   bool
}

func (r *cancelAfterFirstRead) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if !r.done {
		r.done = true
		r.cancel()
	}
	return n, err
}
