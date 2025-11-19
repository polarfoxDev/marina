package runner

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/docker/docker/client"
	"github.com/robfig/cron/v3"

	"github.com/polarfoxDev/marina/internal/backend"
	"github.com/polarfoxDev/marina/internal/database"
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

	var allPaths []string
	var allTags []string

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
		targetLogger.Info("staging %s: %s", target.Type, target.Name)

		switch target.Type {
		case model.TargetVolume:
			paths, cleanup, err := r.stageVolume(ctx, string(job.InstanceID), timestamp, target, targetLogger)
			if err != nil {
				targetLogger.Warn("failed to stage volume: %v", err)
				failedTargets = append(failedTargets, fmt.Sprintf("volume:%s", target.Name))
				continue // Skip this target but continue with others
			}
			targetLogger.Info("volume staged successfully (%d paths)", len(paths))
			allPaths = append(allPaths, paths...)
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

		case model.TargetDB:
			path, cleanup, err := r.stageDatabase(ctx, string(job.InstanceID), timestamp, target, targetLogger)
			if err != nil {
				targetLogger.Warn("failed to stage database: %v", err)
				failedTargets = append(failedTargets, fmt.Sprintf("db:%s", target.Name))
				continue // Skip this target but continue with others
			}
			targetLogger.Info("database dump completed successfully")
			allPaths = append(allPaths, path)
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

		default:
			targetLogger.Warn("unknown target type: %s", target.Type)
			failedTargets = append(failedTargets, fmt.Sprintf("%s:%s", target.Type, target.Name))
			continue
		}

		// Collect tags from all targets
		allTags = append(allTags, fmt.Sprintf("%s:%s", target.Type, target.Name))
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
		// filter out tags of failed targets
		filteredTags := []string{}
		for _, tag := range allTags {
			failed := slices.Contains(failedTargets, tag)
			if !failed {
				filteredTags = append(filteredTags, tag)
			}
		}
		allTags = filteredTags
	}

	allTags = deduplicate(allTags)

	// Perform single backup with all collected paths
	instanceLogger.Info("backing up %d paths to instance %s using backend %s: %s", len(allPaths), job.InstanceID, dest.GetType(), allPaths)
	instanceLogger.Debug("backend timeout: %s", dest.GetResticTimeout())
	
	// For custom image backends, set the logger for streaming output
	if dest.GetType() == backend.BackendTypeCustomImage {
		instanceLogger.Info("using custom image %s, streaming logs in real-time", dest.GetImage())
		// Type assert to CustomImageBackend to set the logger
		if customBackend, ok := dest.(*backend.CustomImageBackend); ok {
			customBackend.SetLogger(instanceLogger)
		}
	}
	
	logs, err := dest.Backup(ctx, allPaths, allTags)
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
