package contract

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	decimalvalue "github.com/domainry/domainry-foundation/decimal"
	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

type systemExpressionClock struct{}

func (systemExpressionClock) Now() time.Time { return time.Now() }

func ExpressionEvaluate(expression expressionmodel.BusinessExpression, context expressionmodel.ExpressionContext) (expressionmodel.ExpressionValue, error) {
	if context.Clock == nil {
		context.Clock = systemExpressionClock{}
	}
	if context.Location == nil {
		context.Location = time.UTC
	}
	return expressionEvaluateAt(expression, context, "$")
}

func expressionEvaluateAt(expression expressionmodel.BusinessExpression, context expressionmodel.ExpressionContext, path string) (expressionmodel.ExpressionValue, error) {
	switch strings.TrimSpace(expression.Kind) {
	case "literal":
		valueType := expressionmodel.ExpressionType{Kind: strings.TrimSpace(expression.ValueType)}
		if valueType.Kind == expressionmodel.ExpressionTypeList {
			valueType.ElementType = strings.TrimSpace(expression.ElementType)
		}
		value, err := expressionNormalizeValue(expression.Value, valueType, context.Location)
		if err != nil {
			return expressionmodel.ExpressionValue{}, expressionError(expression, path+".value", "backend.expression.literal_invalid")
		}
		return expressionmodel.ExpressionValue{Type: valueType, Value: value}, nil
	case "reference":
		return expressionEvaluateReference(expression, context, path)
	case "operation":
		return expressionEvaluateOperation(expression, context, path)
	default:
		return expressionmodel.ExpressionValue{}, expressionError(expression, path+".kind", "backend.expression.kind_invalid")
	}
}

func expressionEvaluateReference(expression expressionmodel.BusinessExpression, context expressionmodel.ExpressionContext, path string) (expressionmodel.ExpressionValue, error) {
	if expression.Reference == nil {
		return expressionmodel.ExpressionValue{}, expressionError(expression, path+".reference", "backend.expression.reference_invalid")
	}
	current, ok := context.Sources[expression.Reference.Source]
	if objectKey := strings.TrimSpace(expression.Reference.ObjectKey); objectKey != "" && ok {
		values, mapOK := current.(map[string]any)
		if !mapOK {
			ok = false
		} else {
			current, ok = values[objectKey]
		}
	}
	for _, segment := range expression.Reference.Path {
		if !ok {
			break
		}
		values, mapOK := current.(map[string]any)
		if !mapOK {
			ok = false
			break
		}
		current, ok = values[segment]
	}
	if !ok {
		return expressionmodel.ExpressionValue{}, expressionError(expression, path+".reference", "backend.expression.reference_value_missing")
	}
	valueType := expressionmodel.ExpressionType{Kind: strings.TrimSpace(expression.ValueType)}
	value, err := expressionNormalizeValue(current, valueType, context.Location)
	if err != nil {
		return expressionmodel.ExpressionValue{}, expressionError(expression, path+".reference", "backend.expression.reference_value_invalid")
	}
	return expressionmodel.ExpressionValue{Type: valueType, Value: value}, nil
}

func expressionEvaluateOperation(expression expressionmodel.BusinessExpression, context expressionmodel.ExpressionContext, path string) (expressionmodel.ExpressionValue, error) {
	values := make([]expressionmodel.ExpressionValue, 0, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		value, err := expressionEvaluateAt(argument, context, fmt.Sprintf("%s.arguments[%d]", path, index))
		if err != nil {
			return expressionmodel.ExpressionValue{}, err
		}
		values = append(values, value)
	}
	resultType, err := ExpressionOperationType(expression, expressionTypes(values), path)
	if err != nil {
		return expressionmodel.ExpressionValue{}, err
	}
	return expressionApplyOperation(expression, values, resultType, context, path)
}

