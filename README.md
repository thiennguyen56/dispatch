# Dispatch

Dispatch is a Go HTTP service that accepts webhook delivery requests, stores
them in SQLite, and sends them through a background worker with retries and
attempt history.

## Requirements

- Go 1.25.11 or later
- Optional: `golang-migrate`, built with the `sqlite` driver, for manual migration administration

To use the optional migration CLI:

```bash
go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.20.1
```

## Run locally

The application applies embedded SQL migrations before starting the API and
worker. It opens `dispatch.db` in the repository root by default; set
`DISPATCH_DB_PATH` to use a different file.

```bash
go run ./cmd/dispatch
```

The server listens on `http://localhost:8080`.

## HTTP API

### Create a delivery

```bash
curl --request POST http://localhost:8080/deliveries \
  --header 'Content-Type: application/json' \
  --data '{
    "url": "https://example.com/webhooks",
    "payload": "{\"event\":\"delivery.created\"}",
    "headers": {
      "Authorization": "Bearer <REDACTED>"
    }
  }'
```

A successful request returns `201 Created`:

```json
{
  "id": "<delivery-id>",
  "message": "Submitted deliveries"
}
```

`url` must be an absolute `http` or `https` URL. Header names must be valid
HTTP field names, and header values cannot contain newlines. `payload` and
`headers` are optional.

### Get a delivery

```bash
curl http://localhost:8080/deliveries/<delivery-id>
```

The response includes the persisted delivery, its status, retry metadata, and
timestamps.

### Errors

Errors use a consistent JSON envelope:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "url scheme must be http or https"
  }
}
```

## Database and migrations

SQLite is embedded: the application opens a database file directly rather than
connecting to a separate database service. The initial schema defines:

- `deliveries`: a durable webhook request, retry state, idempotency key, and
  lease metadata.
- `delivery_attempts`: the audit record for each outbound HTTP attempt.
- `schema_migrations`: managed by `golang-migrate`.

Migration files are in `internal/adapters/outbound/sqlite/migration/` and embedded
into the binary. Startup applies pending migrations to the same database
connection used by the service. Completed migrations are tracked in
`schema_migrations`, so restarting preserves data and skips completed work.
A failed or dirty migration prevents the API and worker from starting. A schema
version newer than the binary also fails startup; migrations never downgrade
automatically. Stop other instances before starting an upgrade: this workflow
is for a single application instance, not concurrent migration runners.

Database timestamps use UTC with exactly nine fractional digits so text
comparisons match chronological order. Migration `002` normalizes existing
UTC timestamps without changing their precision or optional `NULL` values.
Stop older application instances before applying it and restart with the
updated code so they cannot write variable-width timestamps again. Its down
migration retains normalized values, which the older reader also accepts.

```bash
make migrate-up
make migrate-version
make migrate-down                         # reverses one migration
make migrate-create name=add_delivery_index
```

Use a different database file when needed:

```bash
make migrate-up DB_PATH=tmp/test.db
```

## Docker Compose

`compose.yaml` builds and runs the service on port `8080`. It bind-mounts
`./data` to `/var/lib/dispatch`, so the local file `data/dispatch.db` is the
container's `/var/lib/dispatch/dispatch.db`.

Configuration loads `.env` locally and preserves existing environment variables.
Compose passes `.env` into the container and sets `DISPATCH_DB_PATH` to the mounted
path. For a fresh install:

```bash
touch .env
mkdir -p data
podman compose build
podman compose run --rm --no-deps --user root --entrypoint chown dispatch -R dispatch:dispatch /var/lib/dispatch
make compose-up
```

The ownership command grants the image's non-root `dispatch` user access to the
mounted directory. It changes ownership of the host `data` directory as well.
Use `docker compose` instead when using Docker.

No separate migration container or host CLI is required. SQL files ship inside
the binary and run when the container starts, after the mount is available and
before the API and worker start. Startup failures appear in the container logs.
If using the optional CLI manually, target this same file:

```bash
make migrate-version DB_PATH=data/dispatch.db
```

For upgrades, stop Dispatch first, back up the database with SQLite's backup
command, then rebuild and restart:

```bash
podman compose stop dispatch
sqlite3 data/dispatch.db ".backup 'data/dispatch-backup.db'"
make compose-up BUILD=1 RECREATE=1
```

Keep the backup until the upgrade is verified. Do not reset a dirty migration
version automatically; investigate the failed SQL and recover deliberately.

## Development

The outbound HTTP sender performs one POST per call using the stored payload
and headers. Content-Type defaults to `application/json` when absent. It has a
10-second timeout, honors context cancellation, and does not follow redirects.
Only 2xx responses produce a successful outcome. Non-2xx responses return a
failed result with their status; request/transport failures also return an
error, with timeouts distinguished from other failures. The processor must
inspect the outcome even when the error is nil. Response bodies are discarded
with a 64 KiB drain limit. Retry scheduling and attempt persistence belong to
the processor and are not implemented by the sender.

```bash
go test ./...
git diff --check
```

## Project layout

```text
cmd/dispatch/                         application entrypoint
internal/domain/                      Delivery and Attempt domain types
internal/application/                 submit and retrieval use cases
internal/adapters/inbound/httpapi/    HTTP routing, validation, responses
internal/adapters/outbound/sqlite/    SQLite repository and migrations
pkg/logger/                           structured logger setup
```
