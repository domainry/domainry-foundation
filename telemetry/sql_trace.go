package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const maxSQLTraceTemplateRunes = 512

type sqlExecutionTrace struct {
	ctx         context.Context
	span        trace.Span
	operation   string
	template    string
	fingerprint string
}

func startSQLExecutionTrace(ctx context.Context, role, query string) sqlExecutionTrace {
	if ctx == nil {
		ctx = context.Background()
	}
	template := sanitizeSQLTraceTemplate(query)
	operation := sqlOperation(template)
	fingerprint := sqlTraceFingerprint(template)
	attributes := []attribute.KeyValue{
		attribute.String("db.operation.name", operation),
		attribute.String("domainry.db.role", boundedSQLRole(role)),
	}
	if template != "" {
		attributes = append(attributes,
			attribute.String("db.query.summary", template),
			attribute.String("db.query.fingerprint", fingerprint),
		)
	}
	spanCtx, span := otel.Tracer("github.com/domainry/domainry-foundation/telemetry/sql").Start(
		ctx,
		"SQL "+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attributes...),
	)
	return sqlExecutionTrace{ctx: spanCtx, span: span, operation: operation, template: template, fingerprint: fingerprint}
}

func (execution sqlExecutionTrace) finish(err error, output map[string]any) {
	if execution.span == nil {
		return
	}
	if err != nil {
		execution.span.SetStatus(codes.Error, "sql_error")
		execution.span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", err)))
	}
	if output != nil {
		if affected, ok := integerAttribute(output["rows_affected"]); ok {
			execution.span.SetAttributes(attribute.Int64("db.response.affected_rows", affected))
		}
		if returned, ok := integerAttribute(output["row_count"]); ok {
			execution.span.SetAttributes(attribute.Int64("db.response.returned_rows", returned))
		}
	}
	execution.span.End()
}

func integerAttribute(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed <= uint64(^uint64(0)>>1) {
			return int64(typed), true
		}
	}
	return 0, false
}

func sqlTraceFingerprint(template string) string {
	if template == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(template))
	return hex.EncodeToString(digest[:8])
}

// sanitizeSQLTraceTemplate keeps the operation, table and column names needed
// to identify a statement while removing comments and literal values. The
// result is safe to attach to every SQL span even when raw SQL logging is off.
func sanitizeSQLTraceTemplate(input string) string {
	var output strings.Builder
	for index := 0; index < len(input); {
		switch {
		case strings.HasPrefix(input[index:], "--"):
			index += 2
			for index < len(input) && input[index] != '\n' && input[index] != '\r' {
				index++
			}
			output.WriteByte(' ')
		case strings.HasPrefix(input[index:], "/*"):
			index += 2
			for index < len(input) && !strings.HasPrefix(input[index:], "*/") {
				index++
			}
			if index < len(input) {
				index += 2
			}
			output.WriteByte(' ')
		case input[index] == '\'':
			index = consumeSQLTraceSingleQuote(input, index+1)
			output.WriteByte('?')
		case input[index] == '$':
			if end, ok := sqlTraceDollarQuoteEnd(input, index); ok {
				index = end
				output.WriteByte('?')
			} else {
				index = consumeSQLTracePlaceholder(input, index, &output)
			}
		default:
			r, size := utf8.DecodeRuneInString(input[index:])
			if unicode.IsDigit(r) && sqlTraceNumericBoundary(input, index) {
				index += size
				for index < len(input) {
					next, nextSize := utf8.DecodeRuneInString(input[index:])
					if !unicode.IsDigit(next) && next != '.' {
						break
					}
					index += nextSize
				}
				output.WriteByte('?')
			} else {
				output.WriteRune(r)
				index += size
			}
		}
	}
	return truncateSQLTraceRunes(strings.Join(strings.Fields(output.String()), " "), maxSQLTraceTemplateRunes)
}

func consumeSQLTraceSingleQuote(input string, index int) int {
	for index < len(input) {
		if input[index] != '\'' {
			index++
			continue
		}
		if index+1 < len(input) && input[index+1] == '\'' {
			index += 2
			continue
		}
		return index + 1
	}
	return index
}

func sqlTraceDollarQuoteEnd(input string, start int) (int, bool) {
	end := start + 1
	for end < len(input) && (input[end] == '_' || input[end] >= 'a' && input[end] <= 'z' || input[end] >= 'A' && input[end] <= 'Z' || end > start+1 && input[end] >= '0' && input[end] <= '9') {
		end++
	}
	if end >= len(input) || input[end] != '$' {
		return start, false
	}
	delimiter := input[start : end+1]
	closing := strings.Index(input[end+1:], delimiter)
	if closing < 0 {
		return len(input), true
	}
	return end + 1 + closing + len(delimiter), true
}

func consumeSQLTracePlaceholder(input string, index int, output *strings.Builder) int {
	end := index + 1
	for end < len(input) && input[end] >= '0' && input[end] <= '9' {
		end++
	}
	if end > index+1 {
		output.WriteByte('?')
		return end
	}
	output.WriteByte('$')
	return index + 1
}

func sqlTraceNumericBoundary(input string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(input[:index])
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous) && previous != '_'
}

func truncateSQLTraceRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum-1]) + "…"
}
