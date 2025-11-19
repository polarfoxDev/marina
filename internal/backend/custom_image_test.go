package backend

import (
	"context"
	"os"
	"path/filepath"
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

func TestCustomImageBackend_CleanupStagingDir(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	instancePath := filepath.Join(tempDir, "test-instance")
	if err := os.MkdirAll(instancePath, 0o755); err != nil {
		t.Fatalf("failed to create instance directory: %v", err)
	}

	backend, err := NewCustomImageBackend("test-id", "alpine:latest", nil, "test-host", tempDir)
	if err != nil {
		t.Fatalf("NewCustomImageBackend failed: %v", err)
	}
	defer backend.Close()

	// Create some timestamp directories (simulating staging directories)
	timestamps := []string{
		"20240101-120000",
		"20240102-120000",
		"20240103-120000",
	}

	for _, ts := range timestamps {
		tsDir := filepath.Join(instancePath, ts)
		if err := os.MkdirAll(filepath.Join(tsDir, "volume", "test"), 0o755); err != nil {
			t.Fatalf("failed to create timestamp directory: %v", err)
		}
		// Create a dummy file
		dummyFile := filepath.Join(tsDir, "volume", "test", "data.txt")
		if err := os.WriteFile(dummyFile, []byte("test data"), 0o644); err != nil {
			t.Fatalf("failed to create dummy file: %v", err)
		}
	}

	// Verify directories exist
	for _, ts := range timestamps {
		tsDir := filepath.Join(instancePath, ts)
		if _, err := os.Stat(tsDir); os.IsNotExist(err) {
			t.Fatalf("timestamp directory should exist before cleanup: %s", tsDir)
		}
	}

	// Run cleanup
	if err := backend.cleanupStagingDir(instancePath); err != nil {
		t.Fatalf("cleanupStagingDir failed: %v", err)
	}

	// Verify directories are removed
	for _, ts := range timestamps {
		tsDir := filepath.Join(instancePath, ts)
		if _, err := os.Stat(tsDir); !os.IsNotExist(err) {
			t.Errorf("timestamp directory should be removed after cleanup: %s", tsDir)
		}
	}

	// Verify instance directory still exists
	if _, err := os.Stat(instancePath); os.IsNotExist(err) {
		t.Errorf("instance directory should still exist after cleanup: %s", instancePath)
	}
}

func TestCustomImageBackend_SetLogger(t *testing.T) {
	backend, err := NewCustomImageBackend("test-id", "alpine:latest", nil, "test-host", "/tmp/backup")
	if err != nil {
		t.Fatalf("NewCustomImageBackend failed: %v", err)
	}
	defer backend.Close()

	// Test that logger can be set
	mockLogger := &mockLogger{}
	backend.SetLogger(mockLogger)

	if backend.logger == nil {
		t.Error("logger should be set")
	}
}

// mockLogger is a simple logger implementation for testing
type mockLogger struct {
	messages []string
}

func (m *mockLogger) Debug(format string, args ...any) {
	// Store messages for verification if needed
	m.messages = append(m.messages, format)
}

