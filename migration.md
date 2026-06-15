# One-time migration: location-specific roles and departments

This migration converts the existing SQLite database from global roles/departments to location-specific roles/departments.

It will:

- Create a backup next to your database before changing anything.
- Create a copy of every existing role for every existing location.
- Create a copy of every existing department for every existing location.
- Reassign each employee to the new role and department row for that employee's own location.
- Replace the old global `roles` and `departments` tables.
- Delete this `migration.md` file and the migration script after a successful run so the migration is not accidentally run again.

Run it from the repository root:

```sh
go run ./migrations/location_specific_roles_departments.go -db data/cfasuite-hr.db
```

If your production database uses `CFASUITE_DB_PATH`, run:

```sh
go run ./migrations/location_specific_roles_departments.go -db "$CFASUITE_DB_PATH"
```

After it finishes, start the updated app normally:

```sh
go run . serve -db data/cfasuite-hr.db
```

