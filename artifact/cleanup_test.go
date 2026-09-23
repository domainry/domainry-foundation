package artifact

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCleanupServiceTransitionsBeforeIdempotentBlobDeletion(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := newCleanupTestStore()
	store.values[cleanupTestKey("workspace", "elapsed")] = Artifact{
		ID: "elapsed", WorkspaceID: "workspace", Owner: OwnerAudit, Kind: "export",
		StorageReference: "elapsed.bin", Status: StatusAvailable, ScanStatus: ScanNotRequired,
		ExpiresAt: now.Add(-time.Second), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	store.values[cleanupTestKey("workspace", "future")] = Artifact{
		ID: "future", WorkspaceID: "workspace", Owner: OwnerAudit, Kind: "export",
		StorageReference: "future.bin", Status: StatusAvailable, ScanStatus: ScanNotRequired,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	store.content[cleanupTestKey("workspace", "elapsed.bin")] = []byte("elapsed")
	store.content[cleanupTestKey("workspace", "future.bin")] = []byte("future")
	service, err := NewCleanupService(store, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Reconcile(t.Context(), OwnerAudit, "export", now, 10)
	if err != nil || result.Expired != 1 || result.Deleted != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if store.values[cleanupTestKey("workspace", "elapsed")].Status != StatusDeleted || store.deleteStatuses[0] != StatusExpired {
		t.Fatalf("cleanup did not transition before Delete: values=%#v statuses=%v", store.values, store.deleteStatuses)
	}
	if _, found := store.content[cleanupTestKey("workspace", "elapsed.bin")]; found {
		t.Fatal("elapsed content remains")
	}
	if _, found := store.content[cleanupTestKey("workspace", "future.bin")]; !found {
		t.Fatal("future content was deleted")
	}
	result, err = service.Reconcile(t.Context(), OwnerAudit, "export", now, 10)
	if err != nil || result.Expired != 0 || result.Deleted != 0 {
		t.Fatalf("idempotent replay result=%#v err=%v", result, err)
	}
}

func TestCleanupServiceRetriesDeleteFromTerminalState(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := newCleanupTestStore()
	store.values[cleanupTestKey("workspace", "rejected")] = Artifact{
		ID: "rejected", WorkspaceID: "workspace", Owner: OwnerAudit, Kind: "export",
		StorageReference: "rejected.bin", Status: StatusRejected, ScanStatus: ScanRejected,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	store.content[cleanupTestKey("workspace", "rejected.bin")] = []byte("rejected")
	store.deleteError = errors.New("temporary delete failure")
	service, _ := NewCleanupService(store, store)
	if _, err := service.Reconcile(t.Context(), OwnerAudit, "export", now, 10); !errors.Is(err, store.deleteError) {
		t.Fatalf("first cleanup err=%v", err)
	}
	if store.values[cleanupTestKey("workspace", "rejected")].Status != StatusRejected {
		t.Fatal("failed physical cleanup advanced metadata")
	}
	store.deleteError = nil
	result, err := service.Reconcile(t.Context(), OwnerAudit, "export", now, 10)
	if err != nil || result.Deleted != 1 || store.values[cleanupTestKey("workspace", "rejected")].Status != StatusDeleted {
		t.Fatalf("retry result=%#v status=%q err=%v", result, store.values[cleanupTestKey("workspace", "rejected")].Status, err)
	}
	if _, err := service.Reconcile(t.Context(), OwnerDataExchange, "output", now, 10); err == nil {
		t.Fatal("owner-managed content entered Blob cleanup")
	}
}

type cleanupTestStore struct {
	values         map[string]Artifact
	content        map[string][]byte
	deleteError    error
	deleteStatuses []Status
}

func newCleanupTestStore() *cleanupTestStore {
	return &cleanupTestStore{values: map[string]Artifact{}, content: map[string][]byte{}}
}

func cleanupTestKey(workspaceID, id string) string { return workspaceID + "\x00" + id }

func (*cleanupTestStore) Register(context.Context, Artifact) (Artifact, bool, error) {
	panic("unexpected Register")
}
func (s *cleanupTestStore) ByID(_ context.Context, workspaceID, id string) (Artifact, bool, error) {
	value, found := s.values[cleanupTestKey(workspaceID, id)]
	return value, found, nil
}
func (*cleanupTestStore) ByDownloadTokenHash(context.Context, string, string) (Artifact, bool, error) {
	panic("unexpected ByDownloadTokenHash")
}
func (s *cleanupTestStore) Transition(_ context.Context, workspaceID, id string, expected, next Status, scan ScanStatus, at time.Time) (bool, error) {
	key := cleanupTestKey(workspaceID, id)
	value, found := s.values[key]
	if !found || value.Status != expected {
		return false, nil
	}
	value.Status, value.ScanStatus, value.UpdatedAt = next, scan, at
	s.values[key] = value
	return true, nil
}
func (*cleanupTestStore) Bind(context.Context, Binding) (Binding, bool, error) {
	panic("unexpected Bind")
}
func (*cleanupTestStore) Bindings(context.Context, string, string) ([]Binding, error) {
	panic("unexpected Bindings")
}
func (s *cleanupTestStore) List(_ context.Context, workspaceID string, query Query) ([]Artifact, error) {
	statuses := map[Status]bool{}
	for _, status := range query.Statuses {
		statuses[status] = true
	}
	result := []Artifact{}
	for _, value := range s.values {
		if workspaceID != "" && value.WorkspaceID != workspaceID || query.Owner != "" && value.Owner != query.Owner || query.Kind != "" && value.Kind != query.Kind || len(statuses) > 0 && !statuses[value.Status] {
			continue
		}
		if !query.ExpiresAtOrBefore.IsZero() && (value.ExpiresAt.IsZero() || value.ExpiresAt.After(query.ExpiresAtOrBefore)) {
			continue
		}
		result = append(result, value)
	}
	return result, nil
}
func (*cleanupTestStore) Update(context.Context, Mutation) (bool, error) {
	panic("unexpected Update")
}
func (s *cleanupTestStore) Open(_ context.Context, workspaceID, reference string) (io.ReadCloser, error) {
	value, found := s.content[cleanupTestKey(workspaceID, reference)]
	if !found {
		return nil, ErrContentNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}
func (s *cleanupTestStore) Stat(_ context.Context, workspaceID, reference string) (ContentInfo, error) {
	value, found := s.content[cleanupTestKey(workspaceID, reference)]
	if !found {
		return ContentInfo{}, ErrContentNotFound
	}
	return ContentInfo{Reference: reference, Size: int64(len(value))}, nil
}
func (s *cleanupTestStore) Delete(_ context.Context, workspaceID, reference string) error {
	for _, value := range s.values {
		if value.WorkspaceID == workspaceID && value.StorageReference == reference {
			s.deleteStatuses = append(s.deleteStatuses, value.Status)
			break
		}
	}
	if s.deleteError != nil {
		return s.deleteError
	}
	key := cleanupTestKey(workspaceID, reference)
	if _, found := s.content[key]; !found {
		return ErrContentNotFound
	}
	delete(s.content, key)
	return nil
}
