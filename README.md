# Dispatch

Dispatch is a Go HTTP service that accepts webhook delivery requests, stores
them in SQLite, and retrieves them by ID. It currently persists deliveries; a
background delivery processing and retries are not wired into the application yet.

## Requirements

- Go 1.25 or later
- `golang-migrate`, built with the `sqlite` driver, for schema migrations

Install the migration CLI once:

```bash
go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

## Run locally

Apply the migrations before starting the application. Both commands use
`dispatch.db` in the repository root by default.

```bash
make migrate-up
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
- `delivery_attempts`: the future audit record for each outbound HTTP attempt.
- `schema_migrations`: managed by `golang-migrate`.

Migration files are in `internal/adapters/outbound/sqlite/migration/`.

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

`compose.yaml` builds and runs the HTTP service on port `8080`. It declares a
named volume intended for SQLite data. The current bootstrap still opens the
hard-coded `dispatch.db` path, so `DISPATCH_DB_PATH` and that volume are **not
yet wired together**. Run locally with the migration commands above until the
database-path configuration is implemented.

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
