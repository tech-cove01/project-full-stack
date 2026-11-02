package config

import (
	"strconv"
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
	if cfg.OrderStreamWorkers != 1 {
		t.Fatal("order Stream consumption must remain serial by default")
	}
	if cfg.CommunityRelayWorkers != 1 {
		t.Fatal("community relay must remain serial by default")
	}
	if cfg.CommunityRelayBatch != 1 {
		t.Fatal("community relay must retain the original single-event transport by default")
	}
	if cfg.CommunityConsumerCommitBatch != 1 {
		t.Fatal("community offset commits must remain per-message by default")
	}
	if cfg.MySQL.MaxOpenConns != 20 || cfg.MySQL.MaxIdleConns != 20 {
		t.Fatal("the default MySQL pool must retain all twenty permitted connections")
	}
	if cfg.MySQL.InterpolateParams {
		t.Fatal("MySQL parameter interpolation must stay opt-in")
	}
	_, offset := time.Now().In(cfg.Location).Zone()
	if cfg.Location.String() != "Asia/Shanghai" || offset != 8*60*60 {
		t.Fatal("time zone must explicitly be Asia/Shanghai")
	}
}

func TestMySQLInterpolationRequiresStrictExplicitBoolean(t *testing.T) {
	for _, value := range []string{"false", "true"} {
		values := validEnvironment()
		values["GO_MYSQL_INTERPOLATE_PARAMS"] = value
		cfg, err := fromMap(values)
		if err != nil || cfg.MySQL.InterpolateParams != (value == "true") {
			t.Fatal("explicit MySQL interpolation setting was not applied")
		}
	}
	for _, value := range []string{"", "0", "1", "TRUE", "False", " true ", "private-value"} {
		values := validEnvironment()
		values["GO_MYSQL_INTERPOLATE_PARAMS"] = value
		if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_MYSQL_INTERPOLATE_PARAMS") || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid interpolation setting must fail without exposing its value")
		}
	}
}

func TestMySQLPoolLimitsAndIdleInheritance(t *testing.T) {
	for _, test := range []struct {
		name, open, idle   string
		wantOpen, wantIdle int
	}{
		{"default", "", "", 20, 20},
		{"minimum_inherits", "1", "", 1, 1},
		{"override_inherits", "64", "", 64, 64},
		{"maximum_inherits", "128", "", 128, 128},
		{"smaller_idle", "64", "16", 64, 16},
		{"idle_disabled", "1", "0", 1, 0},
		{"idle_only", "", "7", 20, 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := validEnvironment()
			if test.open != "" {
				values["GO_MYSQL_MAX_OPEN_CONNS"] = test.open
			}
			if test.idle != "" {
				values["GO_MYSQL_MAX_IDLE_CONNS"] = test.idle
			}
			cfg, err := fromMap(values)
			if err != nil || cfg.MySQL.MaxOpenConns != test.wantOpen || cfg.MySQL.MaxIdleConns != test.wantIdle {
				t.Fatal("bounded MySQL limits and the configured-open idle default must be applied")
			}
		})
	}
}

func TestMySQLPoolInvalidValuesDoNotLeakTheirContents(t *testing.T) {
	for _, key := range []string{"GO_MYSQL_MAX_OPEN_CONNS", "GO_MYSQL_MAX_IDLE_CONNS"} {
		invalid := []string{"", "-1", "129", "1.5", " 20 ", "private-value", "999999999999999999999"}
		if key == "GO_MYSQL_MAX_OPEN_CONNS" {
			invalid = append(invalid, "0")
		} else {
			invalid = append(invalid, "21")
		}
		for _, value := range invalid {
			values := validEnvironment()
			values[key] = value
			_, err := fromMap(values)
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "private-value") {
				t.Fatal("invalid MySQL pool values must fail with the variable name and no supplied value")
			}
		}
	}
	values := validEnvironment()
	values["GO_MYSQL_MAX_OPEN_CONNS"], values["GO_MYSQL_MAX_IDLE_CONNS"] = "4", "5"
	if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_MYSQL_MAX_IDLE_CONNS") {
		t.Fatal("idle capacity must never exceed the configured open limit")
	}
}

