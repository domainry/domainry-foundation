package expressionmodel

import "testing"

func TestExpressionDiagnosticUsesStableCode(t *testing.T) {
	diagnostic := &ExpressionDiagnostic{Code: "backend.expression.failed"}
	if diagnostic.Error() != diagnostic.Code {
		t.Fatalf("error=%q", diagnostic.Error())
	}
}
