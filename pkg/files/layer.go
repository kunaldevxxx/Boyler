package files

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

const (
	MediaTypeDockerLayer       = "application/vnd.docker.image.rootfs.diff.tar"
	MediaTypeDockerLayerGzip   = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	MediaTypeOCILayer          = "application/vnd.oci.image.layer.v1.tar"
	MediaTypeOCILayerGzip      = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeOCILayerZstd      = "application/vnd.oci.image.layer.v1.tar+zstd"
	MediaTypeOCIRestrictedGzip = "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip"
	MediaTypeOCIRestricted     = "application/vnd.oci.image.layer.nondistributable.v1.tar"
	MediaTypeOCIRestrictedZstd = "application/vnd.oci.image.layer.nondistributable.v1.tar+zstd"
	MediaTypeDockerForeignGzip = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
)

// ApplyLayer applies one OCI/Docker filesystem layer, including whiteout
// semantics. Whiteouts are collected in a first pass so their position in the
// tar stream cannot delete files created by the same layer.
func ApplyLayer(ctx context.Context, source, destination, mediaType string) error {
	if err := os.MkdirAll(destination, 0755); err != nil {
		return fmt.Errorf("create layer destination: %w", err)
	}
	whiteouts, err := scanWhiteouts(ctx, source, mediaType, destination)
	if err != nil {
		return err
	}
	for _, whiteout := range whiteouts {
		if err := ensureParents(destination, whiteout.target); err != nil {
			return err
		}
		if whiteout.opaque {
			if info, err := os.Lstat(whiteout.target); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return fmt.Errorf("unsafe opaque whiteout target %s", whiteout.target)
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			entries, err := os.ReadDir(whiteout.target)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read opaque directory %s: %w", whiteout.target, err)
			}
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(whiteout.target, entry.Name())); err != nil {
					return fmt.Errorf("apply opaque whiteout %s: %w", whiteout.target, err)
				}
			}
			continue
		}
		if err := os.RemoveAll(whiteout.target); err != nil {
			return fmt.Errorf("apply whiteout %s: %w", whiteout.target, err)
		}
	}

	reader, closeReader, err := openLayer(source, mediaType)
	if err != nil {
		return err
	}
	defer closeReader()
	return extractTar(ctx, tar.NewReader(reader), destination)
}

type whiteout struct {
	target string
	opaque bool
}

type pendingHardlink struct {
	path   string
	target string
	header tar.Header
}

func scanWhiteouts(ctx context.Context, source, mediaType, destination string) ([]whiteout, error) {
	reader, closeReader, err := openLayer(source, mediaType)
	if err != nil {
		return nil, err
	}
	defer closeReader()

	var result []whiteout
	tarReader := tar.NewReader(reader)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := tarReader.Next()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, fmt.Errorf("scan layer %s: %w", source, err)
		}
		path, err := secureLayerPath(destination, header.Name)
		if err != nil {
			return nil, err
		}
		base := filepath.Base(path)
		if base == ".wh..wh..opq" {
			result = append(result, whiteout{target: filepath.Dir(path), opaque: true})
		} else if strings.HasPrefix(base, ".wh.") {
			result = append(result, whiteout{target: filepath.Join(filepath.Dir(path), strings.TrimPrefix(base, ".wh."))})
		}
	}
}

func extractTar(ctx context.Context, reader *tar.Reader, destination string) error {
	type directoryMetadata struct {
		path   string
		header tar.Header
	}
	var directories []directoryMetadata
	var hardlinks []pendingHardlink
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			if err := createPendingHardlinks(destination, hardlinks); err != nil {
				return err
			}
			for index := len(directories) - 1; index >= 0; index-- {
				if err := applyMetadata(directories[index].path, &directories[index].header, false); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("read layer archive: %w", err)
		}
		path, err := secureLayerPath(destination, header.Name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(path), ".wh.") {
			continue
		}
		if err := ensureParents(destination, path); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if info, err := os.Lstat(path); err == nil && !info.IsDir() {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			}
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
			directories = append(directories, directoryMetadata{path: path, header: *header})
		case tar.TypeReg, tar.TypeRegA:
			if err := writeRegularFile(path, reader, header); err != nil {
				return err
			}
			if err := applyMetadata(path, header, false); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := replacePath(path); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, path); err != nil {
				return fmt.Errorf("create symlink %s: %w", path, err)
			}
			if err := applyMetadata(path, header, true); err != nil {
				return err
			}
		case tar.TypeLink:
			target, err := secureLayerPath(destination, header.Linkname)
			if err != nil {
				return err
			}
			hardlinks = append(hardlinks, pendingHardlink{path: path, target: target, header: *header})
		case tar.TypeChar, tar.TypeBlock:
			if err := replacePath(path); err != nil {
				return err
			}
			fileType := uint32(unix.S_IFCHR)
			if header.Typeflag == tar.TypeBlock {
				fileType = unix.S_IFBLK
			}
			device := int(unix.Mkdev(uint32(header.Devmajor), uint32(header.Devminor)))
			if err := unix.Mknod(path, fileType|uint32(os.FileMode(header.Mode).Perm()), device); err != nil {
				return fmt.Errorf("create device %s: %w", path, err)
			}
			if err := applyMetadata(path, header, false); err != nil {
				return err
			}
		case tar.TypeFifo:
			if err := replacePath(path); err != nil {
				return err
			}
			if err := unix.Mkfifo(path, uint32(os.FileMode(header.Mode).Perm())); err != nil {
				return fmt.Errorf("create fifo %s: %w", path, err)
			}
			if err := applyMetadata(path, header, false); err != nil {
				return err
			}
		}
	}
}

