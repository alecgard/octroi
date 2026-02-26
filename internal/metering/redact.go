package metering

import (
	"strings"
)

// RedactConfig holds the tool's auth configuration for redaction purposes.
type RedactConfig struct {
	AuthType   string
	AuthConfig map[string]string
}

// RedactHeaders removes sensitive auth values from headers.
func RedactHeaders(headers map[string][]string, cfg RedactConfig) map[string][]string {
	redacted := make(map[string][]string, len(headers))
	for k, v := range headers {
		redacted[k] = v
	}

	// Always redact the Authorization header.
	if _, ok := redacted["Authorization"]; ok {
		redacted["Authorization"] = []string{"[REDACTED]"}
	}

	// Redact the tool-specific injected auth header.
	if cfg.AuthType == "header" {
		headerName := cfg.AuthConfig["header_name"]
		if headerName != "" {
			if _, ok := redacted[headerName]; ok {
				redacted[headerName] = []string{"[REDACTED]"}
			}
		}
	}

	return redacted
}

// RedactBody replaces known auth values in a body string with [REDACTED].
func RedactBody(body []byte, cfg RedactConfig) []byte {
	if len(body) == 0 {
		return body
	}

	s := string(body)

	// Redact the key value if present in the body.
	if key := cfg.AuthConfig["key"]; key != "" && len(key) > 4 {
		s = strings.ReplaceAll(s, key, "[REDACTED]")
	}

	return []byte(s)
}
