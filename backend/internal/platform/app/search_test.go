package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/notesearch"
	"github.com/cityloop/fullstack-go/internal/platform/config"
)

func TestOptionalSearchAssemblyDoesNotDialOrInventFallback(t *testing.T) {
	for _, settings := range []config.Search{{}, {Ready: true, URL: "http://private:secret@localhost", Index: "notes"}, {Ready: true, URL: "http://127.0.0.1:1", Index: "*", Timeout: time.Second}} {
		client := configuredSearch(settings)
		if client != nil {
			client.CloseIdleConnections()
			t.Fatal("invalid optional search must be disabled")
		}
		_, err := notesearch.NewService(nil, client).Search(context.Background(), notesearch.SearchRequest{Query: "咖啡"})
		if !errors.Is(err, notesearch.ErrUnavailable) {
			t.Fatal("disabled search must report unavailable")
		}
	}
	client := configuredSearch(config.Search{Ready: true, URL: "http://127.0.0.1:1", Index: "notes", Timeout: time.Second})
	if client == nil {
		t.Fatal("assembly must not probe unreachable ES during startup")
	}
	client.CloseIdleConnections()
}
