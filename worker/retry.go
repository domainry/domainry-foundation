package worker

import (
	"context"
	"errors"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
)

// FailureClass is the shared technical recovery vocabulary. Business owners
// still decide which domain/provider errors map to each class.
type FailureClass string

const (
	FailureTransient             FailureClass = "transient"
	FailureRateLimited           FailureClass = "rate_limited"
	FailureDependencyUnavailable FailureClass = "dependency_unavailable"
	FailureTerminal              FailureClass = "terminal"
	FailureCancelled             FailureClass = "cancelled"
)

func (class FailureClass) Retryable() bool {
	return class == FailureTransient || class == FailureRateLimited || class == FailureDependencyUnavailable
}

func ClassifyApplicationError(err error) FailureClass {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureCancelled
	}
	switch apperror.KindOf(err) {
	case apperror.KindRateLimited:
		return FailureRateLimited
	case apperror.KindUnavailable:
		return FailureDependencyUnavailable
	case apperror.KindInternal:
		return FailureTransient
	default:
		return FailureTerminal
	}
}

type BackoffKind string

const (
	BackoffFixed       BackoffKind = "fixed"
	BackoffExponential BackoffKind = "exponential"
)

// RetryPolicy is technical configuration supplied by a business owner. An
// attempt is one-based; Deadline is optional and never inferred by platform.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Backoff     BackoffKind
	Deadline    time.Time
	JitterMax   time.Duration
}

func (policy RetryPolicy) Allows(attempt int, now time.Time) bool {
	return attempt > 0 && policy.MaxAttempts > 0 && attempt < policy.MaxAttempts && (policy.Deadline.IsZero() || now.Before(policy.Deadline))
}

func (policy RetryPolicy) Delay(attempt int, jitter JitterSource) time.Duration {
	delay := policy.BaseDelay
	if delay < 0 {
		delay = 0
	}
	if policy.Backoff == BackoffExponential && attempt > 1 {
		for step := 1; step < attempt && delay < policy.MaxDelay; step++ {
			delay *= 2
			if delay <= 0 {
				delay = policy.MaxDelay
				break
			}
		}
	}
	if policy.MaxDelay > 0 && delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if jitter != nil && policy.JitterMax > 0 {
		delay += jitter.Duration(policy.JitterMax)
	}
	return delay
}
