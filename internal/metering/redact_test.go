package metering

import (
	"testing"
)

func TestRedactHeaders(t *testing.T) {
	headers := map[string][]string{
		"Authorization": {"Bearer secret-key"},
		"Content-Type":  {"application/json"},
		"X-Api-Key":     {"my-api-key"},
	}

	cfg := RedactConfig{
		AuthType:   "header",
		AuthConfig: map[string]string{"header_name": "X-Api-Key", "key": "my-api-key"},
	}

	redacted := RedactHeaders(headers, cfg)

	if redacted["Authorization"][0] != "[REDACTED]" {
		t.Errorf("expected Authorization to be redacted, got %s", redacted["Authorization"][0])
	}
	if redacted["X-Api-Key"][0] != "[REDACTED]" {
		t.Errorf("expected X-Api-Key to be redacted, got %s", redacted["X-Api-Key"][0])
	}
	if redacted["Content-Type"][0] != "application/json" {
		t.Errorf("expected Content-Type to be unchanged, got %s", redacted["Content-Type"][0])
	}
}

func TestRedactHeaders_Bearer(t *testing.T) {
	headers := map[string][]string{
		"Authorization": {"Bearer secret"},
	}
	cfg := RedactConfig{AuthType: "bearer", AuthConfig: map[string]string{"key": "secret"}}
	redacted := RedactHeaders(headers, cfg)
	if redacted["Authorization"][0] != "[REDACTED]" {
		t.Errorf("expected Authorization to be redacted")
	}
}

func TestRedactBody(t *testing.T) {
	body := []byte(`{"api_key":"my-secret-key-12345","data":"hello"}`)
	cfg := RedactConfig{
		AuthType:   "bearer",
		AuthConfig: map[string]string{"key": "my-secret-key-12345"},
	}

	redacted := RedactBody(body, cfg)
	expected := `{"api_key":"[REDACTED]","data":"hello"}`
	if string(redacted) != expected {
		t.Errorf("expected %s, got %s", expected, string(redacted))
	}
}

func TestRedactBody_Empty(t *testing.T) {
	result := RedactBody(nil, RedactConfig{})
	if result != nil {
		t.Errorf("expected nil for empty body, got %v", result)
	}
}

func TestRedactBody_ShortKey(t *testing.T) {
	body := []byte(`{"data":"key"}`)
	cfg := RedactConfig{AuthConfig: map[string]string{"key": "key"}}
	result := RedactBody(body, cfg)
	// Short key (<=4) should not be redacted to avoid false positives.
	if string(result) != `{"data":"key"}` {
		t.Errorf("expected no redaction for short key, got %s", string(result))
	}
}
