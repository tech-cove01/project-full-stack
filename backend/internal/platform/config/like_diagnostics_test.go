package config

import "testing"

func TestLikeDiagnosticsAreIndependentAndRequirePrivateDiagnostics(t *testing.T) {
	defaults, err := fromMap(validEnvironment())
	if err != nil || defaults.LikeDiagnostics {
		t.Fatal("Like timings must default off")
	}
	for _, test := range []struct {
		value, address, environment string
		accepted                    bool
	}{
		{"false", "", "development", true},
		{"true", "127.0.0.1:8381", "development", true},
		{"true", "[::1]:8381", "test", true},
		{"true", "", "development", false},
		{"true", "0.0.0.0:8381", "development", false},
		{"true", "localhost:8381", "development", false},
		{"true", "127.0.0.1:8381", "production", false},
		{"1", "127.0.0.1:8381", "development", false},
		{"TRUE", "127.0.0.1:8381", "development", false},
		{"", "127.0.0.1:8381", "development", false},
	} {
		values := validEnvironment()
		values["GO_LIKE_DIAGNOSTICS"], values["GO_DIAGNOSTICS_ADDR"], values["GO_ENV"] = test.value, test.address, test.environment
		cfg, err := fromMap(values)
		if (err == nil) != test.accepted || err == nil && (cfg.LikeDiagnostics != (test.value == "true") || cfg.FeedDiagnostics) {
			t.Fatalf("unexpected independent Like diagnostic guard result for %v", test)
		}
	}
}
