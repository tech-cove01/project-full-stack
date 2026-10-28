package config

import (
	"strings"
	"testing"
	"time"
)

func TestOptionalSearchConfiguration(t *testing.T) {
	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
	}
	if cfg := loadSearch(lookup(nil)); cfg.Ready || cfg.Index != "community-notes-v1" {
		t.Fatal("empty optional search must stay disabled")
	}
	for _, endpoint := range []string{"http://127.0.0.1:9202", "http://localhost:9202/", "http://[::1]:9202", "https://search.example.test"} {
		cfg := loadSearch(lookup(map[string]string{"ES_URL": endpoint, "ES_INDEX": "notes-test", "ES_TIMEOUT_SECONDS": "2"}))
		if !cfg.Ready || cfg.Timeout != 2*time.Second || cfg.Index != "notes-test" {
			t.Fatal("explicit search configuration rejected")
		}
	}
	for _, values := range []map[string]string{
		{"ES_URL": "http://search.example.test"}, {"ES_URL": "http://x:private@localhost:9202"}, {"ES_URL": "http://127.0.0.1:9202/?secret=private"},
		{"ES_URL": "http://127.0.0.1:9202/index"}, {"ES_URL": "http://127.0.0.1:0"}, {"ES_URL": "http://127.0.0.1:9202#private"},
		{"ES_URL": "http://127.0.0.1:9202", "ES_INDEX": "*"}, {"ES_URL": "http://127.0.0.1:9202", "ES_INDEX": "../private"},
		{"ES_URL": "http://127.0.0.1:9202", "ES_TIMEOUT_SECONDS": "0"}, {"ES_URL": "http://127.0.0.1:9202", "ES_TIMEOUT_SECONDS": "11"},
	} {
		cfg := loadSearch(lookup(values))
		if cfg.Ready || strings.Contains(cfg.Reason, "private") {
			t.Fatal("invalid search must be unavailable with a fixed safe reason")
		}
	}
}
