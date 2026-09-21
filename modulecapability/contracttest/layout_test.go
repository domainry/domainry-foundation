package contracttest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRepositoryLayoutAcceptsOnlyCanonicalConstruction(t *testing.T) {
	root := layoutFixture(t)
	writeLayoutFile(t, filepath.Join(root, "capability", "contract.go"), `package capability
import "github.com/domainry/domainry-foundation/modulecapability"
var summary = modulecapability.ModuleSummary{Name: "fixture"}
`)
	writeLayoutFile(t, filepath.Join(root, "internal", "adapter", "binding.go"), `package adapter
import "github.com/domainry/domainry-foundation/modulecapability"
func missing() modulecapability.ModuleSummary { return modulecapability.ModuleSummary{} }
`)
	if err := validateRepositoryLayout(root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRepositoryLayoutRejectsSecondSummaryAuthority(t *testing.T) {
	root := layoutFixture(t)
	writeLayoutFile(t, filepath.Join(root, "capability", "contract.go"), `package capability`)
	stray := filepath.Join(root, "internal", "transport", "capability.go")
	writeLayoutFile(t, stray, `package transport
import "github.com/domainry/domainry-foundation/modulecapability"
var summary = modulecapability.ModuleSummary{Name: "duplicate"}
`)
	err := validateRepositoryLayout(root)
	if err == nil || !strings.Contains(err.Error(), stray) {
		t.Fatalf("duplicate summary error=%v", err)
	}
}

func TestValidateRepositoryLayoutRejectsSecondStaticBindingAuthority(t *testing.T) {
	root := layoutFixture(t)
	writeLayoutFile(t, filepath.Join(root, "capability", "contract.go"), `package capability`)
	stray := filepath.Join(root, "internal", "assembly", "capabilities.go")
	writeLayoutFile(t, stray, `package assembly
import "github.com/domainry/domainry-foundation/modulecapability"
func open() { modulecapability.NewStaticBinding(modulecapability.ModuleSummary{}, nil, nil) }
`)
	err := validateRepositoryLayout(root)
	if err == nil || !strings.Contains(err.Error(), stray) {
		t.Fatalf("duplicate binding error=%v", err)
	}
}

func TestValidateRepositoryLayoutRejectsMissingGuideIndex(t *testing.T) {
	root := t.TempDir()
	writeLayoutFile(t, filepath.Join(root, "capability", "capability.go"), `package capability`)
	writeLayoutFile(t, filepath.Join(root, "capability", "contract.go"), `package capability`)
	err := validateRepositoryLayout(root)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("capability", "agent", "index.json")) {
		t.Fatalf("missing guide index error=%v", err)
	}
}

func layoutFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLayoutFile(t, filepath.Join(root, "capability", "capability.go"), `package capability`)
	writeLayoutFile(t, filepath.Join(root, "capability", "agent", "index.json"), `{}`)
	return root
}

func writeLayoutFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
