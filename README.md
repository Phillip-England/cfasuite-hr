# cfasuite-hr

`cfasuite-hr` is a small Go application for managing Chick-fil-A locations, importing employee bio `.xlsx` files, and exposing active employee data through a token-protected API.

## Quick Start

```sh
go mod download
go run . init
go run . set-admin -username admin -password change-me
go run . serve
```

Open `http://localhost:8217`.

The app uses port `8217` by default. Override it with `CFASUITE_ADDR` or `serve -addr`.

## Configuration

```sh
export CFASUITE_DB_PATH=data/cfasuite-hr.db
export CFASUITE_ADDR=:8217
export CFASUITE_ADMIN_USERNAME=admin
export CFASUITE_ADMIN_PASSWORD=change-me
export CFASUITE_SESSION_SECRET=replace-with-a-long-random-value
```

Admin credentials can also be stored in SQLite:

```sh
go run . set-admin -username admin -password change-me
```

To print shell export commands:

```sh
go run . admin-env -username admin -password change-me
```

## CLI

```sh
cfasuite-hr init
cfasuite-hr db path
cfasuite-hr db reset -yes
cfasuite-hr serve
cfasuite-hr token create -name "Reporting"
cfasuite-hr token list
cfasuite-hr token delete -id 1
cfasuite-hr api-context -base-url https://hr.example.com
```

## Development

See [docs/CODE_ORGANIZATION.md](docs/CODE_ORGANIZATION.md) for the module boundaries and refactor rules to follow as new features are added.

`db path` prints the SQLite file path so you can inspect or copy it. The application itself does not require the `sqlite3` CLI, but if you have it installed you can run:

```sh
sqlite3 "$(cfasuite-hr db path)" ".tables"
```

`db reset -yes` deletes the SQLite database file, removes SQLite sidecar files, and recreates an empty migrated database. This permanently deletes application data, so copy the database first if you need a backup.

## Employee Bio Imports

Create a location in the admin UI, then upload the employee bio `.xlsx` for that location. Imports read:

- `Employee Name`
- `Employee Number`
- `Job`
- `Employee Status`
- `Location Latest Start Date`

Rows with `Employee Status` equal to `Terminated` are skipped. Existing employees missing from the new active set are removed for that location.

## Roles

Admins create available employee roles per location in the admin UI. Roles are separate from the imported `Job` field: `Job` comes from the employee bio, while `role_id` and `role_name` are cfasuite-hr assignments.

New employees imported from an employee bio have no role until the admin assigns one. Open a location to bulk-select employees and apply a role, or clear their role by applying `Unassigned`. If an employee is removed by a later bio sync, that employee's role assignment is removed with the employee row.

Departments are also location-specific. Each location can use its own department names and assignments without affecting other locations.

## Birthday Report Imports

Open a location in the admin UI, then upload the Employee Birthday Reader `.xlsx` report for that location. The report must contain:

- `Employee Name`
- `Birth Date`

The importer matches birthdays to current employees at the selected location by exact employee name. It stores birthdays as `YYYY-MM-DD`. Employees that do not have a matching birthday report row keep `birth_date` as `null` in the API.

## API

Create an API token in the admin UI or CLI. Use either:

```txt
Authorization: Bearer <token>
X-API-Token: <token>
```

Endpoints:

```txt
GET /api/v1/locations
GET /api/v1/locations/{storeNumber}/employees
GET /api/v1/locations/{storeNumber}/employees/{employeeNumber}
```

Store numbers are strings, so leading zeroes such as `03394` are preserved.

Employee responses include:

- `employee_name`
- `employee_number`
- `job`
- `role_id` and `role_name` as assigned by cfasuite-hr, or `null`
- `employee_status`
- `location_latest_start_date`
- `birth_date` as `YYYY-MM-DD` or `null`

Example:

```sh
curl -sS \
  -H "Authorization: Bearer $CFASUITE_TOKEN" \
  "https://hr.example.com/api/v1/locations/03394/employees"
```

To generate a complete copy/paste context block for a large language model or another developer, pass the public endpoint where the app is running:

```sh
cfasuite-hr api-context -base-url https://hr.example.com
```

The generated context includes exact URLs, auth headers, response shapes, birthday behavior, cURL examples, and a Go client example.

## Docker

Build and run:

```sh
docker build -t cfasuite-hr .
docker run --rm -p 8217:8217 \
  -e CFASUITE_ADMIN_USERNAME=admin \
  -e CFASUITE_ADMIN_PASSWORD=change-me \
  -v cfasuite-hr-data:/app/data \
  cfasuite-hr
```

Inside the container, the default database path is `/app/data/cfasuite-hr.db`. The `-v cfasuite-hr-data:/app/data` volume is what keeps that SQLite file outside the disposable container filesystem. If you remove the container but keep the volume, the data remains. If you delete the volume, the SQLite database is deleted too.

To bind the database to a visible host directory instead:

```sh
mkdir -p ./data
docker run --rm -p 8217:8217 \
  -e CFASUITE_ADMIN_USERNAME=admin \
  -e CFASUITE_ADMIN_PASSWORD=change-me \
  -v "$PWD/data:/app/data" \
  cfasuite-hr
```

Your database will be at `./data/cfasuite-hr.db` on the host.

## Installation

```sh
make install
```

This installs the `cfasuite-hr` binary to `/usr/local/bin` by default. Override with:

```sh
make install PREFIX="$HOME/.local"
```
