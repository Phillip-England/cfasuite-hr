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
cfasuite-hr serve
cfasuite-hr token create -name "Reporting"
cfasuite-hr token list
cfasuite-hr token delete -id 1
cfasuite-hr api-context -base-url http://localhost:8217
```

`db path` prints the SQLite file path so you can inspect or copy it. The application itself does not require the `sqlite3` CLI, but if you have it installed you can run:

```sh
sqlite3 "$(cfasuite-hr db path)" ".tables"
```

## Employee Bio Imports

Create a location in the admin UI, then upload the employee bio `.xlsx` for that location. Imports read:

- `Employee Name`
- `Employee Number`
- `Job`
- `Employee Status`
- `Location Latest Start Date`

Rows with `Employee Status` equal to `Terminated` are skipped. Existing employees missing from the new active set are removed for that location.

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
