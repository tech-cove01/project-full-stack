package merchantorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

// ErrToken covers both an unknown token and a token of another shop: the two
// cases must stay indistinguishable so the endpoint cannot be used to probe
// whether a token exists.
var ErrToken = errors.New("核销凭证无效")

var tokenPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type RedeemResult struct {
	Order           View `json:"order"`
	AlreadyRedeemed bool `json:"alreadyRedeemed"`
}

// Redeem consumes a paid order in an authorized shop. The token is a bearer
// value, so authorization still comes from the current membership, never from
// holding the token. Every mutation acquires catalog -> inventory -> order in the
// same order as the user lifecycle to keep one lock order across the service.
func (service *Service) Redeem(ctx context.Context, userID, shopID, token string) (RedeemResult, error) {
	shop, err := positive(shopID)
	if err != nil {
		return RedeemResult{}, ErrInvalidQuery
	}
	if !tokenPattern.MatchString(token) {
		return RedeemResult{}, ErrToken
	}
	var locator struct {
		ID     int64
		ShopID uint64
	}
	if err = service.db.WithContext(ctx).Raw(
		"SELECT id,shop_id FROM tb_voucher_order WHERE redeem_token=?", token).Scan(&locator).Error; err != nil {
		return RedeemResult{}, err
	}
	if locator.ID <= 0 || locator.ShopID != shop {
		return RedeemResult{}, ErrToken
	}
	// Redemption is allowed for STAFF with a concrete scope and for managers.
	if _, err = service.access().MembershipForShop(ctx, userID, decimal(shop)); err != nil {
		return RedeemResult{}, err
	}
	operator, err := positive(userID)
	if err != nil {
		return RedeemResult{}, ErrInvalidQuery
	}

	var result RedeemResult
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding struct{ UserID, VoucherID, ShopID uint64 }
		if err := tx.Raw("SELECT user_id,voucher_id,shop_id FROM tb_voucher_order WHERE id=?", locator.ID).Scan(&binding).Error; err != nil {
			return err
		}
		if binding.ShopID != shop {
			return ErrToken
		}
		var voucher struct{ ID, ShopID uint64 }
		if err := tx.Raw("SELECT id,shop_id FROM tb_voucher WHERE id=? FOR SHARE", binding.VoucherID).Scan(&voucher).Error; err != nil {
			return err
		}
		if voucher.ID == 0 || voucher.ShopID != shop {
			return ErrUnsafe
		}
		var activity struct{ VoucherID, MerchantID uint64 }
		if err := tx.Raw("SELECT voucher_id,merchant_id FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE",
			binding.VoucherID).Scan(&activity).Error; err != nil {
			return err
		}
		if activity.VoucherID == 0 || activity.MerchantID == 0 {
			return ErrUnsafe
		}
		var current struct {
			ID          int64
			ShopID      uint64
			RedeemToken *string
			Status      int
			Version     int
		}
		if err := tx.Raw(`SELECT id,shop_id,redeem_token,status,version
			FROM tb_voucher_order WHERE id=? FOR UPDATE`, locator.ID).Scan(&current).Error; err != nil {
			return err
		}
		if current.ID != locator.ID || current.ShopID != shop ||
			current.RedeemToken == nil || *current.RedeemToken != token {
			return ErrToken
		}
		if current.Status == 3 {
			view, err := service.viewFor(ctx, tx, locator.ID)
			if err != nil {
				return err
			}
			result = RedeemResult{Order: view, AlreadyRedeemed: true}
			return nil
		}
		if current.Status != 2 {
			return ErrConflict
		}
		if current.Version < 0 || current.Version >= math.MaxInt32 {
			return ErrUnsafe
		}
		// NOW(3) is evaluated by this statement after all lock waits; the window
		// check and the state transition cannot be separated by another writer.
		changed := tx.Exec(`UPDATE tb_voucher_order
			SET status=3,use_time=NOW(3),version=version+1
			WHERE id=? AND status=2 AND redeem_token=? AND version=?
			  AND redeem_begin_time<=NOW(3) AND redeem_end_time>NOW(3)`,
			locator.ID, token, current.Version)
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrConflict
		}
		updated, err := service.viewFor(ctx, tx, locator.ID)
		if err != nil {
			return err
		}
		if updated.Status != 3 {
			return ErrUnsafe
		}
		if err = writeRedeemedTransition(ctx, tx, activity.MerchantID, shop, operator,
			binding.VoucherID, binding.UserID, locator.ID, current.Version+1); err != nil {
			return err
		}
		result = RedeemResult{Order: updated}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return RedeemResult{}, err
	}
	return result, nil
}

func writeRedeemedTransition(ctx context.Context, tx *gorm.DB, merchantID, shopID, operator,
	voucherID, buyerUserID uint64, orderID int64, version int) error {
	id := strconv.FormatInt(orderID, 10)
	detail, _ := json.Marshal(map[string]int{"orderStatus": 3, "version": version})
	if err := tx.WithContext(ctx).Exec(`INSERT INTO tb_audit_log
		(merchant_id,shop_id,operator_user_id,operator_type,action,resource_id,detail)
		VALUES(?,?,?,'MERCHANT','ORDER_REDEEMED',?,?)`,
		merchantID, shopID, operator, id, string(detail)).Error; err != nil {
		return err
	}
	// The compensation worker needs the activity and the buyer to restore stock and
	// release eligibility; an event without them cannot be applied later.
	payload := orderEventPayload(id, voucherID, shopID, buyerUserID, "3", "0", "false", version)
	return outbox.Enqueue(ctx, tx, outbox.Event{
		EventID:       "ORDER:" + id + ":" + strconv.Itoa(version) + ":CHANGED",
		AggregateType: "ORDER", AggregateID: id, AggregateVersion: version,
		EventType: "ORDER_CHANGED", Payload: payload})
}
