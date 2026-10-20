package apperror

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

const maxErrorStackFrames = 32

type errorStackCarrier interface {
	ErrorStack() string
}

// StackOf returns a bounded, source-owned stack without exposing raw error
// text. Error implementations outside Foundation may implement ErrorStack to
// participate without depending on AppError.
func StackOf(err error) string {
	if err == nil {
		return ""
	}
	var carrier errorStackCarrier
	if !errors.As(err, &carrier) {
		return ""
	}
	return strings.TrimSpace(carrier.ErrorStack())
}

func captureStack(skip int) string {
	pcs := make([]uintptr, maxErrorStackFrames)
	count := runtime.Callers(skip+2, pcs)
	if count == 0 {
		return ""
	}
	frames := runtime.CallersFrames(pcs[:count])
	var builder strings.Builder
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(frame.Function)
			builder.WriteByte('\n')
			builder.WriteString(frame.File)
			builder.WriteByte(':')
			builder.WriteString(fmt.Sprintf("%d", frame.Line))
		}
		if !more {
			break
		}
	}
	return builder.String()
}
