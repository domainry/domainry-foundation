package contract

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	decimalvalue "github.com/domainry/domainry-foundation/decimal"
	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

var expressionValueTypes = map[string]bool{
	expressionmodel.ExpressionTypeBoolean: true, expressionmodel.ExpressionTypeInteger: true,
	expressionmodel.ExpressionTypeDecimal: true, expressionmodel.ExpressionTypeDate: true,
	expressionmodel.ExpressionTypeDateTime: true, expressionmodel.ExpressionTypeDuration: true,
	expressionmodel.ExpressionTypeText: true, expressionmodel.ExpressionTypeRelation: true,
	expressionmodel.ExpressionTypeList: true,
}

var expressionReferenceSources = map[string]bool{
	"field": true, "input": true, "actor": true, "related_record": true, "step_output": true,
}

func ExpressionValidate(expression expressionmodel.BusinessExpression, environment expressionmodel.ExpressionEnvironment) (expressionmodel.ExpressionType, error) {
	return expressionValidateAt(expression, environment, "$")
}

func expressionValidateAt(expression expressionmodel.BusinessExpression, environment expressionmodel.ExpressionEnvironment, path string) (expressionmodel.ExpressionType, error) {
	switch strings.TrimSpace(expression.Kind) {
	case "literal":
		return expressionValidateLiteral(expression, path)
	case "reference":
		return expressionValidateReference(expression, environment, path)
	case "operation":
		return expressionValidateOperation(expression, environment, path)
	default:
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".kind", "backend.expression.kind_invalid")
	}
}

func expressionValidateLiteral(expression expressionmodel.BusinessExpression, path string) (expressionmodel.ExpressionType, error) {
	valueType := strings.TrimSpace(expression.ValueType)
	if !expressionValueTypes[valueType] {
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".value_type", "backend.expression.type_invalid")
	}
	result := expressionmodel.ExpressionType{Kind: valueType}
	if valueType == expressionmodel.ExpressionTypeList {
		result.ElementType = strings.TrimSpace(expression.ElementType)
		if !expressionValueTypes[result.ElementType] || result.ElementType == expressionmodel.ExpressionTypeList {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".element_type", "backend.expression.type_invalid")
		}
	}
	if !expressionLiteralValid(expression.Value, result) {
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".value", "backend.expression.literal_invalid")
	}
	return result, nil
}

func expressionValidateReference(expression expressionmodel.BusinessExpression, environment expressionmodel.ExpressionEnvironment, path string) (expressionmodel.ExpressionType, error) {
	if expression.Reference == nil || !expressionReferenceSources[strings.TrimSpace(expression.Reference.Source)] || len(expression.Reference.Path) == 0 {
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".reference", "backend.expression.reference_invalid")
	}
	for index, segment := range expression.Reference.Path {
		if strings.TrimSpace(segment) == "" {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, fmt.Sprintf("%s.reference.path[%d]", path, index), "backend.expression.reference_invalid")
		}
	}
	valueType, ok := environment.ReferenceTypes[ExpressionReferenceKey(*expression.Reference)]
	if !ok {
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".reference", "backend.expression.reference_unresolved")
	}
	if declared := strings.TrimSpace(expression.ValueType); declared != "" && declared != valueType.Kind {
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".value_type", "backend.expression.type_mismatch")
	}
	return valueType, nil
}

func expressionValidateOperation(expression expressionmodel.BusinessExpression, environment expressionmodel.ExpressionEnvironment, path string) (expressionmodel.ExpressionType, error) {
	argumentTypes := make([]expressionmodel.ExpressionType, 0, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		argumentType, err := expressionValidateAt(argument, environment, fmt.Sprintf("%s.arguments[%d]", path, index))
		if err != nil {
			return expressionmodel.ExpressionType{}, err
		}
		argumentTypes = append(argumentTypes, argumentType)
	}
	return ExpressionOperationType(expression, argumentTypes, path)
}

