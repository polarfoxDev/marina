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
    // If we get 401 Unauthorized, reload the page to trigger re-authentication
    // But only if this is an auth endpoint or we haven't reloaded recently
    if (response.status === 401) {
      // Check if we recently reloaded to prevent reload loops
      const lastReload = sessionStorage.getItem('lastAuthReload');
      const now = Date.now();
      
      if (!lastReload || now - parseInt(lastReload) > 5000) {
        // Only reload if it's been more than 5 seconds since last reload
        sessionStorage.setItem('lastAuthReload', now.toString());
        window.location.reload();
      }
      
      throw new ApiError(response.status, "Authentication required");
    }
    
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

  async getJobLogs(jobID: number, limit = 1000, nodeUrl?: string): Promise<LogEntry[]> {
    let url = `${API_BASE}/logs/job/${jobID}?limit=${limit}`;
    if (nodeUrl) {
      url += `&nodeUrl=${encodeURIComponent(nodeUrl)}`;
    }
    return fetchJson<LogEntry[]>(url);
  },
};
