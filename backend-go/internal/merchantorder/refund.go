package merchantorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

const maxReasonRunes = 64

// Refund is a merchant-side simulation: only OWNER/MANAGER of the concrete shop
// may call it, and only for a paid order that is still inside its redemption
// window. Inventory is restored once in the same transaction as the state change,
// audit and event; the Redis projection stays owned by the outbox relay.
func (service *Service) Refund(ctx context.Context, userID, orderID, reason string) (View, error) {
	id, err := orderKey(orderID)
	if err != nil {
		return View{}, err
	}
	operator, err := positive(userID)
	if err != nil {
		return View{}, err
	}
	reason, err = refundReason(reason)
	if err != nil {
		return View{}, err
	}
	var locator struct{ ShopID, VoucherID uint64 }
	if err = service.db.WithContext(ctx).Raw(
		"SELECT shop_id,voucher_id FROM tb_voucher_order WHERE id=?", id).Scan(&locator).Error; err != nil {
		return View{}, err
	}
	if locator.ShopID == 0 {
		return View{}, ErrNotFound
	}
	// Management scope is resolved from the current membership, never from the
	// request; a STAFF member cannot refund.
	if _, err = service.access().RequireManager(ctx, userID, decimal(locator.ShopID)); err != nil {
		return View{}, err
	}

	var view View
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding struct{ UserID, VoucherID, ShopID uint64 }
		if err := tx.Raw("SELECT user_id,voucher_id,shop_id FROM tb_voucher_order WHERE id=?", id).Scan(&binding).Error; err != nil {
			return err
		}
		if binding.ShopID != locator.ShopID || binding.VoucherID != locator.VoucherID {
			return ErrUnsafe
		}
		var voucher struct{ ID, ShopID uint64 }
		if err := tx.Raw("SELECT id,shop_id FROM tb_voucher WHERE id=? FOR SHARE", binding.VoucherID).Scan(&voucher).Error; err != nil {
			return err
		}
		if voucher.ID == 0 || voucher.ShopID != binding.ShopID {
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
			ID      int64
			ShopID  uint64
			Status  int
			Version int
		}
		if err := tx.Raw(`SELECT id,shop_id,status,version FROM tb_voucher_order WHERE id=? FOR UPDATE`,
			id).Scan(&current).Error; err != nil {
			return err
		}
		if current.ID != id || current.ShopID != locator.ShopID {
			return ErrUnsafe
		}
		if current.Status == 6 {
			result, err := service.viewFor(ctx, tx, id)
			if err != nil {
				return err
			}
			view = result
			return nil
		}
		if current.Status != 2 {
			return ErrConflict
		}
		if current.Version < 0 || current.Version >= math.MaxInt32 {
			return ErrUnsafe
		}
		changed := tx.Exec(`UPDATE tb_voucher_order
			SET status=6,refund_time=NOW(3),version=version+1
			WHERE id=? AND status=2 AND version=? AND redeem_end_time>NOW(3)`,
			id, current.Version)
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrConflict
		}
		restored := tx.Exec(`UPDATE tb_seckill_voucher SET stock=stock+1
			WHERE voucher_id=? AND stock>=0 AND stock<total_stock`, binding.VoucherID)
		if restored.Error != nil {
			return restored.Error
		}
		if restored.RowsAffected != 1 {
			return ErrUnsafe
		}
		updated, err := service.viewFor(ctx, tx, id)
		if err != nil {
			return err
		}
		if updated.Status != 6 {
			return ErrUnsafe
		}
		if err = writeRefundedTransition(ctx, tx, activity.MerchantID, binding.ShopID, operator,
			id, current.Version+1, reason); err != nil {
			return err
		}
		view = updated
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return View{}, err
	}
	return view, nil
}

func refundReason(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalidQuery
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "MERCHANT_REFUND", nil
	}
	runes := []rune(value)
	if len(runes) > maxReasonRunes {
		value = string(runes[:maxReasonRunes])
	}
	return value, nil
}

func writeRefundedTransition(ctx context.Context, tx *gorm.DB, merchantID, shopID, operator uint64,
	orderID int64, version int, reason string) error {
	id := strconv.FormatInt(orderID, 10)
	detail, _ := json.Marshal(map[string]any{"orderStatus": 6, "reason": reason, "version": version})
	if err := tx.WithContext(ctx).Exec(`INSERT INTO tb_audit_log
		(merchant_id,shop_id,operator_user_id,operator_type,action,resource_id,detail)
		VALUES(?,?,?,'MERCHANT','ORDER_REFUNDED',?,?)`,
		merchantID, shopID, operator, id, string(detail)).Error; err != nil {
		return err
	}
	// releaseEligibility=false: a refunded order keeps the purchase eligibility,
	// so the user cannot buy the same activity again.
	payload, _ := json.Marshal(map[string]string{
		"orderId": id, "targetStatus": "6", "stockRestore": "1",
		"releaseEligibility": "false", "version": strconv.Itoa(version)})
	return outbox.Enqueue(ctx, tx, outbox.Event{
		EventID:       "ORDER:" + id + ":" + strconv.Itoa(version) + ":CHANGED",
		AggregateType: "ORDER", AggregateID: id, AggregateVersion: version,
		EventType: "ORDER_CHANGED", Payload: payload})
}
