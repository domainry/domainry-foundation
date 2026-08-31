package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegistrySeparatesCriticalAndOptionalChecks(t *testing.T) {
	registry := NewRegistry()
	snapshot := registry.Evaluate(t.Context(), []Check{
		{Name: "database", Criticality: Critical, Run: func(context.Context) error { return nil }},
		{Name: "connector", Criticality: Optional, Run: func(context.Context) error { return errors.New("unavailable") }},
	})
	if snapshot.Status != "degraded" || snapshot.Checks[0].LastErrorCode != "check_failed" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	snapshot = registry.Evaluate(t.Context(), []Check{{Name: "database", Criticality: Critical, Run: func(context.Context) error { return errors.New("down") }}})
	if snapshot.Status != "unavailable" || snapshot.Checks[0].LastSuccessAt == "" {
		t.Fatalf("last success/error state was not retained: %#v", snapshot)
	}
}

func TestRegistryBoundsCheckDuration(t *testing.T) {
	registry := NewRegistry()
	snapshot := registry.Evaluate(t.Context(), []Check{{Name: "slow", Criticality: Critical, Timeout: time.Millisecond, Run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}})
	if snapshot.Status != "unavailable" || snapshot.Checks[0].LastErrorCode != "timeout" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestRegistryNilAndRepeatedOptionalChecks(t *testing.T) {
	registry := NewRegistry()
	snapshot := registry.Evaluate(t.Context(), []Check{
		{Name: "nil", Criticality: Optional},
		{Name: "optional-a", Criticality: Optional, Run: func(context.Context) error { return errors.New("a") }},
		{Name: "optional-b", Criticality: Optional, Run: func(context.Context) error { return errors.New("b") }},
	})
	if snapshot.Status != "degraded" || snapshot.Checks[0].Name != "nil" || snapshot.Checks[0].Status != "ok" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
