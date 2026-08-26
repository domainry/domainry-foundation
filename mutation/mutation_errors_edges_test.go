package mutation

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMutationConflictErrorContractAndStableCodes(t *testing.T) {
	cause := errors.New("database conflict")
	tests := []struct {
		kind MutationConflictKind
		code string
	}{
		{MutationConflictUnique, "backend.mutation.unique_conflict"},
		{MutationConflictIdempotency, "backend.mutation.idempotency_conflict"},
		{MutationConflictOptimistic, "backend.mutation.optimistic_conflict"},
		{MutationConflictLeaseLost, "backend.mutation.lease_lost"},
		{"unknown", "backend.mutation.conflict"},
		{"", "backend.mutation.conflict"},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			err := MutationConflict(" record ", " id ", test.kind, cause)
			var conflict *MutationConflictError
			if !errors.As(err, &conflict) || conflict.Resource != "record" || conflict.Identifier != "id" || conflict.Kind != test.kind {
				t.Fatalf("conflict=%#v", conflict)
			}
			if !errors.Is(err, cause) || !IsMutationConflict(fmt.Errorf("wrapped: %w", err), "") || !IsMutationConflict(err, test.kind) {
				t.Fatalf("classification err=%v", err)
			}
			if test.kind != "" && IsMutationConflict(err, MutationConflictKind("different")) {
				t.Fatalf("wrong kind matched err=%v", err)
			}
			if StableConflictCode(test.kind) != test.code {
				t.Fatalf("code=%q want=%q", StableConflictCode(test.kind), test.code)
			}
			if got := err.Error(); got != "mutation conflict: kind="+string(test.kind)+" resource=record identifier=id" {
				t.Fatalf("message=%q", got)
			}
		})
	}
	if IsMutationConflict(nil, "") || IsMutationConflict(errors.New("plain"), "") {
		t.Fatal("non-conflict classified as mutation conflict")
	}
	conflictResult := PolicyConflict(" backend.custom_conflict ", " order ", " order-1 ", " status ")
	if conflict, ok := conflictResult.(*PolicyConflictError); !ok || conflict.Code != "backend.custom_conflict" || conflict.Resource != "order" || conflict.Identifier != "order-1" || conflict.Field != "status" {
		t.Fatalf("policy conflict=%#v", conflictResult)
	}
	if conflict := PolicyConflict("", "order", "order-1", "status").(*PolicyConflictError); conflict.Code != "backend.mutation.predicate_failed" {
		t.Fatalf("default policy conflict=%#v", conflict)
	}
}

func TestTransactionTransientErrorContractAndStableCodes(t *testing.T) {
	cause := errors.New("database unavailable")
	tests := []struct {
		kind TransactionTransientKind
		code string
	}{
		{TransactionTransientDeadlock, "backend.transaction.deadlock"},
		{TransactionTransientLockTimeout, "backend.transaction.lock_timeout"},
		{TransactionTransientSerializationFailure, "backend.transaction.serialization_failure"},
		{"unknown", "backend.transaction.transient_failure"},
		{"", "backend.transaction.transient_failure"},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			err := TransactionTransient(" transaction ", " key ", test.kind, cause)
			var transient *TransactionTransientError
			if !errors.As(err, &transient) || transient.Resource != "transaction" || transient.Identifier != "key" || transient.Kind != test.kind {
				t.Fatalf("transient=%#v", transient)
			}
			if !errors.Is(err, cause) || !IsTransactionTransient(fmt.Errorf("wrapped: %w", err), "") || !IsTransactionTransient(err, test.kind) {
				t.Fatalf("classification err=%v", err)
			}
			if test.kind != "" && IsTransactionTransient(err, TransactionTransientKind("different")) {
				t.Fatalf("wrong kind matched err=%v", err)
			}
			if StableTransientCode(test.kind) != test.code {
				t.Fatalf("code=%q want=%q", StableTransientCode(test.kind), test.code)
			}
			if got := err.Error(); got != "transaction transient failure: kind="+string(test.kind)+" resource=transaction identifier=key" {
				t.Fatalf("message=%q", got)
			}
		})
	}
	if IsTransactionTransient(nil, "") || IsTransactionTransient(errors.New("plain"), "") {
		t.Fatal("non-transient classified as transaction transient")
	}
}

func TestMemoryTransactionMetricsCollectorIgnoresInvalidAndTracksFailures(t *testing.T) {
	var nilCollector *MemoryTransactionMetricsCollector
	nilCollector.ObserveTransaction(TransactionObservation{Attempts: 1})
	if snapshot := nilCollector.TransactionMetricsSnapshot(); snapshot != (TransactionMetricsSnapshot{}) {
		t.Fatalf("nil snapshot=%#v", snapshot)
	}
	collector := NewMemoryTransactionMetricsCollector()
	collector.ObserveTransaction(TransactionObservation{Attempts: 0, Succeeded: true})
	collector.ObserveTransaction(TransactionObservation{WorkspaceID: " workspace ", Scope: " command ", Duration: 2 * time.Millisecond, Attempts: 3, Conflicts: 2, Rollbacks: 1, Retries: 2})
	snapshot := collector.TransactionMetricsSnapshot()
	if snapshot.Transactions != 1 || snapshot.Succeeded != 0 || snapshot.Failed != 1 || snapshot.TotalDuration != 2*time.Millisecond || snapshot.Attempts != 3 || snapshot.Conflicts != 2 || snapshot.Rollbacks != 1 || snapshot.Retries != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
