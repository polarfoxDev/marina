#!/bin/sh
set -e

echo "======================================"
echo "Marina Example Custom Backup Script"
echo "======================================"
echo ""
echo "Instance ID: ${MARINA_INSTANCE_ID:-unknown}"
echo "Hostname: ${MARINA_HOSTNAME:-unknown}"
echo "Timestamp: $(date -Iseconds)"
echo ""

# Check if /backup directory is mounted
if [ ! -d "/backup" ]; then
    echo "ERROR: /backup directory not found"
    exit 1
fi

echo "Backup directory: /backup"
echo "Contents of /backup:"
ls -lah /backup/ || true
echo ""

# Simulate backup work - wait 10 seconds
echo "Starting backup process..."
i=1
while [ $i -le 10 ]; do
    echo "  Processing... ($i/10)"
    sleep 1
    i=$((i + 1))
done

# Randomly succeed 75% of the time (fail 25% of the time)
# Use shell arithmetic with current seconds to simulate randomness
RANDOM_NUM=$(($(date +%s) % 100))
if [ $RANDOM_NUM -lt 25 ]; then
    echo ""
    echo "ERROR: Backup failed! (simulated random failure)"
    echo "This is expected to happen ~25% of the time for testing purposes"
    exit 1
fi

echo ""
echo "✓ Backup completed successfully!"
echo "======================================"
exit 0
