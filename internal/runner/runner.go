package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/robfig/cron/v3"

	"github.com/polarfoxDev/marina/internal/backend"
	"github.com/polarfoxDev/marina/internal/database"
	"github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/model"
)

type cleanupFunc func()

type Runner struct {
	Cron            *cron.Cron
	BackupInstances map[model.InstanceID]backend.Backend // keyed by destination ID
	Docker          *client.Client
	Logger          *logging.Logger
	DB              *database.DB // Database for persistent job status tracking
	HostBackupPath  string       // Actual host path where /backup is mounted from

	// Track scheduled jobs for dynamic updates
	scheduledJobs map[model.InstanceID]cron.EntryID                 // instance ID -> cron entry ID
	jobs          map[model.InstanceID]model.InstanceBackupSchedule // instance ID -> backup job config
}

func New(instances map[model.InstanceID]backend.Backend, docker *client.Client, logger *logging.Logger, db *database.DB, hostBackupPath string) *Runner {
	return &Runner{
		Cron:            cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow))),
		BackupInstances: instances,
		Docker:          docker,
		Logger:          logger,
		DB:              db,
		HostBackupPath:  hostBackupPath,
		scheduledJobs:   make(map[model.InstanceID]cron.EntryID),
		jobs:            make(map[model.InstanceID]model.InstanceBackupSchedule),
	}
}

func (r *Runner) ScheduleBackup(backupSchedule model.InstanceBackupSchedule) error {
	// Check if already scheduled
	if existingEntry, ok := r.scheduledJobs[backupSchedule.InstanceID]; ok {
		// Check if schedule or config changed
		if existing, found := r.jobs[backupSchedule.InstanceID]; found {
			if existing.ScheduleCron == backupSchedule.ScheduleCron && jobsEqual(existing, backupSchedule) {
				// No changes, skip
				return nil
			}
		}
		// Remove old entry
		r.Cron.Remove(existingEntry)
		delete(r.scheduledJobs, backupSchedule.InstanceID)
	}

	// Schedule new job
	entryID, err := r.Cron.AddFunc(backupSchedule.ScheduleCron, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
		defer cancel()

		// Create job status first to get IDs for logger
		var jobStatusID, jobStatusIID int
		if r.DB != nil {
			jobStatus, err := r.DB.ScheduleNewJob(ctx, string(backupSchedule.InstanceID))
			if err != nil {
				r.Logger.Error("failed to create job status: %v", err)
				return
			}
			jobStatusID = jobStatus.ID
			jobStatusIID = jobStatus.IID
		}

		// Create instance-level logger with job status IDs
		instanceLogger := r.Logger.NewJobLogger(string(backupSchedule.InstanceID), jobStatusID, jobStatusIID)

		instanceLogger.Info("instance backup started (%d targets)", len(backupSchedule.Targets))
		startTime := time.Now()

		if err := r.runInstanceBackup(ctx, backupSchedule, jobStatusID, instanceLogger); err != nil {
			instanceLogger.Error("instance backup failed: %v", err)
		} else {
			duration := time.Since(startTime)
			instanceLogger.Info("instance backup completed (duration: %v)", duration)
		}
	})
	if err != nil {
		return err
	}

	r.scheduledJobs[backupSchedule.InstanceID] = entryID
	r.jobs[backupSchedule.InstanceID] = backupSchedule

	nextRunTime := r.getNextRunTime(backupSchedule.InstanceID)
	// Update next run time in DB
	if r.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.DB.UpdateNextRunTime(ctx, string(backupSchedule.InstanceID), nextRunTime); err != nil {
			r.Logger.Warn("failed to update next run time for instance %s: %v", backupSchedule.InstanceID, err)
		}
	}

	return nil
}

// RemoveJob removes a scheduled backup job for an instance
func (r *Runner) RemoveJob(instanceID model.InstanceID) {
	if entryID, ok := r.scheduledJobs[instanceID]; ok {
		r.Cron.Remove(entryID)
		delete(r.scheduledJobs, instanceID)

		if r.DB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.DB.ArchiveInstance(ctx, string(instanceID)); err != nil {
				r.Logger.Warn("failed to archive job status for instance %s: %v", instanceID, err)
			}
		}

		delete(r.jobs, instanceID)
	}
}

