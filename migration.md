# One-time cleanup: remove sign-in PIN data

This migration removes the obsolete `employees.sign_in_pin` column from the SQLite database.

It will:

- Rebuild the `employees` table without `sign_in_pin`.
- Preserve employee IDs, locations, roles, departments, wages, labor exclusions, birthdays, clock-in PINs, timestamps, and the employee uniqueness constraint.
- Recreate the employee location index.
- Run `PRAGMA foreign_key_check` after the rebuild.
- Delete this `migration.md` file and `migrations/drop_sign_in_pin.go` after a successful run.

The rebuild runs in one SQLite transaction. If anything fails before commit, SQLite rolls the database back and the cleanup files remain so you can inspect the error and rerun it.

Run it from the repository root:

```sh
go run ./migrations/drop_sign_in_pin.go -db data/cfasuite-hr.db
```

If your production database uses `CFASUITE_DB_PATH`, run:

```sh
go run ./migrations/drop_sign_in_pin.go -db "$CFASUITE_DB_PATH"
```

After it finishes, start the updated app normally:

```sh
go run . serve -db data/cfasuite-hr.db
```

If the script reports that `employees.sign_in_pin` is already absent, it will still remove this runbook and the migration script.
