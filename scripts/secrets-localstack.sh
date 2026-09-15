#!/usr/bin/env bash
# Exercises the real SSM secret path against LocalStack.
#
# §2.6 of the v1.0 plan proposed proving the staging secret path by deploying it. With no AWS
# account, this substitutes a real Parameter Store implementation for a real account: the SDK, the
# GetParameter call, and SecureString decryption are the production ones. What it does not prove is
# that an ECS task role can reach SSM — that needs an account, and is recorded in DEFERRED.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAME=aegis-localstack
PORT="${LOCALSTACK_PORT:-4566}"

cleanup() {
  if [ "${LOCALSTACK_KEEP:-0}" != "1" ]; then
    docker rm -f "$NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

command -v docker >/dev/null || { echo "secrets-localstack: docker is not on PATH" >&2; exit 1; }

echo "==> starting localstack"
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --rm --name "$NAME" \
  -e SERVICES=ssm \
  -e EAGER_SERVICE_LOADING=1 \
  -p "$PORT:4566" \
  localstack/localstack:3.8 >/dev/null

echo "==> waiting for ssm"
ready=0
for _ in $(seq 1 90); do
  if curl -sf "http://127.0.0.1:$PORT/_localstack/health" 2>/dev/null | grep -q '"ssm": *"\(available\|running\)"'; then
    ready=1
    break
  fi
  sleep 1
done
[ "$ready" = 1 ] || { echo "secrets-localstack: ssm did not come up" >&2; docker logs "$NAME" | tail -40 >&2; exit 1; }

echo "==> running the ssm secret-path tests"
cd "$ROOT/backend"
LOCALSTACK_ENDPOINT="http://127.0.0.1:$PORT" \
  go test -tags localstack ./internal/secrets/... -count=1 -timeout 5m -v
