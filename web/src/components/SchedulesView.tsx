import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { InstanceBackupSchedule } from "../types";
import {
  formatDate,
  formatRelativeTime,
  getStatusColor,
  getStatusLabel,
} from "../utils";
import { TargetBadge } from "./TargetBadge";

export function SchedulesView() {
  const [schedules, setSchedules] = useState<InstanceBackupSchedule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    loadSchedules();
    const interval = setInterval(loadSchedules, 30000); // Refresh every 30s
    return () => clearInterval(interval);
  }, []);

  async function loadSchedules() {
    try {
      const data = await api.getSchedules();
      setSchedules(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load schedules");
    } finally {
      setLoading(false);
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-gray-500">Loading schedules...</div>
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

  return (
    <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
      <div className="mb-8">
        <h1 className="text-3xl font-bold text-gray-900">Backup Schedules</h1>
        <p className="mt-2 text-gray-600">
          Overview of all backup instances and their schedules
        </p>
      </div>

      {schedules.length === 0 ? (
        <div className="text-center py-12 bg-white rounded-lg shadow">
          <p className="text-gray-500">No backup schedules configured</p>
        </div>
      ) : (
        <div className="grid gap-6 md:grid-cols-2 lg:grid-cols-3">
          {schedules.map((schedule) => (
            <Link
              key={schedule.instanceId}
              to={`/instance/${schedule.instanceId}`}
              className="block bg-white rounded-lg shadow hover:shadow-lg transition-shadow"
            >
              <div className="p-6">
                <div className="flex items-center justify-between mb-2">
                  <h2 className="text-xl font-semibold text-gray-900">
                    {schedule.instanceId}
                  </h2>
                  <span
                    className={`inline-flex px-2 py-1 text-xs font-semibold rounded-full ${
                      schedule.latestJobStatus
                        ? getStatusColor(schedule.latestJobStatus)
                        : "text-gray-700 bg-gray-100"
                    }`}
                  >
                    {schedule.latestJobStatus
                      ? getStatusLabel(schedule.latestJobStatus)
                      : "Scheduled"}
                  </span>
                </div>

                <div className="space-y-3 text-sm">
                  <div>
                    <span className="text-gray-500">Schedule:</span>
                    <span className="ml-2 font-mono text-gray-900">
                      {schedule.scheduleCron}
                    </span>
                  </div>

                  <div>
                    <span className="text-gray-500">Next run:</span>
                    <span className="ml-2 text-gray-900">
                      {schedule.nextRunAt ? (
                        <>
                          <span className="block">
                            {formatRelativeTime(schedule.nextRunAt)}
                          </span>
                          <span className="text-xs text-gray-400">
                            {formatDate(schedule.nextRunAt)}
                          </span>
                        </>
                      ) : (
                        "Not scheduled"
                      )}
                    </span>
                  </div>

                  <div>
                    <span className="text-gray-500">Targets:</span>
                    <span className="ml-2 text-gray-900">
                      {schedule.targetIds.length}
                    </span>
                  </div>

                  {schedule.latestJobCompletedAt && (
                    <div>
                      <span className="text-gray-500">Last backup:</span>
                      <span className="ml-2 text-gray-900">
                        {formatRelativeTime(schedule.latestJobCompletedAt)}
                      </span>
                    </div>
                  )}

                  <div>
                    <span className="text-gray-500">Retention:</span>
                    <div className="ml-2 mt-1 text-gray-900">
                      <div className="text-xs space-y-0.5">
                        <div>Daily: {schedule.retention.keepDaily}</div>
                        <div>Weekly: {schedule.retention.keepWeekly}</div>
                        <div>Monthly: {schedule.retention.keepMonthly}</div>
                      </div>
                    </div>
                  </div>
                </div>

                {schedule.targetIds.length > 0 && (
                  <div className="mt-4 pt-4 border-t border-gray-200">
                    <div className="text-xs text-gray-500 mb-2">Targets:</div>
                    <div className="flex flex-wrap gap-1">
                      {schedule.targetIds.slice(0, 4).map((targetId) => (
                        <TargetBadge key={targetId} targetId={targetId} />
                      ))}
                      {schedule.targetIds.length > 4 && (
                        <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700">
                          +{schedule.targetIds.length - 4} more
                        </span>
                      )}
                    </div>
                  </div>
                )}
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
