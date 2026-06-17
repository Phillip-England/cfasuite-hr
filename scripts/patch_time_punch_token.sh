#!/usr/bin/env sh
set -eu

usage() {
  cat <<'USAGE'
Usage:
  ./scripts/patch_time_punch_token.sh [/path/to/cfasuite-hr.db]

If no path is passed, the script uses CFASUITE_DB_PATH. If that is not set,
it tries `cfasuite-hr db path`, then falls back to data/cfasuite-hr.db.

Stop cfasuite-hr before running this script.
USAGE
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required to run this one-off migration." >&2
  echo "Install sqlite3 or redeploy the current cfasuite-hr binary and run: cfasuite-hr init" >&2
  exit 1
fi

DB_PATH="${1:-${CFASUITE_DB_PATH:-}}"
if [ -z "$DB_PATH" ] && command -v cfasuite-hr >/dev/null 2>&1; then
  DB_PATH="$(cfasuite-hr db path 2>/dev/null || true)"
fi
if [ -z "$DB_PATH" ]; then
  DB_PATH="data/cfasuite-hr.db"
fi

if [ ! -f "$DB_PATH" ]; then
  echo "Database not found: $DB_PATH" >&2
  usage >&2
  exit 1
fi

TABLE_EXISTS="$(sqlite3 "$DB_PATH" "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'locations';")"
if [ "$TABLE_EXISTS" != "1" ]; then
  echo "The locations table does not exist in $DB_PATH. Refusing to patch an unexpected database." >&2
  exit 1
fi

TIMESTAMP="$(date +%Y%m%d%H%M%S)"
BACKUP_PATH="$DB_PATH.before-time-punch-token-$TIMESTAMP"
cp "$DB_PATH" "$BACKUP_PATH"
if [ -f "$DB_PATH-wal" ]; then
  cp "$DB_PATH-wal" "$BACKUP_PATH-wal"
fi
if [ -f "$DB_PATH-shm" ]; then
  cp "$DB_PATH-shm" "$BACKUP_PATH-shm"
fi
echo "Backup written to $BACKUP_PATH"

COLUMN_EXISTS="$(sqlite3 "$DB_PATH" "SELECT count(*) FROM pragma_table_info('locations') WHERE name = 'time_punch_token';")"
if [ "$COLUMN_EXISTS" = "0" ]; then
  sqlite3 "$DB_PATH" "ALTER TABLE locations ADD COLUMN time_punch_token TEXT NOT NULL DEFAULT '';"
  echo "Added locations.time_punch_token"
else
  echo "locations.time_punch_token already exists"
fi

sqlite3 "$DB_PATH" <<'SQL'
BEGIN IMMEDIATE;
UPDATE locations
SET time_punch_token = lower(hex(randomblob(32))),
    updated_at = CURRENT_TIMESTAMP
WHERE time_punch_token = '' OR time_punch_token IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_locations_time_punch_token
ON locations(time_punch_token)
WHERE time_punch_token <> '';
COMMIT;
SQL

MISSING_TOKENS="$(sqlite3 "$DB_PATH" "SELECT count(*) FROM locations WHERE time_punch_token = '' OR time_punch_token IS NULL;")"
if [ "$MISSING_TOKENS" != "0" ]; then
  echo "Patch verification failed: $MISSING_TOKENS locations still have no time_punch_token." >&2
  exit 1
fi

echo "Patch complete for $DB_PATH"
echo "Self-deleting $0"
rm -- "$0"
