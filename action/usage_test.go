package action

import (
	"testing"
)

func TestRegistryQueriesPermissionUsagesByOwnerAsOneBatch(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(
		permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"}),
		permissionAction("orders.create", HTTPBinding{Method: "POST", RouteTemplate: "/orders"}),
	); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	request, err := NewPermissionUsageRequest([]PermissionUsageQuery{
		{SourceOwner: "module:missing", PermissionKeys: []string{"missing.read"}},
		{SourceOwner: testOwner, PermissionKeys: []string{"orders.read", "orders.retired"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.QueryPermissionUsages(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	if snapshot.RegistryRevision != registry.Revision() || len(snapshot.Owners) != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Owners[0].SourceOwner != "module:missing" || snapshot.Owners[0].Available || len(snapshot.Owners[0].Usages) != 0 {
		t.Fatalf("missing owner=%#v", snapshot.Owners[0])
	}
	if snapshot.Owners[1].SourceOwner != testOwner || !snapshot.Owners[1].Available || len(snapshot.Owners[1].Usages) != 1 || snapshot.Owners[1].Usages[0].Action.Key != "orders.read" {
		t.Fatalf("available owner=%#v", snapshot.Owners[1])
	}
}

func TestPermissionUsageSnapshotRejectsAuthorityAndQueryDrift(t *testing.T) {
	request, err := NewPermissionUsageRequest([]PermissionUsageQuery{{SourceOwner: testOwner, PermissionKeys: []string{"orders.read"}}})
	if err != nil {
		t.Fatal(err)
	}
	action := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"})
	valid := PermissionUsageSnapshot{
		ContractVersion:  PermissionUsageContractVersion,
		RegistryRevision: "revision",
		Owners: []PermissionUsageOwnerSnapshot{{SourceOwner: testOwner, Available: true, Usages: []PermissionUsage{{
			Permission: *action.Permission,
			Action:     action,
		}}}},
	}
	if err := valid.ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	invalidOwner := valid
	invalidOwner.Owners = append([]PermissionUsageOwnerSnapshot(nil), valid.Owners...)
	invalidOwner.Owners[0].SourceOwner = "other:owner"
	if err := invalidOwner.ValidateFor(request); err == nil {
		t.Fatal("snapshot for an unrequested owner was accepted")
	}
	unavailableWithUsage := valid
	unavailableWithUsage.Owners = append([]PermissionUsageOwnerSnapshot(nil), valid.Owners...)
	unavailableWithUsage.Owners[0].Available = false
	if err := unavailableWithUsage.ValidateFor(request); err == nil {
		t.Fatal("unavailable owner with usages was accepted")
	}
}
