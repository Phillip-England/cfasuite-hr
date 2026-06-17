# Code Organization

`cfasuite-hr` is intentionally small, but it now has several independent feature areas. Keep those areas easy to find, test, and remove. The app should stay simple Go, but simple should not mean one large file where every concern is mixed together.

## Current State

The application is still one Go package, `main`, which is appropriate for this size. The package is now split at the file level:

- `main.go`: executable entrypoint, CLI commands, HTTP routes and handlers, persistence functions, import logic, report logic, rendering, helpers, and embedded templates/CSS.
- `models.go`: application constants and shared domain types used across handlers, persistence, importers, and API responses.
- `main_test.go`: behavior tests for imports, employee assignments, wages, labor parsing, API/auth helpers, and rendering-related helpers.

The next refactors should continue splitting `main.go` into files while keeping `package main` until there is a clear need for internal packages.

## System Boundaries

The application has one runtime system of record:

- SQLite is the only database. Every feature stores relational state in the database opened from `CFASUITE_DB_PATH`, from `CFASUITE_DATA_DIR/cfasuite-hr.db` when `CFASUITE_DATA_DIR` is explicitly set, or from the legacy default `data/cfasuite-hr.db`.
- Do not introduce per-feature database files, local JSON stores, hidden caches, or secondary persistence engines. If a feature needs state, add an idempotent migration and keep the storage helper in the owning module.
- `*sql.DB` is created once at startup and passed through `App`. HTTP handlers, CLI commands, and import/sync functions should receive that database handle explicitly.
- Filesystem writes are allowed only under `CFASUITE_DATA_DIR`, except the SQLite file itself when `CFASUITE_DB_PATH` deliberately points somewhere else.
- Current filesystem-owned paths are `locations/{storeNumber}/profile-pictures/` for profile photos and `tmp/` for transient parser files.
- Uploaded source documents are parsed and discarded. Do not persist raw uploads unless the feature explicitly needs audit storage and the path is documented here first.

Environment variables are the boundary between deployment and code. New configurable paths must be rooted under `CFASUITE_DATA_DIR`; new service configuration should use `CFASUITE_`-prefixed variables and be documented in `README.md`.

## Target File Layout

Use these file boundaries as the code grows:

- `main.go`: `main`, `usage`, and top-level command dispatch only.
- `cli.go`: CLI command implementations such as `serve`, `init`, `db`, `set-admin`, `token`, and `api-context`.
- `app.go`: `App`, `newApp`, route registration, middleware wiring, rendering, and app-wide HTTP helpers.
- `auth.go`: admin login/logout, session cookies, login attempt rate limiting, admin credential checks, and security helpers.
- `tokens.go`: API token creation, listing, deletion, hashing, validation, and token admin handlers.
- `api.go`: token-protected JSON API handlers and API response helpers.
- `db.go`: `openDB`, migrations, settings, schema helpers, and database setup.
- `locations.go`: location persistence and location admin handlers.
- `employees.go`: employee persistence, employee scan helpers, role/department/wage/labor-exclusion assignments, and employee option/status helpers.
- `roles.go`: role persistence and role admin handlers.
- `departments.go`: department persistence and department admin handlers.
- `imports_bio.go`: employee bio upload handler, `.xlsx` parsing, and employee sync logic.
- `imports_birthdays.go`: birthday report upload handler, `.xlsx` parsing, and birthday sync logic.
- `labor.go`: time punch upload handler, PDF parsing, labor enrichment, wage extraction, totals, summaries, and labor table rows.
- `format.go`: date, money, hour, percentage, name, and URL formatting helpers.
- `templates.go`: embedded HTML templates.
- `assets.go`: embedded CSS and asset handlers.

Prefer file-level modularity first. Introduce subpackages only when a module has a clean API and is valuable outside the HTTP app. Good candidates are import parsers and labor report parsing. Avoid moving database-heavy handlers into subpackages unless there is a repository/service layer ready to support that.

## Feature Boundaries

Each feature should own its request handling, business operation, storage helpers, and tests.

Sign in and sign out:

- Owns admin credentials, login page/post, logout, session cookies, login attempt tracking, IP banning, and auth middleware.
- Should not know about locations, employee imports, API response shapes, or labor reports.
- Public surface should be small: `requireAdmin`, `validSession`, `setSession`, `validAdmin`, and login attempt helpers.

API tokens:

- Owns token generation, hashing, validation, token admin UI handlers, token CLI commands, and API auth middleware.
- Should not share session logic with admin auth beyond general crypto helpers.
- Token values are shown once. Store only hashes and prefixes.

Employee bio imports:

