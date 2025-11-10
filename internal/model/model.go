package model

import (
	"time"
)

type TargetType string

const (
	TargetVolume TargetType = "volume"
	TargetDB     TargetType = "db"
)

type InstanceID string

// BackupTarget represents a single volume or database to back up
type BackupTarget struct {
	ID         string     // stable identifier; for volume: "volume:<name>", for DB container: "container:<id>"
	Name       string     // human label (volume name or container name)
	Type       TargetType // volume|db
	InstanceID InstanceID // e.g. "hetzner-s3"
	Retention  Retention
	Exclude    []string
	Tags       []string
	PreHook    string // command inside app/DB container (optional)
	PostHook   string
	// Volume specifics
	VolumeName   string
	Paths        []string // default ["/"]
	AttachedCtrs []string // containers using the volume (for hooks)
	StopAttached bool     // whether to stop attached containers during backup
	// DB specifics
	DBKind      string // "postgres", "mysql", ...
	ContainerID string // DB container to exec dump in
	DumpArgs    []string
}

// InstanceBackupSchedule represents all targets that should be backed up together for an instance
type InstanceBackupSchedule struct {
	InstanceID InstanceID
	Schedule   string // cron schedule from config
	Targets    []BackupTarget
	Retention  Retention // Common retention policy (from first target or config default)
}

type Retention struct {
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
}

type JobState string

const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobSuccess JobState = "success"
	JobFailed  JobState = "failed"
)

type BackupJob struct {
	Target     BackupTarget
	EnqueuedAt time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	State      JobState
	Error      string
	SnapshotID string // restic snapshot id, if known
	BytesAdded int64
	FilesNew   int64
}

// JobStatusState represents the current status of a backup job
type JobStatusState string

const (
	StatusInProgress     JobStatusState = "in_progress"
	StatusSuccess        JobStatusState = "success"
	StatusPartialSuccess JobStatusState = "partial_success" // completed with warnings
	StatusFailed         JobStatusState = "failed"          // hard error
	StatusScheduled      JobStatusState = "scheduled"       // scheduled but not yet executed
	StatusAborted        JobStatusState = "aborted"         // interrupted by restart/shutdown
)

// JobStatus represents the persistent status of a backup target
// Used for API/dashboard display
type JobStatus struct {
	ID                    int            // global unique ID
	IID                   int            // instance unique ID
	InstanceID            InstanceID     // destination instance
	IsActive              bool           // whether the instance is active (= in the config)
	Status                JobStatusState // current status
	LastStartedAt         *time.Time     // when last backup started (nil if never run)
	LastCompletedAt       *time.Time     // when last backup completed (nil if never completed)
	LastTargetsSuccessful int            // number of successfully backed up targets in last run
	LastTargetsTotal      int            // total number of targets in last run
	NextRunAt             *time.Time     // next scheduled run (nil if not scheduled)
	CreatedAt             time.Time      // when this job was first discovered
	UpdatedAt             time.Time      // last status update
}
