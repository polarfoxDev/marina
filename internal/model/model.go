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
	InstanceID   InstanceID
	ScheduleCron string // cron schedule from config
	Targets      []BackupTarget
	Retention    Retention // Common retention policy (from first target or config default)
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type InstanceBackupScheduleView struct {
	InstanceID           InstanceID      `json:"instanceId"`
	ScheduleCron         string          `json:"scheduleCron"` // cron schedule from config
	NextRunAt            *time.Time      `json:"nextRunAt"`    // next scheduled run (nil if not scheduled)
	TargetIDs            []string        `json:"targetIds"`
	Retention            Retention       `json:"retention"` // Common retention policy (from first target or config default)
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
	LatestJobStatus      *JobStatusState `json:"latestJobStatus,omitempty"`      // status of most recent job
	LatestJobCompletedAt *time.Time      `json:"latestJobCompletedAt,omitempty"` // completion time of most recent job
}

type Retention struct {
	KeepDaily   int `json:"keepDaily"`
	KeepWeekly  int `json:"keepWeekly"`
	KeepMonthly int `json:"keepMonthly"`
}

type JobState string

const (
	JobQueued  JobState = "queued"
	JobRunning JobState = "running"
	JobSuccess JobState = "success"
	JobFailed  JobState = "failed"
)

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
	ID                    int            `json:"id"`                    // global unique ID
	IID                   int            `json:"iid"`                   // instance unique ID
	InstanceID            InstanceID     `json:"instanceId"`            // destination instance
	IsActive              bool           `json:"isActive"`              // whether the instance is active (= in the config)
	Status                JobStatusState `json:"status"`                // current status
	LastStartedAt         *time.Time     `json:"lastStartedAt"`         // when last backup started (nil if never run)
	LastCompletedAt       *time.Time     `json:"lastCompletedAt"`       // when last backup completed (nil if never completed)
	LastTargetsSuccessful int            `json:"lastTargetsSuccessful"` // number of successfully backed up targets in last run
	LastTargetsTotal      int            `json:"lastTargetsTotal"`      // total number of targets in last run
	CreatedAt             time.Time      `json:"createdAt"`             // when this job was first discovered
	UpdatedAt             time.Time      `json:"updatedAt"`             // last status update
}
