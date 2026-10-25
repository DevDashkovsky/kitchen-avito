#!/usr/bin/env bash
# Disposable PostgreSQL 16+ and API; never connects to a caller's DATABASE_URL.
set -euo pipefail
cd "$(dirname "$0")/.."
for tool in initdb pg_ctl psql createdb go python3; do
  command -v "$tool" >/dev/null || { echo "Missing tool: $tool" >&2; exit 1; }
done
task_tmp=$(mktemp -d /tmp/kitchen-pg.XXXXXX)
api_pid=''
cleanup() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    for log in init postgres api; do
      if [ -f "$task_tmp/$log.log" ]; then
        echo "--- $log.log (last 100 lines) ---" >&2
        tail -n 100 "$task_tmp/$log.log" >&2 || true
      fi
    done
  fi
  if [ -n "$api_pid" ]; then kill "$api_pid" 2>/dev/null || true; wait "$api_pid" 2>/dev/null || true; fi
  pg_ctl -D "$task_tmp/data" -m immediate -w stop >/dev/null 2>&1 || true
  rm -rf "$task_tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
initdb -D "$task_tmp/data" -U kitchen -A trust --locale-provider=icu --icu-locale=und -E UTF8 >"$task_tmp/init.log" 2>&1
pg_ctl -D "$task_tmp/data" -l "$task_tmp/postgres.log" -o "-h '' -k $task_tmp" -w start >/dev/null
createdb -h "$task_tmp" -U kitchen kitchen_test
createdb -h "$task_tmp" -U kitchen kitchen_upgrade
createdb -h "$task_tmp" -U kitchen kitchen_external_upgrade
export TEST_DATABASE_URL="postgresql:///kitchen_test?host=$task_tmp&user=kitchen"
export EXTERNAL_ID_UPGRADE_DATABASE_URL="postgresql:///kitchen_external_upgrade?host=$task_tmp&user=kitchen"
export UPGRADE_DATABASE_URL="postgresql:///kitchen_upgrade?host=$task_tmp&user=kitchen"
mkdir "$task_tmp/bin"
GOBIN="$task_tmp/bin" go install -tags='no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' github.com/pressly/goose/v3/cmd/goose@v3.27.2
"$task_tmp/bin/goose" -dir migrations postgres "$TEST_DATABASE_URL" up
"$task_tmp/bin/goose" -dir migrations postgres "$UPGRADE_DATABASE_URL" up-to 9
"$task_tmp/bin/goose" -dir migrations postgres "$EXTERNAL_ID_UPGRADE_DATABASE_URL" up-to 10
psql "$TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f seeds/demo.sql >/dev/null
export TEST_GOOSE="$task_tmp/bin/goose"
go test -race -tags=integration ./cmd/kitchen -count=1
# Fresh seed state for the original E2E suite and the real process restart test.
psql "$TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -f seeds/demo.sql >/dev/null
go build -o "$task_tmp/bin/kitchen" ./cmd/kitchen
api_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
export E2E=1 KITCHEN_E2E_URL="http://127.0.0.1:$api_port" E2E_CONFIRMATION_WAIT=20s
start_api() {
  DATABASE_URL="$TEST_DATABASE_URL" HTTP_ADDRESS="127.0.0.1:$api_port" CONFIRMATION_TIMEOUT=5s TIMEOUT_SCAN_INTERVAL=100ms "$task_tmp/bin/kitchen" >"$task_tmp/api.log" 2>&1 &
  api_pid=$!
}
start_api
go test -race -v ./tests/e2e -run '^TestOrderScenarios$' -count=1
export RESTART_FIXTURE="$task_tmp/restart.json"
export EXTERNAL_ID_RESTART_FIXTURE="$task_tmp/external-id-restart.json"
RESTART_PHASE=create go test ./tests/e2e -run '^Test(Idempotency|DishExternalID)Restart$' -count=1
kill "$api_pid"
wait "$api_pid"
api_pid=''
start_api
RESTART_PHASE=replay go test ./tests/e2e -run '^Test(Idempotency|DishExternalID)Restart$' -count=1
