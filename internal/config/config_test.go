package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg := defaults()

	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("expected default read timeout 30s, got %v", cfg.Server.ReadTimeout)
	}
	if cfg.Metering.BatchSize != 100 {
		t.Errorf("expected default batch size 100, got %d", cfg.Metering.BatchSize)
	}
	if cfg.RateLimit.Default != 60 {
		t.Errorf("expected default rate limit 60, got %d", cfg.RateLimit.Default)
	}
}

func TestLoadFromFile(t *testing.T) {
	content := `
server:
  port: 9090
  host: "127.0.0.1"
  read_timeout: 10s
  write_timeout: 15s
database:
  url: "postgres://test:test@localhost:5432/test"
proxy:
  timeout: 5s
  max_request_size: 1048576
metering:
  batch_size: 50
  flush_interval: 2s
rate_limit:
  default: 30
  window: 2m
cors:
  allowed_origins: ["https://example.com"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Proxy.Timeout != 5*time.Second {
		t.Errorf("expected proxy timeout 5s, got %v", cfg.Proxy.Timeout)
	}
	if cfg.Metering.BatchSize != 50 {
		t.Errorf("expected batch size 50, got %d", cfg.Metering.BatchSize)
	}
	if len(cfg.CORS.AllowedOrigins) != 1 || cfg.CORS.AllowedOrigins[0] != "https://example.com" {
		t.Errorf("expected cors origins [https://example.com], got %v", cfg.CORS.AllowedOrigins)
	}
}

func TestLoadNoFile(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with empty path should use defaults: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("OCTROI_DATABASE_URL", "postgres://env:env@envhost:5432/envdb")
	t.Setenv("OCTROI_PORT", "3000")
	t.Setenv("OCTROI_HOST", "10.0.0.1")
	t.Setenv("OCTROI_ENCRYPTION_KEY", "abc123")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Database.URL != "postgres://env:env@envhost:5432/envdb" {
		t.Errorf("expected env database URL, got %s", cfg.Database.URL)
	}
	if cfg.Server.Port != 3000 {
		t.Errorf("expected port 3000, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "10.0.0.1" {
		t.Errorf("expected host 10.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Encryption.Key != "abc123" {
		t.Errorf("expected encryption key abc123, got %s", cfg.Encryption.Key)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{"valid defaults", func(c *Config) {}, false},
		{"port too low", func(c *Config) { c.Server.Port = 0 }, true},
		{"port too high", func(c *Config) { c.Server.Port = 70000 }, true},
		{"zero read timeout", func(c *Config) { c.Server.ReadTimeout = 0 }, true},
		{"empty db url", func(c *Config) { c.Database.URL = "" }, true},
		{"zero proxy timeout", func(c *Config) { c.Proxy.Timeout = 0 }, true},
		{"zero max request size", func(c *Config) { c.Proxy.MaxRequestSize = 0 }, true},
		{"zero batch size", func(c *Config) { c.Metering.BatchSize = 0 }, true},
		{"zero flush interval", func(c *Config) { c.Metering.FlushInterval = 0 }, true},
		{"negative rate limit", func(c *Config) { c.RateLimit.Default = -1 }, true},
		{"zero rate window", func(c *Config) { c.RateLimit.Window = 0 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaults()
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAddr(t *testing.T) {
	cfg := defaults()
	if cfg.Addr() != "0.0.0.0:8080" {
		t.Errorf("expected 0.0.0.0:8080, got %s", cfg.Addr())
	}
}

func TestDatabaseURLForMigrate(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"with sslmode", "postgres://host/db?sslmode=disable", "postgres://host/db?sslmode=disable"},
		{"without sslmode no query", "postgres://host/db", "postgres://host/db?sslmode=disable"},
		{"without sslmode with query", "postgres://host/db?foo=bar", "postgres://host/db?foo=bar&sslmode=disable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Database: DatabaseConfig{URL: tt.url}}
			got := cfg.DatabaseURLForMigrate()
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_OCTROI_VAR", "hello")
	result := expandEnvVars("value: ${TEST_OCTROI_VAR}")
	if result != "value: hello" {
		t.Errorf("expected 'value: hello', got %s", result)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
