package merchantorder

import (
	"encoding/json"
	"testing"
)

// The compensation worker restores Redis from these events. A payload without the
// activity or the buyer can never be applied and would park as NEEDS_REVIEW.
func TestOrderEventPayloadCarriesTheCompensationFields(t *testing.T) {
	for _, test := range []struct {
		targetStatus, stockRestore, releaseEligibility string
	}{
		{"3", "0", "false"}, {"6", "1", "false"}, {"4", "1", "true"},
	} {
		raw := orderEventPayload("653477702128369667", 1038, 7, 42,
			test.targetStatus, test.stockRestore, test.releaseEligibility, 3)
		var payload map[string]string
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		for field, want := range map[string]string{
			"orderId": "653477702128369667", "voucherId": "1038", "shopId": "7",
			"userId": "42", "targetStatus": test.targetStatus,
			"stockRestore": test.stockRestore, "releaseEligibility": test.releaseEligibility,
			"version": "3",
		} {
			if payload[field] != want {
				t.Fatalf("payload %s = %q, want %q (%s)", field, payload[field], want, raw)
			}
		}
	}
}
