package app

import (
	"github.com/cityloop/fullstack-go/internal/notesearch"
	"github.com/cityloop/fullstack-go/internal/platform/config"
)

// Construction is local only: an ES outage cannot block core API startup or
// readiness. The optional search route reports unavailability per request.
func configuredSearch(settings config.Search) *notesearch.Client {
	if !settings.Ready {
		return nil
	}
	client, err := notesearch.NewClient(notesearch.Config{URL: settings.URL, Index: settings.Index, Timeout: settings.Timeout})
	if err != nil {
		return nil
	}
	return client
}
