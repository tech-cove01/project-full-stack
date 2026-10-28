package config

import (
	"strings"
	"testing"
	"time"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"MYSQL_HOST": "127.0.0.1", "MYSQL_PORT": "3308", "MYSQL_DATABASE": "isolated",
		"MYSQL_USER": "fullstack", "MYSQL_PASSWORD": "private-password",
		"REDIS_HOST": "127.0.0.1", "REDIS_PORT": "6380",
	}
}

func fromMap(values map[string]string) (Config, error) {
	return load(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
}

func TestKafkaIsOptionalButValidatedWhenPresent(t *testing.T) {
	cfg, err := fromMap(validEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Kafka.Brokers) != 0 || cfg.Kafka.Topic != "community.events" {
		t.Fatal("an unset broker list must disable the relay without failing startup")
	}
	values := validEnvironment()
	values["KAFKA_BROKERS"] = "127.0.0.1:9092, 127.0.0.1:9093"
	values["KAFKA_TOPIC"] = "community.events.v1"
	cfg, err = fromMap(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Kafka.Brokers) != 2 || cfg.Kafka.Brokers[1] != "127.0.0.1:9093" ||
		cfg.Kafka.Topic != "community.events.v1" {
		t.Fatalf("brokers must be trimmed and kept in order: %+v", cfg.Kafka)
	}
	for _, test := range []struct{ key, value string }{
		{"KAFKA_BROKERS", "127.0.0.1"},
		{"KAFKA_BROKERS", "127.0.0.1:0"},
		{"KAFKA_BROKERS", "127.0.0.1:9092,"},
		{"KAFKA_TOPIC", "bad topic"},
		{"KAFKA_TOPIC", ""},
	} {
		values := validEnvironment()
		values[test.key] = test.value
		if _, err = fromMap(values); err == nil || !strings.Contains(err.Error(), test.key) {
			t.Fatalf("%s=%q must be rejected with a diagnostic", test.key, test.value)
		}
	}
}

func TestKafkaTopicReservedNames(t *testing.T) {
	for _, topic := range []string{".", "..", " . ", " .. "} {
		t.Run("reject_"+topic, func(t *testing.T) {
			values := validEnvironment()
			values["KAFKA_BROKERS"] = "127.0.0.1:9092"
			values["KAFKA_TOPIC"] = topic
			if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "KAFKA_TOPIC") {
				t.Fatal("reserved dot-only Kafka topic must fail during configuration loading")
			}
		})
	}
	for _, topic := range []string{"community.events", ".events", "community..events", "..."} {
		t.Run("accept_"+topic, func(t *testing.T) {
			values := validEnvironment()
			values["KAFKA_BROKERS"] = "127.0.0.1:9092"
			values["KAFKA_TOPIC"] = topic
			cfg, err := fromMap(values)
			if err != nil || cfg.Kafka.Topic != topic {
				t.Fatal("legal dotted Kafka topic must retain its configured name")
			}
		})
	}
}

func TestExplicitDependenciesAndSafeDefaults(t *testing.T) {
	cfg, err := fromMap(validEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "development" || cfg.APIAddr != "127.0.0.1:8082" || cfg.Redis.Database != 0 || cfg.Redis.Password != "" {
		t.Fatalf("unexpected non-secret defaults: environment=%s address=%s Redis DB=%d", cfg.Environment, cfg.APIAddr, cfg.Redis.Database)
	}
	if cfg.OrderPayTimeout != 15*time.Minute {
		t.Fatal("new orders must default to the specified fifteen-minute payment window")
	}
	_, offset := time.Now().In(cfg.Location).Zone()
	if cfg.Location.String() != "Asia/Shanghai" || offset != 8*60*60 {
		t.Fatal("time zone must explicitly be Asia/Shanghai")
	}
}

func TestMissingAndInvalidConfigurationFailsWithoutValues(t *testing.T) {
	for _, key := range []string{"MYSQL_HOST", "MYSQL_PORT", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD", "REDIS_HOST", "REDIS_PORT"} {
		t.Run("missing_"+key, func(t *testing.T) {
			values := validEnvironment()
			delete(values, key)
			_, err := fromMap(values)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected a diagnostic naming %s", key)
			}
		})
	}
	for _, test := range []struct{ key, value string }{
		{"MYSQL_PORT", "3307password"}, {"REDIS_PORT", "0"}, {"REDIS_DATABASE", "-1"},
		{"REDIS_DATABASE", "16"}, {"API_ADDR", "invalid-address"}, {"API_ADDR", "127.0.0.1:0"},
		{"GO_ENV", "invalid-environment"}, {"GO_HEALTH_TIMEOUT", "0s"}, {"GO_STARTUP_TIMEOUT", "2m"},
		{"GO_ORDER_PAY_TIMEOUT", "0s"}, {"GO_ORDER_PAY_TIMEOUT", "500ms"},
		{"GO_ORDER_PAY_TIMEOUT", "1.5s"}, {"GO_ORDER_PAY_TIMEOUT", "25h"},
	} {
		t.Run(test.key+"_"+test.value, func(t *testing.T) {
			values := validEnvironment()
			values[test.key] = test.value
			_, err := fromMap(values)
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("expected a diagnostic naming %s", test.key)
			}
			if strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), "3307password") {
				t.Fatal("configuration error exposed a configured value")
			}
		})
	}
}

func TestTimeoutAndRedisOverrides(t *testing.T) {
	values := validEnvironment()
	values["GO_ENV"] = "production"
	values["API_ADDR"] = "[::1]:8083"
	values["REDIS_PASSWORD"] = "secret"
	values["REDIS_DATABASE"] = "2"
	values["GO_STARTUP_TIMEOUT"] = "3s"
	values["GO_HEALTH_TIMEOUT"] = "500ms"
	values["GO_SHUTDOWN_TIMEOUT"] = "5s"
	values["GO_ORDER_PAY_TIMEOUT"] = "30s"
	cfg, err := fromMap(values)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIAddr != "[::1]:8083" || cfg.Redis.Database != 2 || cfg.Redis.Password != "secret" || cfg.StartupTimeout != 3*time.Second || cfg.ProbeTimeout != 500*time.Millisecond || cfg.ShutdownTimeout != 5*time.Second {
		t.Fatal("explicit overrides were not applied")
	}
	if cfg.OrderPayTimeout != 30*time.Second {
		t.Fatal("order deadline configuration was not applied")
	}
}
