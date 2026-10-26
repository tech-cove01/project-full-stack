package order

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

type lifecycleBinding struct{ UserID, VoucherID, ShopID uint64 }

func (service *Service) ownedBinding(ctx context.Context, user uint64, id int64) (lifecycleBinding, error) {
	var binding lifecycleBinding
	if err := service.db.WithContext(ctx).Raw(`SELECT user_id,voucher_id,shop_id FROM tb_voucher_order
 WHERE id=? AND user_id=?`, id, user).Scan(&binding).Error; err != nil {
		return binding, err
	}
	if binding.UserID == 0 {
		return binding, ErrNotFound
	}
	return binding, nil
}

// Every lifecycle mutation acquires catalog -> inventory -> order in this
// order. The first nonlocking lookup is only a locator, never authorization or
// state evidence; the current locked order is checked again before any write.
func lockLifecycle(ctx context.Context, tx *gorm.DB, id int64, user uint64, binding lifecycleBinding) (record, uint64, error) {
	var catalog struct{ ID, ShopID uint64 }
	if err := tx.WithContext(ctx).Raw("SELECT id,shop_id FROM tb_voucher WHERE id=? FOR SHARE", binding.VoucherID).Scan(&catalog).Error; err != nil {
		return record{}, 0, err
	}
	if catalog.ID == 0 || catalog.ShopID != binding.ShopID {
		return record{}, 0, ErrUnsafe
	}
	var activity struct{ VoucherID, MerchantID uint64 }
	if err := tx.Raw("SELECT voucher_id,merchant_id FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE", binding.VoucherID).Scan(&activity).Error; err != nil {
		return record{}, 0, err
	}
	if activity.VoucherID == 0 || activity.MerchantID == 0 {
		return record{}, 0, ErrUnsafe
	}
	current, err := findOrder(ctx, tx, id, true)
	if err != nil {
		return record{}, 0, err
	}
	if current == nil || current.UserID != user {
		return record{}, 0, ErrNotFound
	}
	if current.VoucherID != binding.VoucherID || current.ShopID != binding.ShopID {
		return record{}, 0, ErrUnsafe
	}
	return *current, activity.MerchantID, nil
}

func secureRedeemToken() (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(entropy[:]), nil
}

// Pay is a development simulation, with no external payment provider. Only
// commit makes the transition successful; uncertain outcomes return no View.
func (service *Service) Pay(ctx context.Context, userID, id string) (View, error) {
	user, err := unsignedID(userID)
	if err != nil {
		return View{}, err
	}
	order, err := orderID(id)
	if err != nil {
		return View{}, err
	}
	binding, err := service.ownedBinding(ctx, user, order)
	if err != nil {
		return View{}, err
	}
	generate := service.newRedeemToken
	if generate == nil {
		generate = secureRedeemToken
	}
	for attempt := 0; attempt < 3; attempt++ {
		var view View
		err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			current, merchant, err := lockLifecycle(ctx, tx, order, user, binding)
			if err != nil {
				return err
			}
			if current.Status == 2 {
				if current.RedeemToken == nil || *current.RedeemToken == "" {
					return ErrUnsafe
				}
				view = toView(current, true)
				return nil
			}
			if current.Status != 1 {
				return ErrConflict
			}
			if current.Version < 0 || current.Version >= math.MaxInt32 {
				return ErrUnsafe
			}
			token, err := generate()
			if err != nil {
				return err
			}
			// NOW(3) belongs to this statement, after all row-lock waits. A time
			// captured at transaction start could otherwise permit a late payment.
			changed := tx.Exec(`UPDATE tb_voucher_order
 SET status=2,pay_time=NOW(3),redeem_token=?,version=version+1
 WHERE id=? AND user_id=? AND status=1 AND version=? AND pay_deadline_at>NOW(3)`, token, order, user, current.Version)
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrConflict
			}
			updated, err := findOrder(ctx, tx, order, true)
			if err != nil {
				return err
			}
			if updated == nil || updated.Status != 2 || updated.Version != current.Version+1 || updated.UserID != user {
				return ErrUnsafe
			}
			if err = writePaidTransition(ctx, tx, *updated, merchant, user, id); err != nil {
				return err
			}
			view = toView(*updated, true)
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err == nil {
			return view, nil
		}
		var duplicate *mysqldriver.MySQLError
		if !errors.As(err, &duplicate) || duplicate.Number != 1062 || !strings.Contains(duplicate.Message, "uk_order_redeem_token") {
			return View{}, err
		}
		// A rare token collision rolls back the complete transaction. Generate
		// another token only in a fresh transaction, with a bounded retry count.
	}
	return View{}, err
}

func writePaidTransition(ctx context.Context, tx *gorm.DB, row record, merchant, user uint64, id string) error {
	detail, _ := json.Marshal(map[string]int{"orderStatus": 2, "version": row.Version})
	if err := tx.WithContext(ctx).Exec(`INSERT INTO tb_audit_log
 (merchant_id,shop_id,operator_user_id,operator_type,action,resource_id,detail)
 VALUES(?,?,?,'USER','ORDER_PAID',?,?)`, merchant, row.ShopID, user, id, string(detail)).Error; err != nil {
		return err
	}
	version := strconv.Itoa(row.Version)
	payload, _ := json.Marshal(map[string]string{"orderId": id, "voucherId": decimal(row.VoucherID), "shopId": decimal(row.ShopID), "userId": decimal(user),
		"targetStatus": "2", "stockRestore": "0", "releaseEligibility": "false", "version": version})
	return outbox.Enqueue(ctx, tx, outbox.Event{EventID: "ORDER:" + id + ":" + version + ":CHANGED", AggregateType: "ORDER", AggregateID: id,
		AggregateVersion: row.Version, EventType: "ORDER_CHANGED", Payload: payload})
}