func TestOrderStreamWorkerCountIsBounded(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
	}{{"1", 1}, {"4", 4}, {"8", 8}} {
		values := validEnvironment()
		values["ORDER_STREAM_WORKERS"] = test.value
		cfg, err := fromMap(values)
		if err != nil || cfg.OrderStreamWorkers != test.want {
			t.Fatalf("explicit worker count %s was not applied", test.value)
		}
	}
	for _, value := range []string{"", "0", "-1", "9", "16", "1.5", "private-value", " 4 "} {
		values := validEnvironment()
		values["ORDER_STREAM_WORKERS"] = value
		if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "ORDER_STREAM_WORKERS") || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid worker count must fail with a bounded diagnostic, without the value")
		}
	}
}

func TestCommunityRelayWorkerCountIsBounded(t *testing.T) {
	for _, value := range []string{"1", "4", "8"} {
		values := validEnvironment()
		values["GO_COMMUNITY_RELAY_WORKERS"] = value
		cfg, err := fromMap(values)
		if err != nil || strconv.Itoa(cfg.CommunityRelayWorkers) != value {
			t.Fatal("explicit bounded community relay count was not applied")
		}
	}
	for _, value := range []string{"", "0", "-1", "9", "1.5", " 4 ", "private-value"} {
		values := validEnvironment()
		values["GO_COMMUNITY_RELAY_WORKERS"] = value
		if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_COMMUNITY_RELAY_WORKERS") || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid relay count must fail without exposing its value")
		}
	}
}

func TestCommunityRelayBatchIsBounded(t *testing.T) {
	for _, value := range []string{"1", "2", "32", "64"} {
		values := validEnvironment()
		values["GO_COMMUNITY_RELAY_BATCH"] = value
		cfg, err := fromMap(values)
		if err != nil || strconv.Itoa(cfg.CommunityRelayBatch) != value {
			t.Fatal("explicit bounded relay batch was not applied")
		}
	}
	for _, value := range []string{"", "0", "-1", "65", "1.5", " 32 ", "private-value"} {
		values := validEnvironment()
		values["GO_COMMUNITY_RELAY_BATCH"] = value
		if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_COMMUNITY_RELAY_BATCH") || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid relay batch must fail without exposing its value")
		}
	}
}

func TestCommunityConsumerCommitBatchIsBounded(t *testing.T) {
	for _, value := range []string{"1", "32", "64"} {
		values := validEnvironment()
		values["GO_COMMUNITY_CONSUMER_COMMIT_BATCH"] = value
		cfg, err := fromMap(values)
		if err != nil || strconv.Itoa(cfg.CommunityConsumerCommitBatch) != value {
			t.Fatal("bounded synchronous offset batch was not applied")
		}
	}
	for _, value := range []string{"", "0", "-1", "65", "1.5", " 32 ", "private-value"} {
		values := validEnvironment()
		values["GO_COMMUNITY_CONSUMER_COMMIT_BATCH"] = value
		if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_COMMUNITY_CONSUMER_COMMIT_BATCH") || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid commit batch must fail without exposing its value")
		}
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

func TestCapacityDiagnosticsAreOptInAndOnlyUseNumericLoopbackOutsideProduction(t *testing.T) {
	cfg, err := fromMap(validEnvironment())
	if err != nil || cfg.DiagnosticsAddr != "" {
		t.Fatal("default configuration must not enable diagnostics")
	}
	for _, address := range []string{"127.0.0.1:8350", "[::1]:8351"} {
		values := validEnvironment()
		values["GO_DIAGNOSTICS_ADDR"] = address
		cfg, err := fromMap(values)
		if err != nil || cfg.DiagnosticsAddr != address {
			t.Fatal("explicit local diagnostics address was rejected")
		}
	}
	for _, address := range []string{"0.0.0.0:8350", ":8350", "localhost:8350", "192.0.2.1:8350", "[::]:8350", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:secret"} {
		values := validEnvironment()
		values["GO_DIAGNOSTICS_ADDR"] = address
		if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_DIAGNOSTICS_ADDR") || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe diagnostics address must fail without configured values")
		}
	}
	values := validEnvironment()
	values["GO_ENV"] = "production"
	values["GO_DIAGNOSTICS_ADDR"] = "127.0.0.1:8350"
	if _, err := fromMap(values); err == nil || !strings.Contains(err.Error(), "GO_DIAGNOSTICS_ADDR") {
		t.Fatal("production must reject capacity diagnostics even on loopback")
	}
}
