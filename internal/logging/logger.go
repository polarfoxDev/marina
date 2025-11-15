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
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        LogLevel  `json:"level"`
	Message      string    `json:"message"`
	InstanceID   string    `json:"instanceId"`   // backup instance ID (e.g., "hetzner-s3")
	TargetID     string    `json:"targetId"`     // specific target ID (e.g., "volume:mydata", "container:abc123")
	JobStatusID  int       `json:"jobStatusId"`  // job_status.id from the status database
	JobStatusIID int       `json:"jobStatusIid"` // job_status.iid from the status database
}

// New creates a new Logger using an existing database connection.
// The caller is responsible for closing the database connection.
func New(db *sql.DB, console io.Writer) (*Logger, error) {
	if console == nil {
		console = os.Stdout
	}

	l := &Logger{
		db:      db,
		console: console,
	}

	return l, nil
}

// Log writes a log entry to both console and database
func (l *Logger) Log(level LogLevel, instanceID, targetID string, jobStatusID, jobStatusIID int, format string, args ...any) {
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
		"INSERT INTO logs (timestamp, level, message, instance_id, target_id, job_status_id, job_status_iid) VALUES (?, ?, ?, ?, ?, ?, ?)",
		timestamp, string(level), message, nullString(instanceID), nullString(targetID), nullInt(jobStatusID), nullInt(jobStatusIID),
	)
	if err != nil {
		// If DB write fails, at least we have console output
		fmt.Fprintf(l.console, "ERROR: failed to write to log database: %v\n", err)
	}
}

// Info logs an info-level message
func (l *Logger) Info(format string, args ...any) {
	l.Log(LevelInfo, "", "", 0, 0, format, args...)
}

// Warn logs a warning-level message
func (l *Logger) Warn(format string, args ...any) {
	l.Log(LevelWarn, "", "", 0, 0, format, args...)
}

// Error logs an error-level message
func (l *Logger) Error(format string, args ...any) {
	l.Log(LevelError, "", "", 0, 0, format, args...)
}

// Debug logs a debug-level message
func (l *Logger) Debug(format string, args ...any) {
	l.Log(LevelDebug, "", "", 0, 0, format, args...)
}

// JobLog logs a message associated with a specific job
func (l *Logger) JobLog(level LogLevel, instanceID, targetID string, jobStatusID, jobStatusIID int, format string, args ...any) {
	l.Log(level, instanceID, targetID, jobStatusID, jobStatusIID, format, args...)
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
	query := "SELECT id, timestamp, level, message, COALESCE(instance_id, ''), COALESCE(target_id, ''), COALESCE(job_status_id, 0), COALESCE(job_status_iid, 0) FROM logs WHERE 1=1"
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

	// Initialize as empty slice so JSON encodes as [] instead of null
	entries := make([]LogEntry, 0)
	for rows.Next() {
		var e LogEntry
		var levelStr string
		if err := rows.Scan(&e.ID, &e.Timestamp, &levelStr, &e.Message, &e.InstanceID, &e.TargetID, &e.JobStatusID, &e.JobStatusIID); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		e.Level = LogLevel(levelStr)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// QueryByJobID retrieves log entries for a specific job status ID
func (l *Logger) QueryByJobID(jobStatusID int, limit int) ([]LogEntry, error) {
	query := "SELECT id, timestamp, level, message, COALESCE(instance_id, ''), COALESCE(target_id, ''), COALESCE(job_status_id, 0), COALESCE(job_status_iid, 0) FROM logs WHERE job_status_id = ? ORDER BY timestamp ASC"
	args := []any{jobStatusID}

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := l.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs by job ID: %w", err)
	}
	defer rows.Close()

	// Initialize as empty slice so JSON encodes as [] instead of null
	entries := make([]LogEntry, 0)
	for rows.Next() {
		var e LogEntry
		var levelStr string
		if err := rows.Scan(&e.ID, &e.Timestamp, &levelStr, &e.Message, &e.InstanceID, &e.TargetID, &e.JobStatusID, &e.JobStatusIID); err != nil {
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

// nullInt returns a sql.NullInt64 for use with nullable columns
func nullInt(i int) sql.NullInt64 {
	if i == 0 {
		return sql.NullInt64{Valid: false}
	}
	return sql.NullInt64{Int64: int64(i), Valid: true}
}

// JobLogger wraps a Logger with instance and optional target context for convenient logging
type JobLogger struct {
	logger       *Logger
	instanceID   string
	targetID     string
	jobStatusID  int
	jobStatusIID int
}

// NewJobLogger creates a JobLogger with instance context (for instance-level logs)
func (l *Logger) NewJobLogger(instanceID string, jobStatusID, jobStatusIID int) *JobLogger {
	return &JobLogger{
		logger:       l,
		instanceID:   instanceID,
		targetID:     "",
		jobStatusID:  jobStatusID,
		jobStatusIID: jobStatusIID,
	}
}

// WithTarget creates a new JobLogger with target context added (for target-specific logs)
func (jl *JobLogger) WithTarget(targetID string) *JobLogger {
	return &JobLogger{
		logger:       jl.logger,
		instanceID:   jl.instanceID,
		targetID:     targetID,
		jobStatusID:  jl.jobStatusID,
		jobStatusIID: jl.jobStatusIID,
	}
}

// Info logs an info-level message with job context
func (jl *JobLogger) Info(format string, args ...any) {
	jl.logger.JobLog(LevelInfo, jl.instanceID, jl.targetID, jl.jobStatusID, jl.jobStatusIID, format, args...)
}

// Warn logs a warning-level message with job context
func (jl *JobLogger) Warn(format string, args ...any) {
	jl.logger.JobLog(LevelWarn, jl.instanceID, jl.targetID, jl.jobStatusID, jl.jobStatusIID, format, args...)
}

// Error logs an error-level message with job context
func (jl *JobLogger) Error(format string, args ...any) {
	jl.logger.JobLog(LevelError, jl.instanceID, jl.targetID, jl.jobStatusID, jl.jobStatusIID, format, args...)
}

// Debug logs a debug-level message with job context
func (jl *JobLogger) Debug(format string, args ...any) {
	jl.logger.JobLog(LevelDebug, jl.instanceID, jl.targetID, jl.jobStatusID, jl.jobStatusIID, format, args...)
}

// Logf provides compatibility with func(string, ...any) signature
func (jl *JobLogger) Logf(format string, args ...any) {
	jl.Info(format, args...)
}
