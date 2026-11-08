#!/usr/bin/env bash
# R-DEL-2 / R-DEL-3 check, the reviewer's path (KICKOFF §10.3):
#   1. clean clone, no .env → docker compose up → healthy in < 180 s → UI reachable → seed present → mock LLM;
#   2. restart (down, up) → the seed is still exactly the same size (idempotent migrations and seed);
#   3. down -v && up → a fresh database is migrated and seeded again;
#   4. a key-only .env (a dummy key, never used for a call) → the API starts in anthropic mode;
#   5. with --with-tests: make test inside the clone, timed (target < 5 min).
# Uses its own compose project and ports, so the dev stack keeps running.
#
#   scripts/fresh-clone-check.sh [--with-tests]
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

up() {
  local start elapsed
  start=$(date +%s)
  docker compose up -d --build --wait --wait-timeout 300 >/dev/null 2>&1 || { docker compose ps; echo "FAILED: compose up"; exit 1; }
  elapsed=$(( $(date +%s) - start ))
  echo "$1: compose healthy after ${elapsed}s"
  [ "$elapsed" -lt 180 ] || { echo "FAILED: took ${elapsed}s (limit 180s)"; exit 1; }
}

check() {
  # The worker publishes a port range (so it can be scaled to two replicas); ask compose which one it got.
  local worker_port url code users mock
  worker_port=$(docker compose port worker 9091 | sed 's/.*://')
  for url in "http://localhost:18080/readyz" "http://localhost:${worker_port}/readyz" "http://localhost:18090/readyz" "http://localhost:18080/"; do
    code=$(curl -s -o /dev/null -w '%{http_code}' "$url")
    echo "  $url -> $code"
    [ "$code" = 200 ] || { echo "FAILED: $url"; exit 1; }
  done
  users=$(docker compose exec -T postgres psql -U scc -d scc -tAc 'select count(*) from users')
  echo "  seeded users: $users"
  [ "$users" = "${2:-5000}" ] || { echo "FAILED: expected ${2:-5000} users"; exit 1; }
  mock=$(curl -s http://localhost:18080/api/system/status | python3 -c 'import json,sys; print(json.load(sys.stdin)["llm"]["mock"])')
  echo "  llm mock: $mock"
  [ "$mock" = "$1" ] || { echo "FAILED: expected llm mock = $1"; exit 1; }
}

up "1. clean clone, no .env"
check True

docker compose down >/dev/null 2>&1
up "2. restart (volume kept)"
check True 5000

docker compose down -v >/dev/null 2>&1
up "3. down -v && up"
check True 5000

# A dummy key only proves the auto rule picks the real adapter; nothing here calls the API.
printf 'ANTHROPIC_API_KEY=sk-ant-dummy-for-the-fresh-clone-check\n' > .env
docker compose up -d --wait --wait-timeout 300 >/dev/null 2>&1 || { echo "FAILED: compose up with a key-only .env"; exit 1; }
echo "4. key-only .env (dummy key)"
check False 5000
rm -f .env
docker compose up -d --wait --wait-timeout 300 >/dev/null 2>&1

if [ "${1:-}" = "--with-tests" ]; then
  start=$(date +%s)
  make test
  echo "5. make test took $(( $(date +%s) - start ))s (target < 300 s)"
fi
echo "fresh-clone-check: OK"
