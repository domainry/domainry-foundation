package schemaownership

import "testing"

func TestTableValidationAndClone(t *testing.T) {
	tables := []Table{{
		Name: "_records", Owner: "shared/records", Kind: KindRelation, WorkspaceScope: ScopeWorkspace,
		RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, UniqueKey: []string{"workspace_id", "record_id"},
		BoundedQueryPath: "workspace and id lookup; paginated workspace list",
		DeletionPolicy:   "owner lifecycle deletes terminal rows after references expire",
	}}
	if err := ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	clone := Clone(tables)
	clone[0].PrimaryKey[0] = "changed"
	if tables[0].PrimaryKey[0] != "id" {
		t.Fatal("schema ownership clone leaked primary-key mutation")
	}
	clone[0].UniqueKey[0] = "changed"
	if tables[0].UniqueKey[0] != "workspace_id" {
		t.Fatal("schema ownership clone leaked unique-key mutation")
	}
	if names := Names(tables); len(names) != 1 || names[0] != "_records" {
		t.Fatalf("names=%v", names)
	}
	unique := []Table{{
		Name: "_unique_records", Owner: "shared/records", Kind: KindState, WorkspaceScope: ScopeInstallation,
		RetentionClass: RetentionInstallation, PrimaryKey: []string{"id"}, UniqueKey: []string{"owner", "kind"},
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
	valid := Table{Name: "records", Owner: "records", Kind: KindEntity, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, EntityIDColumn: "record_id", EntityIDPrefix: "rec", BusinessKey: []string{"record_id"}, BoundedQueryPath: "id", DeletionPolicy: "delete"}
	for name, value := range map[string]Table{
		"name":              {},
		"owner":             {Name: "records"},
		"kind":              {Name: "records", Owner: "records"},
		"scope":             {Name: "records", Owner: "records", Kind: KindEntity},
		"retention":         {Name: "records", Owner: "records", Kind: KindEntity, WorkspaceScope: ScopeWorkspace},
		"identity key":      {Name: "records", Owner: "records", Kind: KindEntity, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct},
		"composite primary": {Name: "records", Owner: "records", Kind: KindRelation, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"workspace_id", "record_id"}, BoundedQueryPath: "id", DeletionPolicy: "delete"},
		"two identity keys": {Name: "records", Owner: "records", Kind: KindEntity, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, UniqueKey: []string{"workspace_id", "id"}, BoundedQueryPath: "id", DeletionPolicy: "delete"},
		"query":             {Name: "records", Owner: "records", Kind: KindRelation, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}},
		"deletion":          {Name: "records", Owner: "records", Kind: KindRelation, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, BoundedQueryPath: "id"},
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

func TestEntityAndNonEntityIdentityRules(t *testing.T) {
	base := Table{Name: "roles", Owner: "identity", Kind: KindEntity, WorkspaceScope: ScopeWorkspace, RetentionClass: RetentionProduct, PrimaryKey: []string{"id"}, EntityIDColumn: "role_id", EntityIDPrefix: "role", BusinessKey: []string{"role_id"}, BoundedQueryPath: "role", DeletionPolicy: "delete"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	invalidEntity := base
	invalidEntity.PrimaryKey = []string{"workspace_id", "role_id"}
	if err := invalidEntity.Validate(); err == nil {
		t.Fatal("entity with a composite physical primary key was accepted")
	}
	nonEntity := base
	nonEntity.Kind = KindRelation
	if err := nonEntity.Validate(); err == nil {
		t.Fatal("relation table with an entity ID was accepted")
	}
	invalidPrefix := base
	invalidPrefix.EntityIDPrefix = "identity_role"
	if err := invalidPrefix.Validate(); err == nil {
		t.Fatal("long or punctuated entity ID prefix was accepted")
	}
	duplicatePrefix := base
	duplicatePrefix.Name = "archived_roles"
	if err := ValidateAll([]Table{base, duplicatePrefix}); err == nil {
		t.Fatal("duplicate entity ID prefix was accepted")
	}
}
