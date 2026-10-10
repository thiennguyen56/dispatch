# Dispatch first-release product plan

Date: October 10, 2026.

This is a proposed backlog based on the current repository, not a declaration
that the service is production-ready. No live-container or load validation was
performed for this assessment.

## Product goal and initial scope

Help developers deliver webhooks asynchronously and help operators understand
and recover failures without opening the database manually.

Working assumption, pending audience confirmation: an API-first, single-tenant
service for the owner's applications, operated as one Go process with persistent
SQLite storage. A hosted product for multiple customer teams requires tenant
ownership, isolated reads and replay permissions, tenant-scoped idempotency,
quotas, and a separate capacity assessment before onboarding customers.

The product promise is:

- A successful submission response means the delivery is durably persisted.
- Submission retries with the same idempotency key return the original delivery.
  The first request wins; replacement content is ignored.
- Dispatch attempts delivery and applies a bounded retry policy.
- Users can inspect the current state and recorded attempts.
- Permanent or exhausted failures become dead letters that operators can act on.
- Outbound delivery is at least once within the retry policy; receiver-side
  deduplication remains necessary to avoid repeated business effects.

## Users and their needs

| User | Main need | Successful outcome |
|---|---|---|
| Integrating developer | Submit a webhook without managing a sender or retry loop | Receives an ID and can safely repeat a submission |
| Operator | Find failed or delayed deliveries and diagnose the cause | Identifies affected deliveries and a clear next action |
| Receiver developer | Recognize repeat attempts | Uses the stable delivery ID to deduplicate processing |

## Current capabilities

| Capability | Evidence and current limitation |
|---|---|
| Submit and retrieve | API, service, and SQLite implementation exist |
| Submission idempotency | Key lookup, unique-index conflict handling, and regression tests exist |
| Claim, send, finalize | Lease-based claiming, HTTP sending, and transactional finalization exist |
| Retries | Transport failures, 408, 429, and 5xx are retried with a fixed one-minute delay |
| Delivery listing | Route and application types exist; handler panics and repository is a stub |
| Attempt history | Route and repository interface exist; handler panics and repository is a stub |
| Deployment | Database path is configurable; Compose currently binds `./data` into the container |
| First-run initialization | Container migration workflow remains incomplete |
| Access and data protection | No API authentication; submission logging includes request contents; retrieval returns the domain entity including headers and lease fields |
| Operations | No health/readiness endpoints, metrics, or release load baseline |

Registered placeholder routes must be completed or removed before exposing a
release. Existing unit tests do not establish end-to-end production readiness.

## Ordered delivery backlog

### DV-01: Browse deliveries — next feature

As an operator, I want to find deliveries by status so I can identify failures
and delays without querying SQLite.

Proposed API: `GET /deliveries?status=dead_letter&limit=20&cursor=...`.

Acceptance criteria:

- Supports all four known statuses; an omitted status returns all deliveries.
- Proposed default page size is 20; maximum is 100. Invalid status, limit, or
  cursor returns `400` with the existing error envelope.
- Uses deterministic ordering by immutable creation time and ID. Following a
  cursor does not repeat records solely because statuses change.
- A cursor is bound to its query filters; changing those filters requires a
  new first-page request.
- Empty results return `200` with an empty array, not `null`.
- Returns a next cursor only when more matching records exist.
- Summaries include ID, status, attempt count, creation/update times, sanitized
  last error, and next retry time where relevant. They exclude request payload,
  credentials, lease tokens, and credential-bearing target URL components.
- Tests cover empty results, invalid parameters, same-time records, filters,
  and multiple pages.

Changing a record's status can legitimately change its membership in a filtered
listing. The first version is a live view, not a frozen database snapshot.

### DV-02: Inspect attempt history — same milestone

As an operator, I want to inspect a delivery's attempts so I can distinguish
receiver failures, timeouts, and successful delivery.

Proposed API: `GET /deliveries/{id}/attempts`.

Acceptance criteria:

- Existing deliveries return attempts in a documented order by attempt number.
- Existing deliveries with no recorded attempts return `200` and an empty array.
- Unknown delivery IDs return `404`; malformed identifiers return `400`.
- Includes attempt number, outcome, HTTP status when available, timestamps,
  duration with an explicit unit, and sanitized error text.
- Does not expose request headers, payloads, lease data, or raw transport errors.
- Uses a bounded page size if replay or future policies can produce large histories.
- Documents that attempts are currently recorded after sending; an interrupted
  request may have no completed attempt record.
- Integration tests include 2xx, retryable HTTP failure, timeout, and a delivery
  with no attempts.

### RD-01: Reproducible installation — first-release requirement

As an operator, I want a documented container startup workflow that works on a
fresh machine and keeps delivery data across restarts.

Acceptance criteria:

- The documented workflow creates writable storage, applies pending migrations
  to the exact database opened by the application, and starts workers afterward.
- Migration failure prevents the API and worker from starting.
- Repeating the workflow preserves data and does not reapply completed migrations.
- Existing deployments have a documented upgrade and backup procedure.
- Database ownership, selected path, and Compose bind-mount behavior are documented.
- A restart and upgrade exercise proves accepted deliveries and history persist.
- SQLite connections have a bounded busy timeout for concurrent writes.
- Missing tables or an incompatible schema fail startup with an actionable error.

### AC-01: Controlled access and safe data — first-release requirement

As an operator, I want only authorized callers to use Dispatch and I want
credentials to stay out of operational responses and logs.

