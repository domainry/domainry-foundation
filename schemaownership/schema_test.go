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
}

func TestTableValidationRejectsIncompleteAndDuplicateContracts(t *testing.T) {
	valid := Table{Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, BoundedQueryPath: "id", DeletionPolicy: "delete"}
	for name, value := range map[string]Table{
		"name":        {},
		"owner":       {Name: "records"},
		"scope":       {Name: "records", Owner: "records"},
		"retention":   {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace},
		"primary key": {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct},
		"query":       {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}},
		"deletion":    {Name: "records", Owner: "records", WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, BoundedQueryPath: "id"},
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
