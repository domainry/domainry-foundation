package schemaownership

import "testing"

func TestTableValidationAndClone(t *testing.T) {
	tables := []Table{{
		Name: "_records", Owner: "shared/records", WorkspaceScope: ScopeWorkspace,
		RetentionClass: RetentionProduct, PrimaryKey: []string{"workspace_id", "id"},
		BoundedQueryPath: "workspace and id lookup; paginated workspace list",
		DeletionPolicy:   "owner lifecycle deletes terminal rows after references expire",
	}}
	if err := ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	clone := Clone(tables)
	clone[0].PrimaryKey[0] = "changed"
	if tables[0].PrimaryKey[0] != "workspace_id" {
		t.Fatal("schema ownership clone leaked primary-key mutation")
	}
	if names := Names(tables); len(names) != 1 || names[0] != "_records" {
		t.Fatalf("names=%v", names)
	}
	unique := []Table{{
		Name: "_unique_records", Owner: "shared/records", WorkspaceScope: ScopeInstallation,
		RetentionClass: RetentionInstallation, UniqueKey: []string{"owner", "kind"},
		BoundedQueryPath: "owner and kind", DeletionPolicy: "installation lifetime",
	}}
	if err := ValidateAll(unique); err != nil {
		t.Fatal(err)
	}
	uniqueClone := Clone(unique)
	uniqueClone[0].UniqueKey[0] = "changed"
	if unique[0].UniqueKey[0] != "owner" {
		t.Fatal("schema ownership clone leaked unique-key mutation")
	}
}

func TestTableValidationRejectsIncompleteAndDuplicateContracts(t *testing.T) {
	valid := Table{Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, BoundedQueryPath: "id", DeletionPolicy: "delete"}
	for name, value := range map[string]Table{
		"name":              {},
		"owner":             {Name: "records"},
		"scope":             {Name: "records", Owner: "records"},
		"retention":         {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace},
		"identity key":      {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct},
		"two identity keys": {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, UniqueKey: []string{"workspace_id", "id"}, BoundedQueryPath: "id", DeletionPolicy: "delete"},
		"query":             {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}},
		"deletion":          {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, BoundedQueryPath: "id"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := value.Validate(); err == nil {
				t.Fatal("incomplete schema ownership contract was accepted")
			}
		})
	}
	if err := ValidateAll([]Table{valid, valid}); err == nil {
		t.Fatal("duplicate schema ownership contract was accepted")
	}
}
