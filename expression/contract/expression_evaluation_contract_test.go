package contract

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

type fixedExpressionClock struct{ now time.Time }

func (clock fixedExpressionClock) Now() time.Time { return clock.now }

func expressionLiteral(valueType string, value any) expressionmodel.BusinessExpression {
	return expressionmodel.BusinessExpression{Kind: "literal", ValueType: valueType, Value: value}
}

func expressionOperation(operator string, arguments ...expressionmodel.BusinessExpression) expressionmodel.BusinessExpression {
	return expressionmodel.BusinessExpression{Kind: "operation", Operator: operator, Arguments: arguments, SourceReference: "rule:1"}
}

func TestExpressionValidatesAllValueTypesAndReferences(t *testing.T) {
	valid := []expressionmodel.BusinessExpression{
		expressionLiteral("boolean", true), expressionLiteral("integer", 7), expressionLiteral("decimal", "1.20"),
		expressionLiteral("date", "2026-07-21"), expressionLiteral("datetime", "2026-07-21T01:02:03Z"),
		expressionLiteral("duration", "30m"), expressionLiteral("text", "hello"), expressionLiteral("relation", "record-1"),
		{Kind: "literal", ValueType: "list", ElementType: "text", Value: []any{"a", "b"}},
	}
	for _, expression := range valid {
		if _, err := ExpressionValidate(expression, expressionmodel.ExpressionEnvironment{}); err != nil {
			t.Fatalf("expression=%+v error=%v", expression, err)
		}
	}
	reference := expressionmodel.BusinessExpression{Kind: "reference", ValueType: "decimal", Reference: &expressionmodel.ExpressionReference{Source: "related_record", ObjectKey: "order", Path: []string{"total"}}}
	key := ExpressionReferenceKey(*reference.Reference)
	valueType, err := ExpressionValidate(reference, expressionmodel.ExpressionEnvironment{ReferenceTypes: map[string]expressionmodel.ExpressionType{key: {Kind: "decimal"}}})
	if err != nil || valueType.Kind != "decimal" || key != "related_record:order:total" {
		t.Fatalf("type=%+v key=%q error=%v", valueType, key, err)
	}
}

func TestExpressionTemporalNormalizationAcceptsCanonicalNativeValues(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 7, 21, 23, 30, 0, 123, time.UTC)
	datetime, err := ExpressionEvaluate(expressionLiteral("datetime", instant), expressionmodel.ExpressionContext{Location: tokyo})
	if err != nil || !datetime.Value.(time.Time).Equal(instant) || datetime.Value.(time.Time).Location() != tokyo {
		t.Fatalf("datetime=%#v err=%v", datetime.Value, err)
	}
	date, err := ExpressionEvaluate(expressionLiteral("date", instant), expressionmodel.ExpressionContext{Location: tokyo})
	if err != nil || date.Value.(time.Time).Format("2006-01-02T15:04:05Z07:00") != "2026-07-22T00:00:00+09:00" {
		t.Fatalf("date=%#v err=%v", date.Value, err)
	}
	duration, err := ExpressionEvaluate(expressionLiteral("duration", 90*time.Minute), expressionmodel.ExpressionContext{Location: tokyo})
	if err != nil || duration.Value != 90*time.Minute {
		t.Fatalf("duration=%#v err=%v", duration.Value, err)
	}
}

