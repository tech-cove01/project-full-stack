package outbox

import (
	"encoding/json"
	"testing"
)

func TestDuplicateEventComparesExactIdentityAndJSONSemantics(t *testing.T) {
	left := Event{EventID: "event", AggregateType: "ACTIVITY", AggregateID: "9007199254740993", AggregateVersion: 1, EventType: "ACTIVITY_OPEN", Payload: json.RawMessage(`{"id":"9007199254740993","version":1}`)}
	right := left
	right.Payload = json.RawMessage(`{ "version": 1, "id": "9007199254740993" }`)
	if !sameEvent(left, right) {
		t.Fatal("JSON object field ordering is not event identity")
	}
	right.Payload = json.RawMessage(`{"id":"9007199254740992","version":1}`)
	if sameEvent(left, right) {
		t.Fatal("different large IDs must not compare equal")
	}
	right = left
	right.EventID = "different"
	if sameEvent(left, right) {
		t.Fatal("a different event ID cannot masquerade as an idempotent enqueue")
	}
}

func TestRetryDelayRemainsBoundedWithoutAnAttemptCutoff(t *testing.T) {
	for _, attempt := range []int{1, 2, 6, 8, 20, 2147483647} {
		delay := retryDelay(attempt)
		if delay < 1 || delay > 60 {
			t.Fatal("retry delays must remain bounded after repeated outages")
		}
	}
}
