// Package archive turns a directory tree into a streaming tar+zstd archive
// and back. It is deliberately minimal: regular files, directories, and
// symlinks only -- staged backup data never contains anything else.
package archive

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// WriteTree walks root and writes its contents as a zstd-compressed tar
// stream to w. Entry names are relative to root, using forward slashes,
// e.g. root itself becomes ".", and root/sub/file becomes "sub/file".
func WriteTree(w io.Writer, root string) error {
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return fmt.Errorf("create zstd writer: %w", err)
	}
	defer zw.Close()

	tw := tar.NewWriter(zw)
	defer tw.Close()

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}
		relPath = filepath.ToSlash(relPath)

		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
		}

		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return fmt.Errorf("build tar header for %s: %w", path, err)
		}
		hdr.Name = relPath
		if info.IsDir() {
			hdr.Name += "/"
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write tar header for %s: %w", relPath, err)
		}

		// Only regular files carry a body.
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open %s: %w", path, err)
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return fmt.Errorf("copy %s into archive: %w", path, err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zstd writer: %w", err)
	}
	return nil
}

// ExtractTree reads a zstd-compressed tar stream from r and recreates it
// under dest. It refuses to write any entry whose cleaned path would land
// outside dest (a "Zip Slip" style path traversal via "../" or an absolute
// path), and skips any entry that isn't a regular file, directory, or
// symlink.
func ExtractTree(r io.Reader, dest string) error {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return fmt.Errorf("create zstd reader: %w", err)
	}
	defer zr.Close()

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination %s: %w", dest, err)
	}

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}

		case tar.TypeSymlink:
			// hdr.Linkname is the symlink's target, not a path within the
			// archive -- it isn't subject to the same traversal check, but
			// a link escaping dest is a normal (if unusual) symlink and
			// following it is the caller's problem, same as any tar tool.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}

		default:
			// Skip device nodes, fifos, etc. -- staged backup data never
			// legitimately contains these.
			continue
		}
	}

	return nil
}

// safeJoin joins dest with the (tar-internal, forward-slash) entry name and
// verifies the result stays within dest.
func safeJoin(dest, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." {
		return dest, nil
	}
	if filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || cleanName == ".." {
		return "", fmt.Errorf("archive entry %q escapes destination directory", name)
	}

	target := filepath.Join(dest, cleanName)
	destWithSep := dest
	if !strings.HasSuffix(destWithSep, string(os.PathSeparator)) {
		destWithSep += string(os.PathSeparator)
	}
	if target != dest && !strings.HasPrefix(target, destWithSep) {
		return "", fmt.Errorf("archive entry %q escapes destination directory", name)
	}
	return target, nil
}