func TestExpressionValidationRejectsInvalidDocumentsWithPrecisePaths(t *testing.T) {
	tests := []struct {
		name        string
		expression  expressionmodel.BusinessExpression
		environment expressionmodel.ExpressionEnvironment
		code        string
		path        string
	}{
		{name: "kind", expression: expressionmodel.BusinessExpression{}, code: "backend.expression.kind_invalid", path: "$.kind"},
		{name: "literal type", expression: expressionLiteral("unknown", 1), code: "backend.expression.type_invalid", path: "$.value_type"},
		{name: "literal value", expression: expressionLiteral("integer", "x"), code: "backend.expression.literal_invalid", path: "$.value"},
		{name: "list type", expression: expressionmodel.BusinessExpression{Kind: "literal", ValueType: "list", ElementType: "list", Value: []any{}}, code: "backend.expression.type_invalid", path: "$.element_type"},
		{name: "list unknown type", expression: expressionmodel.BusinessExpression{Kind: "literal", ValueType: "list", ElementType: "unknown", Value: []any{}}, code: "backend.expression.type_invalid", path: "$.element_type"},
		{name: "reference absent", expression: expressionmodel.BusinessExpression{Kind: "reference"}, code: "backend.expression.reference_invalid", path: "$.reference"},
		{name: "reference source", expression: expressionmodel.BusinessExpression{Kind: "reference", Reference: &expressionmodel.ExpressionReference{Source: "script", Path: []string{"x"}}}, code: "backend.expression.reference_invalid", path: "$.reference"},
		{name: "reference path", expression: expressionmodel.BusinessExpression{Kind: "reference", Reference: &expressionmodel.ExpressionReference{Source: "field"}}, code: "backend.expression.reference_invalid", path: "$.reference"},
		{name: "reference segment", expression: expressionmodel.BusinessExpression{Kind: "reference", Reference: &expressionmodel.ExpressionReference{Source: "field", Path: []string{""}}}, code: "backend.expression.reference_invalid", path: "$.reference.path[0]"},
		{name: "reference unresolved", expression: expressionmodel.BusinessExpression{Kind: "reference", Reference: &expressionmodel.ExpressionReference{Source: "field", Path: []string{"x"}}}, code: "backend.expression.reference_unresolved", path: "$.reference"},
		{name: "reference declared mismatch", expression: expressionmodel.BusinessExpression{Kind: "reference", ValueType: "text", Reference: &expressionmodel.ExpressionReference{Source: "field", Path: []string{"x"}}}, environment: expressionmodel.ExpressionEnvironment{ReferenceTypes: map[string]expressionmodel.ExpressionType{"field:x": {Kind: "decimal"}}}, code: "backend.expression.type_mismatch", path: "$.value_type"},
		{name: "reference inferred", expression: expressionmodel.BusinessExpression{Kind: "operation", Operator: "not", Arguments: []expressionmodel.BusinessExpression{{Kind: "reference", Reference: &expressionmodel.ExpressionReference{Source: "field", Path: []string{"x"}}}}}, environment: expressionmodel.ExpressionEnvironment{ReferenceTypes: map[string]expressionmodel.ExpressionType{"field:x": {Kind: "integer"}}}, code: "backend.expression.arguments_invalid", path: "$.arguments"},
		{name: "nested", expression: expressionOperation("not", expressionLiteral("integer", "x")), code: "backend.expression.literal_invalid", path: "$.arguments[0].value"},
		{name: "operator", expression: expressionOperation("script"), code: "backend.expression.operator_invalid", path: "$.operator"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExpressionValidate(test.expression, test.environment)
			assertExpressionDiagnostic(t, err, test.code, test.path)
		})
	}
}

func TestExpressionValidatesOperationTypes(t *testing.T) {
	boolean := expressionLiteral("boolean", true)
	integer := expressionLiteral("integer", 2)
	decimal := expressionLiteral("decimal", "2.00")
	date := expressionLiteral("date", "2026-07-21")
	datetime := expressionLiteral("datetime", "2026-07-21T00:00:00Z")
	duration := expressionLiteral("duration", "1h")
	text := expressionLiteral("text", "a")
	tests := []struct {
		expression expressionmodel.BusinessExpression
		want       string
	}{
		{expressionOperation("and", boolean, boolean), "boolean"}, {expressionOperation("or", boolean, boolean), "boolean"},
		{expressionOperation("not", boolean), "boolean"}, {expressionOperation("eq", text, text), "boolean"},
		{expressionOperation("ne", integer, integer), "boolean"}, {expressionOperation("gt", decimal, decimal), "boolean"},
		{expressionOperation("gte", date, date), "boolean"}, {expressionOperation("lt", datetime, datetime), "boolean"},
		{expressionOperation("lte", text, text), "boolean"}, {expressionOperation("add", integer, decimal), "decimal"},
		{expressionOperation("subtract", decimal, integer), "decimal"}, {expressionOperation("multiply", integer, integer), "decimal"},
		{expressionOperation("divide", decimal, decimal), "decimal"}, {expressionOperation("percentage", decimal, integer), "decimal"},
		{expressionOperation("date_add", date, duration), "date"}, {expressionOperation("date_add", datetime, duration), "datetime"},
		{expressionOperation("date_diff", date, date), "duration"}, {expressionOperation("now"), "datetime"},
		{expressionOperation("coalesce", text, text), "text"},
	}
	for _, test := range tests {
		valueType, err := ExpressionValidate(test.expression, expressionmodel.ExpressionEnvironment{})
		if err != nil || valueType.Kind != test.want {
			t.Fatalf("operator=%s type=%+v error=%v", test.expression.Operator, valueType, err)
		}
	}
	invalid := []expressionmodel.BusinessExpression{
		expressionOperation("and", boolean), expressionOperation("or", boolean, integer), expressionOperation("not", boolean, boolean),
		expressionOperation("not", integer), expressionOperation("eq", text), expressionOperation("add", integer),
		expressionOperation("eq", text, integer), expressionOperation("gt", boolean, boolean), expressionOperation("add", text, integer),
		expressionOperation("date_add", date), expressionOperation("date_add", integer, duration), expressionOperation("date_add", date, integer),
		expressionOperation("date_diff", date), expressionOperation("date_diff", date, datetime), expressionOperation("now", integer),
		expressionOperation("coalesce"), expressionOperation("coalesce", text, integer),
	}
	for _, expression := range invalid {
		if _, err := ExpressionValidate(expression, expressionmodel.ExpressionEnvironment{}); err == nil {
			t.Fatalf("invalid operator accepted: %+v", expression)
		}
	}
}

