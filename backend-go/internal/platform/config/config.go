// Package config loads explicit process configuration without reading .env files.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type MySQL struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

type Redis struct {
	Host     string
	Port     int
	Database int
	Password string
}

type Config struct {
	Environment     string
	APIAddr         string
	MySQL           MySQL
	Redis           Redis
	Location        *time.Location
	StartupTimeout  time.Duration
	ProbeTimeout    time.Duration
	ShutdownTimeout time.Duration
}

func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Environment: "development", APIAddr: "127.0.0.1:8082",
		StartupTimeout: 8 * time.Second, ProbeTimeout: 2 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
	var err error
	cfg.Location, err = time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return Config{}, fmt.Errorf("load Asia/Shanghai time zone: %w", err)
	}
	if value, ok := lookup("GO_ENV"); ok {
		cfg.Environment = strings.TrimSpace(value)
	}
	switch cfg.Environment {
	case "development", "test", "production":
	default:
		return Config{}, fmt.Errorf("GO_ENV must be development, test, or production")
	}
	if value, ok := lookup("API_ADDR"); ok {
		cfg.APIAddr = strings.TrimSpace(value)
	}
	host, port, err := net.SplitHostPort(cfg.APIAddr)
	if err != nil || strings.ContainsAny(host, "/\\ \t\r\n") {
		return Config{}, fmt.Errorf("API_ADDR must be a host:port address")
	}
	if _, err := parseInt("API_ADDR port", port, 1, 65535); err != nil {
		return Config{}, err
	}
	for _, item := range []struct {
		name   string
		target *string
	}{
		{"MYSQL_HOST", &cfg.MySQL.Host}, {"MYSQL_DATABASE", &cfg.MySQL.Database},
		{"MYSQL_USER", &cfg.MySQL.User}, {"MYSQL_PASSWORD", &cfg.MySQL.Password},
		{"REDIS_HOST", &cfg.Redis.Host},
	} {
		value, ok := lookup(item.name)
		if !ok || strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s must be explicitly configured", item.name)
		}
		*item.target = value
	}
	for _, item := range []struct {
		name   string
		target *int
	}{
		{"MYSQL_PORT", &cfg.MySQL.Port}, {"REDIS_PORT", &cfg.Redis.Port},
	} {
		value, ok := lookup(item.name)
		if !ok || value == "" {
			return Config{}, fmt.Errorf("%s must be explicitly configured", item.name)
		}
		*item.target, err = parseInt(item.name, value, 1, 65535)
		if err != nil {
			return Config{}, err
		}
	}
	// Redis authentication may be disabled on the isolated local instance.
	cfg.Redis.Password, _ = lookup("REDIS_PASSWORD")
	if value, ok := lookup("REDIS_DATABASE"); ok {
		cfg.Redis.Database, err = parseInt("REDIS_DATABASE", value, 0, 15)
		if err != nil {
			return Config{}, err
		}
	}
	for _, item := range []struct {
		name   string
		target *time.Duration
	}{
		{"GO_STARTUP_TIMEOUT", &cfg.StartupTimeout},
		{"GO_HEALTH_TIMEOUT", &cfg.ProbeTimeout},
		{"GO_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
	} {
		if value, ok := lookup(item.name); ok {
			parsed, parseErr := time.ParseDuration(value)
			if parseErr != nil || parsed <= 0 || parsed > time.Minute {
				return Config{}, fmt.Errorf("%s must be a positive duration no greater than 1m", item.name)
			}
			*item.target = parsed
		}
	}
	return cfg, nil
}

func parseInt(name, value string, min, max int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, min, max)
	}
	return parsed, nil
}
