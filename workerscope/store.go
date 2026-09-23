package workerscope

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
)

var scopeColumns = []string{
	"id", "owner", "scope_key", "cursor", "checkpoint", "capacity", "lease_owner", "lease_expires_at",
	"fencing_token", "last_started_at", "last_completed_at", "last_error", "updated_at",
}

type Store struct {
	database Database
	dialect  Dialect
}

func NewStore(database Database, dialect Dialect) *Store {
	return &Store{database: database, dialect: dialect}
}

func (s *Store) validate() error {
	if s == nil || s.database == nil || s.dialect == nil {
		return fmt.Errorf("shared Worker Scope SQL store is incomplete")
	}
	return nil
}

func (s *Store) executor(value Executor) Executor {
	if value != nil {
		return value
	}
	return s.database
}

func (s *Store) queryer(value Queryer) Queryer {
	if value != nil {
		return value
	}
	return s.database
}

// Register records a payload-free worker discovery scope. The update/insert/
// update sequence is portable across all supported dialects and handles a
// concurrent first registration without duplicating dialect-specific upserts.
func (s *Store) Register(ctx context.Context, executor Executor, identity Identity, updatedAt time.Time) error {
	if err := s.validate(); err != nil {
		return err
	}
	identity = identity.normalized()
	if err := identity.validate(); err != nil {
		return err
	}
	now := timestamp(updatedAt)
	target := s.executor(executor)
	update, updateArgs, err := query.NewUpdateBuilder(s.dialect, TableName).Set("updated_at", now).
		Where(identityPredicate(identity)).Build()
	if err != nil {
		return fmt.Errorf("build worker scope refresh: %w", err)
	}
	result, err := target.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return fmt.Errorf("refresh worker scope: %w", err)
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if changed > 0 {
		return nil
	}
	insert, insertArgs, err := query.NewInsertBuilder(s.dialect, TableName).
		Columns("id", "owner", "scope_key", "last_error", "updated_at").Values(identity.ID, identity.Owner, identity.ScopeKey, "", now).Build()
	if err != nil {
		return fmt.Errorf("build worker scope registration: %w", err)
	}
	if _, err = target.ExecContext(ctx, insert, insertArgs...); err == nil {
		return nil
	}
	retried, retryErr := target.ExecContext(ctx, update, updateArgs...)
	if retryErr == nil {
		if changed, rowsErr := retried.RowsAffected(); rowsErr == nil && changed > 0 {
			return nil
		}
	}
	return fmt.Errorf("register worker scope: %w", err)
}

// LockCapacity ensures and locks the one transactional guard row for owner and
// scope. The idempotent UPDATE is the portable write lock used by all modules.
func (s *Store) LockCapacity(ctx context.Context, executor DBTX, identity Identity, updatedAt time.Time) error {
	if err := s.validate(); err != nil {
		return err
	}
	if executor == nil {
		return fmt.Errorf("worker scope capacity transaction is required")
	}
	identity = identity.normalized()
	if err := identity.validate(); err != nil {
		return err
	}
	now := timestamp(updatedAt)
	update, updateArgs, err := query.NewUpdateBuilder(s.dialect, TableName).
		SetExpression("checkpoint", query.Add(query.Column("checkpoint"), query.Value(1))).
		Set("updated_at", now).Where(identityPredicate(identity)).Build()
	if err != nil {
		return err
	}
	result, err := executor.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return err
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if changed > 0 {
		return nil
	}
	insert, insertArgs, err := query.NewInsertBuilder(s.dialect, TableName).
		Columns("id", "owner", "scope_key", "checkpoint", "last_error", "updated_at").
		Values(identity.ID, identity.Owner, identity.ScopeKey, int64(1), "", now).Build()
	if err != nil {
		return err
	}
	if _, err = executor.ExecContext(ctx, insert, insertArgs...); err == nil {
		return nil
	}
	result, retryErr := executor.ExecContext(ctx, update, updateArgs...)
	if retryErr != nil {
		return err
	}
	changed, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return rowsErr
	}
	if changed != 1 {
		return fmt.Errorf("worker scope capacity guard is unavailable")
	}
	return nil
}

