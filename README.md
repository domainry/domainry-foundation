# Domainry Foundation

`domainry-foundation` is the shared Go infrastructure library for Domainry services.
It contains reusable technical mechanisms and deliberately owns no Identity, Notify,
Workflow, or other product-domain behavior.

## Packages

- `apperror`: stable application error contracts and safe parameters.
- `collection`: deterministic generic collection helpers.
- `decimal`: exact, bounded decimal normalization and arithmetic shared by deterministic engines.
- `expression`: storage-neutral expression contracts, validation, and deterministic evaluation.
- `filelock`: cross-platform process file locking.
- `idempotency`: fingerprints, receipt decisions, audit-safe facts, and bounded metrics.
- `logging`: structured Zap logging with stable error and request fields.
- `mutation`: storage mutation conflicts, transient failures, and transaction metrics.
- `requestcontext`: request, correlation, workspace, actor, execution, and trace identity.
- `secrets`: secret providers, key rings, envelope encryption, and redaction.
- `telemetry`: OpenTelemetry setup, async trace links, and bounded SQL metrics.

Application configuration, product branding, localization catalogs, product-domain
models, HTTP handlers, and persistence adapters belong to consuming services rather
than this module. Foundation expression contracts deliberately know nothing about
Runtime records, authorization, or storage.

## Development

```bash
go test ./...
go vet ./...
```
