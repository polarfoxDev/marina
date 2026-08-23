#!/usr/bin/env bash
set -euo pipefail

# Integration test script:
# 1. Build image
# 2. docker compose up -d (brings up MinIO, creates the test bucket, then marina)
# 3. Wait for 2 cron cycles (~90s)
# 4. Use marinactl (built into the image) to list and restore snapshots from S3
# 5. Require at least one snapshot for each target kind, and confirm
#    validation-failed targets were excluded

COMPOSE_FILE=tests/integration/docker-compose.integration.yml
COMPOSE_FILE_MESH=tests/integration/mesh/docker-compose.mesh.yml
IMAGE_TAG=${IMAGE_TAG:-marina:latest}
REQ_DB_KINDS=(postgres mysql mariadb mongo)
REQ_FAIL_DB=(postgres-empty)
REQ_FAIL_VOLUME=(integration_testdata-empty)
RESTORE_DIR=/tmp/marina-it-restore
RESTORE_DIR_PLAIN=/tmp/marina-it-restore-plain

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
# Wait long enough to cover at least one full minute boundary (cron
# '* * * * *') plus the time a full multi-target run (5 DB dumps + 2
# volumes, with container stop/start) actually takes.
sleep 110

FAIL=0

echo "[check] Listing snapshots (encrypted instance)"
LIST_OUT=$(docker exec marina-it marinactl list local-integration || true)
echo "$LIST_OUT"
if ! echo "$LIST_OUT" | grep -Eq '^[0-9]{8}-[0-9]{6}'; then
  echo "ERROR: marinactl list produced no snapshot rows" >&2
  docker compose -f "$COMPOSE_FILE" logs --no-color marina
  FAIL=1
fi

echo "[restore] Restoring latest snapshot (encrypted instance)"
docker exec marina-it rm -rf "$RESTORE_DIR"
docker exec marina-it marinactl restore local-integration latest --out "$RESTORE_DIR"

for KIND in "${REQ_DB_KINDS[@]}"; do
  # postgres -> pg-it, mysql -> mysql-it, mariadb -> mariadb-it, mongo -> mongo-it
  case "$KIND" in
    postgres) CONTAINER_NAME="pg-it" ;;
    mysql) CONTAINER_NAME="mysql-it" ;;
    mariadb) CONTAINER_NAME="mariadb-it" ;;
    mongo) CONTAINER_NAME="mongo-it" ;;
  esac

  if docker exec marina-it sh -c "[ -n \"\$(ls -A $RESTORE_DIR/db/$CONTAINER_NAME 2>/dev/null)\" ]"; then
    echo "[ok] Found $KIND dump (container: $CONTAINER_NAME)"
  else
    echo "ERROR: could not find $KIND dump for container $CONTAINER_NAME" >&2
    FAIL=1
  fi
done

# Targets that failed validation must never have been staged, so they must
# be entirely absent from the manifest -- and therefore from the restore.
for KIND in "${REQ_FAIL_DB[@]}"; do
  if docker exec marina-it sh -c "[ -d $RESTORE_DIR/db/$KIND-it ]"; then
    echo "ERROR: Found $KIND dump but it should have failed validation" >&2
    FAIL=1
  else
    echo "[ok] Confirmed $KIND dump was excluded (validation failed as expected)"
  fi
done

# Check volume snapshot presence and content
FILE_CONTENT=$(docker exec marina-it sh -c "cat $RESTORE_DIR/volume/integration_testdata/test.txt 2>/dev/null || true")
if [[ "$FILE_CONTENT" == "volume test data" ]]; then
  echo "[ok] Volume backup file content verified"
else
  echo "ERROR: volume backup file content mismatch (got: '$FILE_CONTENT')" >&2
  docker exec marina-it sh -c "ls $RESTORE_DIR/volume/ 2>&1 || true"
  FAIL=1
fi

for VOL in "${REQ_FAIL_VOLUME[@]}"; do
  if docker exec marina-it sh -c "[ -d $RESTORE_DIR/volume/$VOL ]"; then
    echo "ERROR: Found volume $VOL but it should have failed validation" >&2
    FAIL=1
  else
    echo "[ok] Confirmed volume $VOL was excluded (validation failed as expected)"
  fi
done

echo "[check] Listing and restoring snapshots (unencrypted instance)"
LIST_OUT_PLAIN=$(docker exec marina-it marinactl list local-integration-plain || true)
echo "$LIST_OUT_PLAIN"
if ! echo "$LIST_OUT_PLAIN" | grep -Eq '^[0-9]{8}-[0-9]{6}'; then
  echo "ERROR: marinactl list produced no snapshot rows for the unencrypted instance" >&2
  FAIL=1
else
  docker exec marina-it rm -rf "$RESTORE_DIR_PLAIN"
  docker exec marina-it marinactl restore local-integration-plain latest --out "$RESTORE_DIR_PLAIN"
  PLAIN_CONTENT=$(docker exec marina-it sh -c "cat $RESTORE_DIR_PLAIN/volume/integration_testdata-plain/test.txt 2>/dev/null || true")
  if [[ "$PLAIN_CONTENT" == "plain instance test data" ]]; then
    echo "[ok] Unencrypted instance volume backup file content verified"
  else
    echo "ERROR: unencrypted instance volume backup content mismatch (got: '$PLAIN_CONTENT')" >&2
    FAIL=1
  fi
fi

echo "[check] Listing snapshots on mesh peer node"
LIST_OUT_MESH=$(docker exec marina-it-mesh-2 marinactl list backup2 || true)
echo "$LIST_OUT_MESH"
if ! echo "$LIST_OUT_MESH" | grep -Eq '^[0-9]{8}-[0-9]{6}'; then
  echo "ERROR: marinactl list produced no snapshot rows on the mesh peer node" >&2
  docker compose -f "$COMPOSE_FILE_MESH" logs --no-color marina
  FAIL=1
else
  echo "[ok] Mesh peer node has at least one snapshot"
fi

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
