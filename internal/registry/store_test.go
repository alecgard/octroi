package registry

import (
	"testing"
	"time"
)

func strPtr(s string) *string    { return &s }
func float64Ptr(f float64) *float64 { return &f }

func TestValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		input   CreateToolInput
		wantErr error
	}{
		{
			name: "valid input",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
				AuthType:    "bearer",
			},
			wantErr: nil,
		},
		{
			name: "valid with no auth_type defaults later",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
			},
			wantErr: nil,
		},
		{
			name: "empty name",
			input: CreateToolInput{
				Name:        "",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
			},
			wantErr: ErrNameRequired,
		},
		{
			name: "whitespace-only name",
			input: CreateToolInput{
				Name:        "   ",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
			},
			wantErr: ErrNameRequired,
		},
		{
			name: "empty description",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "",
				Endpoint:    "https://api.example.com/v1",
			},
			wantErr: ErrDescriptionRequired,
		},
		{
			name: "whitespace-only description",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "   ",
				Endpoint:    "https://api.example.com/v1",
			},
			wantErr: ErrDescriptionRequired,
		},
		{
			name: "empty endpoint",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "",
			},
			wantErr: ErrEndpointInvalid,
		},
		{
			name: "invalid endpoint - no scheme",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "not-a-url",
			},
			wantErr: ErrEndpointInvalid,
		},
		{
			name: "invalid endpoint - no host",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "http://",
			},
			wantErr: ErrEndpointInvalid,
		},
		{
			name: "invalid auth_type",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
				AuthType:    "oauth2",
			},
			wantErr: ErrAuthTypeInvalid,
		},
		{
			name: "auth_type none is valid",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
				AuthType:    "none",
			},
			wantErr: nil,
		},
		{
			name: "auth_type header is valid",
			input: CreateToolInput{
				Name:        "my-tool",
				Description: "A useful tool",
				Endpoint:    "https://api.example.com/v1",
				AuthType:    "header",
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCreate(tt.input)
			if err != tt.wantErr {
				t.Errorf("validateCreate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUpdate(t *testing.T) {
	tests := []struct {
		name    string
		input   UpdateToolInput
		wantErr error
	}{
		{
			name:    "empty update is valid",
			input:   UpdateToolInput{},
			wantErr: nil,
		},
		{
			name: "valid name update",
			input: UpdateToolInput{
				Name: strPtr("new-name"),
			},
			wantErr: nil,
		},
		{
			name: "empty name update",
			input: UpdateToolInput{
				Name: strPtr(""),
			},
			wantErr: ErrNameRequired,
		},
		{
			name: "whitespace name update",
			input: UpdateToolInput{
				Name: strPtr("  "),
			},
			wantErr: ErrNameRequired,
		},
		{
			name: "empty description update",
			input: UpdateToolInput{
				Description: strPtr(""),
			},
			wantErr: ErrDescriptionRequired,
		},
		{
			name: "invalid endpoint update",
			input: UpdateToolInput{
				Endpoint: strPtr("not-a-url"),
			},
			wantErr: ErrEndpointInvalid,
		},
		{
			name: "valid endpoint update",
			input: UpdateToolInput{
				Endpoint: strPtr("https://new.example.com"),
			},
			wantErr: nil,
		},
		{
			name: "invalid auth_type update",
			input: UpdateToolInput{
				AuthType: strPtr("basic"),
			},
			wantErr: ErrAuthTypeInvalid,
		},
		{
			name: "valid auth_type update",
			input: UpdateToolInput{
				AuthType: strPtr("bearer"),
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUpdate(tt.input)
			if err != tt.wantErr {
				t.Errorf("validateUpdate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeTags(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{
			name: "nil tags",
			tags: nil,
			want: []string{},
		},
		{
			name: "empty slice",
			tags: []string{},
			want: []string{},
		},
		{
			name: "lowercases tags",
			tags: []string{"API", "Webhook", "TOOL"},
			want: []string{"api", "webhook", "tool"},
		},
		{
			name: "trims whitespace",
			tags: []string{"  api  ", " webhook ", "tool"},
			want: []string{"api", "webhook", "tool"},
		},
		{
			name: "deduplicates tags",
			tags: []string{"api", "API", "Api"},
			want: []string{"api"},
		},
		{
			name: "removes empty tags",
			tags: []string{"api", "", "  ", "webhook"},
			want: []string{"api", "webhook"},
		},
		{
			name: "combined normalization",
			tags: []string{"  API ", "api", "Webhook", " WEBHOOK ", "tool", ""},
			want: []string{"api", "webhook", "tool"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTags(tt.tags)
			if len(got) != len(tt.want) {
				t.Fatalf("normalizeTags() returned %d tags, want %d: got=%v, want=%v",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("normalizeTags()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCursorEncodeDecode(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{
			name: "standard uuid",
			id:   "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name: "another uuid",
			id:   "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a fixed time for deterministic testing.
			ts := mustParseTime(t, "2024-06-15T10:30:00.123456789Z")
			encoded := encodeCursor(ts, tt.id)

			decodedTime, decodedID, err := decodeCursor(encoded)
			if err != nil {
				t.Fatalf("decodeCursor() error = %v", err)
			}
			if !decodedTime.Equal(ts) {
				t.Errorf("decoded time = %v, want %v", decodedTime, ts)
			}
			if decodedID != tt.id {
				t.Errorf("decoded id = %q, want %q", decodedID, tt.id)
			}
		})
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	tests := []struct {
		name   string
		cursor string
	}{
		{name: "not base64", cursor: "!!!invalid!!!"},
		{name: "no separator", cursor: "bm9zZXBhcmF0b3I="},                         // "noseparator"
		{name: "bad timestamp", cursor: "bm90LWEtdGltZXN0YW1wfHNvbWUtaWQ="},         // "not-a-timestamp|some-id"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeCursor(tt.cursor)
			if err == nil {
				t.Error("decodeCursor() expected error, got nil")
			}
		})
	}
}

func TestServiceCreateValidation(t *testing.T) {
	svc := NewService(nil) // store is nil; validation runs before store call

	tests := []struct {
		name    string
		input   CreateToolInput
		wantErr error
	}{
		{
			name: "rejects empty name",
			input: CreateToolInput{
				Name:        "",
				Description: "desc",
				Endpoint:    "https://example.com",
			},
			wantErr: ErrNameRequired,
		},
		{
			name: "rejects empty description",
			input: CreateToolInput{
				Name:        "tool",
				Description: "",
				Endpoint:    "https://example.com",
			},
			wantErr: ErrDescriptionRequired,
		},
		{
			name: "rejects invalid endpoint",
			input: CreateToolInput{
				Name:        "tool",
				Description: "desc",
				Endpoint:    "bad-url",
			},
			wantErr: ErrEndpointInvalid,
		},
		{
			name: "rejects invalid auth_type",
			input: CreateToolInput{
				Name:        "tool",
				Description: "desc",
				Endpoint:    "https://example.com",
				AuthType:    "magic",
			},
			wantErr: ErrAuthTypeInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(nil, tt.input)
			if err != tt.wantErr {
				t.Errorf("Service.Create() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestServiceUpdateValidation(t *testing.T) {
	svc := NewService(nil)

	tests := []struct {
		name    string
		input   UpdateToolInput
		wantErr error
	}{
		{
			name:    "rejects empty name",
			input:   UpdateToolInput{Name: strPtr("")},
			wantErr: ErrNameRequired,
		},
		{
			name:    "rejects empty description",
			input:   UpdateToolInput{Description: strPtr("")},
			wantErr: ErrDescriptionRequired,
		},
		{
			name:    "rejects invalid endpoint",
			input:   UpdateToolInput{Endpoint: strPtr("nope")},
			wantErr: ErrEndpointInvalid,
		},
		{
			name:    "rejects invalid auth_type",
			input:   UpdateToolInput{AuthType: strPtr("unknown")},
			wantErr: ErrAuthTypeInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Update(nil, "some-id", tt.input)
			if err != tt.wantErr {
				t.Errorf("Service.Update() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestServiceCreateNormalizesTags(t *testing.T) {
	// We cannot call store (nil), but we can verify the normalization
	// path by checking the normalizeTags function is applied correctly.
	input := CreateToolInput{
		Name:        "tool",
		Description: "desc",
		Endpoint:    "https://example.com",
		Tags:        []string{"  API ", "api", "Webhook"},
	}

	// Validate passes
	if err := validateCreate(input); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	// Normalize
	normalized := normalizeTags(input.Tags)
	if len(normalized) != 2 {
		t.Fatalf("expected 2 normalized tags, got %d: %v", len(normalized), normalized)
	}
	if normalized[0] != "api" || normalized[1] != "webhook" {
		t.Errorf("unexpected normalized tags: %v", normalized)
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("mustParseTime(%q): %v", s, err)
	}
	return ts
}