func TestExpressionEvaluatesBusinessDerivationsExactly(t *testing.T) {
	context := expressionmodel.ExpressionContext{
		Sources: map[string]any{
			"input":          map[string]any{"weight": "80.00", "height": "2.00", "paid": "100.00", "refunded": "25.50"},
			"actor":          map[string]any{"active": true},
			"field":          map[string]any{"expires_on": "2026-07-21"},
			"related_record": map[string]any{"package": map[string]any{"remaining": int64(10)}},
			"step_output":    map[string]any{"price": map[string]any{"value": "30.00"}},
		},
		Clock: fixedExpressionClock{now: time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)}, Location: time.FixedZone("UTC+8", 8*60*60),
	}
	ref := func(source, valueType string, path ...string) expressionmodel.BusinessExpression {
		return expressionmodel.BusinessExpression{Kind: "reference", ValueType: valueType, Reference: &expressionmodel.ExpressionReference{Source: source, Path: path}}
	}
	bmi := expressionOperation("divide", ref("input", "decimal", "weight"), expressionOperation("multiply", ref("input", "decimal", "height"), ref("input", "decimal", "height")))
	remainingRefund := expressionOperation("subtract", ref("input", "decimal", "paid"), ref("input", "decimal", "refunded"))
	commission := expressionOperation("percentage", ref("step_output", "decimal", "price", "value"), expressionLiteral("integer", 10))
	expiry := expressionOperation("date_add", ref("field", "date", "expires_on"), expressionLiteral("duration", "24h"))
	for name, test := range map[string]struct {
		expression expressionmodel.BusinessExpression
		want       any
	}{
		"bmi": {bmi, "20.00"}, "refund": {remainingRefund, "74.50"}, "commission": {commission, "3.00"},
		"remaining":      {ref("related_record", "integer", "package", "remaining"), int64(10)},
		"actor":          {ref("actor", "boolean", "active"), true},
		"related object": {expressionmodel.BusinessExpression{Kind: "reference", ValueType: "integer", Reference: &expressionmodel.ExpressionReference{Source: "related_record", ObjectKey: "package", Path: []string{"remaining"}}}, int64(10)},
	} {
		value, err := ExpressionEvaluate(test.expression, context)
		if err != nil || value.Value != test.want {
			t.Fatalf("%s value=%v error=%v", name, value.Value, err)
		}
	}
	value, err := ExpressionEvaluate(expiry, context)
	if err != nil || value.Value.(time.Time).Format("2006-01-02") != "2026-07-22" {
		t.Fatalf("expiry=%v error=%v", value.Value, err)
	}
	value, err = ExpressionEvaluate(expressionOperation("now"), context)
	if err != nil || value.Value.(time.Time).Hour() != 16 {
		t.Fatalf("now=%v error=%v", value.Value, err)
	}
}

