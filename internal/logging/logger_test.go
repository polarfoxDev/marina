package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogger_BasicLogging(t *testing.T) {
	// Create temp database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	console := &bytes.Buffer{}
	logger, err := New(dbPath, console)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Log some messages
	logger.Info("test info message")
	logger.Warn("test warning message")
	logger.Error("test error message")

	// Verify console output contains messages
	output := console.String()
	if !bytes.Contains(console.Bytes(), []byte("test info message")) {
		t.Errorf("console output missing info message: %s", output)
	}
	if !bytes.Contains(console.Bytes(), []byte("test warning message")) {
		t.Errorf("console output missing warning message: %s", output)
	}
	if !bytes.Contains(console.Bytes(), []byte("test error message")) {
		t.Errorf("console output missing error message: %s", output)
	}

	// Verify database was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("database file was not created")
	}
}

func TestLogger_JobLogging(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger, err := New(dbPath, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Log with instance and target context
	logger.JobLog(LevelInfo, "instance-abc", "volume:mydata", "backup started")
	logger.JobLog(LevelInfo, "instance-xyz", "container:db123", "backup completed")

	// Query by target ID
	entries, err := logger.Query(QueryOptions{TargetID: "volume:mydata"})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].InstanceID != "instance-abc" {
		t.Errorf("expected instance ID 'instance-abc', got '%s'", entries[0].InstanceID)
	}
	if entries[0].TargetID != "volume:mydata" {
		t.Errorf("expected target ID 'volume:mydata', got '%s'", entries[0].TargetID)
	}
	if entries[0].Message != "backup started" {
		t.Errorf("expected message 'backup started', got '%s'", entries[0].Message)
	}
}

func TestLogger_QueryByInstance(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger, err := New(dbPath, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Log entries for different instances
	logger.JobLog(LevelInfo, "instance-1", "volume:vol1", "message 1")
	logger.JobLog(LevelInfo, "instance-2", "volume:vol2", "message 2")
	logger.JobLog(LevelInfo, "instance-1", "container:db1", "message 3")

	// Query by instance ID
	entries, err := logger.Query(QueryOptions{InstanceID: "instance-1"})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for instance-1, got %d", len(entries))
	}
}

func TestLogger_QueryByLevel(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger, err := New(dbPath, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Log entries with different levels
	logger.Info("info message")
	logger.Error("error message")
	logger.Warn("warning message")
	logger.Error("another error")

	// Query errors only
	entries, err := logger.Query(QueryOptions{Level: LevelError})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 error entries, got %d", len(entries))
	}

	for _, e := range entries {
		if e.Level != LevelError {
			t.Errorf("expected level ERROR, got %s", e.Level)
		}
	}
}

func TestLogger_QueryByTimeRange(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger, err := New(dbPath, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	start := time.Now()
	logger.Info("message 1")
	time.Sleep(10 * time.Millisecond)
	middle := time.Now()
	time.Sleep(10 * time.Millisecond)
	logger.Info("message 2")
	end := time.Now()

	// Query messages after middle timestamp
	entries, err := logger.Query(QueryOptions{Since: middle})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after middle, got %d", len(entries))
	}
	if entries[0].Message != "message 2" {
		t.Errorf("expected 'message 2', got '%s'", entries[0].Message)
	}

	// Query all messages in range
	entries, err = logger.Query(QueryOptions{Since: start, Until: end})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in range, got %d", len(entries))
	}
}

func TestLogger_QueryWithLimit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger, err := New(dbPath, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Log multiple messages
	for i := 0; i < 10; i++ {
		logger.Info("message %d", i)
	}

	// Query with limit
	entries, err := logger.Query(QueryOptions{Limit: 5})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}

	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestLogger_PruneOldLogs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	logger, err := New(dbPath, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Log some messages
	logger.Info("message 1")
	logger.Info("message 2")
	logger.Info("message 3")

	// Verify all messages exist
	entries, err := logger.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Prune logs older than 1 hour (should delete nothing since all are recent)
	deleted, err := logger.PruneOldLogs(1 * time.Hour)
	if err != nil {
		t.Fatalf("failed to prune logs: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted entries, got %d", deleted)
	}

	// Prune logs older than -1 hour (should delete all)
	deleted, err = logger.PruneOldLogs(-1 * time.Hour)
	if err != nil {
		t.Fatalf("failed to prune logs: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deleted entries, got %d", deleted)
	}

	// Verify all messages are gone
	entries, err = logger.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after pruning, got %d", len(entries))
	}
}

func TestLogger_LogfCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	console := &bytes.Buffer{}
	logger, err := New(dbPath, console)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	// Test Logf method (compatibility with old signature)
	logger.Logf("test message %d", 42)

	// Verify it works like Info
	if !bytes.Contains(console.Bytes(), []byte("test message 42")) {
		t.Errorf("Logf output missing message")
	}

	entries, err := logger.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("failed to query logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Level != LevelInfo {
		t.Errorf("expected INFO level, got %s", entries[0].Level)
	}
}
