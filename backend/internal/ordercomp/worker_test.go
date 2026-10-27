package ordercomp

import (
	"encoding/json"
	"testing"
)

func TestParseAcceptsOnlyCanonicalOrderEvents(t *testing.T) {
	valid := json.RawMessage(`{"voucherId":"1038","userId":"455","orderId":"653018492815015973",
		"targetStatus":"4","stockRestore":"1","releaseEligibility":"true","version":"3"}`)
	parsed, err := parse(valid)
	if err != nil || parsed.VoucherID != "1038" || parsed.ReleaseEligibility != "true" {
		t.Fatalf("valid event rejected: %+v", parsed)
	}
	payment := json.RawMessage(`{"voucherId":"1038","userId":"455","orderId":"653018492815015973",
		"stockRestore":"0","releaseEligibility":"false"}`)
	if parsed, err = parse(payment); err != nil || parsed.StockRestore != "0" {
		t.Fatal("a payment event with stockRestore=0 must stay valid")
	}
	for _, raw := range []string{
		`{"voucherId":"0","userId":"455","orderId":"1","stockRestore":"1","releaseEligibility":"true"}`,
		`{"voucherId":"01038","userId":"455","orderId":"1","stockRestore":"1","releaseEligibility":"true"}`,
		`{"voucherId":"1038","userId":"455","orderId":"-1","stockRestore":"1","releaseEligibility":"true"}`,
		`{"voucherId":"1038","userId":"455","orderId":"1","stockRestore":"2","releaseEligibility":"true"}`,
		`{"voucherId":"1038","userId":"455","orderId":"1","stockRestore":"1","releaseEligibility":"yes"}`,
		`{"voucherId":"1038","userId":"455","orderId":"1","stockRestore":1,"releaseEligibility":"true"}`,
		`not-json`,
	} {
		if _, err := parse(json.RawMessage(raw)); err == nil {
			t.Fatalf("payload %s must be rejected", raw)
		}
	}
}

func TestClassifyNeverTreatsAnUnknownResultAsSuccess(t *testing.T) {
	for _, result := range []int{applied, duplicate} {
		if classify(result) != finishApplied {
			t.Fatalf("result %d must finish the event", result)
		}
	}
	for _, result := range []int{badState, badBound} {
		if classify(result) != needsReconcile {
			t.Fatalf("result %d must park the event for reconciliation", result)
		}
	}
	for _, result := range []int{-1, 2, 99} {
		if classify(result) != unknownResult {
			t.Fatalf("result %d must not be treated as applied", result)
		}
	}
	if reconcileReason(badBound) != "restoring stock would exceed total_stock" ||
		reconcileReason(badState) != "stock key missing, wrong type or not numeric" {
		t.Fatal("reconciliation reasons must stay distinguishable")
	}
}

func TestCanonicalRejectsAmbiguousDigits(t *testing.T) {
	for _, value := range []string{"", "0", "01", "+1", "1 ", "1e3", "18446744073709551616"} {
		if canonical(value) {
			t.Fatalf("%q must not be accepted as an identifier", value)
		}
	}
	if !canonical("653018492815015973") {
		t.Fatal("a snowflake identifier must be accepted")
	}
}
