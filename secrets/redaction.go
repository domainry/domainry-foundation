package secrets

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const Redacted = "[REDACTED]"

var assignmentPattern = regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|authorization|cookie|session|dsn)(\s*[:=]\s*)([^\s,;]+)`)

func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(key)))
	for _, marker := range []string{"password", "secret", "token", "api_key", "apikey", "access_key", "private_key", "credential", "authorization", "cookie", "session", "dsn"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func RedactMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	return RedactValue(value).(map[string]any)
}

func RedactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if IsSensitiveKey(key) {
				out[key] = Redacted
			} else {
				out[key] = RedactValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = RedactValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for index, item := range typed {
			out[index] = RedactMap(item)
		}
		return out
	case error:
		return RedactText(typed.Error())
	case string:
		return RedactText(typed)
	default:
		return value
	}
}

func RedactText(value string) string {
	value = assignmentPattern.ReplaceAllString(value, `$1$2`+Redacted)
	for _, field := range strings.Fields(value) {
		parsed, err := url.Parse(field)
		if err == nil && parsed.User != nil {
			parsed.User = url.UserPassword(Redacted, Redacted)
			value = strings.ReplaceAll(value, field, parsed.String())
		}
	}
	return value
}

func RedactError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", RedactText(err.Error()))
}
