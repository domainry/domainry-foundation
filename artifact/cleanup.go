package artifact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CleanupResult reports durable Artifact transitions, not Delete attempts.
// A retry after a crash can therefore repeat Blob deletion without inflating
// the result or reviving an Artifact.
type CleanupResult struct {
	Scanned int
	Expired int
	Deleted int
}

// CleanupService reconciles one registered Blob-backed owner/kind. Owner-
// managed content (for example Data Exchange job chunks) must be reconciled by
// that owner because a deployment BlobStore cannot delete it safely.
type CleanupService struct {
	artifacts ManagedStore
	content   ContentStore
}

func NewCleanupService(artifacts ManagedStore, content ContentStore) (*CleanupService, error) {
	if artifacts == nil || content == nil {
		return nil, fmt.Errorf("artifact cleanup requires metadata and content stores")
	}
	return &CleanupService{artifacts: artifacts, content: content}, nil
}

// Reconcile first makes elapsed available Artifacts terminal, then deletes
// content only for terminal expired/rejected rows, and finally records the
// deleted tombstone. If the process stops between Delete and Transition, the
// next call repeats Delete and completes the same compare-and-set transition.
func (s *CleanupService) Reconcile(ctx context.Context, owner, kind string, now time.Time, limit int) (CleanupResult, error) {
	result := CleanupResult{}
	owner, kind, now = strings.TrimSpace(owner), strings.TrimSpace(kind), now.UTC()
	registration, found := RegistrationFor(owner, kind)
	if !found || registration.ContentStorage != ContentStorageBlob {
		return result, fmt.Errorf("artifact cleanup owner %q kind %q is not registered as Blob-backed", owner, kind)
	}
	if now.IsZero() {
		return result, fmt.Errorf("artifact cleanup time is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	elapsed, err := s.artifacts.List(ctx, "", Query{
		Owner: owner, Kind: kind, Statuses: []Status{StatusAvailable},
		ExpiresAtOrBefore: now, Limit: limit,
	})
	if err != nil {
		return result, err
	}
	for _, value := range elapsed {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Scanned++
		if value.ExpiresAt.IsZero() || now.Before(value.ExpiresAt) {
			continue
		}
		changed, err := s.artifacts.Transition(ctx, value.WorkspaceID, value.ID, StatusAvailable, StatusExpired, value.ScanStatus, now)
		if err != nil {
			return result, err
		}
		if changed {
			result.Expired++
		}
	}

	terminal, err := s.artifacts.List(ctx, "", Query{
		Owner: owner, Kind: kind, Statuses: []Status{StatusExpired, StatusRejected}, Limit: limit,
	})
	if err != nil {
		return result, err
	}
	for _, value := range terminal {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Scanned++
		if err := s.content.Delete(ctx, value.WorkspaceID, value.StorageReference); err != nil && !errors.Is(err, ErrContentNotFound) {
			return result, err
		}
		changed, err := s.artifacts.Transition(ctx, value.WorkspaceID, value.ID, value.Status, StatusDeleted, value.ScanStatus, now)
		if err != nil {
			return result, err
		}
		if changed {
			result.Deleted++
			continue
		}
		current, found, err := s.artifacts.ByID(ctx, value.WorkspaceID, value.ID)
		if err != nil {
			return result, err
		}
		if found && current.Status != StatusDeleted {
			return result, fmt.Errorf("artifact %q changed while deleting terminal content", value.ID)
		}
	}
	return result, nil
}
