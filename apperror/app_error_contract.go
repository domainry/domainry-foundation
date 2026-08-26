package apperror

import (
	"errors"
	"strings"
)

// New creates a normalized application error.
func New(kind ErrorKind, code string, err error, params map[string]string) error {
	return &AppError{Kind: kind, Code: NormalizeCode(kind, code), Params: cloneParams(params), Err: err}
}

func FromError(kind ErrorKind, err error) error {
	if err == nil {
		return New(kind, DefaultCode(kind), nil, nil)
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return err
	}
	if code, params, ok := codedDetails(err); ok {
		return &AppError{Kind: kind, Code: code, Params: params, Err: err}
	}
	return &AppError{Kind: kind, Code: DefaultCode(kind), Err: err}
}

func KindOf(err error) ErrorKind {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindInternal
}

func CodeOf(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode()
	}
	return DefaultCode(KindInternal)
}

func ParamsOf(err error) map[string]string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorParams()
	}
	return nil
}

func NormalizeCode(kind ErrorKind, code string) string {
	code = strings.TrimSpace(code)
	if IsI18nCode(code) {
		return code
	}
	return DefaultCode(kind)
}

func IsI18nCode(code string) bool {
	if !strings.Contains(code, ".") || strings.ContainsAny(code, " \t\n\r") {
		return false
	}
	for _, char := range code {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func DefaultCode(kind ErrorKind) string {
	switch kind {
	case KindBadRequest:
		return "backend.bad_request"
	case KindForbidden:
		return "backend.forbidden"
	case KindNotFound:
		return "backend.not_found"
	case KindConflict:
		return "backend.conflict"
	default:
		return "backend.internal"
	}
}

func ValueOrDefault(value, fallback string) string {
	if normalized := strings.TrimSpace(value); normalized != "" {
		return normalized
	}
	return fallback
}

func codedDetails(err error) (string, map[string]string, bool) {
	if err == nil {
		return "", nil, false
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if !errors.As(err, &coded) {
		return "", nil, false
	}
	code := strings.TrimSpace(coded.ErrorCode())
	if !IsI18nCode(code) {
		return "", nil, false
	}
	return code, cloneParams(coded.ErrorParams()), true
}

func cloneParams(params map[string]string) map[string]string {
	if len(params) == 0 {
		return nil
	}
	result := make(map[string]string, len(params))
	for key, value := range params {
		result[key] = value
	}
	return result
}
