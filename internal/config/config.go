package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Proxy     ProxyConfig     `yaml:"proxy"`
	Metering  MeteringConfig  `yaml:"metering"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	CORS      CORSConfig      `yaml:"cors"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"` // default: [] (same-origin only when empty; ["*"] for dev)
}

type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type DatabaseConfig struct {
	URL string `yaml:"url"`
}

type ProxyConfig struct {
	Timeout        time.Duration `yaml:"timeout"`
	MaxRequestSize int64         `yaml:"max_request_size"`
}

type MeteringConfig struct {
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
}

type RateLimitConfig struct {
	Default int           `yaml:"default"`
	Window  time.Duration `yaml:"window"`
}

func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}

		expanded := expandEnvVars(string(data))

		if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	applyEnvOverrides(cfg)

	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL: "postgres://octroi:octroi@localhost:5433/octroi?sslmode=disable",
		},
		Proxy: ProxyConfig{
			Timeout:        30 * time.Second,
			MaxRequestSize: 10 * 1024 * 1024,
		},
		Metering: MeteringConfig{
			BatchSize:     100,
			FlushInterval: 5 * time.Second,
		},
		RateLimit: RateLimitConfig{
			Default: 60,
			Window:  time.Minute,
		},
	}
}

func expandEnvVars(s string) string {
	return os.ExpandEnv(s)
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("OCTROI_DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("OCTROI_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("OCTROI_HOST"); v != "" {
		cfg.Server.Host = v
	}
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) MigrationsSource() string {
	return "file://migrations"
}

func (c *Config) DatabaseURLForMigrate() string {
	url := c.Database.URL
	if !strings.Contains(url, "sslmode=") {
		if strings.Contains(url, "?") {
			url += "&sslmode=disable"
		} else {
			url += "?sslmode=disable"
		}
	}
	return url
}
