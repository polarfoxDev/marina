package model

import (
	"time"
)

type TargetType string

const (
	TargetVolume TargetType = "volume"
	TargetDB     TargetType = "db"
)

type DestinationID string

// What we back up (one unit of work)
type BackupTarget struct {
	ID          string        // stable identifier; for volume: "volume:<name>", for DB container: "container:<id>"
	Name        string        // human label (volume name or container name)
	Type        TargetType    // volume|db
	Schedule    string        // cron "0 3 * * *"
	Destination DestinationID // e.g. "hetzner-s3"
	Retention   Retention
	Exclude     []string
	Tags        []string
	PreHook     string // command inside app/DB container (optional)
	PostHook    string
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
