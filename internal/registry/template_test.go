package registry

import (
	"testing"
)

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		name      string
		tmpl      string
		vars      map[string]string
		want      string
		wantErr   bool
	}{
		{
			name: "simple replacement",
			tmpl: "https://{instance}.atlassian.net/rest/api/3",
			vars: map[string]string{"instance": "mycompany"},
			want: "https://mycompany.atlassian.net/rest/api/3",
		},
		{
			name: "multiple vars",
			tmpl: "https://{host}/api/{version}/data",
			vars: map[string]string{"host": "example.com", "version": "v2"},
			want: "https://example.com/api/v2/data",
		},
		{
			name:    "missing var error",
			tmpl:    "https://{instance}.example.com/{path}",
			vars:    map[string]string{"instance": "test"},
			wantErr: true,
		},
		{
			name: "no-op passthrough",
			tmpl: "https://api.example.com/v1",
			vars: map[string]string{},
			want: "https://api.example.com/v1",
		},
		{
			name: "empty map",
			tmpl: "https://static.example.com",
			vars: map[string]string{},
			want: "https://static.example.com",
		},
		{
			name: "nil map no placeholders",
			tmpl: "https://api.example.com",
			vars: nil,
			want: "https://api.example.com",
		},
		{
			name:    "nil map with placeholders",
			tmpl:    "https://{host}.example.com",
			vars:    nil,
			wantErr: true,
		},
		{
			name: "duplicate vars resolved",
			tmpl: "https://{host}/{host}/path",
			vars: map[string]string{"host": "myhost"},
			want: "https://myhost/myhost/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTemplate(tt.tmpl, tt.vars)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTemplateVars(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		want []string
	}{
		{
			name: "single var",
			tmpl: "https://{instance}.example.com",
			want: []string{"instance"},
		},
		{
			name: "multiple unique vars",
			tmpl: "https://{host}/api/{version}",
			want: []string{"host", "version"},
		},
		{
			name: "duplicate vars",
			tmpl: "https://{host}/{host}",
			want: []string{"host"},
		},
		{
			name: "no vars",
			tmpl: "https://example.com",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTemplateVars(tt.tmpl)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
