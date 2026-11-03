package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	JWT      JWTConfig      `yaml:"jwt"`
	Nvidia   NvidiaConfig   `yaml:"nvidia"`
	Google   GoogleConfig   `yaml:"google"`
	Security SecurityConfig `yaml:"security"`
	Providers []ProviderConfig `yaml:"providers"`
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port string `yaml:"port"`
	Mode string `yaml:"mode"`
}

type DatabaseConfig struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
	URL  string `yaml:"url"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type JWTConfig struct {
	Secret      string `yaml:"secret"`
	ExpiryHours int    `yaml:"expiry_hours"`
}

type NvidiaConfig struct {
	APIBase       string `yaml:"api_base"`
	ModelsEndpoint string `yaml:"models_endpoint"`
}

type GoogleConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
}

type SecurityConfig struct {
	AllowRegistration bool    `yaml:"allow_registration"`
	RateLimitRPS      float64 `yaml:"rate_limit_rps"`
	RateLimitBurst    int     `yaml:"rate_limit_burst"`
}

type ProviderConfig struct {
	Name          string `yaml:"name"`
	APIBase       string `yaml:"api_base"`
	ModelsEndpoint string `yaml:"models_endpoint"`
	AuthType      string `yaml:"auth_type"`
}

type OpenRouterConfig struct {
	ConfigURL string         `yaml:"config_url"`
	DocsURL   string         `yaml:"docs_url"`
	Fields    []FieldConfig  `yaml:"fields"`
}

type FieldConfig struct {
	Name        string `yaml:"name"`
	Value       string `yaml:"value"`
	Description string `yaml:"description"`
}

type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if port := os.Getenv("PORT"); port != "" {
		cfg.Server.Port = port
	}
	if host := os.Getenv("HOST"); host != "" {
		cfg.Server.Host = host
	}
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		cfg.JWT.Secret = secret
	}
	if id := os.Getenv("GOOGLE_CLIENT_ID"); id != "" {
		cfg.Google.ClientID = id
	}
	if secret := os.Getenv("GOOGLE_CLIENT_SECRET"); secret != "" {
		cfg.Google.ClientSecret = secret
	}
	if ru := os.Getenv("GOOGLE_REDIRECT_URL"); ru != "" {
		cfg.Google.RedirectURL = ru
	}
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		cfg.Database.URL = dbURL
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_REGISTRATION"))); v != "" {
		cfg.Security.AllowRegistration = v == "1" || v == "true" || v == "yes"
	}

	if cfg.Security.RateLimitRPS <= 0 {
		cfg.Security.RateLimitRPS = 2
	}
	if cfg.Security.RateLimitBurst <= 0 {
		cfg.Security.RateLimitBurst = 30
	}

	return cfg, nil
}