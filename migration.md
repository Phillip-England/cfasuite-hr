# Media and Correction Removal Migration

This one-time migration removes:

- the `time_punch_corrections` table and index
- `locations.time_punch_token` and its index
- the employee profile-photo columns
- every `profile-pictures` directory below the app data directory

The script creates a timestamped SQLite backup before changing anything. It deletes itself only after the database and filesystem cleanup succeeds.

## Run

1. Stop `cfasuite-hr` so nothing writes to SQLite during the migration.
2. Make sure the `sqlite3` command is installed.
3. From the project root, run:

```sh
chmod +x scripts/remove_media_and_corrections.sh
./scripts/remove_media_and_corrections.sh
```

The default database is `data/cfasuite-hr.db`, and the default data directory is the database's parent directory.

For custom paths, pass the database and data directory explicitly:

```sh
./scripts/remove_media_and_corrections.sh /path/to/cfasuite-hr.db /path/to/cfasuite-data
```

You can also use `CFASUITE_DB_PATH` and `CFASUITE_DATA_DIR`.

On success, a backup named like `cfasuite-hr.db.before-media-removal-20260618-120000` remains beside the database and the migration script removes itself. If the migration fails, the script stays in place and prints the backup path.