func expressionApplyOperation(expression expressionmodel.BusinessExpression, values []expressionmodel.ExpressionValue, resultType expressionmodel.ExpressionType, context expressionmodel.ExpressionContext, path string) (expressionmodel.ExpressionValue, error) {
	operator := strings.TrimSpace(expression.Operator)
	switch operator {
	case "and", "or":
		result := operator == "and"
		for _, value := range values {
			if operator == "and" {
				result = result && value.Value.(bool)
			} else {
				result = result || value.Value.(bool)
			}
		}
		return expressionmodel.ExpressionValue{Type: resultType, Value: result}, nil
	case "not":
		return expressionmodel.ExpressionValue{Type: resultType, Value: !values[0].Value.(bool)}, nil
	case "eq", "ne", "gt", "gte", "lt", "lte":
		result := expressionCompare(values[0], values[1], operator)
		return expressionmodel.ExpressionValue{Type: resultType, Value: result}, nil
	case "add", "subtract", "multiply", "divide", "percentage":
		decimalConfig, _ := decimalvalue.NormalizeDecimalConfig(nil)
		left, right := fmt.Sprint(values[0].Value), fmt.Sprint(values[1].Value)
		var result string
		var calculationErr error
		switch operator {
		case "add":
			result, calculationErr = decimalvalue.AddDecimals(left, right, decimalConfig)
		case "subtract":
			result, calculationErr = decimalvalue.SubtractDecimals(left, right, decimalConfig)
		case "multiply":
			result, calculationErr = decimalvalue.MultiplyDecimals(left, right, decimalConfig)
		case "divide":
			result, calculationErr = decimalvalue.DivideDecimals(left, right, decimalConfig)
		case "percentage":
			result, calculationErr = decimalvalue.PercentageDecimal(left, right, decimalConfig)
		}
		if calculationErr != nil {
			return expressionmodel.ExpressionValue{}, expressionError(expression, path, calculationErr.(*decimalvalue.DecimalError).Code)
		}
		return expressionmodel.ExpressionValue{Type: resultType, Value: result}, nil
	case "date_add":
		base := values[0].Value.(time.Time)
		duration := values[1].Value.(time.Duration)
		if values[0].Type.Kind == expressionmodel.ExpressionTypeDate {
			return expressionmodel.ExpressionValue{Type: resultType, Value: expressionAddCalendarDuration(base, duration)}, nil
		}
		return expressionmodel.ExpressionValue{Type: resultType, Value: base.Add(duration)}, nil
	case "date_diff":
		left, right := values[0].Value.(time.Time), values[1].Value.(time.Time)
		if values[0].Type.Kind == expressionmodel.ExpressionTypeDate {
			return expressionmodel.ExpressionValue{Type: resultType, Value: expressionCalendarDateDifference(left, right)}, nil
		}
		return expressionmodel.ExpressionValue{Type: resultType, Value: left.Sub(right)}, nil
	case "now":
		return expressionmodel.ExpressionValue{Type: resultType, Value: context.Clock.Now().In(context.Location)}, nil
	case "coalesce":
		for _, value := range values {
			if value.Value != nil {
				return expressionmodel.ExpressionValue{Type: resultType, Value: value.Value}, nil
			}
		}
		return expressionmodel.ExpressionValue{Type: resultType}, nil
	default:
		return expressionmodel.ExpressionValue{}, expressionError(expression, path+".operator", "backend.expression.operator_invalid")
	}
}

func expressionAddCalendarDuration(value time.Time, duration time.Duration) time.Time {
	days := int(duration / (24 * time.Hour))
	remainder := duration % (24 * time.Hour)
	return value.AddDate(0, 0, days).Add(remainder)
}

func expressionCalendarDateDifference(left, right time.Time) time.Duration {
	leftDate := time.Date(left.Year(), left.Month(), left.Day(), 0, 0, 0, 0, time.UTC)
	rightDate := time.Date(right.Year(), right.Month(), right.Day(), 0, 0, 0, 0, time.UTC)
	return leftDate.Sub(rightDate)
}

