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
	ErrAuthTypeInvalid     = errors.New("auth_type must be one of: none, bearer, header, query")
	ErrModeInvalid         = errors.New("mode must be one of: service, api")
	ErrVariablesMissing    = errors.New("variables do not satisfy all template placeholders")
)

// validAuthTypes is the set of accepted auth_type values.
var validAuthTypes = map[string]bool{
	"none":   true,
	"bearer": true,
	"header": true,
	"query":  true,
}

// validModes is the set of accepted mode values.
var validModes = map[string]bool{
	"service": true,
	"api":     true,
}

// Service provides validated business logic over the registry Store.
type Service struct {
	store *Store
}

// NewService creates a new Service wrapping the given Store.
func NewService(store *Store) *Service {
	return &Service{store: store}
}

// Create validates the input and creates the tool.
func (s *Service) Create(ctx context.Context, input CreateToolInput) (*Tool, error) {
	if input.Mode == "" {
		input.Mode = "service"
	}
	if input.AuthType == "" {
		input.AuthType = "none"
	}
	if input.AuthConfig == nil {
		input.AuthConfig = map[string]string{}
	}
	if input.Variables == nil {
		input.Variables = map[string]string{}
	}
	if err := validateCreate(input); err != nil {
		return nil, err
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

// Update validates the input and applies the update.
func (s *Service) Update(ctx context.Context, id string, input UpdateToolInput) (*Tool, error) {
	if err := validateUpdate(input); err != nil {
		return nil, err
	}
	// Cross-field validation for API mode: when endpoint or variables change,
	// we need to validate the template against the full set of variables.
	if input.Mode != nil || input.Endpoint != nil || input.Variables != nil {
		existing, err := s.store.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		mode := existing.Mode
		if input.Mode != nil {
			mode = *input.Mode
		}
		if mode == "api" {
			endpoint := existing.Endpoint
			if input.Endpoint != nil {
				endpoint = *input.Endpoint
			}
			variables := existing.Variables
			if input.Variables != nil {
				variables = *input.Variables
			}
			if err := validateAPIEndpoint(endpoint, variables); err != nil {
				return nil, err
			}
		}
	}
	return s.store.Update(ctx, id, input)
}

// Delete removes a tool by its ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Search performs a text search across tools.
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
	if input.Mode != "" && !validModes[input.Mode] {
		return ErrModeInvalid
	}
	if input.Mode == "api" {
		if err := validateAPIEndpoint(input.Endpoint, input.Variables); err != nil {
			return err
		}
	} else {
		if err := validateEndpoint(input.Endpoint); err != nil {
			return err
		}
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
	if input.Mode != nil && !validModes[*input.Mode] {
		return ErrModeInvalid
	}
	// Endpoint-only validation for service mode (cross-field API validation is in Update).
	if input.Endpoint != nil && input.Mode == nil {
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

// validateAPIEndpoint resolves the template with variables and validates the resulting URL.
func validateAPIEndpoint(endpoint string, variables map[string]string) error {
	if strings.TrimSpace(endpoint) == "" {
		return ErrEndpointInvalid
	}
	resolved, err := ResolveTemplate(endpoint, variables)
	if err != nil {
		return ErrVariablesMissing
	}
	return validateEndpoint(resolved)
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

