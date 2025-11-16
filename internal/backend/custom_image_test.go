package backend

import (
	"context"
	"testing"
)

func TestCustomImageBackend_Interface(t *testing.T) {
	// Test that CustomImageBackend implements the Backend interface
	var _ Backend = (*CustomImageBackend)(nil)
	
	// Test basic creation
	backend, err := NewCustomImageBackend("test-id", "alpine:latest", map[string]string{"TEST": "value"}, "test-host")
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
	backend, err := NewCustomImageBackend("test-id", "alpine:latest", nil, "test-host")
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
