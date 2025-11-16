export type JobStatusState =
  | "in_progress"
  | "success"
  | "partial_success"
  | "failed"
  | "scheduled"
  | "aborted";

export interface Retention {
  keepDaily: number;
  keepWeekly: number;
  keepMonthly: number;
}

export interface InstanceBackupSchedule {
  instanceId: string;
  nodeName?: string; // Optional node name for mesh mode
  scheduleCron: string;
  nextRunAt: string | null;
  targetIds: string[];
  retention: Retention;
  createdAt: string;
  updatedAt: string;
  latestJobStatus?: JobStatusState;
  latestJobCompletedAt?: string | null;
}

export interface JobStatus {
  id: number;
  iid: number;
  instanceId: string;
  nodeName?: string; // Name of the node (for mesh mode)
  nodeUrl?: string; // URL of the node (for mesh mode, used to fetch logs)
  isActive: boolean;
  status: JobStatusState;
  lastStartedAt: string | null;
  lastCompletedAt: string | null;
  lastTargetsSuccessful: number;
  lastTargetsTotal: number;
  createdAt: string;
  updatedAt: string;
}

export type LogLevel = "INFO" | "WARN" | "ERROR" | "DEBUG";

export interface LogEntry {
  id: number;
  timestamp: string;
  level: LogLevel;
  message: string;
  instanceId: string | null;
  targetId: string | null;
  jobStatusId: number | null;
  jobStatusIid: number | null;
}

export interface SystemLogEntry {
  id: string;
  timestamp: string;
  level: LogLevel;
  message: string;
  nodeName: string;
}

export interface HealthResponse {
  status: string;
  time: string;
}
