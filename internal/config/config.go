// Package config loads and validates runtime configuration.
package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type Mode string

const (
	ModeDevelopment Mode = "development"
	ModeProduction  Mode = "production"
)

type Config struct {
	Mode       Mode
	HTTP       HTTP
	Telemetry  Telemetry
	Database   Database
	DevAuth    bool
	ConfigFile string
}

type HTTP struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

type Telemetry struct {
	Mode     string
	Endpoint string
}

type Database struct {
	URL string
}

type fileConfig struct {
	Mode      *Mode          `json:"mode"`
	HTTP      *fileHTTP      `json:"http"`
	Telemetry *fileTelemetry `json:"telemetry"`
	Database  *fileDatabase  `json:"database"`
	DevAuth   *bool          `json:"dev_auth"`
}

type fileHTTP struct {
	Address           *string `json:"address"`
	ReadHeaderTimeout *string `json:"read_header_timeout"`
	ReadTimeout       *string `json:"read_timeout"`
	WriteTimeout      *string `json:"write_timeout"`
	IdleTimeout       *string `json:"idle_timeout"`
	ShutdownTimeout   *string `json:"shutdown_timeout"`
	MaxHeaderBytes    *int    `json:"max_header_bytes"`
	MaxBodyBytes      *int64  `json:"max_body_bytes"`
}

type fileTelemetry struct {
	Mode     *string `json:"mode"`
	Endpoint *string `json:"endpoint"`
}

type fileDatabase struct {
	URL *string `json:"url"`
}

func Default() Config {
	return Config{
		Mode: ModeDevelopment,
		HTTP: HTTP{
			Address:           "127.0.0.1:8080",
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   15 * time.Second,
			MaxHeaderBytes:    1 << 20,
			MaxBodyBytes:      1 << 20,
		},
		Telemetry: Telemetry{Mode: "noop"},
	}
}

// Load applies defaults, an optional JSON file, TPTG_ environment variables,
// and flags in that order. Callers pass os.Environ() explicitly for testability.
func Load(args, environ []string) (Config, error) {
	cfg := Default()
	fileName, err := configFileArg(args)
	if err != nil {
		return Config{}, err
	}
	if fileName != "" {
		if err := applyFile(&cfg, fileName); err != nil {
			return Config{}, err
		}
		cfg.ConfigFile = fileName
	}
	if err := applyEnvironment(&cfg, environ); err != nil {
		return Config{}, err
	}
	if err := applyFlags(&cfg, args); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func configFileArg(args []string) (string, error) {
	set := flag.NewFlagSet("config-file", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	name := set.String("config", "", "configuration file")
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--config" || arg == "-config" {
			if index+1 >= len(args) {
				return "", errors.New("flag --config requires a value")
			}
			return args[index+1], nil
		}
		if strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-config=") {
			return strings.SplitN(arg, "=", 2)[1], nil
		}
	}
	_ = name
	return "", nil
}

func applyFile(cfg *Config, name string) error {
	file, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var input fileConfig
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode config file: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return input.apply(cfg)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode config file: multiple JSON values")
		}
		return fmt.Errorf("decode config file: %w", err)
	}
	return nil
}

func (input fileConfig) apply(cfg *Config) error {
	if input.Mode != nil {
		cfg.Mode = *input.Mode
	}
	if input.DevAuth != nil {
		cfg.DevAuth = *input.DevAuth
	}
	if input.HTTP != nil {
		if input.HTTP.Address != nil {
			cfg.HTTP.Address = *input.HTTP.Address
		}
		if input.HTTP.MaxHeaderBytes != nil {
			cfg.HTTP.MaxHeaderBytes = *input.HTTP.MaxHeaderBytes
		}
		if input.HTTP.MaxBodyBytes != nil {
			cfg.HTTP.MaxBodyBytes = *input.HTTP.MaxBodyBytes
		}
		for name, value := range map[string]*string{
			"read_header_timeout": input.HTTP.ReadHeaderTimeout, "read_timeout": input.HTTP.ReadTimeout,
			"write_timeout": input.HTTP.WriteTimeout, "idle_timeout": input.HTTP.IdleTimeout,
			"shutdown_timeout": input.HTTP.ShutdownTimeout,
		} {
			if value == nil {
				continue
			}
			duration, err := time.ParseDuration(*value)
			if err != nil {
				return fmt.Errorf("http.%s: %w", name, err)
			}
			switch name {
			case "read_header_timeout":
				cfg.HTTP.ReadHeaderTimeout = duration
			case "read_timeout":
				cfg.HTTP.ReadTimeout = duration
			case "write_timeout":
				cfg.HTTP.WriteTimeout = duration
			case "idle_timeout":
				cfg.HTTP.IdleTimeout = duration
			case "shutdown_timeout":
				cfg.HTTP.ShutdownTimeout = duration
			}
		}
	}
	if input.Telemetry != nil {
		if input.Telemetry.Mode != nil {
			cfg.Telemetry.Mode = *input.Telemetry.Mode
		}
		if input.Telemetry.Endpoint != nil {
			cfg.Telemetry.Endpoint = *input.Telemetry.Endpoint
		}
	}
	if input.Database != nil && input.Database.URL != nil {
		cfg.Database.URL = *input.Database.URL
	}
	return nil
}

