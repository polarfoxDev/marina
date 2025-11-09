package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIntegrationWorkflow demonstrates a complete workflow of logging and querying
func TestIntegrationWorkflow(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "integration.db")
	console := &bytes.Buffer{}
	
	logger, err := New(dbPath, console)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()
	
	// Simulate a backup workflow
	t.Log("Simulating marina backup workflow...")
	
	// System startup
	logger.Info("marina starting...")
	logger.Info("loaded instance: hetzner-s3 -> s3://bucket")
	logger.Info("scheduler started")
	
	// Simulate first backup job
	jobLogger1 := logger.NewJobLogger("volume:data", "hetzner-s3")
	jobLogger1.Info("backup job started")
	time.Sleep(10 * time.Millisecond)
	jobLogger1.Info("stopping attached container app-1")
	time.Sleep(10 * time.Millisecond)
	jobLogger1.Info("backup job completed successfully (duration: 5.2s)")
	
	// Simulate second backup job with error
	jobLogger2 := logger.NewJobLogger("volume:logs", "local-backup")
	jobLogger2.Info("backup job started")
	time.Sleep(10 * time.Millisecond)
	jobLogger2.Error("backup job failed: connection timeout")
	
	// Simulate third backup job
	jobLogger3 := logger.NewJobLogger("container:postgres", "hetzner-s3")
	jobLogger3.Info("backup job started")
	jobLogger3.Debug("dump output: pg_dump successful")
	jobLogger3.Info("backup job completed successfully (duration: 12.8s)")
	
	// More system logs
	logger.Info("triggering rediscovery...")
	logger.Info("discovered 5 targets")
	
	// Now query and verify the logs
	t.Run("QueryAllLogs", func(t *testing.T) {
		entries, err := logger.Query(QueryOptions{})
		if err != nil {
			t.Fatalf("failed to query all logs: %v", err)
		}
		// Should have at least 11 log entries
		if len(entries) < 11 {
			t.Errorf("expected at least 11 entries, got %d", len(entries))
		}
		t.Logf("Total log entries: %d", len(entries))
	})
	
	t.Run("QueryByJob", func(t *testing.T) {
		entries, err := logger.Query(QueryOptions{JobID: "volume:data"})
		if err != nil {
			t.Fatalf("failed to query by job: %v", err)
		}
		if len(entries) != 3 {
			t.Errorf("expected 3 entries for volume:data, got %d", len(entries))
		}
		t.Logf("Job 'volume:data' has %d log entries", len(entries))
	})
	
	t.Run("QueryByInstance", func(t *testing.T) {
		entries, err := logger.Query(QueryOptions{InstanceID: "hetzner-s3"})
		if err != nil {
			t.Fatalf("failed to query by instance: %v", err)
		}
		// Should have logs from 2 jobs
		if len(entries) < 6 {
			t.Errorf("expected at least 6 entries for hetzner-s3, got %d", len(entries))
		}
		t.Logf("Instance 'hetzner-s3' has %d log entries", len(entries))
	})
	
	t.Run("QueryErrors", func(t *testing.T) {
		entries, err := logger.Query(QueryOptions{Level: LevelError})
		if err != nil {
			t.Fatalf("failed to query errors: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("expected 1 error entry, got %d", len(entries))
		}
		if len(entries) > 0 {
			if entries[0].Message != "backup job failed: connection timeout" {
				t.Errorf("unexpected error message: %s", entries[0].Message)
			}
			t.Logf("Found error: %s", entries[0].Message)
		}
	})
	
	t.Run("QuerySystemLogs", func(t *testing.T) {
		// System logs have empty job_id
		allEntries, err := logger.Query(QueryOptions{})
		if err != nil {
			t.Fatalf("failed to query all logs: %v", err)
		}
		systemLogs := 0
		for _, e := range allEntries {
			if e.JobID == "" {
				systemLogs++
			}
		}
		if systemLogs < 5 {
			t.Errorf("expected at least 5 system logs, got %d", systemLogs)
		}
		t.Logf("System log entries: %d", systemLogs)
	})
	
	t.Run("VerifyConsoleOutput", func(t *testing.T) {
		output := console.String()
		// Verify console contains key messages
		if !bytes.Contains(console.Bytes(), []byte("marina starting...")) {
			t.Error("console missing startup message")
		}
		if !bytes.Contains(console.Bytes(), []byte("[job:volume:data]")) {
			t.Error("console missing job context")
		}
		if !bytes.Contains(console.Bytes(), []byte("[instance:hetzner-s3]")) {
			t.Error("console missing instance context")
		}
		t.Logf("Console output length: %d bytes", len(output))
	})
	
	t.Run("VerifyDatabaseFile", func(t *testing.T) {
		// Check that database files exist
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			t.Error("database file does not exist")
		}
		// WAL mode should create additional files
		walPath := dbPath + "-wal"
		if _, err := os.Stat(walPath); os.IsNotExist(err) {
			t.Log("WAL file not yet created (this is OK)")
		}
	})
}
