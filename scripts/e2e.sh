#!/usr/bin/env bash
# End-to-end smoke test: a real contract, a real chain, a real database, the real indexer and API.
#
# Brings up Anvil, Postgres, and Redis, applies migrations, and runs the e2e-tagged Go tests.
# Everything it starts, it stops.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PG_NAME=aegis-e2e-pg
REDIS_NAME=aegis-e2e-redis
ANVIL_PID=""

DB_DSN="${DB_DSN:-postgres://pb:pb_local@localhost:5432/aegis?sslmode=disable}"
REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export DB_DSN REDIS_ADDR

cleanup() {
  [ -n "$ANVIL_PID" ] && kill "$ANVIL_PID" 2>/dev/null || true
  if [ "${E2E_KEEP:-0}" != "1" ]; then
    docker rm -f "$PG_NAME" "$REDIS_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

for bin in docker forge cast anvil go; do
  command -v "$bin" >/dev/null || { echo "e2e: $bin is not on PATH" >&2; exit 1; }
done

echo "==> starting postgres and redis"
docker rm -f "$PG_NAME" "$REDIS_NAME" >/dev/null 2>&1 || true
docker run -d --rm --name "$PG_NAME" \
  -e POSTGRES_USER=pb -e POSTGRES_PASSWORD=pb_local -e POSTGRES_DB=aegis \
  -p 5432:5432 postgres:16-alpine >/dev/null
docker run -d --rm --name "$REDIS_NAME" -p 6379:6379 redis:7-alpine >/dev/null

echo "==> waiting for postgres"
for _ in $(seq 1 60); do
  docker exec "$PG_NAME" psql -U pb -d aegis -c 'select 1' >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$PG_NAME" psql -U pb -d aegis -c 'select 1' >/dev/null

echo "==> applying migrations"
docker exec -i "$PG_NAME" psql -U pb -d aegis -v ON_ERROR_STOP=1 \
  < "$ROOT/backend/migrations/000001_init_schema.up.sql" >/dev/null

echo "==> starting anvil"
anvil --port 8545 --chain-id 31337 --silent &
ANVIL_PID=$!
for _ in $(seq 1 30); do
  cast chain-id --rpc-url http://127.0.0.1:8545 >/dev/null 2>&1 && break
  sleep 1
done
cast chain-id --rpc-url http://127.0.0.1:8545 >/dev/null

echo "==> running end-to-end tests"
cd "$ROOT/backend"
go test -tags e2e ./internal/e2e/... -count=1 -v
