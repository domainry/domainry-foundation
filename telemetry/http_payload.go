package telemetry

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

type boundedByteCapture struct {
	limit     int
	total     int64
	data      []byte
	truncated bool
}

func newBoundedByteCapture(limit int) *boundedByteCapture {
	if limit <= 0 {
		limit = defaultDiagnosticContentBytes
	}
	return &boundedByteCapture{limit: limit, data: make([]byte, 0, limit)}
}

func (capture *boundedByteCapture) Write(payload []byte) {
	if capture == nil || len(payload) == 0 {
		return
	}
	capture.total += int64(len(payload))
	remaining := capture.limit - len(capture.data)
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		capture.data = append(capture.data, payload[:remaining]...)
	}
	if capture.total > int64(capture.limit) {
		capture.truncated = true
	}
}

type capturingReadCloser struct {
	io.ReadCloser
	capture *boundedByteCapture
}

func (reader *capturingReadCloser) Read(payload []byte) (int, error) {
	count, err := reader.ReadCloser.Read(payload)
	reader.capture.Write(payload[:count])
	return count, err
}

func diagnosticHTTPInput(request *http.Request, capture *boundedByteCapture) map[string]any {
	if request == nil {
		return nil
	}
	input := map[string]any{}
	if query := diagnosticQuery(request.URL); len(query) > 0 {
		input["query"] = query
	}
	if capture != nil {
		input["content"] = diagnosticHTTPContent(request.Header.Get("Content-Type"), request.ContentLength, capture)
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

func diagnosticHTTPOutput(header http.Header, capture *boundedByteCapture) map[string]any {
	if capture == nil {
		return nil
	}
	return map[string]any{"content": diagnosticHTTPContent(header.Get("Content-Type"), -1, capture)}
}

func diagnosticQuery(target *url.URL) map[string]any {
	if target == nil || target.RawQuery == "" {
		return nil
	}
	values := target.Query()
	result := make(map[string]any, len(values))
	for key, entries := range values {
		result[key] = append([]string(nil), entries...)
	}
	return result
}

func diagnosticHTTPContent(contentType string, declaredLength int64, capture *boundedByteCapture) map[string]any {
	result := map[string]any{
		"bytes":     capture.total,
		"truncated": capture.truncated || declaredLength > int64(capture.limit),
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err == nil && mediaType != "" {
		result["content_type"] = mediaType
	}
	if !diagnosticJSONMediaType(mediaType) {
		result["content_omitted"] = "non_json"
		return result
	}
	if result["truncated"] == true {
		result["content_omitted"] = "size_limit"
		return result
	}
	if len(capture.data) == 0 {
		return result
	}
	var value any
	if err := json.Unmarshal(capture.data, &value); err != nil {
		result["content_omitted"] = "invalid_json"
		return result
	}
	result["json"] = value
	return result
}

func diagnosticJSONMediaType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}
