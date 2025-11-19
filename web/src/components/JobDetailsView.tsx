import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api";
import type { JobStatus, JobStatusState, LogEntry, LogLevel } from "../types";
import {
  formatDate,
  getLogLevelColor,
  getStatusColor,
  getStatusLabel,
  shouldIncludeLogLevel,
} from "../utils";
import { TargetBadge } from "./TargetBadge";
import { formatTargetName } from "../utils";

export function JobDetailsView() {
  const { instanceId, jobId } = useParams<{
    instanceId: string;
    jobId: string;
  }>();
  const [job, setJob] = useState<JobStatus | null>(null);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [filteredLogs, setFilteredLogs] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Filters
  const [targetFilter, setTargetFilter] = useState<string>("all");
  const [levelFilter, setLevelFilter] = useState<LogLevel>("INFO");

  const loadJobStatus = useCallback(async () => {
    if (!jobId || !instanceId) return;

    try {
      const numericJobId = parseInt(jobId, 10);
      const statuses = await api.getJobStatus(instanceId);
      const foundJob = statuses.find((j) => j.id === numericJobId);
      if (foundJob) {
        setJob(foundJob);
      }
      setError(null);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load job status"
      );
    } finally {
      setLoading(false);
    }
  }, [jobId, instanceId]);

  const loadLogs = useCallback(
    async (nodeUrl?: string) => {
      if (!jobId) return;

      try {
        const numericJobId = parseInt(jobId, 10);
        // Pass nodeUrl if the job is from a remote node
        const logsData = await api.getJobLogs(numericJobId, 5000, nodeUrl);
        setLogs(logsData);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load logs");
      }
    },
    [jobId]
  );

  const applyFilters = useCallback(() => {
    let filtered = [...logs];

    if (targetFilter !== "all") {
      filtered = filtered.filter((log) => log.targetId === targetFilter);
    }

    filtered = filtered.filter((log) =>
      shouldIncludeLogLevel(log.level, levelFilter)
    );

    setFilteredLogs(filtered);
  }, [logs, targetFilter, levelFilter]);

  // Load job status and logs together initially and when status changes
  // Initial load: only when jobId changes
  useEffect(() => {
    if (jobId) {
      loadJobStatus();
    }
  }, [jobId, loadJobStatus]);

  // Load logs whenever jobId or nodeUrl changes (nodeUrl may be empty/undefined)
  useEffect(() => {
    if (!jobId) return;
    loadLogs(job?.nodeUrl || undefined);
  }, [jobId, job?.nodeUrl, loadLogs]);

  // Track previous status to detect transitions
  const [prevStatus, setPrevStatus] = useState<JobStatusState | null>(null);

  // Poll job status every 5 seconds when job is in progress
  useEffect(() => {
    if (!jobId || job?.status !== "in_progress") return;

    const statusInterval = setInterval(() => {
      loadJobStatus();
    }, 5000);

    return () => clearInterval(statusInterval);
  }, [jobId, job?.status, loadJobStatus]);

  // Detect status transitions from "in_progress" to finished state
  // and load logs one final time to capture any remaining log entries
  useEffect(() => {
    if (!job) return;

    // If previous status was "in_progress" and current status is not
    if (prevStatus === "in_progress" && job.status !== "in_progress") {
      // Load logs one final time after the job completes
      loadLogs(job.nodeUrl || undefined);
    }

    // Update previous status
    setPrevStatus(job.status);
  }, [job, job?.status, job?.nodeUrl, prevStatus, loadLogs]);

  // Separate effect for log polling when job is in progress
  useEffect(() => {
    if (!jobId || !job || job.status !== "in_progress") return;

    // Poll logs every 1 second when job is in progress
    const logsInterval = setInterval(() => {
      loadLogs(job.nodeUrl || undefined);
    }, 1000);

    return () => clearInterval(logsInterval);
  }, [jobId, job, job?.status, job?.nodeUrl, loadLogs]);

  useEffect(() => {
    applyFilters();
  }, [applyFilters]);

  // Get unique target IDs from logs
  const targetIds = Array.from(
    new Set(logs.map((log) => log.targetId).filter(Boolean))
  ) as string[];

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-gray-500">Loading job details...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-red-600">Error: {error}</div>
      </div>
    );
  }

  if (!job) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-gray-500">Job not found</div>
      </div>
    );
  }

  return (
    <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
      <div className="mb-8">
        <Link
          to={`/instance/${instanceId}`}
          className="text-blue-600 hover:text-blue-800 mb-4 inline-block"
        >
          ← Back to Jobs
        </Link>
        <h1 className="text-3xl font-bold text-gray-900">
          Job #{job.iid} - {instanceId}
        </h1>
      </div>

      {/* Job Status Card */}
      <div className="bg-white shadow rounded-lg p-6 mb-6">
        <h2 className="text-xl font-semibold text-gray-900 mb-4">Job Status</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          <div>
            <div className="text-sm text-gray-500">Status</div>
            <span
              className={`inline-flex px-2 py-1 text-xs font-semibold rounded-full ${getStatusColor(
                job.status
              )} mt-1`}
            >
              {getStatusLabel(job.status)}
            </span>
          </div>
          <div>
            <div className="text-sm text-gray-500">Targets</div>
            <div className="text-lg font-medium text-gray-900">
              {job.lastTargetsSuccessful} / {job.lastTargetsTotal}
            </div>
          </div>
          <div>
            <div className="text-sm text-gray-500">Started</div>
            <div className="text-sm font-medium text-gray-900">
              {formatDate(job.lastStartedAt)}
            </div>
          </div>
          <div>
            <div className="text-sm text-gray-500">Completed</div>
            <div className="text-sm font-medium text-gray-900">
              {formatDate(job.lastCompletedAt)}
            </div>
          </div>
        </div>
      </div>

      {/* Filters */}
      <div className="bg-white shadow rounded-lg p-6 mb-6">
        <h2 className="text-xl font-semibold text-gray-900 mb-4">
          Filter Logs
        </h2>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-2">
              Target
            </label>
            <select
              value={targetFilter}
              onChange={(e) => setTargetFilter(e.target.value)}
              className="block w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
            >
              <option value="all">All Targets</option>
              {targetIds.map((targetId) => (
                <option key={targetId} value={targetId}>
                  {formatTargetName(targetId)}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-2">
              Log Level
            </label>
            <select
              value={levelFilter}
              onChange={(e) => setLevelFilter(e.target.value as LogLevel)}
              className="block w-full border-gray-300 rounded-md shadow-sm focus:ring-blue-500 focus:border-blue-500"
            >
              <option value="DEBUG">DEBUG</option>
              <option value="INFO">INFO</option>
              <option value="WARN">WARN</option>
              <option value="ERROR">ERROR</option>
            </select>
          </div>
        </div>
        <div className="mt-4 text-sm text-gray-600">
          Showing {filteredLogs.length} of {logs.length} log entries
        </div>
      </div>

      {/* Logs */}
      <div className="bg-white shadow rounded-lg overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-200">
          <h2 className="text-xl font-semibold text-gray-900">Logs</h2>
        </div>

        {filteredLogs.length === 0 ? (
          <div className="text-center py-12">
            <p className="text-gray-500">No logs found with current filters</p>
          </div>
        ) : (
          <div className="overflow-auto max-h-[600px]">
            <table className="min-w-full divide-y divide-gray-200">
              <thead className="bg-gray-50 sticky top-0">
                <tr>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                    Timestamp
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                    Level
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                    Target
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                    Message
                  </th>
                </tr>
              </thead>
              <tbody className="bg-white divide-y divide-gray-200">
                {filteredLogs.map((log) => (
                  <tr key={log.id} className="hover:bg-gray-50">
                    <td className="px-6 py-4 whitespace-nowrap text-xs text-gray-500">
                      {formatDate(log.timestamp)}
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap">
                      <span
                        className={`inline-flex px-2 py-1 text-xs font-semibold rounded ${getLogLevelColor(
                          log.level
                        )}`}
                      >
                        {log.level}
                      </span>
                    </td>
                    <td className="px-6 py-4 whitespace-nowrap text-xs">
                      {log.targetId ? (
                        <TargetBadge targetId={log.targetId} />
                      ) : (
                        <span className="text-gray-500">-</span>
                      )}
                    </td>
                    <td className="px-6 py-4 text-sm text-gray-900">
                      <div className="break-all max-w-3xl">
                        {log.message.split("\n").map((line, i) => (
                          <div key={i}>{line || "\u00A0"}</div>
                        ))}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
