package logging

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// LogLevel represents the severity of a log entry
type LogLevel string

const (
	LevelDebug LogLevel = "DEBUG"
	LevelInfo  LogLevel = "INFO"
	LevelWarn  LogLevel = "WARN"
	LevelError LogLevel = "ERROR"
)

// Logger provides structured logging with both console and database output
type Logger struct {
	db      *sql.DB
	console io.Writer
	mu      sync.Mutex
}

// LogEntry represents a single log entry
type LogEntry struct {
	ID         int64
	Timestamp  time.Time
	Level      LogLevel
	Message    string
	InstanceID string // backup instance ID (e.g., "hetzner-s3")
	TargetID   string // specific target ID (e.g., "volume:mydata", "container:abc123")
}

// New creates a new Logger with SQLite backend
func New(dbPath string, console io.Writer) (*Logger, error) {
	if console == nil {
		console = os.Stdout
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	l := &Logger{
		db:      db,
		console: console,
	}

	if err := l.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return l, nil
}

// initSchema creates the log table if it doesn't exist
func (l *Logger) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		instance_id TEXT,
		target_id TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_logs_instance_id ON logs(instance_id);
	CREATE INDEX IF NOT EXISTS idx_logs_target_id ON logs(target_id);
	CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
	`
	_, err := l.db.Exec(schema)
	return err
}

// Close closes the database connection
func (l *Logger) Close() error {
	if l.db != nil {
		return l.db.Close()
	}
	return nil
}

// Log writes a log entry to both console and database
func (l *Logger) Log(level LogLevel, instanceID, targetID, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	message := fmt.Sprintf(format, args...)
	timestamp := time.Now()

	// Write to console with timestamp
	prefix := timestamp.Format("2006-01-02 15:04:05")
	if instanceID != "" {
		prefix += fmt.Sprintf(" [%s", instanceID)
		if targetID != "" {
			prefix += fmt.Sprintf("/%s", targetID)
		}
		prefix += "]"
	}
	fmt.Fprintf(l.console, "%s %s: %s\n", prefix, level, message)

	// Write to database
	_, err := l.db.Exec(
		"INSERT INTO logs (timestamp, level, message, instance_id, target_id) VALUES (?, ?, ?, ?, ?)",
		timestamp, string(level), message, nullString(instanceID), nullString(targetID),
	)
	if err != nil {
		// If DB write fails, at least we have console output
		fmt.Fprintf(l.console, "ERROR: failed to write to log database: %v\n", err)
	}
}

// Info logs an info-level message
func (l *Logger) Info(format string, args ...any) {
	l.Log(LevelInfo, "", "", format, args...)
}

// Warn logs a warning-level message
func (l *Logger) Warn(format string, args ...any) {
	l.Log(LevelWarn, "", "", format, args...)
}

// Error logs an error-level message
func (l *Logger) Error(format string, args ...any) {
	l.Log(LevelError, "", "", format, args...)
}

// Debug logs a debug-level message
func (l *Logger) Debug(format string, args ...any) {
	l.Log(LevelDebug, "", "", format, args...)
}

// JobLog logs a message associated with a specific job
func (l *Logger) JobLog(level LogLevel, instanceID, targetID, format string, args ...any) {
	l.Log(level, instanceID, targetID, format, args...)
}

// Logf provides compatibility with the old Logf func(string, ...any) signature
func (l *Logger) Logf(format string, args ...any) {
	l.Info(format, args...)
}

// QueryOptions defines filters for querying logs
type QueryOptions struct {
	InstanceID string
	TargetID   string
	Level      LogLevel
	Since      time.Time
	Until      time.Time
	Limit      int
}

// Query retrieves log entries based on filters
func (l *Logger) Query(opts QueryOptions) ([]LogEntry, error) {
	query := "SELECT id, timestamp, level, message, COALESCE(instance_id, ''), COALESCE(target_id, '') FROM logs WHERE 1=1"
	args := []any{}

	if opts.InstanceID != "" {
		query += " AND instance_id = ?"
		args = append(args, opts.InstanceID)
	}
	if opts.TargetID != "" {
		query += " AND target_id = ?"
		args = append(args, opts.TargetID)
	}
	if opts.Level != "" {
		query += " AND level = ?"
		args = append(args, string(opts.Level))
	}
	if !opts.Since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, opts.Since)
	}
	if !opts.Until.IsZero() {
		query += " AND timestamp <= ?"
		args = append(args, opts.Until)
	}

	query += " ORDER BY timestamp DESC"

	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := l.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		var levelStr string
		if err := rows.Scan(&e.ID, &e.Timestamp, &levelStr, &e.Message, &e.InstanceID, &e.TargetID); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		e.Level = LogLevel(levelStr)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// PruneOldLogs removes log entries older than the specified duration
func (l *Logger) PruneOldLogs(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := l.db.Exec("DELETE FROM logs WHERE timestamp < ?", cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune logs: %w", err)
	}
	return result.RowsAffected()
}

// nullString returns a sql.NullString for use with nullable columns
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// JobLogger wraps a Logger with instance and optional target context for convenient logging
type JobLogger struct {
	logger     *Logger
	instanceID string
	targetID   string
}

// NewJobLogger creates a JobLogger with instance context (for instance-level logs)
func (l *Logger) NewJobLogger(instanceID string) *JobLogger {
	return &JobLogger{
		logger:     l,
		instanceID: instanceID,
		targetID:   "",
	}
}

// WithTarget creates a new JobLogger with target context added (for target-specific logs)
func (jl *JobLogger) WithTarget(targetID string) *JobLogger {
	return &JobLogger{
		logger:     jl.logger,
		instanceID: jl.instanceID,
		targetID:   targetID,
	}
}

// Info logs an info-level message with job context
func (jl *JobLogger) Info(format string, args ...any) {
	jl.logger.JobLog(LevelInfo, jl.instanceID, jl.targetID, format, args...)
}

// Warn logs a warning-level message with job context
func (jl *JobLogger) Warn(format string, args ...any) {
	jl.logger.JobLog(LevelWarn, jl.instanceID, jl.targetID, format, args...)
}

// Error logs an error-level message with job context
func (jl *JobLogger) Error(format string, args ...any) {
	jl.logger.JobLog(LevelError, jl.instanceID, jl.targetID, format, args...)
}

// Debug logs a debug-level message with job context
func (jl *JobLogger) Debug(format string, args ...any) {
	jl.logger.JobLog(LevelDebug, jl.instanceID, jl.targetID, format, args...)
}

// Logf provides compatibility with func(string, ...any) signature
func (jl *JobLogger) Logf(format string, args ...any) {
	jl.Info(format, args...)
}