func TestExpressionEvaluatesBooleanComparisonNullAndDateOperations(t *testing.T) {
	values := []struct {
		expression expressionmodel.BusinessExpression
		want       any
	}{
		{expressionOperation("and", expressionLiteral("boolean", true), expressionLiteral("boolean", false)), false},
		{expressionOperation("or", expressionLiteral("boolean", false), expressionLiteral("boolean", true)), true},
		{expressionOperation("not", expressionLiteral("boolean", false)), true},
		{expressionOperation("eq", expressionLiteral("integer", 2), expressionLiteral("integer", 2)), true},
		{expressionOperation("ne", expressionLiteral("text", "a"), expressionLiteral("text", "b")), true},
		{expressionOperation("gt", expressionLiteral("decimal", "2.00"), expressionLiteral("decimal", "1.00")), true},
		{expressionOperation("lt", expressionLiteral("decimal", "1.00"), expressionLiteral("decimal", "2.00")), true},
		{expressionOperation("gte", expressionLiteral("date", "2026-07-21"), expressionLiteral("date", "2026-07-21")), true},
		{expressionOperation("lt", expressionLiteral("datetime", "2026-07-20T00:00:00Z"), expressionLiteral("datetime", "2026-07-21T00:00:00Z")), true},
		{expressionOperation("lte", expressionLiteral("text", "a"), expressionLiteral("text", "b")), true},
		{expressionOperation("coalesce", expressionLiteral("text", nil), expressionLiteral("text", "fallback")), "fallback"},
	}
	for _, test := range values {
		value, err := ExpressionEvaluate(test.expression, expressionmodel.ExpressionContext{})
		if err != nil || value.Value != test.want {
			t.Fatalf("operator=%s value=%v error=%v", test.expression.Operator, value.Value, err)
		}
	}
	difference, err := ExpressionEvaluate(expressionOperation("date_diff", expressionLiteral("date", "2026-07-22"), expressionLiteral("date", "2026-07-21")), expressionmodel.ExpressionContext{})
	if err != nil || difference.Value != 24*time.Hour {
		t.Fatalf("difference=%v error=%v", difference.Value, err)
	}
	difference, err = ExpressionEvaluate(expressionOperation("date_diff", expressionLiteral("datetime", "2026-07-22T01:00:00Z"), expressionLiteral("datetime", "2026-07-22T00:00:00Z")), expressionmodel.ExpressionContext{})
	if err != nil || difference.Value != time.Hour {
		t.Fatalf("datetime difference=%v error=%v", difference.Value, err)
	}
	allNil, err := ExpressionEvaluate(expressionOperation("coalesce", expressionLiteral("text", nil)), expressionmodel.ExpressionContext{})
	if err != nil || allNil.Value != nil {
		t.Fatalf("coalesce=%v error=%v", allNil.Value, err)
	}
}

