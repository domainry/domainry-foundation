package operation

import (
	"fmt"
	"strings"
	"time"
)

func operationTimestampMillis(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0, fmt.Errorf("invalid UTC timestamp %q: %w", value, err)
	}
	return parsed.UTC().UnixMilli(), nil
}

func operationTimestampText(value int64) string {
	if value == 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339Nano)
}

func operationTimeMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func mustOperationTimestampMillis(value string) int64 {
	millis, _ := operationTimestampMillis(value)
	return millis
}
