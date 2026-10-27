package communityrelay

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBackoffDoublesAndStopsAtTheCap(t *testing.T) {
	for attempts, want := range map[int]int{
		1: 2, 2: 4, 3: 8, 7: 128, 8: 256, 9: 256, 0: 2, -3: 2,
	} {
		if got := backoffSeconds(attempts); got != want {
			t.Fatalf("backoff(%d) = %d, want %d", attempts, got, want)
		}
	}
}

func TestTruncateKeepsTheRowWithinTheColumnLimit(t *testing.T) {
	if truncate(nil) != "delivery failed" {
		t.Fatal("a nil cause must still produce a readable message")
	}
	long := ""
	for i := 0; i < 600; i++ {
		long += "错"
	}
	if runes := []rune(truncate(errors.New(long))); len(runes) != 500 {
		t.Fatalf("message must be capped at 500 runes, got %d", len(runes))
	}
	if truncate(errors.New("broker unreachable")) != "broker unreachable" {
		t.Fatal("a short message must be kept as-is")
	}
}

func TestEnvelopeKeepsTheConsumerContract(t *testing.T) {
	body, err := json.Marshal(envelope{
		EventID: "FOLLOW:1:2:3", EventType: "FOLLOW_CHANGED", AggregateType: "FOLLOW",
		AggregateID: "2", Version: 3,
		Payload: json.RawMessage(`{"userId":"1","followUserId":"2","active":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"eventId", "eventType", "aggregateType", "aggregateId", "version", "payload"} {
		if _, present := decoded[key]; !present {
			t.Fatalf("envelope is missing %s: %s", key, body)
		}
	}
	payload, ok := decoded["payload"].(map[string]any)
	if !ok || payload["followUserId"] != "2" {
		t.Fatalf("payload must stay a nested object, got %s", body)
	}
}
