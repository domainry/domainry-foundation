package moduleinfo

import "testing"

func TestInventorySortsAndRejectsSaaSHTTP(t *testing.T) {
	value, err := NewInventory([]Descriptor{
		{Key: "party", Mode: DeploymentModeModule, Capabilities: []string{"directory"}, Persistence: Persistence{Mode: PersistenceBorrowedHost, SchemaOwner: "party"}},
		{Key: "identity", Mode: DeploymentModeSaaS, Capabilities: []string{"authentication"}, Persistence: Persistence{Mode: PersistenceServiceOwned, SchemaOwner: "identity"}},
	})
	if err != nil || value.Modules[0].Key != "identity" {
		t.Fatalf("inventory=%+v err=%v", value, err)
	}
	_, err = NewInventory([]Descriptor{{Key: "identity", Mode: DeploymentModeSaaS, Capabilities: []string{"authentication"}, HTTPAdapters: []HTTPAdapter{{Name: "invalid"}}, Persistence: Persistence{Mode: PersistenceServiceOwned, SchemaOwner: "identity"}}})
	if err == nil {
		t.Fatal("SaaS in-process HTTP was accepted")
	}
}