// SyncBackups updates the scheduler with a new set of discovered backups
// Adds new backups, removes deleted ones, and updates changed ones
func (r *Runner) SyncBackups(newBackups []model.InstanceBackupSchedule) {
	r.Logger.Info("syncing %d discovered instance backups...", len(newBackups))
	newSet := make(map[model.InstanceID]model.InstanceBackupSchedule)
	for _, j := range newBackups {
		newSet[j.InstanceID] = j
	}

	if r.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.DB.AddOrUpdateSchedules(ctx, newSet); err != nil {
			r.Logger.Warn("failed to update schedules: %v", err)
		}
	}

	// Remove jobs that no longer exist
	for id := range r.jobs {
		if _, exists := newSet[id]; !exists {
			r.Logger.Info("removing instance job %s (no longer exists)", id)
			r.RemoveJob(id)
		}
	}

	// Add or update jobs
	for id, job := range newSet {
		// Validate instance exists
		if _, ok := r.BackupInstances[job.InstanceID]; !ok {
			r.Logger.Warn("instance job %s references unknown instance, skipping", id)
			continue
		}

		// Check if it's new or changed BEFORE scheduling
		existing, found := r.jobs[id]
		isNew := !found
		isChanged := found && !jobsEqual(existing, job)

		if err := r.ScheduleBackup(job); err != nil {
			r.Logger.Error("schedule instance %s: %v", id, err)
		} else {
			// Only log if it's new or changed
			if isNew || isChanged {
				r.Logger.Info("scheduled instance %s (%d targets, schedule: %s)", id, len(job.Targets), job.ScheduleCron)
			}
		}
	}
}

// jobsEqual checks if two instance backup jobs are functionally equivalent
func jobsEqual(a, b model.InstanceBackupSchedule) bool {
	if a.ScheduleCron != b.ScheduleCron || len(a.Targets) != len(b.Targets) {
		return false
	}

	// Compare targets (simplified - just check IDs and key fields)
	aIDs := make(map[string]bool)
	for _, t := range a.Targets {
		aIDs[t.ID] = true
	}
	for _, t := range b.Targets {
		if !aIDs[t.ID] {
			return false
		}
	}

	return true
}
func (r *Runner) Start()                   { r.Cron.Start() }
func (r *Runner) Stop(ctx context.Context) { r.Cron.Stop() }

func (r *Runner) TriggerNow(ctx context.Context, job model.InstanceBackupSchedule) error {
	// Get or create job status to get IDs for logger
	var jobStatusID, jobStatusIID int
	if r.DB != nil {
		jobStatus, err := r.DB.ScheduleNewJob(ctx, string(job.InstanceID))
		if err != nil {
			return fmt.Errorf("failed to create job status: %w", err)
		}
		jobStatusID = jobStatus.ID
		jobStatusIID = jobStatus.IID
	}

	instanceLogger := r.Logger.NewJobLogger(string(job.InstanceID), jobStatusID, jobStatusIID)
	return r.runInstanceBackup(ctx, job, jobStatusID, instanceLogger)
}

// getNextRunTime retrieves the next scheduled run time for an instance from the cron entry
func (r *Runner) getNextRunTime(instanceID model.InstanceID) *time.Time {
	if entryID, ok := r.scheduledJobs[instanceID]; ok {
		entry := r.Cron.Entry(entryID)
		if !entry.Next.IsZero() {
			return &entry.Next
		}
	}
	return nil
}

// updateJobStatus is a helper to update job status in the database
func (r *Runner) updateJobStatus(ctx context.Context, jobStatusID int, updateFn func(*model.JobStatus)) error {
	if r.DB == nil {
		return nil
	}

	jobStatus, err := r.DB.GetJobByID(ctx, jobStatusID)
	if err != nil {
		return fmt.Errorf("failed to get job status: %w", err)
	}
	if jobStatus == nil {
		return fmt.Errorf("job status not found for ID %d", jobStatusID)
	}

	updateFn(jobStatus)

	if err := r.DB.UpdateJobStatus(ctx, jobStatus); err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	return nil
}

