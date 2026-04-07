package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig    `yaml:"server" json:"server"`
	Routing  RoutingConfig   `yaml:"routing" json:"routing"`
	Backends []BackendConfig `yaml:"backends" json:"backends"`
	Hooks    []HookConfig    `yaml:"hooks" json:"hooks"`
}

type ServerConfig struct {
	Listen       string        `yaml:"listen" json:"listen"`
	ReadTimeout  time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout  time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
}

type RoutingConfig struct {
	Routes []RouteConfig `yaml:"routes" json:"routes"`
}

type RouteConfig struct {
	Name            string   `yaml:"name" json:"name"`
	Priority        int      `yaml:"priority" json:"priority"`
	InboundProtocol string   `yaml:"inbound_protocol" json:"inbound_protocol"`
	PathPrefix      string   `yaml:"path_prefix" json:"path_prefix"`
	EndpointKinds   []string `yaml:"endpoint_kinds" json:"endpoint_kinds"`
	Backend         string   `yaml:"backend" json:"backend"`
}

type BackendConfig struct {
	Name           string            `yaml:"name" json:"name"`
	Protocol       string            `yaml:"protocol" json:"protocol"`
	BaseURL        string            `yaml:"base_url" json:"base_url"`
	Auth           AuthConfig        `yaml:"auth" json:"auth"`
	DefaultHeaders map[string]string `yaml:"default_headers" json:"default_headers"`
}

type AuthMode string

const (
	AuthModeInject       AuthMode = "inject"
	AuthModePassthrough  AuthMode = "passthrough"
	AuthModePreferClient AuthMode = "prefer_client"
)

type AuthConfig struct {
	Mode   AuthMode `yaml:"mode" json:"mode"`
	Header string   `yaml:"header" json:"header"`
	Value  string   `yaml:"value" json:"value"`
}

type HookConfig struct {
	Name    string         `yaml:"name" json:"name"`
	Type    string         `yaml:"type" json:"type"`
	Stage   string         `yaml:"stage" json:"stage"`
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Config  map[string]any `yaml:"config" json:"config"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if format == "yml" {
		format = "yaml"
	}
	return Parse(raw, format)
}

func Parse(data []byte, format string) (*Config, error) {
	expanded := expandEnvVars(string(data))

	var cfg Config
	switch format {
	case "yaml":
		if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
			return nil, fmt.Errorf("parse yaml config: %w", err)
		}
	case "json":
		if err := json.Unmarshal([]byte(expanded), &cfg); err != nil {
			return nil, fmt.Errorf("parse json config: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported config format: %s", format)
	}

	cfg.setDefaults()

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 60 * time.Second
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = 120 * time.Second
	}
	for i := range c.Backends {
		if c.Backends[i].Auth.Mode == "" {
			c.Backends[i].Auth.Mode = AuthModeInject
		}
	}
}

func (c *Config) validate() error {
	backendNames := make(map[string]bool, len(c.Backends))
	for _, b := range c.Backends {
		if b.Name == "" {
			return fmt.Errorf("backend name is required")
		}
		if b.Protocol == "" {
			return fmt.Errorf("backend %q: protocol is required", b.Name)
		}
		if b.BaseURL == "" {
			return fmt.Errorf("backend %q: base_url is required", b.Name)
		}
		if backendNames[b.Name] {
			return fmt.Errorf("duplicate backend name: %q", b.Name)
		}
		backendNames[b.Name] = true

		switch b.Auth.Mode {
		case AuthModeInject, AuthModePassthrough, AuthModePreferClient:
		default:
			return fmt.Errorf("backend %q: unknown auth mode %q", b.Name, b.Auth.Mode)
		}
	}

	routeNames := make(map[string]bool, len(c.Routing.Routes))
	for _, r := range c.Routing.Routes {
		if r.Name == "" {
			return fmt.Errorf("route name is required")
		}
		if routeNames[r.Name] {
			return fmt.Errorf("duplicate route name: %q", r.Name)
		}
		routeNames[r.Name] = true

		if r.Backend == "" {
			return fmt.Errorf("route %q: backend is required", r.Name)
		}
		if !backendNames[r.Backend] {
			return fmt.Errorf("route %q: references unknown backend %q", r.Name, r.Backend)
		}
		if r.PathPrefix == "" {
			return fmt.Errorf("route %q: path_prefix is required", r.Name)
		}
	}

	for _, h := range c.Hooks {
		if h.Name == "" {
			return fmt.Errorf("hook name is required")
		}
		if h.Type == "" {
			return fmt.Errorf("hook %q: type is required", h.Name)
		}
	}

	return nil
}

func expandEnvVars(s string) string {
	return os.Expand(s, func(key string) string {
		if val, ok := os.LookupEnv(key); ok {
			return val
		}
		return "${" + key + "}"
	})
}
