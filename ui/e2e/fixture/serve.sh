#!/bin/sh
# Starts the server the console's browser checks run against (0050): builds
# it, empties the database HOPLOCK_E2E_DSN names, migrates, seeds, binds the
# three operators' roles, and runs it in the foreground. Playwright's
# webServer runs this and waits for /console/; CI runs it the same way.
#
# The database is emptied first so a run starts from the seed and nothing
# else: point HOPLOCK_E2E_DSN at a database that holds nothing you want.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
work="$repo/ui/build/e2e"
: "${HOPLOCK_E2E_DSN:?set HOPLOCK_E2E_DSN to a Postgres database the run may empty}"
mkdir -p "$work"
sed "s#@DSN@#$HOPLOCK_E2E_DSN#" "$here/control.yaml" > "$work/control.yaml"
PGOPTIONS='-c client_min_messages=warning' psql -q -v ON_ERROR_STOP=1 "$HOPLOCK_E2E_DSN" -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null
cd "$repo"
# A fixed version, so the Deployment view reads the same in every screenshot
# whatever the tree's VCS state (the Makefile's LDFLAGS, pinned).
go build -ldflags "-X github.com/hoplock/control/internal/daemon.version=v0.0.0-e2e" -o "$work/hoplock-control" ./cmd/hoplock-control
bin="$work/hoplock-control"
"$bin" migrate --config "$work/control.yaml" >/dev/null
# A fixed identity, so the deployment chrome is the same in every screenshot.
# The server keeps an identity it finds (store/deployment.go inserts its own
# only ON CONFLICT DO NOTHING), so this is the seed's database-direct path,
# not a route the server binds.
psql -q -v ON_ERROR_STOP=1 "$HOPLOCK_E2E_DSN" -c "INSERT INTO deployment (instance_id, created_at, log_epoch) VALUES ('inst-0050e2e0a1b2c3d4e5f60718293a4b5c', '2026-01-05T09:00:00Z', '0050e2e000000000');" >/dev/null
"$bin" seed --config "$work/control.yaml" --file "$here/seed.yaml" >/dev/null
for binding in ada:admin avery:auditor fenn:fleet-admin; do
  "$bin" identity role-bind --config "$work/control.yaml" --subject "${binding%%:*}" --role "${binding#*:}" >/dev/null
done
exec "$bin" --config "$work/control.yaml"
