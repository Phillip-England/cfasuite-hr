# Server Migration: `time_punch_token`

The server error:

```txt
SQL logic error: no such column: time_punch_token (1)
```

means the running application queried `locations.time_punch_token`, but the SQLite database file on the server does not have that column.

In the current source, startup calls `migrate(db)` from `serve`, `init`, and admin/token commands, and that migration already adds `locations.time_punch_token`. If your server is running this exact current build and pointed at the intended database, a restart should normally patch it automatically.

Most likely causes:

- The deployed binary/container is older than this source.
- The app is using a different database path than the one you initialized or inspected.
- The app was started in a way that skipped the current `migrate(db)` code.
- Startup migration failed earlier, then a request hit code that selects `time_punch_token`.

## Preferred Fix

Deploy the current app build, then restart it. On startup, `cfasuite-hr serve` should run the built-in migration.

You can also run the built-in migration directly:

```sh
cfasuite-hr init -db /path/to/cfasuite-hr.db
```

If you use `CFASUITE_DB_PATH`, this is enough:

```sh
cfasuite-hr init
```

Then start the app again.

## One-Off Patch Script

Use this only if you need to patch the existing server database directly.

The script is:

```txt
scripts/patch_time_punch_token.sh
```

It does four things:

1. Creates a timestamped backup next to the database.
2. Adds `locations.time_punch_token` if it is missing.
3. Fills blank tokens for existing locations and creates the unique index.
4. Deletes itself after a successful run.

## Run Steps

Stop the app first so SQLite is quiet:

```sh
sudo systemctl stop cfasuite-hr
```

Find the database path. Common paths are:

```txt
data/cfasuite-hr.db
/app/data/cfasuite-hr.db
```

If the binary is available on the server:

```sh
cfasuite-hr db path
```

Make sure `sqlite3` exists:

```sh
sqlite3 --version
```

Run the patch with the real database path:

```sh
chmod +x scripts/patch_time_punch_token.sh
./scripts/patch_time_punch_token.sh /path/to/cfasuite-hr.db
```

Verify the column exists:

```sh
sqlite3 /path/to/cfasuite-hr.db "PRAGMA table_info(locations);"
```

You should see `time_punch_token` in the output.

Start the app again:

```sh
sudo systemctl start cfasuite-hr
```

Check logs:

```sh
sudo journalctl -u cfasuite-hr -n 100 --no-pager
```

## Docker Notes

If the app runs in Docker, stop the container first, then run the patch against the mounted database volume. For the Dockerfile in this repo, the in-container default path is:

```txt
/app/data/cfasuite-hr.db
```

If you can exec into the container and it has `sqlite3`, run:

```sh
sqlite3 /app/data/cfasuite-hr.db "PRAGMA table_info(locations);"
```

If it does not have `sqlite3`, patch from the host against the mounted volume path, or rebuild/redeploy the current image and let startup migration run.

## Rollback

The script writes a backup like:

```txt
cfasuite-hr.db.before-time-punch-token-YYYYMMDDHHMMSS
```

To roll back, stop the app, move the current database aside, copy the backup back to the original database path, then start the app again.
