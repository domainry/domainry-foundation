// Package logcontract verifies the source-owned event-name contract without
// executing application code.
package logcontract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/logging"
)

type Violation struct {
	Path    string
	Line    int
	Message string
}

func (violation Violation) String() string {
	return fmt.Sprintf("%s:%d: %s", violation.Path, violation.Line, violation.Message)
}

// CheckFile verifies log event literals in one production Go source file.
func CheckFile(path string) ([]Violation, error) {
	if strings.HasSuffix(path, "_test.go") {
		return nil, nil
	}
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return nil, err
	}
	loggingAliases := map[string]bool{}
	for _, imported := range parsed.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil || importPath != "github.com/domainry/domainry-foundation/logging" {
			continue
		}
		name := "logging"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		loggingAliases[name] = true
	}
	if len(loggingAliases) == 0 {
		return nil, nil
	}
	loggerVariables := map[string]bool{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for index, value := range assignment.Rhs {
			if index >= len(assignment.Lhs) || !isFromContextCall(value, loggingAliases) {
				continue
			}
			if identifier, ok := assignment.Lhs[index].(*ast.Ident); ok {
				loggerVariables[identifier.Name] = true
			}
		}
		return true
	})

	violations := []Violation{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		eventIndex := -1
		if packageName, ok := selector.X.(*ast.Ident); ok && loggingAliases[packageName.Name] && selector.Sel.Name == "RecordFailure" {
			eventIndex = 1
		} else if isLoggerMethod(selector.Sel.Name) && (isFromContextCall(selector.X, loggingAliases) || isLoggerVariable(selector.X, loggerVariables)) {
			eventIndex = 0
		}
		if eventIndex < 0 || eventIndex >= len(call.Args) {
			return true
		}
		position := files.Position(call.Args[eventIndex].Pos())
		literal, ok := call.Args[eventIndex].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			violations = append(violations, Violation{Path: path, Line: position.Line, Message: "log event must be a source-owned string literal"})
			return true
		}
		event, unquoteErr := strconv.Unquote(literal.Value)
		if unquoteErr != nil || !logging.ValidEventName(event) {
			violations = append(violations, Violation{Path: path, Line: position.Line, Message: fmt.Sprintf("invalid log event %q; use bounded snake_case and put dynamic identity in fields", event)})
		}
		return true
	})
	return violations, nil
}

func isFromContextCall(expression ast.Expr, aliases map[string]bool) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "FromContext" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && aliases[identifier.Name]
}

func isLoggerVariable(expression ast.Expr, variables map[string]bool) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && variables[identifier.Name]
}

func isLoggerMethod(name string) bool {
	switch name {
	case "Debug", "Info", "Warn", "Error", "DPanic", "Panic", "Fatal":
		return true
	default:
		return false
	}
}