// runInstanceBackup executes all backups for an instance in a single Restic operation
func (r *Runner) runInstanceBackup(ctx context.Context, job model.InstanceBackupSchedule, jobStatusID int, instanceLogger *logging.JobLogger) error {
	dest, ok := r.BackupInstances[job.InstanceID]
	if !ok {
		return fmt.Errorf("instance %q not found", job.InstanceID)
	}

	nextRunTime := r.getNextRunTime(job.InstanceID)
	// Update next run time in DB (best effort - don't block backup on DB issues)
	if r.DB != nil {
		dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.DB.UpdateNextRunTime(dbCtx, string(job.InstanceID), nextRunTime); err != nil {
			r.Logger.Warn("failed to update next run time for instance %s: %v", job.InstanceID, err)
		}
	}

	startTime := time.Now()

	// Update job status to in-progress
	if err := r.updateJobStatus(ctx, jobStatusID, func(status *model.JobStatus) {
		status.Status = model.StatusInProgress
		status.LastStartedAt = &startTime
		status.LastTargetsTotal = len(job.Targets)
	}); err != nil {
		return err
	}

	// Use instance start time as timestamp for all staged paths
	timestamp := startTime.Format("20060102-150405")

	// Collect all staging paths from all targets
	var allPaths []string
	var allTags []string
	var allExcludes []string

	// Track cleanup functions to defer
	var cleanups []cleanupFunc
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	// Track failed targets
	var failedTargets []string

	// Process each target and collect staged paths
	for _, target := range job.Targets {
		// Create target-specific logger for detailed logs
		targetLogger := instanceLogger.WithTarget(target.ID)
		targetLogger.Info("preparing %s: %s", target.Type, target.Name)

		switch target.Type {
		case model.TargetVolume:
			paths, cleanup, err := r.prepareVolumeBackup(ctx, string(job.InstanceID), timestamp, target, targetLogger)
			if err != nil {
				targetLogger.Warn("failed to prepare volume: %v", err)
				failedTargets = append(failedTargets, fmt.Sprintf("volume:%s", target.Name))
				continue // Skip this target but continue with others
			}
			allPaths = append(allPaths, paths...)
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

		case model.TargetDB:
			path, cleanup, err := r.prepareDBBackup(ctx, string(job.InstanceID), timestamp, target, targetLogger)
			if err != nil {
				targetLogger.Warn("failed to prepare db: %v", err)
				failedTargets = append(failedTargets, fmt.Sprintf("db:%s", target.Name))
				continue // Skip this target but continue with others
			}
			allPaths = append(allPaths, path)
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

		default:
			targetLogger.Warn("unknown target type: %s", target.Type)
			failedTargets = append(failedTargets, fmt.Sprintf("unknown:%s", target.Name))
			continue
		}

		// Collect tags and excludes from all targets
		allTags = append(allTags, target.Tags...)
		allExcludes = append(allExcludes, target.Exclude...)
	}
	// Check if all targets failed
	if len(allPaths) == 0 {
		if err := r.updateJobStatus(ctx, jobStatusID, func(status *model.JobStatus) {
			status.Status = model.StatusFailed
			now := time.Now()
			status.LastCompletedAt = &now
			status.LastTargetsSuccessful = 0
		}); err != nil {
			r.Logger.Warn("failed to update job status: %v", err)
		}

		if len(failedTargets) > 0 {
			return fmt.Errorf("all targets failed: %v", failedTargets)
		}
		return fmt.Errorf("no paths to backup")
	}

	// Log warning if some targets failed
	if len(failedTargets) > 0 {
		instanceLogger.Warn("backup proceeding with %d/%d targets (%d failed: %v)",
			len(allPaths), len(job.Targets), len(failedTargets), failedTargets)
	}

	// Deduplicate tags and excludes
	allTags = deduplicate(allTags)
	allExcludes = deduplicate(allExcludes)

	// Perform single backup with all collected paths
	instanceLogger.Info("backing up %d paths to instance %s using backend %s: %s", len(allPaths), job.InstanceID, dest.GetType(), allPaths)
	if dest.GetType() == backend.BackendTypeCustomImage {
		instanceLogger.Debug("using custom image %s, log output will appear as soon as execution finished", dest.GetImage())
	}
	logs, err := dest.Backup(ctx, allPaths, allTags, allExcludes)
	instanceLogger.Debug("%s", logs)
	if err != nil {
		if updateErr := r.updateJobStatus(ctx, jobStatusID, func(status *model.JobStatus) {
			status.Status = model.StatusFailed
			now := time.Now()
			status.LastCompletedAt = &now
			status.LastTargetsSuccessful = 0
		}); updateErr != nil {
			r.Logger.Warn("failed to update job status: %v", updateErr)
		}
		return fmt.Errorf("backup failed: %w", err)
	}

	// Apply retention policy
	_, _ = dest.DeleteOldSnapshots(ctx, job.Retention.KeepDaily, job.Retention.KeepWeekly, job.Retention.KeepMonthly)

	// Update job status to success/partial success
	if err := r.updateJobStatus(ctx, jobStatusID, func(status *model.JobStatus) {
		status.Status = model.StatusSuccess
		if len(failedTargets) > 0 {
			status.Status = model.StatusPartialSuccess
		}
		now := time.Now()
		status.LastCompletedAt = &now
		status.LastTargetsSuccessful = len(job.Targets) - len(failedTargets)
	}); err != nil {
		r.Logger.Warn("failed to update job status: %v", err)
	}

	return nil
}

