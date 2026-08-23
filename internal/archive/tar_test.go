package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestWriteExtractTree_RoundTrip(t *testing.T) {
	src := t.TempDir()

	mustWriteFile(t, filepath.Join(src, "a.txt"), "hello", 0o644)
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	mustWriteFile(t, filepath.Join(src, "sub", "b.txt"), "world", 0o600)

	if runtime.GOOS != "windows" {
		if err := os.Symlink("a.txt", filepath.Join(src, "link-to-a")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := WriteTree(&buf, src); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}

	dest := t.TempDir()
	if err := ExtractTree(&buf, dest); err != nil {
		t.Fatalf("ExtractTree: %v", err)
	}

	gotA := mustReadFile(t, filepath.Join(dest, "a.txt"))
	if gotA != "hello" {
		t.Fatalf("a.txt = %q, want %q", gotA, "hello")
	}
	gotB := mustReadFile(t, filepath.Join(dest, "sub", "b.txt"))
	if gotB != "world" {
		t.Fatalf("sub/b.txt = %q, want %q", gotB, "world")
	}

	info, err := os.Stat(filepath.Join(dest, "a.txt"))
	if err != nil {
		t.Fatalf("stat a.txt: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("a.txt mode = %v, want 0644", info.Mode().Perm())
	}

	if runtime.GOOS != "windows" {
		linkTarget, err := os.Readlink(filepath.Join(dest, "link-to-a"))
		if err != nil {
			t.Fatalf("readlink: %v", err)
		}
		if linkTarget != "a.txt" {
			t.Fatalf("link target = %q, want %q", linkTarget, "a.txt")
		}
	}
}

func TestExtractTree_RejectsPathTraversal(t *testing.T) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "../escape.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     4,
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("evil")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}

	var zBuf bytes.Buffer
	zw, err := zstd.NewWriter(&zBuf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}

	dest := t.TempDir()
	err = ExtractTree(&zBuf, dest)
	if err == nil {
		t.Fatal("expected error extracting a path-traversal entry, got nil")
	}

	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); statErr == nil {
		t.Fatal("path traversal entry was written outside dest")
	}
}

func mustWriteFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
