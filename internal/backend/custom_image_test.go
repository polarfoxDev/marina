package backend

import (
	"context"
	"testing"
)

func TestCustomImageBackend_Interface(t *testing.T) {
	// Test that CustomImageBackend implements the Backend interface
	var _ Backend = (*CustomImageBackend)(nil)

	// Test basic creation
	backend, err := NewCustomImageBackend("test-id", "alpine:latest", map[string]string{"TEST": "value"}, "test-host", "/tmp/backup")
	if err != nil {
		t.Fatalf("NewCustomImageBackend failed: %v", err)
	}

	if backend.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", backend.ID)
	}

	if backend.CustomImage != "alpine:latest" {
		t.Errorf("expected CustomImage 'alpine:latest', got %q", backend.CustomImage)
	}

	if backend.Hostname != "test-host" {
		t.Errorf("expected Hostname 'test-host', got %q", backend.Hostname)
	}

	// Test cleanup
	if err := backend.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestCustomImageBackend_DeleteOldSnapshots(t *testing.T) {
	backend, err := NewCustomImageBackend("test-id", "alpine:latest", nil, "test-host", "/tmp/backup")
	if err != nil {
		t.Fatalf("NewCustomImageBackend failed: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// DeleteOldSnapshots should be a no-op for custom images
	output, err := backend.DeleteOldSnapshots(ctx, 7, 4, 6)
	if err != nil {
		t.Errorf("DeleteOldSnapshots failed: %v", err)
	}

	if output != "" {
		t.Errorf("expected empty output, got %q", output)
	}
}

func TestLineWriter(t *testing.T) {
	var allLogs []string
	writer := &lineWriter{
		logger:  nil, // No logger for this test
		allLogs: &allLogs,
	}

	// Test writing complete lines
	_, err := writer.Write([]byte("line 1\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if len(allLogs) != 1 || allLogs[0] != "line 1" {
		t.Errorf("expected ['line 1'], got %v", allLogs)
	}

	// Test writing multiple lines at once
	_, err = writer.Write([]byte("line 2\nline 3\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if len(allLogs) != 3 {
		t.Errorf("expected 3 lines, got %d", len(allLogs))
	}

	// Test partial line (should not be added yet)
	_, err = writer.Write([]byte("partial"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if len(allLogs) != 3 {
		t.Errorf("partial line should not be added yet, expected 3 lines, got %d", len(allLogs))
	}

	// Complete the partial line
	_, err = writer.Write([]byte(" line\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if len(allLogs) != 4 || allLogs[3] != "partial line" {
		t.Errorf("expected 4 lines with 'partial line' as last, got %v", allLogs)
	}

	// Test flush with remaining data
	_, err = writer.Write([]byte("final"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	writer.flush()

	if len(allLogs) != 5 || allLogs[4] != "final" {
		t.Errorf("expected 5 lines with 'final' as last, got %v", allLogs)
	}
}
