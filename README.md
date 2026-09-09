# Dispatch

Dispatch is a Go HTTP service that accepts webhook delivery requests, stores
them in SQLite, and retrieves them by ID. It currently persists deliveries; a
background sender and retry worker have not been implemented yet.

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
