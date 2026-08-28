package service

import (
	"encoding/json"
	"testing"

	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

func TestExpressionServiceDelegatesStableContract(t *testing.T) {
	expression, err := ExpressionDecode(json.RawMessage(`{"kind":"literal","value_type":"text","value":"ready"}`))
	if err != nil {
		t.Fatalf("decode expression: %v", err)
	}
	expressionType, err := ExpressionValidate(expression, expressionmodel.ExpressionEnvironment{})
	if err != nil {
		t.Fatalf("validate expression: %v", err)
	}
	if expressionType.Kind == "" {
		t.Fatal("validated expression type is empty")
	}
	value, err := ExpressionEvaluate(expression, expressionmodel.ExpressionContext{})
	if err != nil {
		t.Fatalf("evaluate expression: %v", err)
	}
	if value.Value != "ready" {
		t.Fatalf("evaluated value=%#v", value)
	}
	reference := expressionmodel.ExpressionReference{Source: "actor", Path: []string{"user_id"}}
	if got := ExpressionReferenceKey(reference); got == "" {
		t.Fatal("reference key is empty")
	}
}
