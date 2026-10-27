package merchantorder

import (
	"encoding/json"
	"strconv"
)

// orderEventPayload builds the ORDER_CHANGED payload shared by the merchant
// transitions. The compensation worker restores Redis from this event, so the
// activity and the buyer must be part of it: an event without them can never be
// applied later and would park as NEEDS_REVIEW forever.
func orderEventPayload(orderID string, voucherID, shopID, buyerUserID uint64,
	targetStatus, stockRestore, releaseEligibility string, version int) []byte {
	payload, _ := json.Marshal(map[string]string{
		"orderId": orderID, "voucherId": decimal(voucherID), "shopId": decimal(shopID),
		"userId": decimal(buyerUserID), "targetStatus": targetStatus,
		"stockRestore": stockRestore, "releaseEligibility": releaseEligibility,
		"version": strconv.Itoa(version),
	})
	return payload
}
