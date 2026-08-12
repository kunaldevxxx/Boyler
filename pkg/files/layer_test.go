package files

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

type layerEntry struct {
	name     string
	content  string
	typeflag byte
	linkname string
}

func TestApplyLayerHandlesWhiteouts(t *testing.T) {
	rootfs := t.TempDir()
	base := writeLayer(t, MediaTypeOCILayerGzip, []layerEntry{
		{name: "keep.txt", content: "keep"},
		{name: "remove.txt", content: "remove"},
		{name: "opaque/old.txt", content: "old"},
	})
	top := writeLayer(t, MediaTypeOCILayerGzip, []layerEntry{
		{name: ".wh.remove.txt"},
		{name: "opaque/.wh..wh..opq"},
		{name: "opaque/new.txt", content: "new"},
	})

	if err := ApplyLayer(context.Background(), base, rootfs, MediaTypeOCILayerGzip); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayer(context.Background(), top, rootfs, MediaTypeOCILayerGzip); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(rootfs, "keep.txt"), "keep")
	assertFile(t, filepath.Join(rootfs, "opaque", "new.txt"), "new")
	for _, path := range []string{
		filepath.Join(rootfs, "remove.txt"),
		filepath.Join(rootfs, ".wh.remove.txt"),
		filepath.Join(rootfs, "opaque", "old.txt"),
		filepath.Join(rootfs, "opaque", ".wh..wh..opq"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be absent, got %v", path, err)
		}
	}
}

func TestApplyLayerSupportsZstd(t *testing.T) {
	rootfs := t.TempDir()
	layer := writeLayer(t, MediaTypeOCILayerZstd, []layerEntry{{name: "zstd.txt", content: "supported"}})
	if err := ApplyLayer(context.Background(), layer, rootfs, MediaTypeOCILayerZstd); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(rootfs, "zstd.txt"), "supported")
}

func TestApplyLayerRejectsPathTraversal(t *testing.T) {
	layer := writeLayer(t, MediaTypeOCILayerGzip, []layerEntry{{name: "../escape", content: "bad"}})
	if err := ApplyLayer(context.Background(), layer, t.TempDir(), MediaTypeOCILayerGzip); err == nil {
		t.Fatal("expected unsafe path error")
	}
}

func TestApplyLayerRejectsHardlinkThroughSymlinkParent(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "host-file")
	if err := os.WriteFile(outsideFile, []byte("host"), 0600); err != nil {
		t.Fatal(err)
	}
	rootfs := t.TempDir()
	layer := writeLayer(t, MediaTypeOCILayerGzip, []layerEntry{
		{name: "escape", typeflag: tar.TypeSymlink, linkname: outside},
		{name: "copy", typeflag: tar.TypeLink, linkname: "escape/host-file"},
	})
	if err := ApplyLayer(context.Background(), layer, rootfs, MediaTypeOCILayerGzip); err == nil {
		t.Fatal("expected unsafe hardlink target error")
	}
	assertFile(t, outsideFile, "host")
	info, err := os.Stat(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("host file mode changed to %o", info.Mode().Perm())
	}
}

func TestApplyLayerSupportsForwardHardlinks(t *testing.T) {
	rootfs := t.TempDir()
	layer := writeLayer(t, MediaTypeOCILayerGzip, []layerEntry{
		{name: "copy", typeflag: tar.TypeLink, linkname: "target"},
		{name: "target", content: "content"},
	})
	if err := ApplyLayer(context.Background(), layer, rootfs, MediaTypeOCILayerGzip); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(rootfs, "copy"), "content")
}

func writeLayer(t *testing.T, mediaType string, entries []layerEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "layer")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var writer io.WriteCloser
	switch mediaType {
	case MediaTypeOCILayerGzip:
		writer = gzip.NewWriter(file)
	case MediaTypeOCILayerZstd:
		writer, err = zstd.NewWriter(file)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported test media type %s", mediaType)
	}
	tarWriter := tar.NewWriter(writer)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		size := int64(len(entry.content))
		if typeflag != tar.TypeReg {
			size = 0
		}
		header := &tar.Header{Name: entry.name, Mode: 0644, Size: size, Typeflag: typeflag, Linkname: entry.linkname}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := io.Copy(tarWriter, bytes.NewBufferString(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFile(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != expected {
		t.Fatalf("%s = %q", path, content)
	}
}
