package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/polarfoxDev/marina/internal/model"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

func InitDB(dbPath string) (*DB, error) {
	// Retry logic for handling concurrent initialization
	var db *sql.DB
	var err error
	maxRetries := 5
	baseDelay := 100 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}

		db, err = sql.Open("sqlite", dbPath)
		if err != nil {
			if attempt == maxRetries-1 {
				return nil, fmt.Errorf("failed to open database after %d attempts: %w", maxRetries, err)
			}
			continue
		}

		// Set connection pool limits for better concurrency
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(time.Minute * 5)

		// Configure SQLite pragmas for better concurrency and performance
		pragmas := []string{
			"PRAGMA busy_timeout = 10000", // 10 second timeout - set this FIRST
			"PRAGMA journal_mode = WAL",
			"PRAGMA foreign_keys = ON",
			"PRAGMA synchronous = NORMAL", // Faster writes with WAL mode
			"PRAGMA cache_size = -64000",  // 64MB cache
			"PRAGMA temp_store = MEMORY",  // Use memory for temp tables
		}

		pragmaFailed := false
		for _, pragma := range pragmas {
			if _, err := db.Exec(pragma); err != nil {
				db.Close()
				if attempt == maxRetries-1 {
					return nil, fmt.Errorf("failed to set pragma %q after %d attempts: %w", pragma, maxRetries, err)
				}
				pragmaFailed = true
				break
			}
		}

		if pragmaFailed {
			continue
		}

		// Try to create schema
		if err := createSchema(db); err != nil {
			db.Close()
			if attempt == maxRetries-1 {
				return nil, fmt.Errorf("failed to create schema after %d attempts: %w", maxRetries, err)
			}
			continue
		}

		// Success!
		return &DB{db: db}, nil
	}

	// Ensure any open connection is closed before returning error
	if db != nil {
		db.Close()
	}
	return nil, fmt.Errorf("failed to initialize database after %d attempts: %w", maxRetries, err)
}

