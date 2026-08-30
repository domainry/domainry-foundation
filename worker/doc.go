// Package worker provides deployment-neutral durable-worker primitives.
//
// It owns process lifecycle, claim admission, lease and fencing value types,
// heartbeat supervision, retry timing, wakeup hints, bounded dispatch, metrics,
// and deterministic fault seams. Business modules remain responsible for
// durable task schemas, state transitions, error classification, compensation,
// reconciliation, and terminal policy.
package worker
