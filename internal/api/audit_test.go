package api

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "remote addr only",
			remoteAddr: "192.168.1.1:12345",
			want:       "192.168.1.1:12345",
		},
		{
			name:       "single X-Forwarded-For",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50"},
			want:       "203.0.113.50",
		},
		{
			name:       "multiple X-Forwarded-For takes first",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.50, 70.41.3.18, 150.172.238.178"},
			want:       "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For with spaces",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "  203.0.113.50 , 70.41.3.18 "},
			want:       "203.0.113.50",
		},
		{
			name:       "X-Real-IP fallback",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "203.0.113.50"},
			want:       "203.0.113.50",
		},
		{
			name:       "X-Real-IP with whitespace",
			remoteAddr: "10.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "  203.0.113.50  "},
			want:       "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.50",
				"X-Real-IP":       "198.51.100.10",
			},
			want: "203.0.113.50",
		},
		{
			name:       "empty X-Forwarded-For falls through to X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			headers: map[string]string{
				"X-Forwarded-For": "",
				"X-Real-IP":       "198.51.100.10",
			},
			want: "198.51.100.10",
		},
		{
			name:       "empty headers fall through to RemoteAddr",
			remoteAddr: "10.0.0.1:12345",
			headers: map[string]string{
				"X-Forwarded-For": "",
				"X-Real-IP":       "",
			},
			want: "10.0.0.1:12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				if v != "" {
					r.Header.Set(k, v)
				}
			}
			got := clientIP(r)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseForwardedFor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"203.0.113.50", "203.0.113.50"},
		{"203.0.113.50, 70.41.3.18", "203.0.113.50"},
		{"203.0.113.50, 70.41.3.18, 150.172.238.178", "203.0.113.50"},
		{"  203.0.113.50  ", "203.0.113.50"},
		{"  203.0.113.50 , 70.41.3.18 ", "203.0.113.50"},
		{"2001:db8::1, 203.0.113.50", "2001:db8::1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseForwardedFor(tt.input)
			if got != tt.want {
				t.Errorf("parseForwardedFor(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
