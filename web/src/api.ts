import type {
  HealthResponse,
  InstanceBackupSchedule,
  JobStatus,
  LogEntry,
  SystemLogEntry,
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

async function fetchJson<T>(
  url: string,
  options: RequestInit = {}
): Promise<T> {
  // Add auth token from localStorage if available
  const token = localStorage.getItem("marina_auth_token");
  const headers = new Headers(options.headers || {});

  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  const response = await fetch(url, {
    ...options,
    headers,
  });

  if (!response.ok) {
    // If we get 401 Unauthorized, clear token and reload to trigger re-authentication
    if (response.status === 401) {
      // Clear the invalid token
      localStorage.removeItem("marina_auth_token");

      // Check if we recently reloaded to prevent reload loops
      const lastReload = sessionStorage.getItem("lastAuthReload");
      const now = Date.now();

      if (!lastReload || now - parseInt(lastReload) > 5000) {
        // Only reload if it's been more than 5 seconds since last reload
        sessionStorage.setItem("lastAuthReload", now.toString());
        window.location.reload();
      }

      throw new ApiError(response.status, "Authentication required");
    }

    const text = await response.text();
    throw new ApiError(response.status, text || response.statusText);
  }

  return response.json();
}

async function postJson<T>(url: string, body: unknown): Promise<T> {
  return fetchJson<T>(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
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

  async getJobLogs(
    jobID: number,
    limit = 1000,
    nodeUrl?: string
  ): Promise<LogEntry[]> {
    let url = `${API_BASE}/logs/job/${jobID}?limit=${limit}`;
    if (nodeUrl) {
      url += `&nodeUrl=${encodeURIComponent(nodeUrl)}`;
    }
    return fetchJson<LogEntry[]>(url);
  },

  async getSystemLogs(
    limit = 1000,
    level?: string
  ): Promise<SystemLogEntry[]> {
    let url = `${API_BASE}/logs/system?limit=${limit}`;
    if (level) {
      url += `&level=${encodeURIComponent(level)}`;
    }
    return fetchJson<SystemLogEntry[]>(url);
  },

  async login(password: string): Promise<{ token: string }> {
    const result = await postJson<{ token: string }>(`${API_BASE}/auth/login`, {
      password,
    });
    // Store token in localStorage
    localStorage.setItem("marina_auth_token", result.token);
    return result;
  },

  async logout(): Promise<void> {
    // Clear token from localStorage
    localStorage.removeItem("marina_auth_token");
    await postJson<void>(`${API_BASE}/auth/logout`, {});
  },
};
