package expressionmodel

import "time"

const (
	ExpressionTypeBoolean  = "boolean"
	ExpressionTypeInteger  = "integer"
	ExpressionTypeDecimal  = "decimal"
	ExpressionTypeDate     = "date"
	ExpressionTypeDateTime = "datetime"
	ExpressionTypeDuration = "duration"
	ExpressionTypeText     = "text"
	ExpressionTypeRelation = "relation"
	ExpressionTypeList     = "list"
)

type BusinessExpression struct {
	Kind            string               `json:"kind"`
	ValueType       string               `json:"value_type,omitempty"`
	ElementType     string               `json:"element_type,omitempty"`
	Value           any                  `json:"value,omitempty"`
	Reference       *ExpressionReference `json:"reference,omitempty"`
	Operator        string               `json:"operator,omitempty"`
	Arguments       []BusinessExpression `json:"arguments,omitempty"`
	SourceReference string               `json:"source_reference,omitempty"`
}

type ExpressionReference struct {
	Source    string   `json:"source"`
	Path      []string `json:"path"`
	ObjectKey string   `json:"object_key,omitempty"`
}

type ExpressionType struct {
	Kind        string
	ElementType string
}

type ExpressionValue struct {
	Type  ExpressionType
	Value any
}

type ExpressionEnvironment struct {
	ReferenceTypes map[string]ExpressionType
}

type ExpressionContext struct {
	Sources  map[string]any
	Location *time.Location
	Clock    ExpressionClock
}

type ExpressionClock interface {
	Now() time.Time
}

type ExpressionDiagnostic struct {
	Code            string `json:"code"`
	Path            string `json:"path"`
	SourceReference string `json:"source_reference,omitempty"`
}

func (diagnostic *ExpressionDiagnostic) Error() string {
	return diagnostic.Code
}
