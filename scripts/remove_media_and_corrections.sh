#!/bin/sh
set -eu

SCRIPT_PATH=$0
DB_PATH=${1:-${CFASUITE_DB_PATH:-data/cfasuite-hr.db}}
DATA_DIR=${2:-${CFASUITE_DATA_DIR:-$(dirname "$DB_PATH")}}

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required." >&2
  exit 1
fi
if [ ! -f "$DB_PATH" ]; then
  echo "Database not found: $DB_PATH" >&2
  exit 1
fi

timestamp=$(date +%Y%m%d-%H%M%S)
backup_path="$DB_PATH.before-media-removal-$timestamp"
sqlite3 "$DB_PATH" ".backup '$backup_path'"
echo "Backup created: $backup_path"

drop_column_sql() {
  table=$1
  column=$2
  if [ "$(sqlite3 "$DB_PATH" "SELECT count(*) FROM pragma_table_info('$table') WHERE name = '$column';")" -gt 0 ]; then
    printf 'ALTER TABLE %s DROP COLUMN %s;\n' "$table" "$column"
  fi
}

schema_sql="
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
DROP TABLE IF EXISTS time_punch_corrections;
DROP INDEX IF EXISTS idx_time_punch_corrections_location;
DROP INDEX IF EXISTS idx_locations_time_punch_token;
$(drop_column_sql employees profile_photo_data_url)
$(drop_column_sql employees profile_photo_needs_update)
$(drop_column_sql locations time_punch_token)
COMMIT;
PRAGMA foreign_keys = ON;
"

if ! sqlite3 -bail "$DB_PATH" "$schema_sql"; then
  echo "Migration failed. The database transaction was rolled back; backup remains at $backup_path" >&2
  exit 1
fi

remaining=$(sqlite3 "$DB_PATH" "
SELECT
  (SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'time_punch_corrections') +
  (SELECT count(*) FROM pragma_table_info('employees') WHERE name IN ('profile_photo_data_url', 'profile_photo_needs_update')) +
  (SELECT count(*) FROM pragma_table_info('locations') WHERE name = 'time_punch_token');
")
if [ "$remaining" -ne 0 ]; then
  echo "Migration verification failed; backup remains at $backup_path" >&2
  exit 1
fi

# Rewrite the file so removed photo and correction data is not left in free pages.
sqlite3 "$DB_PATH" "VACUUM;"

if [ -d "$DATA_DIR/locations" ]; then
  find "$DATA_DIR/locations" -type d -name profile-pictures -prune -exec rm -rf {} +
fi

foreign_key_issues=$(sqlite3 "$DB_PATH" "PRAGMA foreign_key_check;")
if [ -n "$foreign_key_issues" ]; then
  echo "Foreign-key verification failed; backup remains at $backup_path" >&2
  exit 1
fi

echo "Removed correction data, secret-link tokens, profile-photo columns, and profile pictures."
echo "Migration complete. Removing $SCRIPT_PATH"
rm -- "$SCRIPT_PATH"