func (s *Store) ScopeKeys(ctx context.Context, source Queryer, filter ScopeQuery) ([]string, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	filter.Owner, filter.AfterKey = strings.TrimSpace(filter.Owner), strings.TrimSpace(filter.AfterKey)
	if _, found := RegistrationFor(filter.Owner); !found {
		return nil, fmt.Errorf("worker scope owner %q is not registered", filter.Owner)
	}
	predicate := query.Predicate(query.Equal("owner", filter.Owner))
	if filter.AfterKey != "" {
		if filter.Order != ScopeKeyAscending {
			return nil, fmt.Errorf("worker scope cursor requires scope-key order")
		}
		predicate = query.And(predicate, query.GreaterThan("scope_key", filter.AfterKey))
	}
	builder := query.NewSelectBuilder(s.dialect, TableName).Columns("scope_key").Where(predicate)
	switch filter.Order {
	case 0, ScopeKeyAscending:
		builder.OrderBy(query.Ascending("scope_key"))
	case UpdatedAtAscending:
		builder.OrderBy(query.Ascending("updated_at"), query.Ascending("scope_key"))
	case UpdatedAtDescending:
		builder.OrderBy(query.Descending("updated_at"), query.Ascending("scope_key"))
	default:
		return nil, fmt.Errorf("worker scope order is invalid")
	}
	if filter.Limit > 0 {
		builder.Limit(filter.Limit)
	}
	statement, args, err := builder.Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.queryer(source).QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values, rows.Err()
}

func (s *Store) ClaimLease(ctx context.Context, executor DBTX, claim LeaseClaim) (Scope, bool, error) {
	if err := s.validate(); err != nil {
		return Scope{}, false, err
	}
	if executor == nil {
		executor = s.database
	}
	claim.Identity = claim.Identity.normalized()
	claim.LeaseOwner = strings.TrimSpace(claim.LeaseOwner)
	if err := claim.Identity.validate(); err != nil {
		return Scope{}, false, err
	}
	if claim.LeaseOwner == "" || claim.Now.IsZero() || claim.LeaseExpiresAt.IsZero() || !claim.LeaseExpiresAt.After(claim.Now) {
		return Scope{}, false, fmt.Errorf("worker scope lease claim is invalid")
	}
	now, expires := timestamp(claim.Now), timestamp(claim.LeaseExpiresAt)
	statement, args, err := query.NewUpdateBuilder(s.dialect, TableName).
		Set("lease_owner", claim.LeaseOwner).Set("lease_expires_at", expires).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("last_started_at", now).Set("last_error", "").Set("updated_at", now).
		Where(query.And(identityPredicate(claim.Identity), query.Or(query.Equal("lease_owner", ""), query.LessThanOrEqual("lease_expires_at", now)))).Build()
	if err != nil {
		return Scope{}, false, err
	}
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return Scope{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return Scope{}, false, err
	}
	value, found, err := s.Get(ctx, executor, claim.Identity)
	return value, found, err
}

