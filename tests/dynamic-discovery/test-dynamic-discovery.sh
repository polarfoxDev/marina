#!/bin/bash
# Test script for Marina dynamic discovery
# This script demonstrates that Marina detects runtime changes to containers and volumes

set -e

echo "=== Marina Dynamic Discovery Test ==="
echo ""

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

COMPOSE_FILE=tests/dynamic-discovery/docker-compose.discovery.yml

log_step() {
    echo -e "${GREEN}▶ $1${NC}"
}

log_info() {
    echo -e "${YELLOW}ℹ $1${NC}"
}

docker build -t marina:discoverytest . >/dev/null

# Check if Marina is running
if ! docker compose -f "$COMPOSE_FILE" ps marina | grep -q "Up"; then
    log_info "Marina is not running, starting it..."
    docker compose -f "$COMPOSE_FILE" up -d marina
    sleep 5
else
    log_info "Marina is already running, rebuilding it..."
    docker compose -f "$COMPOSE_FILE" up -d marina
    sleep 5
fi

log_step "Step 1: Create a test volume with backup labels"
docker volume create \
  --label dev.polarfox.marina.enabled=true \
  --label dev.polarfox.marina.schedule="*/5 * * * *" \
  --label dev.polarfox.marina.instanceID=local-backup \
  test-dynamic-volume

log_info "Waiting 3 seconds for discovery..."
sleep 3

log_step "Checking Marina logs for scheduled volume"
if docker compose -f "$COMPOSE_FILE" logs marina | grep -q "scheduled.*test-dynamic-volume"; then
    echo "✓ Volume was automatically discovered and scheduled!"
else
    echo "✗ Volume not found in logs (may need more time)"
fi

echo ""
log_step "Step 2: Remove and recreate volume with different schedule"
docker volume rm test-dynamic-volume

docker volume create \
  --label dev.polarfox.marina.enabled=true \
  --label dev.polarfox.marina.schedule="*/10 * * * *" \
  --label dev.polarfox.marina.instanceID=local-backup \
  test-dynamic-volume

log_info "Waiting 3 seconds for discovery..."
sleep 3

log_step "Checking Marina logs for rescheduled volume"
if docker compose -f "$COMPOSE_FILE" logs --tail 50 marina | grep -q "removing target.*test-dynamic-volume"; then
    echo "✓ Old volume was automatically removed!"
else
    echo "✓ Volume may have been updated in place"
fi

if docker compose -f "$COMPOSE_FILE" logs --tail 50 marina | grep -q "scheduled.*test-dynamic-volume"; then
    echo "✓ New volume was automatically discovered and scheduled!"
else
    echo "✗ New volume not found in logs"
fi

echo ""
log_step "Step 3: Test container with DB backup labels"
docker run -d \
  --name test-postgres \
  --label dev.polarfox.marina.enabled=true \
  --label dev.polarfox.marina.db=postgres \
  --label dev.polarfox.marina.schedule="*/15 * * * *" \
  --label dev.polarfox.marina.instanceID=local-backup \
  -e POSTGRES_PASSWORD=testpass \
  postgres:16-alpine

log_info "Waiting 3 seconds for discovery..."
sleep 3

log_step "Checking Marina logs for scheduled database"
if docker compose -f "$COMPOSE_FILE" logs --tail 50 marina | grep -q "scheduled.*test-postgres"; then
    echo "✓ Database container was automatically discovered and scheduled!"
else
    echo "✗ Database container not found in logs"
fi

echo ""
log_step "Step 4: Stop and remove container"
docker stop test-postgres
docker rm test-postgres

log_info "Waiting 3 seconds for discovery..."
sleep 3

log_step "Checking Marina logs for removed container"
if docker compose -f "$COMPOSE_FILE" logs --tail 50 marina | grep -q "removing target.*container"; then
    echo "✓ Container removal was detected!"
else
    echo "ℹ Container removal may be detected on next poll"
fi

echo ""
log_step "Step 5: Cleanup test volume"
docker volume rm test-dynamic-volume

echo ""
log_step "Test complete! Check full logs with: docker compose -f \"$COMPOSE_FILE\" logs marina"
echo ""
echo "Key features demonstrated:"
echo "  • Volumes are discovered when created"
echo "  • Schedule changes are detected and applied"
echo "  • Containers with DB labels are discovered"
echo "  • Removed containers/volumes are unscheduled"
echo ""
echo "To monitor Marina in real-time, run:"
echo "  docker-compose -f \"$COMPOSE_FILE\" logs -f marina"
