package logging

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionLoggingUsesFoundationBoundary(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source guard path")
	}
	root := filepath.Dir(filepath.Dir(currentFile))
	forbiddenZapCalls := map[string]bool{
		"Any": true, "Error": true, "L": true, "New": true, "NewDevelopment": true,
		"NewExample": true, "NewNop": true, "NewProduction": true, "ReplaceGlobals": true, "S": true,
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		zapAliases := map[string]bool{}
		for _, imported := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath == "log" || importPath == "log/slog" {
				t.Errorf("%s imports forbidden logger %q", relative, importPath)
			}
			if importPath == "go.uber.org/zap" {
				alias := "zap"
				if imported.Name != nil {
					alias = imported.Name.Name
				}
				zapAliases[alias] = true
			}
		}
		relative = filepath.ToSlash(relative)
		if relative == "logging/logging.go" || relative == "logging/file_archive.go" || len(zapAliases) == 0 {
			return nil
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if ok && zapAliases[identifier.Name] && forbiddenZapCalls[selector.Sel.Name] {
				t.Errorf("%s calls forbidden zap.%s; use domainry-foundation/logging", relative, selector.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