func (s *Store) CompleteLease(ctx context.Context, executor Executor, completion LeaseCompletion) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	completion.Identity = completion.Identity.normalized()
	completion.LeaseOwner = strings.TrimSpace(completion.LeaseOwner)
	if err := completion.Identity.validate(); err != nil {
		return false, err
	}
	if completion.LeaseOwner == "" || completion.FencingToken < 1 || completion.CompletedAt.IsZero() {
		return false, fmt.Errorf("worker scope lease completion is invalid")
	}
	completedAt := timestamp(completion.CompletedAt)
	statement, args, err := query.NewUpdateBuilder(s.dialect, TableName).
		Set("lease_owner", "").Set("lease_expires_at", "").Set("last_completed_at", completedAt).
		Set("checkpoint", completion.Checkpoint).Set("updated_at", completedAt).
		Where(leasePredicate(completion.Identity, completion.LeaseOwner, completion.FencingToken)).Build()
	if err != nil {
		return false, err
	}
	result, err := s.executor(executor).ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *Store) FailLease(ctx context.Context, executor Executor, failure LeaseFailure) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	failure.Identity = failure.Identity.normalized()
	failure.LeaseOwner = strings.TrimSpace(failure.LeaseOwner)
	if err := failure.Identity.validate(); err != nil {
		return false, err
	}
	if failure.LeaseOwner == "" || failure.FencingToken < 1 || failure.FailedAt.IsZero() || failure.Cause == nil {
		return false, fmt.Errorf("worker scope lease failure is invalid")
	}
	now := timestamp(failure.FailedAt)
	statement, args, err := query.NewUpdateBuilder(s.dialect, TableName).
		Set("lease_owner", "").Set("lease_expires_at", "").Set("last_error", failure.Cause.Error()).Set("updated_at", now).
		Where(leasePredicate(failure.Identity, failure.LeaseOwner, failure.FencingToken)).Build()
	if err != nil {
		return false, err
	}
	result, err := s.executor(executor).ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *Store) Get(ctx context.Context, source Queryer, identity Identity) (Scope, bool, error) {
	if err := s.validate(); err != nil {
		return Scope{}, false, err
	}
	identity = identity.normalized()
	if err := identity.validate(); err != nil {
		return Scope{}, false, err
	}
	statement, args, err := query.NewSelectBuilder(s.dialect, TableName).Columns(scopeColumns...).Where(identityPredicate(identity)).Build()
	if err != nil {
		return Scope{}, false, err
	}
	value, err := scanScope(s.queryer(source).QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, false, nil
	}
	return value, err == nil, err
}

func (s *Store) ActiveLeaseGuard(identity Identity, leaseOwner string, fencingToken int64, now time.Time) (*query.SelectBuilder, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	identity = identity.normalized()
	if err := identity.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(leaseOwner) == "" || fencingToken < 1 || now.IsZero() {
		return nil, fmt.Errorf("worker scope active lease guard is invalid")
	}
	return query.NewSelectBuilder(s.dialect, TableName).Columns("id").Where(query.And(
		leasePredicate(identity, leaseOwner, fencingToken), query.GreaterThan("lease_expires_at", timestamp(now)),
	)), nil
}

func (s *Store) CountLeases(ctx context.Context, source Queryer, owner, leaseOwnerPrefix string, now time.Time) (LeaseCounts, error) {
	if err := s.validate(); err != nil {
		return LeaseCounts{}, err
	}
	owner, leaseOwnerPrefix = strings.TrimSpace(owner), strings.TrimSpace(leaseOwnerPrefix)
	if _, found := RegistrationFor(owner); !found || now.IsZero() {
		return LeaseCounts{}, fmt.Errorf("worker scope lease count is invalid")
	}
	predicate := query.Predicate(query.And(query.Equal("owner", owner), query.NotEqual("lease_owner", "")))
	if leaseOwnerPrefix != "" {
		predicate = query.And(predicate, query.Or(
			query.Equal("lease_owner", leaseOwnerPrefix), query.Like("lease_owner", leaseOwnerPrefix+":%"), query.Like("lease_owner", leaseOwnerPrefix+"-%"),
		))
	}
	nowText := timestamp(now)
	live := query.Coalesce(query.Sum(query.CaseWhen(query.GreaterThan("lease_expires_at", nowText), 1).Else(0)), query.Value(0))
	expired := query.Coalesce(query.Sum(query.CaseWhen(query.And(query.NotEqual("lease_expires_at", ""), query.LessThanOrEqual("lease_expires_at", nowText)), 1).Else(0)), query.Value(0))
	statement, args, err := query.NewSelectBuilder(s.dialect, TableName).
		Projections(query.Project(live), query.Project(expired)).Where(predicate).Build()
	if err != nil {
		return LeaseCounts{}, err
	}
	var liveValue, expiredValue sql.NullInt64
	if err := s.queryer(source).QueryRowContext(ctx, statement, args...).Scan(&liveValue, &expiredValue); err != nil {
		return LeaseCounts{}, err
	}
	return LeaseCounts{Live: liveValue.Int64, Expired: expiredValue.Int64}, nil
}