Acceptance criteria:

- The initial single-tenant release requires an API credential or an explicitly
  documented trusted gateway enforcing authentication. Invalid credentials
  cannot submit, inspect, or replay deliveries.
- API credentials can be rotated through a documented procedure.
- Submitted payloads and authorization headers are absent from routine logs.
- Public responses use explicit response types rather than serializing the
  entire domain entity.
- Internal database errors produce a generic `500` response; missing deliveries
  produce `404`.
- Request-body and header limits are documented and enforced. Proposed starting
  body cap: 1 MiB, subject to actual integration requirements; oversize requests
  return `413`.
- Rate and capacity limits are configured from expected usage; rejected work is
  clearly distinguished from accepted durable work.
- Outbound targets follow an explicit destination policy. For the internal pilot,
  an allowlist can include approved private services. Unapproved destinations,
  metadata endpoints, and DNS changes must not bypass the policy. Redirects
  remain disabled. See the [OWASP SSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html).

### RT-01: Predictable retry policy

As an integrating developer, I want retries to accommodate temporary receiver
failures without overwhelming the receiver.

Acceptance criteria:

- Documents retryable statuses, transport-error handling, maximum attempts, and
  permanent-failure behavior.
- Replaces fixed delays with configured exponential backoff, jitter, and a cap.
- Supports valid `Retry-After` values where applicable; malformed values use
  backoff. Both HTTP-date and delay-seconds forms are covered by tests. See
  [HTTP Retry-After semantics](https://www.rfc-editor.org/rfc/rfc9110.html#name-retry-after).
- The policy defines a maximum delivery age. If a requested delay would exceed
  that deadline, the result is explicit rather than silently retrying earlier.
- Exposes the next scheduled time and terminal reason to operators.
- Restarting preserves the schedule; exhausted deliveries are not claimed again.
- The lease duration exceeds the configured request and finalization timeouts
  with margin. Multiple-worker ownership and shutdown scenarios are tested.

### RC-01: Controlled dead-letter replay

As an operator, I want to retry a dead letter after fixing the receiver while
preserving the original history.

Proposed first version: `POST /deliveries/{id}/replays`. Create a new delivery
linked to the original, with a fresh retry budget and an operator-supplied reason.
This is a product proposal; confirm replay semantics before implementation.

Acceptance criteria:

- Only authorized operators can replay; the first version accepts dead letters.
- Unknown originals return `404`; ineligible originals return `409`.
- Original delivery state and history remain unchanged.
- Returns the new delivery ID and records the original relationship, operator
  identity, reason, and time.
- Repeated replay requests with the same idempotency key create only one replay.
- Documentation explains that the new outbound delivery ID represents deliberate
  reexecution, so receivers may apply business effects again.
- Does not permit arbitrary endpoint or payload edits in the initial version.

### OP-01: Operate and recover the service — first-release requirement

As an operator, I want to detect a stopped sender, a growing queue, and storage
failure before users report missing webhooks.

Acceptance criteria:

- Liveness indicates the process is running. Readiness checks database/schema
  availability and worker health without sending a webhook.
- Metrics cover pending count, oldest due delivery age, attempt outcomes and
  latency, dead-letter count, and database errors.
- Alert thresholds are selected from measured normal usage, with an identified
  owner and documented response steps.
- A backup/restore drill preserves consistent delivery and attempt data.
- Retention and the idempotency-key validity window are published together;
  deleting delivery history must not silently promise indefinite deduplication.
- CI runs relevant tests and race checks, builds the container, and exercises
  fresh-database startup, restart recovery, and graceful shutdown.
- A load test establishes supported throughput, queue delay, and payload size on
  the intended deployment. Publish measured limits instead of assumed capacity.

## Implementation order and release decision

1. Finish DV-01 and DV-02. This is the next functional milestone already started
   in the repository.
2. Complete RD-01 and AC-01 before a real pilot handles credentials or important
   business events.
3. Complete RT-01 and OP-01; exercise failure, restart, backup, and shutdown paths.
4. Add RC-01 so operators can recover dead letters through the API.
5. Run a pilot with representative producers and receivers. Validate the documented
   limits and operating procedures before declaring the first release ready.

An API-only first release is sufficient. A dashboard, billing, multiple-region
deployment, and broader storage infrastructure should follow validated demand.

## Readiness checklist

- [ ] A fresh install and an upgrade follow the documented workflow successfully.
- [ ] All exposed routes are implemented and have defined error behavior.
- [ ] A client can submit, safely repeat submission, inspect, diagnose, and replay.
- [ ] Success, transient failure, permanent failure, timeout, and restart recovery
  are demonstrated end to end.
- [ ] API access, destination policy, response redaction, and request limits are enforced.
- [ ] A stopped worker or failed database changes readiness and triggers an alert.
- [ ] Shutdown waits for bounded finalization before closing storage.
- [ ] Restore from backup and retention/idempotency behavior are verified.
- [ ] Documentation matches the running API and measured capacity.

## Decisions to confirm

- First audience: internal applications, other self-hosting developers, or hosted customers?
- Expected volume, maximum payload, and acceptable delivery delay?
- Approved internal/external receiver destinations and authentication requirements?
- Retry window, history retention, and idempotency-key validity window?
- Operator ownership of monitoring, backups, and replay permissions?

The page-size and body-size values above are proposed defaults, not measured
requirements or implemented limits. Acceptance criteria become release gates
once scope and audience are confirmed.
