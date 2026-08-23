package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentWritesDoNotSurfaceBusyTimeout reproduces the scenario where
// concurrent writers on a pool with SetMaxOpenConns > 1 hit connections that
// never had "PRAGMA busy_timeout" applied to them, causing SQLite to fail
// immediately with "database is locked (5) (SQLITE_BUSY)" instead of waiting.
func TestConcurrentWritesDoNotSurfaceBusyTimeout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	const goroutines = 50

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			instanceID := fmt.Sprintf("instance-%d", i)
			if _, err := db.ScheduleNewJob(ctx, instanceID); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var busyErrors []error
	for err := range errCh {
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			busyErrors = append(busyErrors, err)
			continue
		}
		t.Fatalf("unexpected error from concurrent write: %v", err)
	}

	if len(busyErrors) > 0 {
		t.Fatalf("got %d SQLITE_BUSY/\"database is locked\" errors out of %d concurrent writers; "+
			"busy_timeout is not being applied to every pooled connection (first: %v)",
			len(busyErrors), goroutines, busyErrors[0])
	}
}

// TestPragmasApplyToEveryPooledConnection verifies that the per-connection
// pragmas (busy_timeout, foreign_keys, synchronous, cache_size, temp_store)
// are set on every physical connection the pool opens, not just the first
// one used during InitDB.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pragma_check.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	const numConns = 5
	conns := make([]*sql.Conn, numConns)
	for i := 0; i < numConns; i++ {
		c, err := db.db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to acquire pooled connection %d: %v", i, err)
		}
		conns[i] = c
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	for i, c := range conns {
		var busyTimeout int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("conn %d: failed to query busy_timeout: %v", i, err)
		}
		if busyTimeout != 10000 {
			t.Errorf("conn %d: busy_timeout = %d, want 10000", i, busyTimeout)
		}

		var foreignKeys int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("conn %d: failed to query foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, foreignKeys)
		}

		var synchronous int
		if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatalf("conn %d: failed to query synchronous: %v", i, err)
		}
		if synchronous != 1 {
			t.Errorf("conn %d: synchronous = %d, want 1 (NORMAL)", i, synchronous)
		}

		var cacheSize int
		if err := c.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cacheSize); err != nil {
			t.Fatalf("conn %d: failed to query cache_size: %v", i, err)
		}
		if cacheSize != -64000 {
			t.Errorf("conn %d: cache_size = %d, want -64000", i, cacheSize)
		}

		var tempStore int
		if err := c.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&tempStore); err != nil {
			t.Fatalf("conn %d: failed to query temp_store: %v", i, err)
		}
		if tempStore != 2 {
			t.Errorf("conn %d: temp_store = %d, want 2 (MEMORY)", i, tempStore)
		}
	}
}
