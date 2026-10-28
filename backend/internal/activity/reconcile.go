package activity

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Reconcile rebuilds Redis state for an ACTIVE activity whose stock or gate was
// lost. The order of operations is the contract:
//  1. close admission at N+1, a version no pending ACTIVITY_OPEN can match;
//  2. prove the order stream, unresolved reviews and pending compensations are empty;
//  3. rewrite stock at N+2 using the values read inside this FOR UPDATE row;
//  4. commit the version bump with the audit row and the OPEN event.
//
// A blocked inspection leaves the gate closed on purpose. The whole check runs at
// READ_COMMITTED with locking reads so a cancellation committed while this
// transaction waited for the activity row cannot stay invisible.
func (service *Service) Reconcile(ctx context.Context, userID, activityID string, confirm bool) (View, error) {
	if !confirm {
		return View{}, invalid("需要显式确认已处理完在途预约与补偿")
	}
	user, err := positiveID(userID)
	if err != nil {
		return View{}, err
	}
	id, err := positiveID(activityID)
	if err != nil {
		return View{}, err
	}
	var located struct{ ShopID uint64 }
	if err = service.db.WithContext(ctx).Raw("SELECT shop_id FROM tb_voucher WHERE id=?", id).Scan(&located).Error; err != nil {
		return View{}, err
	}
	if located.ShopID == 0 {
		return View{}, ErrNotFound
	}
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		member, err := lockManager(ctx, tx, user, located.ShopID)
		if err != nil {
			return err
		}
		var activity struct {
			VoucherID      uint64
			MerchantID     uint64
			PublishStatus  string
			PublishVersion int
			Stock          int
			BeginTime      time.Time
			EndTime        time.Time
		}
		if err = tx.WithContext(ctx).Raw(`SELECT voucher_id,merchant_id,publish_status,
			publish_version,stock,begin_time,end_time
			FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE`, id).Scan(&activity).Error; err != nil {
			return err
		}
		if activity.VoucherID == 0 {
			return ErrNotFound
		}
		if activity.MerchantID != member.merchantID {
			return ErrForbidden
		}
		if activity.PublishStatus != "ACTIVE" {
			return ErrNotActive
		}
		// A previous attempt may have rewritten Redis and then rolled back the
		// database audit/outbox. Fence above both versions; never rewind Redis
		// or permanently strand the activity behind that prepared generation.
		baseVersion := activity.PublishVersion
		if service.redis != nil {
			value, readErr := service.redis.HGet(ctx, "seckill:gate:"+activityID, "version").Result()
			if readErr != nil && readErr != redis.Nil {
				return readErr
			}
			if readErr == nil {
				observed, parseErr := strconv.Atoi(value)
				if parseErr != nil || observed < 1 || observed > 2147483647 {
					return ErrUnsafe
				}
				if observed > baseVersion {
					baseVersion = observed
				}
			}
		}
		closedVersion, err := nextVersion(baseVersion)
		if err != nil {
			return err
		}
		closeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err = service.gate.Close(closeCtx, activityID, closedVersion)
		cancel()
		if err != nil {
			return err
		}
		blockers, err := service.blockers(ctx, tx, activityID)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			return blockedError{reason: strings.Join(blockers, ",")}
		}
		target, err := nextVersion(closedVersion)
		if err != nil {
			return err
		}
		if err = service.gate.Reconcile(ctx, activityID, target, activity.Stock,
			activity.BeginTime, activity.EndTime); err != nil {
			return err
		}
		changed := tx.Table("tb_seckill_voucher").
			Where("voucher_id=? AND publish_version=?", id, activity.PublishVersion).
			Updates(map[string]any{"publish_version": target})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrUnsafe
		}
		if err = audit(ctx, tx, member.merchantID, located.ShopID, user, "ACTIVITY_RECONCILED", id,
			map[string]any{"version": target, "previousVersion": activity.PublishVersion,
				"closedVersion": closedVersion}); err != nil {
			return err
		}
		return enqueue(ctx, tx, id, target, "OPEN")
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

// blockers enumerates work a DB snapshot alone cannot see. Locking reads keep a
// concurrent cancellation from committing between the check and the rewrite.
func (service *Service) blockers(ctx context.Context, tx *gorm.DB, activityID string) ([]string, error) {
	inflight, err := service.inflight(ctx)
	if err != nil {
		return nil, err
	}
	reasons := make([]string, 0, 3)
	switch inflight {
	case 1:
		reasons = append(reasons, "ORDER_STREAM_PENDING")
	case 2:
		reasons = append(reasons, "ORDER_STREAM_UNREAD")
	}
	var reviewIDs []uint64
	if err = tx.WithContext(ctx).Raw(`SELECT id FROM tb_order_review
		WHERE voucher_id=? AND status IN ('OPEN','REQUEUED') FOR UPDATE`, activityID).Scan(&reviewIDs).Error; err != nil {
		return nil, err
	}
	if len(reviewIDs) > 0 {
		reasons = append(reasons, "ORDER_REVIEW_UNRESOLVED")
	}
	var outboxIDs []uint64
	if err = tx.WithContext(ctx).Raw(`SELECT id FROM tb_outbox
		WHERE delivery_status IN ('PENDING','PROCESSING','NEEDS_REVIEW')
		  AND aggregate_type IN ('ORDER','RESERVATION')
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.voucherId'))=?
		FOR UPDATE`, activityID).Scan(&outboxIDs).Error; err != nil {
		return nil, err
	}
	if len(outboxIDs) > 0 {
		reasons = append(reasons, "OUTBOX_PENDING")
	}
	return reasons, nil
}

// inflight returns 0 for an idle stream, 1 for pending entries and 2 for
// delivered-but-unread entries. A missing stream means nothing was ever accepted.
func (service *Service) inflight(ctx context.Context) (int, error) {
	return streamInflight(ctx, service.redis, "stream.orders")
}

func streamInflight(ctx context.Context, client *redis.Client, stream string) (int, error) {
	if client == nil {
		return 0, ErrUnsafe
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	kind, err := client.Type(bounded, stream).Result()
	if err != nil {
		return 0, err
	}
	if kind == "none" {
		return 0, nil
	}
	if kind != "stream" {
		return 0, ErrUnsafe
	}
	groups, err := client.XInfoGroups(bounded, stream).Result()
	if err != nil {
		return 0, err
	}
	if len(groups) == 0 {
		length, err := client.XLen(bounded, stream).Result()
		if err != nil {
			return 0, err
		}
		if length > 0 {
			return 2, nil
		}
		return 0, nil
	}
	for _, group := range groups {
		if group.Pending > 0 {
			return 1, nil
		}
		if group.Lag != 0 {
			return 2, nil
		}
	}
	return 0, nil
}
