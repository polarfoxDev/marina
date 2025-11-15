#!/bin/sh
set -e

# Marina container entrypoint
# Runs both the backup manager and the API server

# Ensure required directories exist
mkdir -p /var/lib/marina /backup/tmp

# Function to handle shutdown gracefully
shutdown() {
    echo "Shutting down Marina services..."
    kill -TERM "$manager_pid" "$api_pid" 2>/dev/null || true
    wait "$manager_pid" "$api_pid" 2>/dev/null || true
    echo "Marina services stopped"
    exit 0
}

trap shutdown TERM INT

# Start the backup manager in the background
echo "Starting Marina backup manager..."
marina &
manager_pid=$!

# Give the manager a moment to initialize the database
sleep 1

# Start the API server in the background
echo "Starting Marina API server on port ${API_PORT:-8080}..."
marina-api &
api_pid=$!

# Wait for both processes
echo "Marina is running (Manager PID: $manager_pid, API PID: $api_pid)"
echo "Press Ctrl+C to stop"

# Wait for either process to exit (POSIX-compatible)
while kill -0 "$manager_pid" 2>/dev/null && kill -0 "$api_pid" 2>/dev/null; do
    sleep 1
done

# If we get here, one of the processes died
# Get exit code of the process that exited
if ! kill -0 "$manager_pid" 2>/dev/null; then
    wait "$manager_pid"
    exit_code=$?
    echo "Marina manager exited with code $exit_code"
elif ! kill -0 "$api_pid" 2>/dev/null; then
    wait "$api_pid"
    exit_code=$?
    echo "Marina API exited with code $exit_code"
else
    exit_code=1
    echo "Unknown error: both processes still running"
fi

shutdown
