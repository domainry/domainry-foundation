package ratelimit

import (
	"context"
	"sync"
	"time"
)

const DefaultMemoryCapacity = 10_000
const DefaultMemoryIdleTTL = 30 * time.Minute

type Stats struct {
	Entries     int
	Capacity    int
	Evictions   uint64
	Expirations uint64
}

type Decision struct {
	Allowed    bool
	Count      int
	Limit      int
	RetryAfter time.Duration
}

// Limiter is ctx-first so a production shared-backend implementation can
// cancel network or database I/O. MemoryLimiter is intended for dev and tests.
type Limiter interface {
	Allow(context.Context, string, int, time.Duration) (Decision, error)
}

type memoryBucket struct {
	windowStart time.Time
	lastSeen    time.Time
	count       int
}

type MemoryLimiter struct {
	mu          sync.Mutex
	buckets     map[string]memoryBucket
	capacity    int
	idleTTL     time.Duration
	evictions   uint64
	expirations uint64
	now         func() time.Time
}

func NewMemoryLimiter(capacity int) *MemoryLimiter {
	if capacity <= 0 {
		capacity = DefaultMemoryCapacity
	}
	return &MemoryLimiter{buckets: map[string]memoryBucket{}, capacity: capacity, idleTTL: DefaultMemoryIdleTTL, now: time.Now}
}

func (l *MemoryLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if limit <= 0 || window <= 0 {
		return Decision{Allowed: true, Limit: limit}, nil
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evictExpired(now)
	bucket, exists := l.buckets[key]
	if !exists && len(l.buckets) >= l.capacity {
		l.evictOldest()
	}
	if !exists || bucket.windowStart.IsZero() || now.Sub(bucket.windowStart) >= window {
		bucket = memoryBucket{windowStart: now}
	}
	bucket.count++
	bucket.lastSeen = now
	l.buckets[key] = bucket
	decision := Decision{Allowed: bucket.count <= limit, Count: bucket.count, Limit: limit}
	if !decision.Allowed {
		decision.RetryAfter = window - now.Sub(bucket.windowStart)
	}
	return decision, nil
}

func (l *MemoryLimiter) evictOldest() {
	oldestKey := ""
	oldest := time.Unix(1<<62, 0)
	for key, bucket := range l.buckets {
		if bucket.lastSeen.Before(oldest) {
			oldestKey, oldest = key, bucket.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
		l.evictions++
	}
}

func (l *MemoryLimiter) evictExpired(now time.Time) {
	if l.idleTTL <= 0 {
		return
	}
	for key, bucket := range l.buckets {
		if !bucket.lastSeen.IsZero() && now.Sub(bucket.lastSeen) >= l.idleTTL {
			delete(l.buckets, key)
			l.expirations++
		}
	}
}

func (l *MemoryLimiter) Stats() Stats {
	if l == nil {
		return Stats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{Entries: len(l.buckets), Capacity: l.capacity, Evictions: l.evictions, Expirations: l.expirations}
}
