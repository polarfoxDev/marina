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
SNAP_TXT=$(docker exec -e RESTIC_PASSWORD="$RESTIC_PASSWORD" marina-it /usr/local/bin/restic -r /backup/repo snapshots || true)
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
LATEST_LS=$(docker exec -e RESTIC_PASSWORD="$RESTIC_PASSWORD" marina-it /usr/local/bin/restic -r /backup/repo ls latest)

for KIND in "${REQ_DB_KINDS[@]}"; do
  # Check for database dumps under /dbs/<container-name>/ pattern
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
  
  if echo "$LATEST_LS" | grep -q "/dbs/$CONTAINER_NAME/"; then
    echo "[ok] Found $KIND dump (container: $CONTAINER_NAME)"
  else
    echo "ERROR: could not find $KIND dump for container $CONTAINER_NAME" >&2
    FAIL=1
  fi
done

# Check volume snapshot presence by verifying file from volume-writer
if echo "$LATEST_LS" | grep -q "/vol/"; then
  echo "[ok] Found volume backup"
  if echo "$LATEST_LS" | grep -q "hello\.txt"; then
    echo "[ok] Found test volume file (hello.txt) in latest snapshot"
  else
    echo "WARN: hello.txt not found in volume backup" >&2
  fi
else
  echo "ERROR: volume backup not found" >&2
  FAIL=1
fi

if [[ "$FAIL" -ne 0 ]]; then
  echo "Integration test FAILED" >&2
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
