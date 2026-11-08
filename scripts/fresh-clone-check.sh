#!/usr/bin/env bash
# R-DEL-2 check: clean clone → docker compose up (no .env, so the auto rule must pick the mock LLM) → all
# services healthy in < 180 s → UI reachable → seed present. Uses its own compose project and ports, so the
# dev stack keeps running.
#
#   scripts/fresh-clone-check.sh [--with-tests]    --with-tests also runs `make test` inside the clone
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
export COMPOSE_PROJECT_NAME=scc-fresh API_PORT=18080 WORKER_PORTS=19091-19092 PROVIDER_PORT=18090 POSTGRES_PORT=55433
unset LLM_PROVIDER ANTHROPIC_API_KEY

cleanup() {
  (cd "$tmp/scc" 2>/dev/null && docker compose down -v --remove-orphans >/dev/null 2>&1) || true
  rm -rf "$tmp"
}
trap cleanup EXIT

git clone -q "$root" "$tmp/scc"
cd "$tmp/scc"
[ ! -e .env ] || { echo "fresh clone unexpectedly has .env"; exit 1; }

start=$(date +%s)
docker compose up -d --build --wait --wait-timeout 300
elapsed=$(( $(date +%s) - start ))
echo "compose healthy after ${elapsed}s"

for url in "http://localhost:18080/readyz" "http://localhost:19091/readyz" "http://localhost:18090/readyz" "http://localhost:18080/"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$url")
  echo "$url -> $code"
  [ "$code" = 200 ] || { echo "FAILED: $url"; exit 1; }
done
users=$(docker compose exec -T postgres psql -U scc -d scc -tAc 'select count(*) from users')
echo "seeded users: $users"
[ "$users" -ge 3000 ] || { echo "FAILED: seed too small"; exit 1; }
mock=$(curl -s http://localhost:18080/api/system/status | python3 -c 'import json,sys; print(json.load(sys.stdin)["llm"]["mock"])')
echo "llm mock without .env: $mock"
[ "$mock" = True ] || { echo "FAILED: expected the mock LLM without a key"; exit 1; }

if [ "${1:-}" = "--with-tests" ]; then
  make test
fi
[ "$elapsed" -lt 180 ] || { echo "FAILED: took ${elapsed}s (limit 180s)"; exit 1; }
echo "fresh-clone-check: OK"
