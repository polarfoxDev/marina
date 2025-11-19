import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api";
import type { JobStatus } from "../types";
import {
  formatDate,
  formatRelativeTime,
  getStatusLabel,
} from "../utils";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card } from "../components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";

// Map job status to badge variant
function getStatusBadgeVariant(status: string): "default" | "success" | "destructive" | "warning" | "secondary" {
  if (status === "completed") return "success";
  if (status === "failed") return "destructive";
  if (status === "running") return "warning";
  return "secondary";
}

export function JobStatusesView() {
  const { instanceId } = useParams<{ instanceId: string }>();
  const [jobs, setJobs] = useState<JobStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadJobs = useCallback(async () => {
    if (!instanceId) return;

    try {
      const data = await api.getJobStatus(instanceId);
      setJobs(data.sort((a, b) => b.iid - a.iid)); // Sort by iid descending
      setError(null);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to load job statuses"
      );
    } finally {
      setLoading(false);
    }
  }, [instanceId]);

  useEffect(() => {
    if (instanceId) {
      loadJobs();
      const interval = setInterval(loadJobs, 10000); // Refresh every 10s
      return () => clearInterval(interval);
    }
  }, [instanceId, loadJobs]);

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-gray-500">Loading job statuses...</div>
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
        <Button variant="link" asChild className="mb-4 p-0 h-auto">
          <Link to="/">← Back to Schedules</Link>
        </Button>
        <h1 className="text-3xl font-bold text-gray-900">
          Backup Jobs: {instanceId}
        </h1>
        <p className="mt-2 text-gray-600">
          History of backup jobs for this instance
        </p>
        {jobs.length > 0 && (() => {
          const nodes = [...new Set(jobs.map(j => j.nodeName).filter(Boolean))];
          if (nodes.length > 1) {
            return (
              <p className="mt-1 text-sm text-blue-600">
                Showing jobs from {nodes.length} node(s): {nodes.join(", ")}
              </p>
            );
          }
          return null;
        })()}
      </div>

      {jobs.length === 0 ? (
        <Card className="text-center py-12">
          <p className="text-gray-500">
            No backup jobs found for this instance
          </p>
        </Card>
      ) : (
        <Card>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Job #</TableHead>
                <TableHead>Node</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Targets</TableHead>
                <TableHead>Started</TableHead>
                <TableHead>Completed</TableHead>
                <TableHead>Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="font-medium">
                    #{job.iid}
                  </TableCell>
                  <TableCell>
                    {job.nodeName ? (
                      <Badge variant="default" className="bg-blue-100 text-blue-800 hover:bg-blue-100">
                        {job.nodeName}
                      </Badge>
                    ) : (
                      <span className="text-gray-400">Local</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={getStatusBadgeVariant(job.status)}>
                      {getStatusLabel(job.status)}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {job.lastTargetsSuccessful} / {job.lastTargetsTotal}
                  </TableCell>
                  <TableCell>
                    <div>{formatRelativeTime(job.lastStartedAt)}</div>
                    <div className="text-xs text-gray-400">
                      {formatDate(job.lastStartedAt)}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div>{formatRelativeTime(job.lastCompletedAt)}</div>
                    <div className="text-xs text-gray-400">
                      {formatDate(job.lastCompletedAt)}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Button variant="link" asChild className="p-0 h-auto">
                      <Link to={`/instance/${instanceId}/job/${job.id}`}>
                        View Details
                      </Link>
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
