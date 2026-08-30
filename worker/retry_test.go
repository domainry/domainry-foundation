package worker

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
)

type fixedJitter time.Duration

func (jitter fixedJitter) Duration(time.Duration) time.Duration { return time.Duration(jitter) }

func TestFailureClassificationAndRetryPolicy(t *testing.T) {
	if ClassifyApplicationError(context.Canceled) != FailureCancelled || ClassifyApplicationError(apperror.New(apperror.KindRateLimited, "limited", nil, nil)) != FailureRateLimited || ClassifyApplicationError(apperror.New(apperror.KindUnavailable, "down", nil, nil)) != FailureDependencyUnavailable {
		t.Fatal("worker failure classification drifted")
	}
	now := time.Date(2026, time.July, 19, 0, 0, 0, 0, time.UTC)
	policy := RetryPolicy{MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 3 * time.Second, Backoff: BackoffExponential, Deadline: now.Add(time.Minute), JitterMax: time.Second}
	if !policy.Allows(3, now) || policy.Allows(4, now) || policy.Delay(3, fixedJitter(200*time.Millisecond)) != 3200*time.Millisecond {
		t.Fatal("worker retry policy contract drifted")
	}
}
