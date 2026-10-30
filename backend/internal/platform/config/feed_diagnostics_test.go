package config

import "testing"

func TestFeedDiagnosticsAreExplicitAndRequirePrivateDiagnostics(t *testing.T) {
	defaults, err := fromMap(validEnvironment())
	if err != nil || defaults.FeedDiagnostics {
		t.Fatal("Feed timings must default off")
	}
	for _, test := range []struct {
		value, address, environment string
		accepted                    bool
	}{
		{"false", "", "development", true},
		{"true", "127.0.0.1:8351", "development", true},
		{"true", "[::1]:8351", "test", true},
		{"true", "", "development", false},
		{"true", "0.0.0.0:8351", "development", false},
		{"true", "127.0.0.1:8351", "production", false},
		{"1", "127.0.0.1:8351", "development", false},
		{"TRUE", "127.0.0.1:8351", "development", false},
		{"", "127.0.0.1:8351", "development", false},
	} {
		values := validEnvironment()
		values["GO_FEED_DIAGNOSTICS"], values["GO_DIAGNOSTICS_ADDR"], values["GO_ENV"] = test.value, test.address, test.environment
		cfg, err := fromMap(values)
		if (err == nil) != test.accepted || err == nil && cfg.FeedDiagnostics != (test.value == "true") {
			t.Fatalf("unexpected diagnostic guard result for %v", test)
		}
	}
}
