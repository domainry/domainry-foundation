package apperror

import "strings"

const redactedErrorParamValue = "[REDACTED]"

// SanitizeParams redacts secrets before coded error parameters cross a transport boundary.
func SanitizeParams(params map[string]string) map[string]string {
	if len(params) == 0 {
		return nil
	}
	result := make(map[string]string, len(params))
	sensitiveField := false
	for _, key := range []string{"field", "field_path", "parameter_path", "path"} {
		if isSensitiveAuthoringErrorName(params[key]) {
			sensitiveField = true
			break
		}
	}
	for key, value := range params {
		if isSensitiveAuthoringErrorName(key) || sensitiveField && isAuthoringErrorValueKey(key) {
			result[key] = redactedErrorParamValue
			continue
		}
		result[key] = value
	}
	return result
}

func isAuthoringErrorValueKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "actual", "value", "input", "payload", "material":
		return true
	default:
		return false
	}
}

func isSensitiveAuthoringErrorName(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, marker := range []string{"password", "access_token", "refresh_token", "id_token", "client_secret", "secret_value", "secret_material", "authorization", "credential", "signature", "api_key_value", "private_key"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
