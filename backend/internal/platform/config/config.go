// Package config loads explicit process configuration without reading .env files.
package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// topicPattern matches Kafka's documented topic character set and length.
var topicPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

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

// Kafka is optional. An empty broker list disables the community relay instead
// of failing startup: local development must work without a broker.
type Kafka struct {
	Brokers []string
	Topic   string
}

type Config struct {
	Environment        string
	APIAddr            string
	DiagnosticsAddr    string
	FeedDiagnostics    bool
	MySQL              MySQL
	Redis              Redis
	Kafka              Kafka
	AI                 AI
	Search             Search
	Location           *time.Location
	StartupTimeout     time.Duration
	ProbeTimeout       time.Duration
	ShutdownTimeout    time.Duration
	OrderPayTimeout    time.Duration
	OrderStreamWorkers int
}

func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Environment: "development", APIAddr: "127.0.0.1:8082",
		StartupTimeout: 8 * time.Second, ProbeTimeout: 2 * time.Second,
		ShutdownTimeout:    10 * time.Second,
		OrderPayTimeout:    15 * time.Minute,
		OrderStreamWorkers: 1,
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
	if value, ok := lookup("GO_DIAGNOSTICS_ADDR"); ok && strings.TrimSpace(value) != "" {
		cfg.DiagnosticsAddr = strings.TrimSpace(value)
		host, port, splitErr := net.SplitHostPort(cfg.DiagnosticsAddr)
		ip := net.ParseIP(host)
		if cfg.Environment == "production" || splitErr != nil || ip == nil || !ip.IsLoopback() {
			return Config{}, fmt.Errorf("GO_DIAGNOSTICS_ADDR requires a numeric loopback address in development or test")
		}
		if _, err := parseInt("GO_DIAGNOSTICS_ADDR port", port, 1, 65535); err != nil {
			return Config{}, err
		}
	}
	if value, ok := lookup("GO_FEED_DIAGNOSTICS"); ok {
		if value != "true" && value != "false" {
			return Config{}, fmt.Errorf("GO_FEED_DIAGNOSTICS must be true or false")
		}
		cfg.FeedDiagnostics = value == "true"
		if cfg.FeedDiagnostics && (cfg.DiagnosticsAddr == "" || cfg.Environment == "production") {
			return Config{}, fmt.Errorf("GO_FEED_DIAGNOSTICS requires isolated loopback diagnostics outside production")
		}
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
	if value, ok := lookup("GO_ORDER_PAY_TIMEOUT"); ok {
		parsed, parseErr := time.ParseDuration(value)
		if parseErr != nil || parsed < time.Second || parsed > 24*time.Hour || parsed%time.Second != 0 {
			return Config{}, fmt.Errorf("GO_ORDER_PAY_TIMEOUT must be whole seconds between 1s and 24h")
		}
		cfg.OrderPayTimeout = parsed
	}
	if value, ok := lookup("ORDER_STREAM_WORKERS"); ok {
		cfg.OrderStreamWorkers, err = parseInt("ORDER_STREAM_WORKERS", value, 1, 8)
		if err != nil {
			return Config{}, err
		}
	}
	cfg.Kafka.Topic = "community.events"
	if value, ok := lookup("KAFKA_BROKERS"); ok && strings.TrimSpace(value) != "" {
		for _, broker := range strings.Split(value, ",") {
			broker = strings.TrimSpace(broker)
			host, port, splitErr := net.SplitHostPort(broker)
			if splitErr != nil || host == "" || strings.ContainsAny(host, "/\\ \t\r\n") {
				return Config{}, fmt.Errorf("KAFKA_BROKERS must be a comma-separated host:port list")
			}
			if _, err := parseInt("KAFKA_BROKERS port", port, 1, 65535); err != nil {
				return Config{}, err
			}
			cfg.Kafka.Brokers = append(cfg.Kafka.Brokers, broker)
		}
	}
	if value, ok := lookup("KAFKA_TOPIC"); ok {
		topic := strings.TrimSpace(value)
		if !topicPattern.MatchString(topic) || topic == "." || topic == ".." {
			return Config{}, fmt.Errorf("KAFKA_TOPIC must be 1-249 characters of letters, digits, dot, underscore or dash, and cannot be . or ..")
		}
		cfg.Kafka.Topic = topic
	}
	cfg.AI = loadAI(lookup)
	cfg.Search = loadSearch(lookup)
	return cfg, nil
}

func parseInt(name, value string, min, max int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, min, max)
	}
	return parsed, nil
}
