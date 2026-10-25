# Domainry Foundation

`domainry-foundation` is the shared Go infrastructure library for Domainry services.
It contains reusable technical mechanisms and deliberately owns no Identity, Notify,
Workflow, or other product-domain behavior.

## Packages

- `apperror`: stable application error contracts and safe parameters.
- `capacity`: bounded process-local admission control by workspace and use case.
- `collection`: deterministic generic collection helpers.
- `decimal`: exact, bounded decimal normalization and arithmetic shared by deterministic engines.
- `definition`: canonical shared Definition contracts, `_definitions` / `_definition_versions` schema, and the SQL Store opened locally by each module or SaaS service.
- `expression`: storage-neutral expression contracts, validation, and deterministic evaluation.
- `filelock`: cross-platform process file locking.
- `health`: bounded technical check evaluation and diagnostic history.
- `idempotency`: fingerprints, receipt decisions, audit-safe facts, and bounded metrics.
- `logging`: one structured Zap root logger with stable error/request fields and separate application/access/infrastructure archives.
- `mutation`: storage mutation conflicts, transient failures, and transaction metrics.
- `modulehttp`: deployment-neutral, host-enforced HTTP Adapter declarations for in-process modules.
- `moduleinfo`: normalized active-module capabilities, deployment topology, HTTP, and persistence ownership.
- `requestcontext`: the API request-ID alias of trace identity plus workspace, actor, execution, and OpenTelemetry span identity.
- `ratelimit`: storage-neutral decisions and a bounded process-local limiter.
- `safehttp`: outbound HTTP destination validation with DNS and redirect enforcement.
- `secrets`: secret providers, key rings, envelope encryption, and redaction.
- `telemetry`: OpenTelemetry setup, HTTP/async trace propagation, bounded SQL metrics, and dynamically controlled boundary diagnostics.
- `worker`: process lifecycle, durable worker contracts, retry, wakeup, and fair ordering.

Application configuration, product branding, localization catalogs, product-domain
models, HTTP handlers, and product-specific persistence adapters belong to consuming
services rather than this module. Canonical persistence kernels for explicitly shared
tables belong here so their DDL and invariants have one maintenance point. Foundation
expression contracts deliberately know nothing about Runtime records, authorization,
or storage.

## Diagnostic boundary logs

`processhost.Open` installs one process-wide diagnostic policy for inbound HTTP,
outbound HTTP, SQL, and infrastructure adapters. `LOG_DIAGNOSTIC_ENABLED` and
`LOG_DIAGNOSTIC_FEATURES` select boundaries; `LOG_HTTP_CONTENT`,
`LOG_SQL_STATEMENT`, and `LOG_CONTENT_MAX_BYTES` control bounded content. Setting
`LOG_DIAGNOSTIC_POLICY_FILE` to a complete JSON policy enables atomic hot reload
without rebuilding routers, clients, or database pools.

File logging routes ordinary process and business events to `application`,
inbound `http_request` events to `access`, and every `dependency_call` emitted
by outbound HTTP, SQL, or another infrastructure adapter to `infrastructure`.

HTTP content is limited to JSON and passes through recursive secret-key redaction.
When `LOG_SQL_STATEMENT` is enabled, SQL events contain a bounded, directly
executable statement with driver arguments rendered as escaped SQL literals, plus
argument types. Disable that switch when parameter values must not enter diagnostic
logs. Adapter-specific instrumentation may permanently disable HTTP content capture
for identity or credential boundaries even when the process policy enables it.

## Development

```bash
go test ./...
go vet ./...
```