// prepareVolumeBackup prepares a volume for backup and returns the staged paths and cleanup function
func (r *Runner) prepareVolumeBackup(ctx context.Context, instanceID, timestamp string, target model.BackupTarget, jobLogger *logging.JobLogger) ([]string, cleanupFunc, error) {
	// Execute pre-hook in first attached container
	if target.PreHook != "" && len(target.AttachedCtrs) > 0 {
		jobLogger.Debug("executing pre-hook")
		output, err := docker.ExecInContainer(ctx, r.Docker, target.AttachedCtrs[0], []string{"/bin/sh", "-lc", target.PreHook})
		if err != nil {
			return nil, nil, fmt.Errorf("prehook: %w", err)
		}
		if output != "" {
			jobLogger.Debug("pre-hook output: %s", output)
		}
		// Defer post-hook
		defer func() {
			if target.PostHook != "" {
				jobLogger.Debug("executing post-hook")
				output, err := docker.ExecInContainer(ctx, r.Docker, target.AttachedCtrs[0], []string{"/bin/sh", "-lc", target.PostHook})
				if err != nil {
					jobLogger.Warn("post-hook failed: %v", err)
				} else if output != "" {
					jobLogger.Debug("post-hook output: %s", output)
				}
			}
		}()
	}

	// Stop attached containers if needed
	var stoppedContainers []string
	if target.StopAttached && len(target.AttachedCtrs) > 0 {
		for _, ctr := range target.AttachedCtrs {
			running, err := docker.IsContainerRunning(ctx, r.Docker, ctr)
			if err != nil {
				return nil, nil, fmt.Errorf("check container state: %w", err)
			}
			if !running {
				continue
			}

			// Skip if mounted read-only
			ctrInfo, err := r.Docker.ContainerInspect(ctx, ctr)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect container: %w", err)
			}
			if len(ctrInfo.Mounts) > 0 && ctrInfo.Mounts[0].Mode == "ro" {
				jobLogger.Info("container %s is mounted read-only, skipping stop", ctr)
				continue
			}

			jobLogger.Info("stopping container %s", ctr)
			if err := docker.StopContainer(ctx, r.Docker, ctr); err != nil {
				return nil, nil, fmt.Errorf("stop container: %w", err)
			}
			stoppedContainers = append(stoppedContainers, ctr)
		}
	}

	// Copy volume data to staging
	jobLogger.Info("copying volume %s to staging", target.VolumeName)
	stagedPaths, err := docker.CopyVolumeToStaging(ctx, r.Docker, r.HostBackupPath, instanceID, timestamp, target.VolumeName, target.Paths)
	if err != nil {
		// Restart stopped containers before returning error
		for _, ctr := range stoppedContainers {
			_ = docker.StartContainer(ctx, r.Docker, ctr)
		}
		return nil, nil, err
	}

	// Create cleanup function
	cleanup := func() {
		// Clean up staging directory
		if len(stagedPaths) > 0 {
			firstPath := stagedPaths[0]
			dir := firstPath
			for {
				parent := filepath.Dir(dir)
				if parent == "/backup" {
					_ = os.RemoveAll(dir)
					break
				}
				if parent == dir || parent == "/" {
					break
				}
				dir = parent
			}
		}

		// Restart stopped containers
		for _, ctr := range stoppedContainers {
			jobLogger.Info("restarting container %s", ctr)
			_ = docker.StartContainer(ctx, r.Docker, ctr)
		}
	}

	return stagedPaths, cleanup, nil
}

