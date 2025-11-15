import type { JobStatusState, LogLevel } from "./types";

export function formatDate(dateString: string | null): string {
  if (!dateString) return "Never";
  const date = new Date(dateString);
  return date.toLocaleString();
}

export function formatRelativeTime(dateString: string | null): string {
  if (!dateString) return "Never";
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins} minute${diffMins > 1 ? "s" : ""} ago`;
  if (diffHours < 24) return `${diffHours} hour${diffHours > 1 ? "s" : ""} ago`;
  return `${diffDays} day${diffDays > 1 ? "s" : ""} ago`;
}

export function getStatusColor(status: JobStatusState): string {
  switch (status) {
    case "success":
      return "text-green-700 bg-green-100";
    case "partial_success":
      return "text-yellow-700 bg-yellow-100";
    case "failed":
      return "text-red-700 bg-red-100";
    case "in_progress":
      return "text-blue-700 bg-blue-100";
    case "scheduled":
      return "text-gray-700 bg-gray-100";
    case "aborted":
      return "text-orange-700 bg-orange-100";
    default:
      return "text-gray-700 bg-gray-100";
  }
}

export function getStatusLabel(status: JobStatusState): string {
  switch (status) {
    case "in_progress":
      return "In Progress";
    case "partial_success":
      return "Partial Success";
    default:
      return status.charAt(0).toUpperCase() + status.slice(1);
  }
}

export function getLogLevelColor(level: LogLevel): string {
  switch (level) {
    case "ERROR":
      return "text-red-700 bg-red-50";
    case "WARN":
      return "text-yellow-700 bg-yellow-50";
    case "INFO":
      return "text-blue-700 bg-blue-50";
    case "DEBUG":
      return "text-gray-700 bg-gray-50";
    default:
      return "text-gray-700 bg-gray-50";
  }
}

interface ParsedTarget {
  type: "vol" | "dbs";
  name: string;
  id?: string;
}

export function parseTargetId(targetId: string): ParsedTarget | null {
  // Format: vol:name or dbs:name:id
  const parts = targetId.split(":");

  if (parts[0] === "vol" && parts.length === 2) {
    return {
      type: "vol",
      name: parts[1],
    };
  }

  if (parts[0] === "dbs" && parts.length >= 3) {
    return {
      type: "dbs",
      name: parts[1],
      id: parts.slice(2).join(":"), // Rejoin in case ID contains colons
    };
  }

  return null;
}

export function formatTargetName(targetId: string): string {
  const parsed = parseTargetId(targetId);
  return parsed ? parsed.name : targetId;
}