func TestExpressionDateSemanticsAcrossDSTTimezonesMonthEndAndLeapYear(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	dateCases := []struct{ name, start, want string }{
		{name: "dst fall back", start: "2026-11-01", want: "2026-11-02"},
		{name: "dst spring forward", start: "2026-03-08", want: "2026-03-09"},
		{name: "month end", start: "2026-01-31", want: "2026-02-01"},
		{name: "leap day", start: "2024-02-28", want: "2024-02-29"},
	}
	for _, test := range dateCases {
		t.Run(test.name, func(t *testing.T) {
			context := expressionmodel.ExpressionContext{Location: newYork}
			value, err := ExpressionEvaluate(expressionOperation("date_add", expressionLiteral("date", test.start), expressionLiteral("duration", "24h")), context)
			if err != nil || value.Value.(time.Time).Format("2006-01-02") != test.want {
				t.Fatalf("date_add(%s)=%v err=%v want=%s", test.start, value.Value, err, test.want)
			}
			difference, err := ExpressionEvaluate(expressionOperation("date_diff", expressionLiteral("date", test.want), expressionLiteral("date", test.start)), context)
			if err != nil || difference.Value != 24*time.Hour {
				t.Fatalf("date_diff(%s,%s)=%v err=%v", test.want, test.start, difference.Value, err)
			}
		})
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	datetime, err := ExpressionEvaluate(expressionOperation("date_add", expressionLiteral("datetime", "2026-07-21T23:30:00Z"), expressionLiteral("duration", "2h")), expressionmodel.ExpressionContext{Location: tokyo})
	if err != nil || datetime.Value.(time.Time).Format(time.RFC3339) != "2026-07-22T10:30:00+09:00" {
		t.Fatalf("cross-timezone datetime=%v err=%v", datetime.Value, err)
	}
}

func TestExpressionEvaluationReportsStableFailures(t *testing.T) {
	missing := expressionmodel.BusinessExpression{Kind: "reference", ValueType: "text", SourceReference: "action:step", Reference: &expressionmodel.ExpressionReference{Source: "input", Path: []string{"missing"}}}
	withoutReference := missing
	withoutReference.Reference = nil
	_, err := ExpressionEvaluate(withoutReference, expressionmodel.ExpressionContext{})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_invalid", "$.reference")
	_, err = ExpressionEvaluate(missing, expressionmodel.ExpressionContext{Sources: map[string]any{}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_missing", "$.reference")
	_, err = ExpressionEvaluate(missing, expressionmodel.ExpressionContext{Sources: map[string]any{"input": map[string]any{}}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_missing", "$.reference")
	invalid := missing
	invalid.Reference = &expressionmodel.ExpressionReference{Source: "input", Path: []string{"nested", "value"}}
	_, err = ExpressionEvaluate(invalid, expressionmodel.ExpressionContext{Sources: map[string]any{"input": map[string]any{"nested": "not-an-object"}}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_missing", "$.reference")
	invalid.Reference = &expressionmodel.ExpressionReference{Source: "input", Path: []string{"value"}}
	_, err = ExpressionEvaluate(invalid, expressionmodel.ExpressionContext{Sources: map[string]any{"input": map[string]any{"value": map[string]any{}}}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_invalid", "$.reference")
	invalid.Reference = &expressionmodel.ExpressionReference{Source: "related_record", ObjectKey: "package", Path: []string{"value"}}
	_, err = ExpressionEvaluate(invalid, expressionmodel.ExpressionContext{Sources: map[string]any{"related_record": map[string]any{"package": "not-an-object"}}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_missing", "$.reference")
	_, err = ExpressionEvaluate(invalid, expressionmodel.ExpressionContext{Sources: map[string]any{"related_record": "not-an-object"}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_missing", "$.reference")
	_, err = ExpressionEvaluate(invalid, expressionmodel.ExpressionContext{Sources: map[string]any{}})
	assertExpressionDiagnostic(t, err, "backend.expression.reference_value_missing", "$.reference")
	_, err = ExpressionEvaluate(expressionOperation("divide", expressionLiteral("decimal", "1"), expressionLiteral("decimal", "0")), expressionmodel.ExpressionContext{})
	assertExpressionDiagnostic(t, err, "backend.decimal.divide_by_zero", "$")
	_, err = ExpressionEvaluate(expressionmodel.BusinessExpression{}, expressionmodel.ExpressionContext{})
	assertExpressionDiagnostic(t, err, "backend.expression.kind_invalid", "$.kind")
	_, err = ExpressionEvaluate(expressionOperation("not", expressionLiteral("integer", "bad")), expressionmodel.ExpressionContext{})
	assertExpressionDiagnostic(t, err, "backend.expression.literal_invalid", "$.arguments[0].value")
	_, err = ExpressionEvaluate(expressionOperation("not", expressionLiteral("integer", 1)), expressionmodel.ExpressionContext{})
	assertExpressionDiagnostic(t, err, "backend.expression.arguments_invalid", "$.arguments")
}

func TestExpressionDecode(t *testing.T) {
	expression, err := ExpressionDecode(json.RawMessage(`{"kind":"literal","value_type":"text","value":"ok"}`))
	if err != nil || expression.Value != "ok" {
		t.Fatalf("expression=%+v error=%v", expression, err)
	}
	_, err = ExpressionDecode(json.RawMessage(`{`))
	assertExpressionDiagnostic(t, err, "backend.expression.document_invalid", "$")
}

func TestExpressionNormalizesTypedListElements(t *testing.T) {
	expression := expressionmodel.BusinessExpression{Kind: "literal", ValueType: "list", ElementType: "integer", Value: []any{"1", 2}}
	value, err := ExpressionEvaluate(expression, expressionmodel.ExpressionContext{})
	if err != nil {
		t.Fatalf("evaluate typed list: %v", err)
	}
	items := value.Value.([]any)
	if items[0] != int64(1) || items[1] != int64(2) {
		t.Fatalf("typed list=%#v", items)
	}
	invalid := expression
	invalid.Value = []any{"bad"}
	if _, err := ExpressionEvaluate(invalid, expressionmodel.ExpressionContext{}); err == nil {
		t.Fatal("expected typed list normalization error")
	}
}

func TestExpressionServiceRemainingOperationAndValueEdges(t *testing.T) {
	for _, test := range []struct {
		expression expressionmodel.BusinessExpression
		want       any
	}{
		{expressionOperation("add", expressionLiteral("decimal", "1.20"), expressionLiteral("integer", 2)), "3.20"},
		{expressionOperation("subtract", expressionLiteral("integer", 5), expressionLiteral("decimal", "2.25")), "2.75"},
		{expressionOperation("multiply", expressionLiteral("decimal", "1.50"), expressionLiteral("integer", 2)), "3.00"},
		{expressionOperation("divide", expressionLiteral("decimal", "3.00"), expressionLiteral("integer", 2)), "1.50"},
		{expressionOperation("eq", expressionLiteral("decimal", "1.00"), expressionLiteral("decimal", "1.00")), true},
		{expressionOperation("lt", expressionLiteral("integer", 1), expressionLiteral("integer", 2)), true},
		{expressionOperation("gt", expressionLiteral("integer", 2), expressionLiteral("integer", 1)), true},
		{expressionOperation("eq", expressionLiteral("date", "2026-07-21"), expressionLiteral("date", "2026-07-21")), true},
		{expressionOperation("gt", expressionLiteral("date", "2026-07-22"), expressionLiteral("date", "2026-07-21")), true},
	} {
		value, err := ExpressionEvaluate(test.expression, expressionmodel.ExpressionContext{})
		if err != nil || value.Value != test.want {
			t.Fatalf("operator=%s value=%v error=%v", test.expression.Operator, value.Value, err)
		}
	}
	now, err := ExpressionEvaluate(expressionOperation("now"), expressionmodel.ExpressionContext{})
	if err != nil || now.Value.(time.Time).IsZero() {
		t.Fatalf("system now=%v error=%v", now.Value, err)
	}
	list, err := ExpressionEvaluate(expressionmodel.BusinessExpression{Kind: "literal", ValueType: "list", ElementType: "text", Value: []string{"a"}}, expressionmodel.ExpressionContext{})
	if err != nil || len(list.Value.([]any)) != 1 {
		t.Fatalf("list=%v error=%v", list.Value, err)
	}
	for _, literal := range []expressionmodel.BusinessExpression{
		expressionLiteral("boolean", "true"), expressionLiteral("date", "bad"), expressionLiteral("datetime", "bad"),
		expressionLiteral("duration", "bad"), {Kind: "literal", ValueType: "list", ElementType: "text", Value: "bad"},
		expressionLiteral("unknown", "bad"), expressionLiteral("text", map[string]any{}),
	} {
		if _, err := ExpressionEvaluate(literal, expressionmodel.ExpressionContext{}); err == nil {
			t.Fatalf("invalid literal evaluated: %+v", literal)
		}
	}
	if expressionCompare(expressionmodel.ExpressionValue{Type: expressionmodel.ExpressionType{Kind: "text"}, Value: "a"}, expressionmodel.ExpressionValue{Type: expressionmodel.ExpressionType{Kind: "text"}, Value: "b"}, "bad") {
		t.Fatal("invalid comparison operator matched")
	}
	if _, err := expressionApplyOperation(expressionOperation("bad"), nil, expressionmodel.ExpressionType{}, expressionmodel.ExpressionContext{}, "$"); err == nil {
		t.Fatal("invalid apply operator accepted")
	}
}

func assertExpressionDiagnostic(t *testing.T, err error, code, path string) {
	t.Helper()
	var diagnostic *expressionmodel.ExpressionDiagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Path != path {
		t.Fatalf("diagnostic=%#v", err)
	}
}
