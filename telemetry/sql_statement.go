package telemetry

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// renderExecutableSQL expands the placeholder forms used by the SQL drivers
// supported by Domainry. Quoted strings, identifiers, comments and PostgreSQL
// dollar-quoted bodies are copied without interpreting placeholder-like text.
func renderExecutableSQL(statement string, arguments []sqlDiagnosticArgument) string {
	if statement == "" || len(arguments) == 0 {
		return statement
	}
	named := make(map[string]sqlDiagnosticArgument, len(arguments))
	for _, argument := range arguments {
		if name := strings.ToLower(strings.TrimSpace(argument.name)); name != "" {
			named[name] = argument
		}
	}
	questionPlaceholders := !hasNonQuestionSQLPlaceholder(statement, arguments, named)
	var rendered strings.Builder
	rendered.Grow(len(statement) + len(arguments)*16)
	questionIndex := 0
	for index := 0; index < len(statement); {
		switch statement[index] {
		case '\'', '"', '`':
			end := sqlQuotedEnd(statement, index, statement[index])
			rendered.WriteString(statement[index:end])
			index = end
			continue
		case '[':
			end := sqlBracketIdentifierEnd(statement, index)
			rendered.WriteString(statement[index:end])
			index = end
			continue
		case '-':
			if index+1 < len(statement) && statement[index+1] == '-' {
				end := sqlLineEnd(statement, index+2)
				rendered.WriteString(statement[index:end])
				index = end
				continue
			}
		case '#':
			end := sqlLineEnd(statement, index+1)
			rendered.WriteString(statement[index:end])
			index = end
			continue
		case '/':
			if index+1 < len(statement) && statement[index+1] == '*' {
				end := sqlBlockCommentEnd(statement, index+2)
				rendered.WriteString(statement[index:end])
				index = end
				continue
			}
		case '$':
			if tag := sqlDollarQuoteTag(statement[index:]); tag != "" {
				end := index + len(tag)
				if relative := strings.Index(statement[end:], tag); relative >= 0 {
					end += relative + len(tag)
				} else {
					end = len(statement)
				}
				rendered.WriteString(statement[index:end])
				index = end
				continue
			}
			end := sqlDigitsEnd(statement, index+1)
			if end > index+1 {
				ordinal, _ := strconv.Atoi(statement[index+1 : end])
				if ordinal > 0 && ordinal <= len(arguments) {
					rendered.WriteString(sqlDiagnosticLiteral(arguments[ordinal-1].value))
					index = end
					continue
				}
			}
		case '?':
			if questionPlaceholders && (index+1 >= len(statement) || (statement[index+1] != '|' && statement[index+1] != '&')) && questionIndex < len(arguments) {
				rendered.WriteString(sqlDiagnosticLiteral(arguments[questionIndex].value))
				questionIndex++
				index++
				continue
			}
		case '@':
			if index+1 < len(statement) && statement[index+1] != '@' {
				end := sqlIdentifierEnd(statement, index+1)
				if end > index+1 {
					if argument, found := sqlNamedArgument(statement[index+1:end], arguments, named); found {
						rendered.WriteString(sqlDiagnosticLiteral(argument.value))
						index = end
						continue
					}
				}
			}
		case ':':
			if (index == 0 || statement[index-1] != ':') && (index+1 >= len(statement) || statement[index+1] != '=') {
				end := sqlIdentifierEnd(statement, index+1)
				if end > index+1 {
					if argument, found := sqlNamedArgument(statement[index+1:end], arguments, named); found {
						rendered.WriteString(sqlDiagnosticLiteral(argument.value))
						index = end
						continue
					}
				}
			}
		}
		rendered.WriteByte(statement[index])
		index++
	}
	return rendered.String()
}

func hasNonQuestionSQLPlaceholder(statement string, arguments []sqlDiagnosticArgument, named map[string]sqlDiagnosticArgument) bool {
	for index := 0; index < len(statement); {
		switch statement[index] {
		case '\'', '"', '`':
			index = sqlQuotedEnd(statement, index, statement[index])
			continue
		case '[':
			index = sqlBracketIdentifierEnd(statement, index)
			continue
		case '-':
			if index+1 < len(statement) && statement[index+1] == '-' {
				index = sqlLineEnd(statement, index+2)
				continue
			}
		case '#':
			index = sqlLineEnd(statement, index+1)
			continue
		case '/':
			if index+1 < len(statement) && statement[index+1] == '*' {
				index = sqlBlockCommentEnd(statement, index+2)
				continue
			}
		case '$':
			if tag := sqlDollarQuoteTag(statement[index:]); tag != "" {
				end := index + len(tag)
				if relative := strings.Index(statement[end:], tag); relative >= 0 {
					index = end + relative + len(tag)
				} else {
					index = len(statement)
				}
				continue
			}
			end := sqlDigitsEnd(statement, index+1)
			if end > index+1 {
				ordinal, _ := strconv.Atoi(statement[index+1 : end])
				if ordinal > 0 && ordinal <= len(arguments) {
					return true
				}
			}
		case '@':
			if index+1 < len(statement) && statement[index+1] != '@' {
				end := sqlIdentifierEnd(statement, index+1)
				if end > index+1 {
					if _, found := sqlNamedArgument(statement[index+1:end], arguments, named); found {
						return true
					}
				}
			}
		case ':':
			if (index == 0 || statement[index-1] != ':') && (index+1 >= len(statement) || statement[index+1] != '=') {
				end := sqlIdentifierEnd(statement, index+1)
				if end > index+1 {
					if _, found := sqlNamedArgument(statement[index+1:end], arguments, named); found {
						return true
					}
				}
			}
		}
		index++
	}
	return false
}