// prepareDBBackup prepares a database backup and returns the staged path and cleanup function
func (r *Runner) prepareDBBackup(ctx context.Context, instanceID, timestamp string, target model.BackupTarget, jobLogger *logging.JobLogger) (string, cleanupFunc, error) {
	// Execute pre-hook
	if target.PreHook != "" {
		jobLogger.Debug("executing pre-hook")
		output, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", target.PreHook})
		if err != nil {
			return "", nil, fmt.Errorf("prehook: %w", err)
		}
		if output != "" {
			jobLogger.Debug("pre-hook output: %s", output)
		}
		// Defer post-hook
		defer func() {
			if target.PostHook != "" {
				jobLogger.Debug("executing post-hook")
				output, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", target.PostHook})
				if err != nil {
					jobLogger.Warn("post-hook failed: %v", err)
				} else if output != "" {
					jobLogger.Debug("post-hook output: %s", output)
				}
			}
		}()
	}

	// Create dump inside DB container (use same timestamp as instance backup)
	containerDumpDir := fmt.Sprintf("/tmp/marina-%s", timestamp)
	mk := fmt.Sprintf("mkdir -p %q", containerDumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", mk}); err != nil {
		return "", nil, fmt.Errorf("prepare dump dir: %w", err)
	}

	// Prepare host staging directory
	hostStagingDir := filepath.Join("/backup", instanceID, timestamp, "dbs", target.Name)
	if err := os.MkdirAll(hostStagingDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare host staging: %w", err)
	}

	// Build and execute dump command
	dumpCmd, dumpFile, err := buildDumpCmd(target, containerDumpDir)
	if err != nil {
		return "", nil, err
	}

	jobLogger.Debug("executing dump command")
	output, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", dumpCmd})
	if err != nil {
		return "", nil, fmt.Errorf("dump failed: %w", err)
	}
	jobLogger.Debug("dump output: %s", output)

	// Copy dump file from container
	hostDumpPath, err := docker.CopyFileFromContainer(ctx, r.Docker, target.ContainerID, dumpFile, hostStagingDir, func(expected, written int64) {
		if expected > 0 && expected != written {
			jobLogger.Warn("copy warning: expected %d bytes, wrote %d", expected, written)
		}
	})
	if err != nil {
		return "", nil, err
	}

	// Create cleanup function
	cleanup := func() {
		// Clean up container dump directory
		_, _ = docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", fmt.Sprintf("rm -rf %q", containerDumpDir)})
		// Clean up host staging directory
		_ = os.RemoveAll(hostStagingDir)
	}

	return hostDumpPath, cleanup, nil
}

// deduplicate removes duplicate strings from a slice
func deduplicate(slice []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
func buildDumpCmd(t model.BackupTarget, dumpDir string) (cmd string, output string, err error) {
	switch t.DBKind {
	case "postgres":
		file := filepath.Join(dumpDir, "dump.sql")
		// Use pg_dumpall to dump all databases with postgres user
		// PGPASSWORD env var should be set in container
		args := stringsJoin(append([]string{"pg_dumpall", "-U", "postgres"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mysql":
		file := filepath.Join(dumpDir, "dump.sql")
		// Build dump command with automatic credential fallback
		// If no dump.args provided, try MYSQL_ROOT_PASSWORD, then MYSQL_PASSWORD
		if len(t.DumpArgs) == 0 {
			cmd := fmt.Sprintf(`
				mysqldump --single-transaction --all-databases -uroot -p"$MYSQL_ROOT_PASSWORD" > %q 2>/tmp/dump.err || \
				(echo "Root dump failed, trying MYSQL_USER..." >&2 && \
				 mysqldump --single-transaction --all-databases -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" > %q)
			`, file, file)
			return cmd, file, nil
		}
		// Use provided dump.args
		baseArgs := []string{"mysqldump", "--single-transaction", "--all-databases"}
		args := stringsJoin(append(baseArgs, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mariadb":
		file := filepath.Join(dumpDir, "dump.sql")
		// Build dump command with automatic credential fallback
		// If no dump.args provided, try MARIADB_ROOT_PASSWORD, then MARIADB_PASSWORD
		if len(t.DumpArgs) == 0 {
			cmd := fmt.Sprintf(`
				mariadb-dump --single-transaction --all-databases -uroot -p"$MARIADB_ROOT_PASSWORD" > %q 2>/tmp/dump.err || \
				(echo "Root dump failed, trying MARIADB_USER..." >&2 && \
				 mariadb-dump --single-transaction --all-databases -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" > %q)
			`, file, file)
			return cmd, file, nil
		}
		// Use provided dump.args
		baseArgs := []string{"mariadb-dump", "--single-transaction", "--all-databases"}
		args := stringsJoin(append(baseArgs, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mongo":
		file := filepath.Join(dumpDir, "dump.archive")
		args := stringsJoin(append([]string{"mongodump", "--archive"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	default:
		return "", "", fmt.Errorf("unsupported db kind %q", t.DBKind)
	}
}

func stringsJoin(ss ...string) string { return strings.Join(ss, " ") }