func ExpressionOperationType(expression expressionmodel.BusinessExpression, arguments []expressionmodel.ExpressionType, path string) (expressionmodel.ExpressionType, error) {
	operator := strings.TrimSpace(expression.Operator)
	booleanType := expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeBoolean}
	decimalType := expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeDecimal}
	switch operator {
	case "and", "or":
		if len(arguments) < 2 || !expressionAllType(arguments, expressionmodel.ExpressionTypeBoolean) {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.arguments_invalid")
		}
		return booleanType, nil
	case "not":
		if len(arguments) != 1 || arguments[0].Kind != expressionmodel.ExpressionTypeBoolean {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.arguments_invalid")
		}
		return booleanType, nil
	case "eq", "ne", "gt", "gte", "lt", "lte":
		if len(arguments) != 2 || !expressionComparable(arguments[0], arguments[1], operator) {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.type_mismatch")
		}
		return booleanType, nil
	case "add", "subtract", "multiply", "divide", "percentage":
		if len(arguments) != 2 || !expressionAllNumeric(arguments) {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.type_mismatch")
		}
		return decimalType, nil
	case "date_add":
		if len(arguments) != 2 || (arguments[0].Kind != expressionmodel.ExpressionTypeDate && arguments[0].Kind != expressionmodel.ExpressionTypeDateTime) || arguments[1].Kind != expressionmodel.ExpressionTypeDuration {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.type_mismatch")
		}
		return arguments[0], nil
	case "date_diff":
		if len(arguments) != 2 || !expressionAllTemporal(arguments) {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.type_mismatch")
		}
		return expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeDuration}, nil
	case "now":
		if len(arguments) != 0 {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.arguments_invalid")
		}
		return expressionmodel.ExpressionType{Kind: expressionmodel.ExpressionTypeDateTime}, nil
	case "coalesce":
		if len(arguments) < 1 || !expressionAllSameType(arguments) {
			return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".arguments", "backend.expression.type_mismatch")
		}
		return arguments[0], nil
	default:
		return expressionmodel.ExpressionType{}, expressionDiagnostic(expression, path+".operator", "backend.expression.operator_invalid")
	}
}

func expressionLiteralValid(value any, valueType expressionmodel.ExpressionType) bool {
	if value == nil {
		return true
	}
	switch valueType.Kind {
	case expressionmodel.ExpressionTypeBoolean:
		_, ok := value.(bool)
		return ok
	case expressionmodel.ExpressionTypeInteger:
		_, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
		return err == nil
	case expressionmodel.ExpressionTypeDecimal:
		config, _ := decimalvalue.NormalizeDecimalConfig(nil)
		_, err := decimalvalue.NormalizeDecimal(value, config)
		return err == nil
	case expressionmodel.ExpressionTypeDate:
		_, err := time.Parse("2006-01-02", strings.TrimSpace(fmt.Sprint(value)))
		return err == nil
	case expressionmodel.ExpressionTypeDateTime:
		_, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(fmt.Sprint(value)))
		return err == nil
	case expressionmodel.ExpressionTypeDuration:
		_, err := time.ParseDuration(strings.TrimSpace(fmt.Sprint(value)))
		return err == nil
	case expressionmodel.ExpressionTypeText, expressionmodel.ExpressionTypeRelation:
		_, ok := value.(string)
		return ok
	case expressionmodel.ExpressionTypeList:
		items, ok := expressionListItems(value)
		if !ok {
			return false
		}
		for _, item := range items {
			if !expressionLiteralValid(item, expressionmodel.ExpressionType{Kind: valueType.ElementType}) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func expressionListItems(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		items := make([]any, len(typed))
		for index := range typed {
			items[index] = typed[index]
		}
		return items, true
	default:
		return nil, false
	}
}

func expressionAllType(values []expressionmodel.ExpressionType, kind string) bool {
	for _, value := range values {
		if value.Kind != kind {
			return false
		}
	}
	return true
}

func expressionAllNumeric(values []expressionmodel.ExpressionType) bool {
	for _, value := range values {
		if value.Kind != expressionmodel.ExpressionTypeInteger && value.Kind != expressionmodel.ExpressionTypeDecimal {
			return false
		}
	}
	return true
}

func expressionAllTemporal(values []expressionmodel.ExpressionType) bool {
	if len(values) != 2 || values[0].Kind != values[1].Kind {
		return false
	}
	return values[0].Kind == expressionmodel.ExpressionTypeDate || values[0].Kind == expressionmodel.ExpressionTypeDateTime
}

func expressionAllSameType(values []expressionmodel.ExpressionType) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return false
		}
	}
	return true
}

func expressionComparable(left, right expressionmodel.ExpressionType, operator string) bool {
	if left != right {
		return false
	}
	if operator == "eq" || operator == "ne" {
		return true
	}
	return left.Kind != expressionmodel.ExpressionTypeBoolean && left.Kind != expressionmodel.ExpressionTypeList
}

func ExpressionReferenceKey(reference expressionmodel.ExpressionReference) string {
	key := strings.TrimSpace(reference.Source) + ":"
	if strings.TrimSpace(reference.ObjectKey) != "" {
		key += strings.TrimSpace(reference.ObjectKey) + ":"
	}
	return key + strings.Join(reference.Path, ".")
}

func expressionDiagnostic(expression expressionmodel.BusinessExpression, path, code string) error {
	return &expressionmodel.ExpressionDiagnostic{Code: code, Path: path, SourceReference: strings.TrimSpace(expression.SourceReference)}
}