var envSetters = map[string]func(*Config, string) error{
	"TPTG_MODE":               func(c *Config, v string) error { c.Mode = Mode(v); return nil },
	"TPTG_HTTP_ADDRESS":       func(c *Config, v string) error { c.HTTP.Address = v; return nil },
	"TPTG_DATABASE_URL":       func(c *Config, v string) error { c.Database.URL = v; return nil },
	"TPTG_TELEMETRY_MODE":     func(c *Config, v string) error { c.Telemetry.Mode = v; return nil },
	"TPTG_TELEMETRY_ENDPOINT": func(c *Config, v string) error { c.Telemetry.Endpoint = v; return nil },
	"TPTG_DEV_AUTH":           func(c *Config, v string) error { parsed, err := strconv.ParseBool(v); c.DevAuth = parsed; return err },
}

func applyEnvironment(cfg *Config, environ []string) error {
	for _, entry := range environ {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, "TPTG_") {
			continue
		}
		setter, ok := envSetters[name]
		if !ok {
			return fmt.Errorf("unknown environment variable %s", name)
		}
		if err := setter(cfg, value); err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
	}
	return nil
}

func applyFlags(cfg *Config, args []string) error {
	set := flag.NewFlagSet("thinkpixeltg", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&cfg.ConfigFile, "config", cfg.ConfigFile, "configuration file")
	set.Var((*modeValue)(&cfg.Mode), "mode", "development or production")
	set.StringVar(&cfg.HTTP.Address, "http-address", cfg.HTTP.Address, "HTTP listen address")
	set.StringVar(&cfg.Telemetry.Mode, "telemetry-mode", cfg.Telemetry.Mode, "noop, local, or otlp")
	set.StringVar(&cfg.Telemetry.Endpoint, "telemetry-endpoint", cfg.Telemetry.Endpoint, "OTLP endpoint")
	set.StringVar(&cfg.Database.URL, "database-url", cfg.Database.URL, "PostgreSQL URL")
	set.BoolVar(&cfg.DevAuth, "dev-auth", cfg.DevAuth, "enable development authentication")
	if err := set.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	return nil
}

type modeValue Mode

func (value *modeValue) String() string         { return string(*value) }
func (value *modeValue) Set(input string) error { *value = modeValue(input); return nil }

func (cfg Config) Validate() error {
	if cfg.Mode != ModeDevelopment && cfg.Mode != ModeProduction {
		return fmt.Errorf("invalid mode %q", cfg.Mode)
	}
	if strings.TrimSpace(cfg.HTTP.Address) == "" {
		return errors.New("http address is required")
	}
	for name, duration := range map[string]time.Duration{
		"read header": cfg.HTTP.ReadHeaderTimeout, "read": cfg.HTTP.ReadTimeout,
		"write": cfg.HTTP.WriteTimeout, "idle": cfg.HTTP.IdleTimeout, "shutdown": cfg.HTTP.ShutdownTimeout,
	} {
		if duration <= 0 {
			return fmt.Errorf("http %s timeout must be positive", name)
		}
	}
	if cfg.HTTP.MaxHeaderBytes < 1024 || cfg.HTTP.MaxBodyBytes < 1 {
		return errors.New("http limits must be positive and bounded")
	}
	if cfg.Telemetry.Mode != "noop" && cfg.Telemetry.Mode != "local" && cfg.Telemetry.Mode != "otlp" {
		return fmt.Errorf("invalid telemetry mode %q", cfg.Telemetry.Mode)
	}
	if cfg.Telemetry.Mode == "otlp" && strings.TrimSpace(cfg.Telemetry.Endpoint) == "" {
		return errors.New("OTLP telemetry requires an endpoint")
	}
	if cfg.Mode == ModeProduction && cfg.DevAuth {
		return errors.New("development authentication is forbidden in production")
	}
	return nil
}

// String deliberately omits secret values and is safe for startup diagnostics.
func (cfg Config) String() string {
	database := "unset"
	if cfg.Database.URL != "" {
		database = "[REDACTED]"
	}
	return fmt.Sprintf("mode=%s http_address=%s telemetry_mode=%s telemetry_endpoint=%s database_url=%s dev_auth=%t",
		cfg.Mode, cfg.HTTP.Address, cfg.Telemetry.Mode, cfg.Telemetry.Endpoint, database, cfg.DevAuth)
}
