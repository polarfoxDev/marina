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

# Wait for either process to exit
wait -n $manager_pid $api_pid

# If we get here, one of the processes died
exit_code=$?
echo "One of the Marina services exited with code $exit_code"
shutdown
