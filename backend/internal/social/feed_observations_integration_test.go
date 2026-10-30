package social

import (
	"runtime"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

// A normal real HTTP cold/hot read checks the actual middleware and app-owned
// loader observation wiring. There is no delay/fault injection in this case.
func TestIntegrationFeedObservationSeparatesHTTPAndSharedWork(t *testing.T) {
	f := newCardFeed(t)
	observations := taildiag.New()
	taildiag.Instrument(f.r.ORM, f.r.SQL, f.r.Redis)
	f.cards.WithDiagnostics(observations)
	f.service.WithDiagnostics(observations)
	f.start(t)
	for user := 0; user < 2; user++ {
		page, status := f.request(t, user, "limit=2")
		if status != 200 || len(page.Items) != 2 {
			t.Fatal("observed HTTP changed the authorized page")
		}
	}
	deadline := time.Now().Add(time.Second)
	for observations.Snapshot().SharedTasks < 1 {
		if time.Now().After(deadline) {
			t.Fatal("shared trace did not finish after HTTP result")
		}
		runtime.Gosched()
	}
	snapshot := observations.Snapshot()
	if snapshot.Requests != 2 || snapshot.SharedTasks != 1 {
		t.Fatalf("shared tasks were mixed with HTTP completions: requests=%d shared=%d", snapshot.Requests, snapshot.SharedTasks)
	}
	for _, stage := range []taildiag.Stage{taildiag.AuthResolve, taildiag.FeedFacts, taildiag.FeedCards, taildiag.FeedCounts} {
		if snapshot.Stages[stage].Count != 2 {
			t.Fatalf("actual HTTP stage %s was not observed twice", snapshot.Stages[stage].Name)
		}
	}
	if snapshot.Stages[taildiag.CardsColdSQL].Count != 1 || snapshot.Stages[taildiag.SQLBegin].Count != 2 || f.cards.Stats().ContentSQLBatches != 1 {
		t.Fatal("observations did not retain cold/hot batch and fact boundaries")
	}
	for _, stage := range snapshot.Stages {
		if stage.Active != 0 || stage.TrackedActive != 0 || stage.UntrackedActive != 0 || stage.OldestActiveNS != 0 {
			t.Fatalf("finished HTTP/shared work retained active stage %s", stage.Name)
		}
	}
	t.Log("real HTTP cold/hot: auth/facts/cards/counts each 2; separate shared tasks 1; content SQL 1; all stages converged")
}
