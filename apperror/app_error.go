package apperror

import "strings"

type ErrorKind string

const (
	KindBadRequest  ErrorKind = "bad_request"
	KindForbidden   ErrorKind = "forbidden"
	KindNotFound    ErrorKind = "not_found"
	KindConflict    ErrorKind = "conflict"
	KindRateLimited ErrorKind = "rate_limited"
	KindUnavailable ErrorKind = "unavailable"
	KindInternal    ErrorKind = "internal"
)

type AppError struct {
	Kind   ErrorKind
	Code   string
	Params map[string]string
	Err    error
}

// CodedError carries a stable machine-readable error code without assigning an
// HTTP/application error kind. Domain policies and adapters use it at leaf
// boundaries; Application maps it to an AppError.
type CodedError struct {
	Code   string
	Params map[string]string
}

func (e *CodedError) Error() string { return e.Code }

func (e *CodedError) ErrorCode() string { return strings.TrimSpace(e.Code) }

func (e *CodedError) ErrorParams() map[string]string {
	if len(e.Params) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.Params))
	for key, value := range e.Params {
		out[key] = value
	}
	return out
}

func (e *AppError) Error() string {
	if e.Code != "" {
		return e.Code
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *AppError) Unwrap() error { return e.Err }

func (e *AppError) ErrorCode() string { return strings.TrimSpace(e.Code) }

func (e *AppError) ErrorParams() map[string]string {
	if len(e.Params) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.Params))
	for key, value := range e.Params {
		out[key] = value
	}
	return out
}
