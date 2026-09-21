package contracttest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const moduleCapabilityImport = "github.com/domainry/domainry-foundation/modulecapability"

// VerifyRepositoryLayout enforces the one public location for a Runtime or
// module owner's capability contract. Execution adapters may expose raw route
// or schema facts, but they must not construct a second ModuleSummary.
func VerifyRepositoryLayout(t testing.TB) {
	t.Helper()
	_, caller, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("resolve capability layout test caller")
	}
	root := filepath.Dir(filepath.Dir(caller))
	if err := validateRepositoryLayout(root); err != nil {
		t.Fatal(err)
	}
}

func validateRepositoryLayout(root string) error {
	capabilityRoot := filepath.Join(root, "capability")
	contractPath := filepath.Join(capabilityRoot, "contract.go")
	for _, required := range []string{
		filepath.Join(capabilityRoot, "capability.go"),
		contractPath,
		filepath.Join(capabilityRoot, "agent", "index.json"),
	} {
		info, err := os.Stat(required)
		if err != nil || info.IsDir() {
			return fmt.Errorf("canonical capability repository file is missing: %s", required)
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "docs", "vendor", "node_modules", "target":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, imported := range file.Imports {
			if strings.Trim(imported.Path.Value, `"`) != moduleCapabilityImport {
				continue
			}
			name := "modulecapability"
			if imported.Name != nil {
				name = imported.Name.Name
			}
			if name != "_" && name != "." {
				aliases[name] = true
			}
		}
		if len(aliases) == 0 {
			return nil
		}
		constructsContract := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CompositeLit:
				selector, ok := value.Type.(*ast.SelectorExpr)
				qualifier, qualified := selectorQualifier(selector)
				if ok && qualified && aliases[qualifier] && selector.Sel.Name == "ModuleSummary" && len(value.Elts) != 0 {
					constructsContract = true
				}
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				qualifier, qualified := selectorQualifier(selector)
				if ok && qualified && aliases[qualifier] && selector.Sel.Name == "NewStaticBinding" {
					constructsContract = true
				}
			}
			return !constructsContract
		})
		if constructsContract && filepath.Clean(path) != filepath.Clean(contractPath) {
			return fmt.Errorf("module capability contract construction must live only in %s; found %s", contractPath, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("inspect capability repository layout: %w", err)
	}
	return nil
}

func selectorQualifier(selector *ast.SelectorExpr) (string, bool) {
	if selector == nil {
		return "", false
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return identifier.Name, true
}