// CleanupInterruptedJobs resets any jobs that were interrupted by a restart
func (d *DB) CleanupInterruptedJobs(ctx context.Context) (int, error) {
	query := `
		UPDATE job_status 
		SET status = ?, updated_at = ?
		WHERE status IN (?, ?)
	`

	result, err := d.db.ExecContext(
		ctx,
		query,
		model.StatusAborted,
		time.Now(),
		model.StatusInProgress,
		model.StatusScheduled,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup interrupted jobs: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return int(rowsAffected), nil
}

func createSchema(db *sql.DB) error {
	// Create unified schema for both job status and logs
	schema := `
	CREATE TABLE IF NOT EXISTS job_status (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		iid INTEGER NOT NULL,
		instance_id TEXT NOT NULL,
		is_active INTEGER DEFAULT 1,
		status TEXT NOT NULL,
		last_targets_successful INTEGER DEFAULT 0,
		last_targets_total INTEGER DEFAULT 0,
		last_started_at TIMESTAMP,
		last_completed_at TIMESTAMP,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_job_status_iid_instance ON job_status(iid, instance_id);
	CREATE INDEX IF NOT EXISTS idx_job_status_instance ON job_status(instance_id);
	CREATE INDEX IF NOT EXISTS idx_job_status_status ON job_status(status);
	CREATE INDEX IF NOT EXISTS idx_job_status_active ON job_status(is_active);

	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		instance_id TEXT,
		target_id TEXT,
		job_status_id INTEGER,
		job_status_iid INTEGER
	);

	CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_logs_instance_id ON logs(instance_id);
	CREATE INDEX IF NOT EXISTS idx_logs_target_id ON logs(target_id);
	CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
	CREATE INDEX IF NOT EXISTS idx_logs_job_status_id ON logs(job_status_id);
	CREATE INDEX IF NOT EXISTS idx_logs_job_status_iid ON logs(job_status_iid);

	CREATE TABLE IF NOT EXISTS backup_schedules (
		instance_id TEXT NOT NULL PRIMARY KEY,
		schedule_cron TEXT NOT NULL,
		next_run_at TIMESTAMP,
		retention_keep_daily INTEGER DEFAULT 0,
		retention_keep_weekly INTEGER DEFAULT 0,
		retention_keep_monthly INTEGER DEFAULT 0,
		targets TEXT,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_backup_schedules_instance_id ON backup_schedules(instance_id);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}

	return nil
}

// Close closes the database connection
func (d *DB) Close() error {
	return d.db.Close()
}

// GetDB returns the underlying *sql.DB for use by other packages (e.g., logger)
func (d *DB) GetDB() *sql.DB {
	return d.db
}

func (d *DB) UpdateNextRunTime(ctx context.Context, instanceID string, nextRunTime *time.Time) error {
	query := `
		UPDATE backup_schedules
		SET next_run_at = ?, updated_at = ?
		WHERE instance_id = ?
	`

	_, err := d.db.ExecContext(ctx, query, nextRunTime, time.Now(), instanceID)
	if err != nil {
		return fmt.Errorf("failed to update next run time for instance %s: %w", instanceID, err)
	}

	return nil
}

func (d *DB) AddOrUpdateSchedules(ctx context.Context, schedules map[model.InstanceID]model.InstanceBackupSchedule) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete schedules not in the provided map
	if len(schedules) > 0 {
		deleteQuery := `DELETE FROM backup_schedules WHERE instance_id NOT IN (` + placeholders(len(schedules)) + `)`
		args := make([]any, 0, len(schedules))
		for instanceID := range schedules {
			args = append(args, instanceID)
		}
		_, err = tx.ExecContext(ctx, deleteQuery, args...)
		if err != nil {
			return fmt.Errorf("failed to delete old schedules: %w", err)
		}
	} else {
		// If no schedules provided, delete all
		_, err = tx.ExecContext(ctx, `DELETE FROM backup_schedules`)
		if err != nil {
			return fmt.Errorf("failed to delete all schedules: %w", err)
		}
	}

	// Upsert provided schedules
	query := `
	INSERT INTO backup_schedules (
		instance_id, schedule_cron,
		retention_keep_daily, retention_keep_weekly, retention_keep_monthly,
		targets,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(instance_id) DO UPDATE SET
		schedule_cron = excluded.schedule_cron,
		retention_keep_daily = excluded.retention_keep_daily,
		retention_keep_weekly = excluded.retention_keep_weekly,
		retention_keep_monthly = excluded.retention_keep_monthly,
		targets = excluded.targets,
		updated_at = excluded.updated_at
	`

	now := time.Now()
	for _, sched := range schedules {
		targetIDs := make([]string, 0, len(sched.Targets))
		for _, target := range sched.Targets {
			targetIDs = append(targetIDs, target.ID)
		}
		targetsStr := strings.Join(targetIDs, ",")

		_, err := tx.ExecContext(ctx, query,
			sched.InstanceID,
			sched.ScheduleCron,
			sched.Retention.KeepDaily,
			sched.Retention.KeepWeekly,
			sched.Retention.KeepMonthly,
			targetsStr,
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to upsert schedule for instance %s: %w", sched.InstanceID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (d *DB) GetAllSchedules(ctx context.Context) ([]*model.InstanceBackupScheduleView, error) {
	query := `
	SELECT instance_id, schedule_cron, next_run_at,
		retention_keep_daily, retention_keep_weekly, retention_keep_monthly, targets,
		created_at, updated_at
	FROM backup_schedules
	`

	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query backup schedules: %w", err)
	}
	defer rows.Close()

	schedules := make([]*model.InstanceBackupScheduleView, 0)
	for rows.Next() {
		schedule := &model.InstanceBackupScheduleView{}
		var retention model.Retention
		var targetsCSV string
		err := rows.Scan(
			&schedule.InstanceID,
			&schedule.ScheduleCron,
			&schedule.NextRunAt,
			&retention.KeepDaily,
			&retention.KeepWeekly,
			&retention.KeepMonthly,
			&targetsCSV,
			&schedule.CreatedAt,
			&schedule.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan backup schedule: %w", err)
		}
		if targetsCSV == "" {
			schedule.TargetIDs = []string{}
		} else {
			parts := strings.Split(targetsCSV, ",")
			schedule.TargetIDs = make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					schedule.TargetIDs = append(schedule.TargetIDs, p)
				}
			}
		}
		schedule.Retention = retention
		schedules = append(schedules, schedule)
	}

	return schedules, rows.Err()
}

func (d *DB) ScheduleNewJob(ctx context.Context, instanceID string) (*model.JobStatus, error) {
	query := `
	INSERT INTO job_status (
		instance_id, iid, is_active, status,
		last_started_at, last_completed_at,
		last_targets_successful, last_targets_total,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	// iid is next available integer ID for the instance
	var iid int
	err := d.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(iid), 0) + 1 FROM job_status WHERE instance_id = ?", instanceID).Scan(&iid)
	if err != nil {
		return nil, fmt.Errorf("failed to get next iid: %w", err)
	}

	result, err := d.db.ExecContext(ctx, query, instanceID, iid, 1, model.StatusScheduled, nil, nil, 0, 0, time.Now(), time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to start new job: %w", err)
	}

	jobID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return d.GetJobByID(ctx, int(jobID))
}

// UpdateJobStatus updates a job status record
func (d *DB) UpdateJobStatus(ctx context.Context, status *model.JobStatus) error {
	now := time.Now()
	status.UpdatedAt = now

	query := `
	UPDATE job_status SET
		status = ?,
		last_started_at = ?,
		last_completed_at = ?,
		last_targets_successful = ?,
		last_targets_total = ?,
		updated_at = ?
	WHERE id = ?
	`

	_, err := d.db.ExecContext(ctx, query,
		status.Status,
		status.LastStartedAt,
		status.LastCompletedAt,
		status.LastTargetsSuccessful,
		status.LastTargetsTotal,
		status.UpdatedAt,
		status.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert job status: %w", err)
	}

	return nil
}

// GetJobStatus retrieves all job statuses for a given instance ID
func (d *DB) GetJobStatus(ctx context.Context, instanceID string) ([]*model.JobStatus, error) {
	query := `
	SELECT id, iid, instance_id, is_active, status,
		last_started_at, last_completed_at,
		last_targets_successful, last_targets_total,
		created_at, updated_at
	FROM job_status
	WHERE instance_id = ?
	ORDER BY id DESC
	`

	rows, err := d.db.QueryContext(ctx, query, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query job statuses: %w", err)
	}
	defer rows.Close()

	// Initialize as empty slice so JSON encodes as [] instead of null
	statuses := make([]*model.JobStatus, 0)
	for rows.Next() {
		status := &model.JobStatus{}
		err := rows.Scan(
			&status.ID, &status.IID,
			&status.InstanceID, &status.IsActive, &status.Status,
			&status.LastStartedAt, &status.LastCompletedAt,
			&status.LastTargetsSuccessful, &status.LastTargetsTotal,
			&status.CreatedAt, &status.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job status: %w", err)
		}
		statuses = append(statuses, status)
	}

	return statuses, rows.Err()
}

// GetJobByID retrieves a job status by its ID
func (d *DB) GetJobByID(ctx context.Context, jobID int) (*model.JobStatus, error) {
	query := `
	SELECT id, iid, instance_id, is_active, status,
		last_started_at, last_completed_at,
		last_targets_successful, last_targets_total,
		created_at, updated_at
	FROM job_status
	WHERE id = ?
	`

	row := d.db.QueryRowContext(ctx, query, jobID)

	status := &model.JobStatus{}
	err := row.Scan(
		&status.ID, &status.IID,
		&status.InstanceID, &status.IsActive, &status.Status,
		&status.LastStartedAt, &status.LastCompletedAt,
		&status.LastTargetsSuccessful, &status.LastTargetsTotal,
		&status.CreatedAt, &status.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan job status: %w", err)
	}

	return status, nil
}

func (d *DB) ArchiveInstance(ctx context.Context, inactiveInstanceID string) error {
	_, err := d.db.ExecContext(ctx, "UPDATE job_status SET is_active = 0 WHERE instance_id = ?", inactiveInstanceID)
	if err != nil {
		return fmt.Errorf("failed to mark instance inactive: %w", err)
	}
	return nil
}

func (d *DB) ArchiveOldInstances(ctx context.Context, activeInstanceIDs []string) error {
	query := `
		UPDATE job_status
		SET is_active = 0
		WHERE instance_id NOT IN (` + placeholders(len(activeInstanceIDs)) + `)
	`
	args := make([]any, len(activeInstanceIDs))
	for i, id := range activeInstanceIDs {
		args[i] = id
	}

	_, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to mark inactive instances: %w", err)
	}
	return nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ",?"
	}
	return s
}
