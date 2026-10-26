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

func TestExplicitDependenciesAndSafeDefaults(t *testing.T) {
	cfg, err := fromMap(validEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "development" || cfg.APIAddr != "127.0.0.1:8082" || cfg.Redis.Database != 0 || cfg.Redis.Password != "" {
		t.Fatalf("unexpected non-secret defaults: environment=%s address=%s Redis DB=%d", cfg.Environment, cfg.APIAddr, cfg.Redis.Database)
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
	cfg, err := fromMap(values)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIAddr != "[::1]:8083" || cfg.Redis.Database != 2 || cfg.Redis.Password != "secret" || cfg.StartupTimeout != 3*time.Second || cfg.ProbeTimeout != 500*time.Millisecond || cfg.ShutdownTimeout != 5*time.Second {
		t.Fatal("explicit overrides were not applied")
	}
}