- Owns required bio columns, `.xlsx` parsing, active employee sync, terminated/skipped row handling, and preservation of data that should survive a bio sync.
- Should not render location pages except through the upload handler redirect.
- Parser tests should use byte input and not require a running server.

Birthday report imports:

- Owns required birthday columns, `.xlsx` parsing, employee name matching, date normalization, and birthday update counts.
- Should not create employees. It only enriches employees already present for a location.

Time punch and labor:

- Owns PDF text extraction, report parsing, wage extraction, assignment enrichment, salary labor logic, exclusion filtering, totals, and labor view rows.
- Keep parsing and calculations testable without HTTP.
- Do not let labor parsing mutate database state except through explicit functions such as wage update from report.

Sales calendar and productivity goals:

- Owns daily sales storage, sales upload calendar state, and monthly productivity goals.
- Monthly productivity goals are location-scoped and month-scoped using `YYYY-MM`.
- Productivity goals are stored in SQLite, not in per-month files or local configuration.

Employee data:

- Owns employee persistence, scanning rows, role/department assignment, wage assignment, labor exclusion assignment, and employee API shapes.
- Employee number is the durable cross-location identity for wage propagation. Location plus employee number is the unique row identity.

Locations, roles, and departments:

- Locations own store name/number and location admin flows.
- Roles and departments are location-scoped. Names may repeat across locations.
- Role/department assignment should not overwrite imported `Job`.

UI templates and CSS:

- Templates should stay dumb. Put formatting and aggregation in Go helpers before rendering.
- A template may display data and submit forms, but it should not encode business rules that belong in import, employee, or labor functions.

## Adding a New Component

When adding a new feature, create or extend the module that matches the feature, then wire it at the edges:

1. Define ownership first: name the feature module, its routes, its database tables/columns, and any `CFASUITE_DATA_DIR` paths it owns.
2. Add domain types in `models.go` only if they are shared across multiple modules. Keep private, feature-only structs in that feature file.
3. Add schema changes in `db.go` and make migrations idempotent.
4. Add persistence functions in the owning module or a clearly named persistence file.
5. Add parser/calculation code as pure functions when possible.
6. Add HTTP handlers in the owning module and register routes in `app.go`.
7. Add CLI commands in `cli.go` only when the feature needs command-line access.
8. Add focused tests next to the behavior being changed.
9. Update `README.md` for user-facing behavior and update this document if the component adds a new module, table family, or filesystem path.

If a new component requires touching more than three unrelated modules, stop and define a small data flow first. That usually means a boundary is missing.

## Removing a Component

A component should be removable without hunting through unrelated code:

1. Remove its route registrations from `app.go`.
2. Remove its CLI command wiring from `cli.go`, if any.
3. Remove its handlers, pure parser/calculation functions, and persistence helpers from the owning file.
4. Remove its templates/CSS only after handlers are gone.
5. Leave old migrations in place unless there is an explicit data migration plan. SQLite schema history should remain forward-compatible for existing installs.
6. Remove or update tests that exercised the deleted behavior.

## Naming Rules

- Handler methods use nouns from the route plus action: `birthdayUpload`, `laborUpload`, `tokenCreate`, `locationShow`.
- Persistence functions use verbs: `listEmployees`, `getLocation`, `createToken`, `assignEmployeeWage`.
- Parser functions start with `parse`: `parseBio`, `parseBirthdays`, `parseTimePunchText`.
- Formatting functions start with `format` or `normalize`.
- Keep exported names only for JSON/domain types that need to be reflected or shared. Most functions should remain unexported.

## Testing Rules

- Parser tests should feed bytes or text directly into parser functions.
- Sync/import tests should use a temporary SQLite database and assert database state after import.
- Auth/token tests should verify invalid, expired, and valid cases.
- Labor tests should cover parsing, totals, wage extraction, salary handling, and exclusion behavior separately.
- HTTP tests are useful at route boundaries, but most business logic should be testable without HTTP.

## Refactor Order

The safest path is mechanical extraction first, behavior changes second:

1. Move shared types/constants to `models.go`. This is already done.
2. Move database setup and settings helpers to `db.go`.
3. Move auth/session/rate-limit helpers and handlers to `auth.go`.
4. Move API token functions and handlers to `tokens.go`.
5. Move route registration/rendering to `app.go`.
6. Move employee/location/role/department persistence and handlers into their files.
7. Move each document import into its own file.
8. Move labor/time punch parsing and calculations into `labor.go`.
9. Move templates and CSS last, because they are noisy and should not obscure behavior changes.

After each extraction, run `go test ./...`. Do not combine a large file move with a feature change unless the feature change requires it.