func createPendingHardlinks(root string, pending []pendingHardlink) error {
	for len(pending) > 0 {
		remaining := pending[:0]
		created := 0
		for _, link := range pending {
			if err := ensureParents(root, link.path); err != nil {
				return err
			}
			if err := validateHardlinkTarget(root, link.target); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					remaining = append(remaining, link)
					continue
				}
				return err
			}
			if err := replacePath(link.path); err != nil {
				return err
			}
			if err := os.Link(link.target, link.path); err != nil {
				return fmt.Errorf("create hard link %s: %w", link.path, err)
			}
			if err := applyMetadata(link.path, &link.header, false); err != nil {
				return err
			}
			created++
		}
		if created == 0 {
			return fmt.Errorf("unresolved hard link target %s", remaining[0].target)
		}
		pending = remaining
	}
	return nil
}

func validateHardlinkTarget(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unsafe hard link target %s", target)
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe hard link target %s: symlink component", target)
		}
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe hard link target %s: not a regular file", target)
	}
	return nil
}

func applyMetadata(path string, header *tar.Header, symlink bool) error {
	if err := os.Lchown(path, header.Uid, header.Gid); err != nil && os.Geteuid() == 0 {
		return fmt.Errorf("set ownership on %s: %w", path, err)
	}
	if symlink {
		return nil
	}
	if err := os.Chmod(path, os.FileMode(header.Mode)); err != nil {
		return fmt.Errorf("set mode on %s: %w", path, err)
	}
	modTime := header.ModTime
	if modTime.IsZero() {
		modTime = time.Unix(0, 0)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		return fmt.Errorf("set timestamps on %s: %w", path, err)
	}
	for key, value := range header.PAXRecords {
		const prefix = "SCHILY.xattr."
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if err := unix.Setxattr(path, strings.TrimPrefix(key, prefix), []byte(value), 0); err != nil && err != unix.ENOTSUP && !(err == unix.EPERM && os.Geteuid() != 0) {
			return fmt.Errorf("set xattr on %s: %w", path, err)
		}
	}
	return nil
}

func openLayer(path, mediaType string) (io.Reader, func() error, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open layer %s: %w", path, err)
	}
	closeFile := func() error { return file.Close() }
	switch mediaType {
	case "", MediaTypeDockerLayerGzip, MediaTypeDockerForeignGzip, MediaTypeOCILayerGzip, MediaTypeOCIRestrictedGzip:
		reader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, nil, fmt.Errorf("open gzip layer %s: %w", path, err)
		}
		return reader, func() error {
			readerErr := reader.Close()
			fileErr := file.Close()
			if readerErr != nil {
				return readerErr
			}
			return fileErr
		}, nil
	case MediaTypeOCILayerZstd, MediaTypeOCIRestrictedZstd:
		reader, err := zstd.NewReader(file)
		if err != nil {
			file.Close()
			return nil, nil, fmt.Errorf("open zstd layer %s: %w", path, err)
		}
		return reader, func() error { reader.Close(); return file.Close() }, nil
	case MediaTypeDockerLayer, MediaTypeOCILayer, MediaTypeOCIRestricted:
		return file, closeFile, nil
	default:
		file.Close()
		return nil, nil, fmt.Errorf("unsupported layer media type %q", mediaType)
	}
}

func secureLayerPath(root, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." {
		return root, nil
	}
	if filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q in image layer", name)
	}
	path := filepath.Join(root, cleanName)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path %q in image layer", name)
	}
	return path, nil
}

func ensureParents(root, path string) error {
	parent := filepath.Dir(path)
	current := root
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe parent %s in image layer", current)
		}
	}
	return nil
}

func writeRegularFile(path string, reader io.Reader, header *tar.Header) error {
	if err := replacePath(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode).Perm())
	if err != nil {
		return fmt.Errorf("create layer file %s: %w", path, err)
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("write layer file %s: %w", path, copyErr)
	}
	return closeErr
}

func replacePath(path string) error {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
