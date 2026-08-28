package service

import (
	"encoding/json"

	expressioncontract "github.com/domainry/domainry-foundation/expression/contract"
	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

// Expression Service retains the owner-facing API while delegating all pure,
// cross-owner expression semantics to the stable Expression contract.
func ExpressionValidate(expression expressionmodel.BusinessExpression, environment expressionmodel.ExpressionEnvironment) (expressionmodel.ExpressionType, error) {
	return expressioncontract.ExpressionValidate(expression, environment)
}

func ExpressionEvaluate(expression expressionmodel.BusinessExpression, context expressionmodel.ExpressionContext) (expressionmodel.ExpressionValue, error) {
	return expressioncontract.ExpressionEvaluate(expression, context)
}

func ExpressionReferenceKey(reference expressionmodel.ExpressionReference) string {
	return expressioncontract.ExpressionReferenceKey(reference)
}

func ExpressionDecode(raw json.RawMessage) (expressionmodel.BusinessExpression, error) {
	return expressioncontract.ExpressionDecode(raw)
}