func (s *Store) ForceReleaseLease(ctx context.Context, executor DBTX, release LeaseRelease) (LeaseReleaseResult, bool, error) {
	if err := s.validate(); err != nil {
		return LeaseReleaseResult{}, false, err
	}
	if executor == nil {
		return LeaseReleaseResult{}, false, fmt.Errorf("worker scope lease release transaction is required")
	}
	release.Identity = release.Identity.normalized()
	release.ExpectedLeaseOwner = strings.TrimSpace(release.ExpectedLeaseOwner)
	if release.Identity.ID == "" || release.Identity.Owner == "" || release.ExpectedLeaseOwner == "" || release.ExpectedFencingToken < 1 || release.Now.IsZero() {
		return LeaseReleaseResult{}, false, fmt.Errorf("worker scope lease release is invalid")
	}
	if _, found := RegistrationFor(release.Identity.Owner); !found {
		return LeaseReleaseResult{}, false, fmt.Errorf("worker scope owner %q is not registered", release.Identity.Owner)
	}
	predicate := releaseIdentityPredicate(release.Identity)
	statement, args, err := query.NewSelectBuilder(s.dialect, TableName).
		Columns("lease_owner", "lease_expires_at", "fencing_token").Where(predicate).Build()
	if err != nil {
		return LeaseReleaseResult{}, false, err
	}
	var currentOwner, currentExpiresAt string
	var currentToken int64
	if err := executor.QueryRowContext(ctx, statement, args...).Scan(&currentOwner, &currentExpiresAt, &currentToken); errors.Is(err, sql.ErrNoRows) {
		return LeaseReleaseResult{}, false, nil
	} else if err != nil {
		return LeaseReleaseResult{}, false, err
	}
	if currentOwner != release.ExpectedLeaseOwner || currentToken != release.ExpectedFencingToken {
		return LeaseReleaseResult{}, false, nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, currentExpiresAt)
	if err != nil {
		return LeaseReleaseResult{}, false, fmt.Errorf("worker scope lease expiry is invalid: %w", err)
	}
	eligibility := "expired"
	if expiresAt.After(release.Now) {
		if !release.AllowUnexpired {
			return LeaseReleaseResult{}, false, nil
		}
		eligibility = "verified_stuck"
	}
	statement, args, err = query.NewUpdateBuilder(s.dialect, TableName).
		Set("lease_owner", "").Set("lease_expires_at", "").
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", timestamp(release.Now)).
		Where(query.And(predicate, query.Equal("lease_owner", release.ExpectedLeaseOwner), query.Equal("fencing_token", release.ExpectedFencingToken))).Build()
	if err != nil {
		return LeaseReleaseResult{}, false, err
	}
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return LeaseReleaseResult{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return LeaseReleaseResult{}, false, err
	}
	return LeaseReleaseResult{
		PreviousLeaseOwner: currentOwner, PreviousFencingToken: currentToken,
		NextFencingToken: currentToken + 1, PreviousLeaseExpiresAt: expiresAt, Eligibility: eligibility,
	}, true, nil
}

func identityPredicate(identity Identity) query.Predicate {
	return query.And(query.Equal("id", identity.ID), query.Equal("owner", identity.Owner), query.Equal("scope_key", identity.ScopeKey))
}

func releaseIdentityPredicate(identity Identity) query.Predicate {
	predicate := query.Predicate(query.And(query.Equal("id", identity.ID), query.Equal("owner", identity.Owner)))
	if identity.ScopeKey != "" {
		predicate = query.And(predicate, query.Equal("scope_key", identity.ScopeKey))
	}
	return predicate
}

func leasePredicate(identity Identity, owner string, fencingToken int64) query.Predicate {
	return query.And(identityPredicate(identity), query.Equal("lease_owner", strings.TrimSpace(owner)), query.Equal("fencing_token", fencingToken))
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func scanScope(row interface{ Scan(...any) error }) (Scope, error) {
	var value Scope
	err := row.Scan(
		&value.ID, &value.Owner, &value.ScopeKey, &value.Cursor, &value.Checkpoint, &value.Capacity,
		&value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.LastStartedAt,
		&value.LastCompletedAt, &value.LastError, &value.UpdatedAt,
	)
	return value, err
}
