import type {
  HealthResponse,
  InstanceBackupSchedule,
  JobStatus,
  LogEntry,
} from "./types";

// Use relative /api path - works with reverse proxies since they forward requests
// to the same origin where the frontend is served
const API_BASE = "/api";

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

async function fetchJson<T>(url: string): Promise<T> {
  const response = await fetch(url);

  if (!response.ok) {
    const text = await response.text();
    throw new ApiError(response.status, text || response.statusText);
  }

  return response.json();
}

export const api = {
  async health(): Promise<HealthResponse> {
    return fetchJson<HealthResponse>(`${API_BASE}/health`);
  },

  async getSchedules(): Promise<InstanceBackupSchedule[]> {
    return fetchJson<InstanceBackupSchedule[]>(`${API_BASE}/schedules/`);
  },

  async getJobStatus(instanceID: string): Promise<JobStatus[]> {
    return fetchJson<JobStatus[]>(
      `${API_BASE}/status/${encodeURIComponent(instanceID)}`
    );
  },

  async getJobLogs(jobID: number, limit = 1000): Promise<LogEntry[]> {
    return fetchJson<LogEntry[]>(
      `${API_BASE}/logs/job/${jobID}?limit=${limit}`
    );
  },
};