func sqlNamedArgument(name string, arguments []sqlDiagnosticArgument, named map[string]sqlDiagnosticArgument) (sqlDiagnosticArgument, bool) {
	if argument, found := named[strings.ToLower(strings.TrimSpace(name))]; found {
		return argument, true
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "p") {
		ordinal, err := strconv.Atoi(lower[1:])
		if err == nil && ordinal > 0 && ordinal <= len(arguments) {
			return arguments[ordinal-1], true
		}
	}
	return sqlDiagnosticArgument{}, false
}

func sqlDiagnosticLiteral(value any) string {
	if value == nil {
		return "NULL"
	}
	if valuer, ok := value.(driver.Valuer); ok {
		resolved, err := valuer.Value()
		if err == nil {
			return sqlDiagnosticLiteral(resolved)
		}
	}
	switch typed := value.(type) {
	case string:
		return sqlQuotedString(typed)
	case []byte:
		return "X'" + hex.EncodeToString(typed) + "'"
	case time.Time:
		return sqlQuotedString(typed.Format("2006-01-02 15:04:05.999999999-07:00"))
	case bool:
		if typed {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return strconv.FormatInt(int64(typed), 10)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return sqlFloatLiteral(float64(typed), 32)
	case float64:
		return sqlFloatLiteral(typed, 64)
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() {
		switch reflected.Kind() {
		case reflect.String:
			return sqlQuotedString(reflected.String())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.FormatInt(reflected.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.FormatUint(reflected.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			return sqlFloatLiteral(reflected.Float(), reflected.Type().Bits())
		case reflect.Bool:
			if reflected.Bool() {
				return "TRUE"
			}
			return "FALSE"
		}
	}
	return sqlQuotedString(fmt.Sprint(value))
}

func sqlFloatLiteral(value float64, bits int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return sqlQuotedString(strconv.FormatFloat(value, 'g', -1, bits))
	}
	return strconv.FormatFloat(value, 'g', -1, bits)
}

func sqlQuotedString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func sqlQuotedEnd(statement string, start int, quote byte) int {
	for index := start + 1; index < len(statement); index++ {
		if statement[index] == '\\' && index+1 < len(statement) {
			index++
			continue
		}
		if statement[index] != quote {
			continue
		}
		if index+1 < len(statement) && statement[index+1] == quote {
			index++
			continue
		}
		return index + 1
	}
	return len(statement)
}

func sqlBracketIdentifierEnd(statement string, start int) int {
	for index := start + 1; index < len(statement); index++ {
		if statement[index] != ']' {
			continue
		}
		if index+1 < len(statement) && statement[index+1] == ']' {
			index++
			continue
		}
		return index + 1
	}
	return len(statement)
}

func sqlLineEnd(statement string, start int) int {
	if relative := strings.IndexByte(statement[start:], '\n'); relative >= 0 {
		return start + relative + 1
	}
	return len(statement)
}

func sqlBlockCommentEnd(statement string, start int) int {
	if relative := strings.Index(statement[start:], "*/"); relative >= 0 {
		return start + relative + 2
	}
	return len(statement)
}

func sqlDigitsEnd(statement string, start int) int {
	index := start
	for index < len(statement) && statement[index] >= '0' && statement[index] <= '9' {
		index++
	}
	return index
}

func sqlIdentifierEnd(statement string, start int) int {
	index := start
	for index < len(statement) {
		character := statement[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			break
		}
		index++
	}
	return index
}

func sqlDollarQuoteTag(statement string) string {
	if len(statement) < 2 || statement[0] != '$' {
		return ""
	}
	if statement[1] == '$' {
		return "$$"
	}
	if (statement[1] < 'a' || statement[1] > 'z') && (statement[1] < 'A' || statement[1] > 'Z') && statement[1] != '_' {
		return ""
	}
	for index := 2; index < len(statement); index++ {
		character := statement[index]
		if character == '$' {
			return statement[:index+1]
		}
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return ""
		}
	}
	return ""
}
