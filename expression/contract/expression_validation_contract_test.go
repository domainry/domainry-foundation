package contract

import (
	"testing"

	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

func TestExpressionValidationContractLiteralAndHelperEdges(t *testing.T) {
	for _, expression := range []expressionmodel.BusinessExpression{
		{Kind: "literal", ValueType: "text", Value: nil},
		{Kind: "literal", ValueType: "list", ElementType: "text", Value: []string{"a"}},
	} {
		if _, err := ExpressionValidate(expression, expressionmodel.ExpressionEnvironment{}); err != nil {
			t.Fatalf("valid expression=%+v err=%v", expression, err)
		}
	}
	for _, expression := range []expressionmodel.BusinessExpression{
		{Kind: "literal", ValueType: "list", ElementType: "text", Value: "not-a-list"},
		{Kind: "literal", ValueType: "list", ElementType: "integer", Value: []any{"bad"}},
	} {
		if _, err := ExpressionValidate(expression, expressionmodel.ExpressionEnvironment{}); err == nil {
			t.Fatalf("invalid expression accepted=%+v", expression)
		}
	}
	if expressionAllSameType(nil) {
		t.Fatal("empty types unexpectedly same")
	}
	integer := expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeInteger}
	decimal := expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeDecimal}
	if expressionAllSameType([]expressionmodel.ExpressionType{integer, decimal}) {
		t.Fatal("different types unexpectedly same")
	}
	boolean := expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeBoolean}
	if expressionComparable(integer, decimal, "eq") || expressionComparable(boolean, boolean, "unknown") {
		t.Fatal("invalid comparison unexpectedly accepted")
	}
	if !expressionComparable(integer, integer, "ne") {
		t.Fatal("equality comparison unexpectedly rejected")
	}
	if expressionLiteralValid("value", expressionmodel.ExpressionType{Kind: "unknown"}) {
		t.Fatal("unknown literal type unexpectedly accepted")
	}
	if expressionAllTemporal([]expressionmodel.ExpressionType{integer}) {
		t.Fatal("single temporal argument unexpectedly accepted")
	}
}
