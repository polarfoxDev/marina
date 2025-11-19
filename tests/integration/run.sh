#!/usr/bin/env bash
set -euo pipefail

# Integration test script:
# 1. Build image
# 2. docker compose up -d
# 3. Wait for 2 cron cycles (~90s)
# 4. Exec into marina container and list restic snapshots
# 5. Require at least one snapshot for each target kind

COMPOSE_FILE=tests/integration/docker-compose.integration.yml
COMPOSE_FILE_MESH=tests/integration/mesh/docker-compose.mesh.yml
IMAGE_TAG=${IMAGE_TAG:-marina:latest}
REQ_DB_KINDS=(postgres mysql mariadb mongo)
REQ_FAIL_DB=(postgres-empty)
REQ_FAIL_VOLUME=(integration_testdata-empty)
RESTIC_PASSWORD="testpass"

# if docker network 'mesh' does not exist, create it
if ! docker network ls --format '{{.Name}}' | grep -q '^mesh$'; then
  echo "[info] Creating docker network 'mesh'"
  docker network create mesh
fi

echo "[build] Building image ${IMAGE_TAG}"
docker build -t ${IMAGE_TAG} . >/dev/null

echo "[up] Starting integration stacks"
docker compose -f "$COMPOSE_FILE" up -d --quiet-pull
docker compose -f "$COMPOSE_FILE_MESH" up -d --quiet-pull

# Give services time to initialize
sleep 15

echo "[wait] Waiting for cron cycles to produce snapshots"
# Wait ~90 seconds to cover at least one minute boundary (cron '* * * * *')
sleep 90

echo "[check] Listing snapshots"
SNAP_TXT=$(docker exec -e RESTIC_PASSWORD="$RESTIC_PASSWORD" marina-it /usr/local/bin/restic -r /repo snapshots || true)
if [[ -z "$SNAP_TXT" ]]; then
  echo "ERROR: restic snapshots produced no output" >&2
  docker compose -f "$COMPOSE_FILE" logs --no-color marina
  exit 1
fi
echo "$SNAP_TXT" | sed -n '1,200p'

# Basic sanity: require at least one snapshot row with an ID-like token
if ! echo "$SNAP_TXT" | grep -Eq '^[[:space:]]*[0-9a-f]{8}'; then
  echo "ERROR: expected at least one snapshot entry" >&2
  exit 1
fi

# Simple presence checks by tag (runner passes tags from labels; here none custom so rely on paths)
# We validate existence of dump files by grepping output of 'restic ls latest'
FAIL=0
LATEST_LS=$(docker exec -e RESTIC_PASSWORD="$RESTIC_PASSWORD" marina-it /usr/local/bin/restic -r /repo ls latest)

for KIND in "${REQ_DB_KINDS[@]}"; do
  # Check for database dumps under /db/<container-name>/ pattern
  # postgres -> pg-it, mysql -> mysql-it, mariadb -> mariadb-it, mongo -> mongo-it
  case "$KIND" in
    postgres)
      CONTAINER_NAME="pg-it"
      ;;
    mysql)
      CONTAINER_NAME="mysql-it"
      ;;
    mariadb)
      CONTAINER_NAME="mariadb-it"
      ;;
    mongo)
      CONTAINER_NAME="mongo-it"
      ;;
  esac
  
  if echo "$LATEST_LS" | grep -q "/db/$CONTAINER_NAME/"; then
    echo "[ok] Found $KIND dump (container: $CONTAINER_NAME)"
  else
    echo "ERROR: could not find $KIND dump for container $CONTAINER_NAME" >&2
    FAIL=1
  fi
done

# Check that empty database dumps are NOT present
for KIND in "${REQ_FAIL_DB[@]}"; do
  if echo "$LATEST_LS" | grep -q "/db/$KIND-it/"; then
    echo "ERROR: Found $KIND dump but it should have failed validation" >&2
    FAIL=1
  else
    echo "[ok] Confirmed $KIND dump was excluded (validation failed as expected)"
  fi
done

# Check volume snapshot presence by verifying file from volume-writer
if echo "$LATEST_LS" | grep -q "/volume/"; then
  echo "[ok] Found volume backup"
  # restore and verify file content inside container
  docker exec marina-it mkdir -p /tmp/marina-it-restore
  docker exec -e RESTIC_PASSWORD="$RESTIC_PASSWORD" marina-it /usr/local/bin/restic -r /repo restore latest --target /tmp/marina-it-restore >/dev/null
  FILE_CONTENT=$(docker exec marina-it sh -c 'cat /tmp/marina-it-restore/backup/local-integration/*/volume/integration_testdata/test.txt 2>/dev/null || true')
  if [[ -n "$FILE_CONTENT" ]]; then
    if [[ "$FILE_CONTENT" == "volume test data" ]]; then
      echo "[ok] Volume backup file content verified"
    else
      echo "ERROR: volume backup file content mismatch (got: '$FILE_CONTENT')" >&2
      FAIL=1
    fi
  else
    echo "ERROR: volume backup file not found after restore" >&2
    docker exec marina-it sh -c 'ls /tmp/marina-it-restore/backup/local-integration/*/volume/integration_testdata/ || true'
    FAIL=1
  fi
else
  echo "ERROR: volume backup not found" >&2
  FAIL=1
fi

# Check that empty volumes are NOT present
for VOL in "${REQ_FAIL_VOLUME[@]}"; do
  if echo "$LATEST_LS" | grep -q "/volume/$VOL/"; then
    echo "ERROR: Found volume $VOL but it should have failed validation" >&2
    FAIL=1
  else
    echo "[ok] Confirmed volume $VOL was excluded (validation failed as expected)"
  fi
done

# Verify validation error messages appear in logs
echo "[check] Verifying validation error messages in logs"
LOGS=$(docker compose -f "$COMPOSE_FILE" logs --no-color marina)
if echo "$LOGS" | grep -q "validation failed.*0 bytes"; then
  echo "[ok] Found validation failure messages in logs"
else
  echo "ERROR: expected validation failure messages in logs" >&2
  FAIL=1
fi

if [[ "$FAIL" -ne 0 ]]; then
  echo "Integration test FAILED" >&2
  if [[ -n "${DEBUG:-}" ]]; then
    read -n 1 -s -r -p "Press any key to continue and tear down..."
    echo ""
  fi
  echo "[down] Tearing down integration stacks"
  docker compose -f "$COMPOSE_FILE_MESH" down -v >/dev/null || true
  docker compose -f "$COMPOSE_FILE" down -v >/dev/null || true
  exit 1
fi

echo "Integration test PASSED"

# wait for user input before tearing down (only if DEBUG is set)

if [[ -n "${DEBUG:-}" ]]; then
  read -n 1 -s -r -p "Press any key to continue and tear down..."
  echo ""
fi
echo "[down] Tearing down integration stacks"
docker compose -f "$COMPOSE_FILE" down -v >/dev/null || true
docker compose -f "$COMPOSE_FILE_MESH" down -v >/dev/null || true
