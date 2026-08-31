package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryLimiterEnforcesWindowCapacityAndCancellation(t *testing.T) {
	limiter := NewMemoryLimiter(2)
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	for attempt := 1; attempt <= 3; attempt++ {
		decision, err := limiter.Allow(t.Context(), "agent:a", 2, time.Minute)
		if err != nil || decision.Allowed != (attempt <= 2) {
			t.Fatalf("attempt=%d decision=%+v err=%v", attempt, decision, err)
		}
	}
	_, _ = limiter.Allow(t.Context(), "agent:b", 1, time.Minute)
	_, _ = limiter.Allow(t.Context(), "agent:c", 1, time.Minute)
	if len(limiter.buckets) != 2 {
		t.Fatalf("capacity not enforced: %d", len(limiter.buckets))
	}
	stats := limiter.Stats()
	if stats.Entries != 2 || stats.Capacity != 2 || stats.Evictions != 1 {
		t.Fatalf("unexpected eviction stats: %+v", stats)
	}
	now = now.Add(time.Minute)
	decision, err := limiter.Allow(t.Context(), "agent:a", 2, time.Minute)
	if err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("window did not reset: %+v err=%v", decision, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := limiter.Allow(ctx, "cancelled", 1, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error=%v", err)
	}
}

func TestMemoryLimiterExpiresIdleReconstructibleBuckets(t *testing.T) {
	limiter := NewMemoryLimiter(4)
	limiter.idleTTL = time.Minute
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	_, _ = limiter.Allow(t.Context(), "old", 2, time.Minute)
	now = now.Add(2 * time.Minute)
	_, _ = limiter.Allow(t.Context(), "new", 2, time.Minute)
	stats := limiter.Stats()
	if stats.Entries != 1 || stats.Expirations != 1 {
		t.Fatalf("idle buckets were not expired: %+v", stats)
	}
}

func TestMemoryLimiterDefaultAndDegenerateBoundaries(t *testing.T) {
	limiter := NewMemoryLimiter(0)
	if limiter.capacity != DefaultMemoryCapacity {
		t.Fatalf("default capacity = %d", limiter.capacity)
	}
	for _, test := range []struct {
		limit  int
		window time.Duration
	}{
		{limit: 0, window: time.Minute},
		{limit: 1, window: 0},
	} {
		decision, err := limiter.Allow(t.Context(), "ignored", test.limit, test.window)
		if err != nil || !decision.Allowed || decision.Limit != test.limit {
			t.Fatalf("limit=%d window=%v decision=%+v error=%v", test.limit, test.window, decision, err)
		}
	}
	if stats := (*MemoryLimiter)(nil).Stats(); stats != (Stats{}) {
		t.Fatalf("nil limiter stats = %+v", stats)
	}
}

func TestMemoryLimiterHandlesZeroBucketTimesAndDisabledCleanup(t *testing.T) {
	limiter := NewMemoryLimiter(2)
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	limiter.idleTTL = 0
	limiter.buckets["zero"] = memoryBucket{}
	limiter.evictExpired(now)
	if decision, err := limiter.Allow(t.Context(), "zero", 1, time.Minute); err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("zero bucket decision=%+v error=%v", decision, err)
	}
	limiter.buckets["expired-window"] = memoryBucket{windowStart: now.Add(-2 * time.Minute), lastSeen: now, count: 2}
	if decision, err := limiter.Allow(t.Context(), "expired-window", 1, time.Minute); err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("expired window decision=%+v error=%v", decision, err)
	}

	limiter.buckets = map[string]memoryBucket{"zero-last-seen": {windowStart: now}}
	limiter.idleTTL = time.Minute
	limiter.evictExpired(now.Add(2 * time.Minute))
	if _, exists := limiter.buckets["zero-last-seen"]; !exists {
		t.Fatal("zero last-seen bucket was expired")
	}

	limiter.buckets = map[string]memoryBucket{}
	limiter.evictOldest()
	if limiter.evictions != 0 {
		t.Fatalf("empty eviction count = %d", limiter.evictions)
	}
	for attempt := 0; attempt < 32; attempt++ {
		limiter.buckets = map[string]memoryBucket{}
		for index := 0; index < 32; index++ {
			limiter.buckets[string(rune('a'+index))] = memoryBucket{lastSeen: now.Add(time.Duration(index) * time.Second)}
		}
		limiter.evictOldest()
	}
}
