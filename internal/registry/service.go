package registry

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// Validation errors returned by the Service layer.
var (
	ErrNameRequired        = errors.New("name is required")
	ErrDescriptionRequired = errors.New("description is required")
	ErrEndpointInvalid     = errors.New("endpoint must be a valid URL")
	ErrAuthTypeInvalid     = errors.New("auth_type must be one of: none, bearer, header")
)

// validAuthTypes is the set of accepted auth_type values.
var validAuthTypes = map[string]bool{
	"none":   true,
	"bearer": true,
	"header": true,
}

// Service provides validated business logic over the registry Store.
type Service struct {
	store *Store
}

// NewService creates a new Service wrapping the given Store.
func NewService(store *Store) *Service {
	return &Service{store: store}
}

// Create validates the input, normalizes tags, and creates the tool.
func (s *Service) Create(ctx context.Context, input CreateToolInput) (*Tool, error) {
	if err := validateCreate(input); err != nil {
		return nil, err
	}
	input.Tags = normalizeTags(input.Tags)
	if input.AuthType == "" {
		input.AuthType = "none"
	}
	if input.AuthConfig == nil {
		input.AuthConfig = map[string]string{}
	}
	return s.store.Create(ctx, input)
}

// GetByID retrieves a tool by its ID.
func (s *Service) GetByID(ctx context.Context, id string) (*Tool, error) {
	return s.store.GetByID(ctx, id)
}

// List returns a paginated list of tools.
func (s *Service) List(ctx context.Context, params ToolListParams) ([]*Tool, string, error) {
	return s.store.List(ctx, params)
}

// Update validates the input, normalizes tags if provided, and applies the update.
func (s *Service) Update(ctx context.Context, id string, input UpdateToolInput) (*Tool, error) {
	if err := validateUpdate(input); err != nil {
		return nil, err
	}
	if input.Tags != nil {
		normalized := normalizeTags(*input.Tags)
		input.Tags = &normalized
	}
	return s.store.Update(ctx, id, input)
}

// Delete removes a tool by its ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Search performs a text/tag search across tools.
func (s *Service) Search(ctx context.Context, query string, limit int, cursor string) ([]*Tool, string, error) {
	return s.store.Search(ctx, query, limit, cursor)
}

// validateCreate checks that all required fields are present and valid.
func validateCreate(input CreateToolInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return ErrNameRequired
	}
	if strings.TrimSpace(input.Description) == "" {
		return ErrDescriptionRequired
	}
	if err := validateEndpoint(input.Endpoint); err != nil {
		return err
	}
	if input.AuthType != "" {
		if !validAuthTypes[input.AuthType] {
			return ErrAuthTypeInvalid
		}
	}
	return nil
}

// validateUpdate checks that any provided fields are valid.
func validateUpdate(input UpdateToolInput) error {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return ErrNameRequired
	}
	if input.Description != nil && strings.TrimSpace(*input.Description) == "" {
		return ErrDescriptionRequired
	}
	if input.Endpoint != nil {
		if err := validateEndpoint(*input.Endpoint); err != nil {
			return err
		}
	}
	if input.AuthType != nil {
		if !validAuthTypes[*input.AuthType] {
			return ErrAuthTypeInvalid
		}
	}
	return nil
}

// validateEndpoint checks that the endpoint is a well-formed URL with a scheme and host.
func validateEndpoint(endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return ErrEndpointInvalid
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ErrEndpointInvalid
	}
	return nil
}

// normalizeTags lowercases, trims whitespace, and deduplicates tags.
func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		t := strings.ToLower(strings.TrimSpace(tag))
		if t == "" {
			continue
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
