# Domainry module capability contract v1

`modulecapability.Binding` is embedded by every top-level module SDK Binding.
It is a facet of the already-opened business Binding, not a second provider
lifecycle.

## Topology invariant

- Module mode implements the typed methods directly.
- SaaS mode exposes the same Binding through `HTTPPrefix` and constructs a
  `RemoteBinding` with `OpenRemote`.
- `ModuleSummary`, every `CategoryDocument`, validation diagnostics, contract
  version, validation revision, and contract digest are identical in both modes.
- Current deployment mode is deliberately not disclosed. Supported deployment
  modes are part of the source contract and therefore do not change when the
  selected topology changes.
- Mounted `modulehttp.Adapter` routes, SaaS health/discovery endpoints, service
  authentication, workers, persistence, and operations routes are deployment
  evidence and never enter this model-facing contract automatically.

## Typed Binding methods

1. `CapabilitySummary` returns the small module identity, adaptation scenarios,
   dependencies, and category index used for PRD-driven module selection.
2. `CapabilityCategory` returns one exact category containing all of its full
   OpenAPI Operations and their exact transitive component closure, plus any
   bounded source-owned non-endpoint projections used by that category (for
   example registered Connector definitions).
3. `ValidateCapabilityCandidate` sends one owner-scoped model-authoring
   fragment to the module that owns its semantic rules. Plane may aggregate
   diagnostics but must not reimplement those rules. The method is present on
   every Binding; modules with no authorable candidate parameters disclose no
   validation scopes and reject every attempted kind instead of inventing a
   validator.

## Authoring validation contract

Validation is for durable module configuration authored in `backend/model`,
not for replaying arbitrary product endpoint requests. OpenAPI and the live
module remain the contract and enforcement boundary for endpoint inputs.

Every request uses exactly one common `AuthoringFragment`:

```json
{"collection":"scheduled_jobs","key":"daily_settlement","value":{}}
```

`value` is the unchanged source-owned emission value. Plane may resolve the
fragment from a project source pointer, but it must not wrap, rename, flatten,
default, or translate module fields. Referenced context is a bounded array of
the same fragments, uniquely sorted by `collection` and `key`; it is not an
owner-shaped aggregate DTO. The caller sends only explicitly bound context,
never secrets, credentials, live principals, or the entire project model.
Ledger collection rendering may inject the envelope `key` into a configured
identity field. Owners that validate the pre-render source use
`DecodeKeyedAuthoringValue` to perform that mapping locally: a missing repeated
identity is supplied from the envelope, while a conflicting repeated identity
fails closed. This does not mutate the request or authorize Plane to rewrite
module parameters.

For every string in a category's compact `validation_scopes` index, the full
category document contains one same-key `validation_contracts` entry. It
describes the accepted candidate collections and the only referenced
collections the caller may provide. `coverage: all_candidates` requires every
emission in the candidate collections to have exactly one owner binding;
`coverage: explicit` is reserved for a specialized validator that does not
apply to every fragment in a shared collection. Conditional reference requirements and
all field-level semantics remain owner rules and return owner-attributed
diagnostics. A validation scope that only mirrors a runtime HTTP request is not
model-facing and must not be disclosed here.

A category is rejected above 20 operations, 50 source projections, 50
validation scopes, or 1 MiB of canonical JSON. Required collection fields
always encode as JSON arrays, including empty `[]`; they never alternate with
`null`. Owners split larger adapters by stable business semantics (or
stable projection batches for large registries); they never truncate a category
or omit an operation/projection from the selected batch. Every projection is
included in the module contract digest.

## Canonical HTTP mapping

The only shared capability wire adapter is:

- `GET /.well-known/domainry/module-capability/v1/summary`
- `GET /.well-known/domainry/module-capability/v1/categories/{key}`
- `POST /.well-known/domainry/module-capability/v1/validate`

`NewHTTPHandler` requires a service authenticator and cannot be constructed
without one. `OpenRemote` accepts a request authorizer, requires the caller to
pin both the expected module key and SHA-256 digest, and fails during opening
when the Remote differs. Opening eagerly loads and validates the complete category
bundle, so a summary cannot hide stale or partial category content.
If a module's existing SaaS business transport already embeds `Binding`
instead of being constructed by `OpenRemote`, its Factory must call
`VerifyPinnedBinding` with the source-owned module key and digest before it
connects or returns that transport. This is the same full-bundle gate, not a
second disclosure protocol.
The shared client bounds response sizes, maps and redacts stable errors, honors
caller deadlines, invokes the Remote factory's service/application authorizer,
and marks transport-unavailable failures retryable. It deliberately performs
one HTTP attempt, including for validation POSTs; retry and circuit-breaker
policy stays in the module-specific Remote client/transport, which already
knows its idempotency and application-scope rules.

## Endpoint content

Standard OpenAPI is the only source for method, path, operation ID, parameters,
request body, responses, security schemes, and wire schemas. The outer category
document does not repeat those fields.

Each Operation contains one `x-domainry-capability` extension for facts OpenAPI
does not express:

- semantic owner and optional nested provider key;
- authorization mode: anonymous, principal-only, fixed permissions, or
  owner-dynamic policy;
- effect class and idempotency semantics;
- exceptional transport behavior, such as SSE resume semantics, only when it
  changes correct invocation behavior.

Assembly chains and validation scopes live at module/category level. They are
not copied onto every endpoint.

## Removed from the model-facing endpoint document

The v1 contract intentionally does not include the old duplicated
`endpoint_identity`, `method`, `path`, frontend targets, Runtime-domain
projection, Runtime/owner client method status, readiness counters, runtime
configuration status, provider verification status, scenario verification
status, or product-assembly disposition. These are either derivable from
OpenAPI, build/deployment diagnostics, or batch-level facts.

Authorization disclosure contains no live users, roles, grants, AccessBundles,
credentials, or policy decisions. Runtime enforcement remains with the source
owner and Identity.

## Canonical identity and conformance

`NewStaticBinding` canonicalizes the complete summary/category bundle and
computes its SHA-256 identity. The digest excludes only the digest fields
themselves. `validation_contract_version` versions the shared wire shape;
module implementations bump their source-owned `validation_revision` whenever
semantic rules change without a schema change.

Every module consumes `modulecapability/contracttest` to verify:

- complete, uniquely owned and deterministic categories;
- complete OpenAPI operations and exact component closure;
- direct Module versus HTTP Remote byte parity;
- validation result/error parity;
- startup rejection of stale Remote contract identity.