func expressionNormalizeValue(value any, valueType expressionmodel.ExpressionType, location *time.Location) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch valueType.Kind {
	case expressionmodel.ExpressionTypeBoolean:
		result, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("boolean")
		}
		return result, nil
	case expressionmodel.ExpressionTypeInteger:
		text := strings.TrimSpace(fmt.Sprint(value))
		result, err := strconv.ParseInt(text, 10, 64)
		return result, err
	case expressionmodel.ExpressionTypeDecimal:
		config, _ := decimalvalue.NormalizeDecimalConfig(nil)
		return decimalvalue.NormalizeDecimal(value, config)
	case expressionmodel.ExpressionTypeDate:
		if result, ok := value.(time.Time); ok {
			result = result.In(location)
			return time.Date(result.Year(), result.Month(), result.Day(), 0, 0, 0, 0, location), nil
		}
		return time.ParseInLocation("2006-01-02", strings.TrimSpace(fmt.Sprint(value)), location)
	case expressionmodel.ExpressionTypeDateTime:
		if result, ok := value.(time.Time); ok {
			return result.In(location), nil
		}
		result, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(fmt.Sprint(value)))
		if err != nil {
			return nil, err
		}
		return result.In(location), nil
	case expressionmodel.ExpressionTypeDuration:
		if result, ok := value.(time.Duration); ok {
			return result, nil
		}
		return time.ParseDuration(strings.TrimSpace(fmt.Sprint(value)))
	case expressionmodel.ExpressionTypeText, expressionmodel.ExpressionTypeRelation:
		result, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("text")
		}
		return result, nil
	case expressionmodel.ExpressionTypeList:
		var items []any
		switch typed := value.(type) {
		case []any:
			items = typed
		case []string:
			items = make([]any, len(typed))
			for index := range typed {
				items[index] = typed[index]
			}
		default:
			return nil, fmt.Errorf("list")
		}
		result := make([]any, len(items))
		for index, item := range items {
			normalized, err := expressionNormalizeValue(item, expressionmodel.ExpressionType{Kind: valueType.ElementType}, location)
			if err != nil {
				return nil, err
			}
			result[index] = normalized
		}
		return result, nil
	default:
		return nil, fmt.Errorf("type")
	}
}

func expressionCompare(left, right expressionmodel.ExpressionValue, operator string) bool {
	var comparison int
	switch left.Type.Kind {
	case expressionmodel.ExpressionTypeInteger:
		leftValue, rightValue := left.Value.(int64), right.Value.(int64)
		if leftValue < rightValue {
			comparison = -1
		} else if leftValue > rightValue {
			comparison = 1
		}
	case expressionmodel.ExpressionTypeDecimal:
		config, _ := decimalvalue.NormalizeDecimalConfig(nil)
		difference, _ := decimalvalue.SubtractDecimals(fmt.Sprint(left.Value), fmt.Sprint(right.Value), config)
		if strings.HasPrefix(difference, "-") {
			comparison = -1
		} else if difference != "0.00" {
			comparison = 1
		}
	case expressionmodel.ExpressionTypeDate, expressionmodel.ExpressionTypeDateTime:
		leftValue, rightValue := left.Value.(time.Time), right.Value.(time.Time)
		if leftValue.Before(rightValue) {
			comparison = -1
		} else if leftValue.After(rightValue) {
			comparison = 1
		}
	default:
		comparison = strings.Compare(fmt.Sprint(left.Value), fmt.Sprint(right.Value))
	}
	switch operator {
	case "eq":
		return comparison == 0
	case "ne":
		return comparison != 0
	case "gt":
		return comparison > 0
	case "gte":
		return comparison >= 0
	case "lt":
		return comparison < 0
	case "lte":
		return comparison <= 0
	default:
		return false
	}
}

func expressionTypes(values []expressionmodel.ExpressionValue) []expressionmodel.ExpressionType {
	result := make([]expressionmodel.ExpressionType, len(values))
	for index := range values {
		result[index] = values[index].Type
	}
	return result
}

func expressionError(expression expressionmodel.BusinessExpression, path, code string) error {
	return &expressionmodel.ExpressionDiagnostic{Code: code, Path: path, SourceReference: strings.TrimSpace(expression.SourceReference)}
}

func ExpressionDecode(raw json.RawMessage) (expressionmodel.BusinessExpression, error) {
	var expression expressionmodel.BusinessExpression
	if err := json.Unmarshal(raw, &expression); err != nil {
		return expression, &expressionmodel.ExpressionDiagnostic{Code: "backend.expression.document_invalid", Path: "$"}
	}
	return expression, nil
}
