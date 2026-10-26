package order

import (
	"context"
	"strconv"
	"time"
)

func (service *Service) Get(ctx context.Context, userID, id string) (View, error) {
	user, err := unsignedID(userID)
	if err != nil {
		return View{}, err
	}
	order, err := orderID(id)
	if err != nil {
		return View{}, err
	}
	var row record
	if err = service.db.WithContext(ctx).Raw("SELECT "+columns+" FROM tb_voucher_order WHERE id=? AND user_id=?", order, user).Scan(&row).Error; err != nil {
		return View{}, err
	}
	if row.ID != 0 {
		return toView(row, true), nil
	}
	// A forged Redis hash cannot make another person's committed order visible.
	var current record
	if err = service.db.WithContext(ctx).Raw("SELECT "+columns+" FROM tb_voucher_order WHERE id=?", order).Scan(&current).Error; err != nil {
		return View{}, err
	}
	if current.ID != 0 {
		if current.UserID != user {
			return View{}, ErrNotFound
		}
		// Creation may have committed after the first owned query. A newly
		// visible order still belongs to this user; polling must not stop at 404.
		return toView(current, true), nil
	}
	rejected, err := findRejection(ctx, service.db, id)
	if err != nil {
		return View{}, err
	}
	if rejected != nil {
		if rejected.UserID != userID {
			return View{}, ErrNotFound
		}
		return View{ID: id, State: Failed, Message: rejected.Reason}, nil
	}
	reservation, err := service.redis.HGetAll(ctx, "order:reservation:"+id).Result()
	if err != nil {
		return View{}, err
	}
	if reservation["userId"] != userID {
		return View{}, ErrNotFound
	}
	if _, err = unsignedID(reservation["voucherId"]); err != nil {
		return View{}, ErrUnsafe
	}
	// Redis CREATED/FAILED is only a cache. Without a durable DB fact either
	// remains PROCESSING; neither can justify inventory release or an ACK.
	return View{ID: id, State: Processing, Message: "订单处理中"}, nil
}

func (service *Service) List(ctx context.Context, userID string, query Query) ([]View, uint64, error) {
	user, err := unsignedID(userID)
	if err != nil {
		return nil, 0, err
	}
	if query.Current == 0 || query.PageSize == 0 || query.PageSize > 50 {
		return nil, 0, ErrPagination
	}
	db := service.db.WithContext(ctx)
	var total uint64
	if err = db.Raw("SELECT COUNT(*) FROM tb_voucher_order WHERE user_id=?", user).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]View, 0)
	offset, present := pageOffset(query, total)
	if !present {
		return items, total, nil
	}
	var rows []record
	if err = db.Raw("SELECT "+columns+" FROM tb_voucher_order WHERE user_id=? ORDER BY create_time DESC,id DESC LIMIT ? OFFSET ?", user, query.PageSize, offset).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		items = append(items, toView(row, false))
	}
	return items, total, nil
}

func (service *Service) Seckill(ctx context.Context, userID, voucherID string) (string, error) {
	user, err := unsignedID(userID)
	if err != nil {
		return "", err
	}
	voucher, err := unsignedID(voucherID)
	if err != nil {
		return "", err
	}
	var active struct{ ID int64 }
	if err = service.db.WithContext(ctx).Raw("SELECT id FROM tb_voucher_order WHERE user_id=? AND voucher_id=? AND active_flag=1 LIMIT 1", user, voucher).Scan(&active).Error; err != nil {
		return "", err
	}
	if active.ID != 0 {
		if active.ID < 1 {
			return "", ErrUnsafe
		}
		return strconv.FormatInt(active.ID, 10), nil
	}
	if recovered, err := service.admission.Recover(ctx, voucherID, userID); err != nil {
		return "", err
	} else if recovered != "" {
		if err := service.checkAcceptedIdentity(ctx, recovered, userID, voucherID); err != nil {
			return "", err
		}
		return recovered, nil
	}
	var activity struct {
		VoucherID          uint64
		PublishVersion     int
		BeginTime, EndTime time.Time
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT sv.voucher_id,sv.publish_version,sv.begin_time,sv.end_time
 FROM tb_seckill_voucher sv JOIN tb_voucher v ON v.id=sv.voucher_id
 WHERE sv.voucher_id=? AND sv.publish_status='ACTIVE' AND v.status=1`, voucher).Scan(&activity).Error; err != nil {
		return "", err
	}
	if activity.VoucherID == 0 {
		return "", ErrActivity
	}
	for attempt := 0; attempt < 5; attempt++ {
		now := time.Now()
		id, err := service.admission.NextID(ctx, now)
		if err != nil {
			return "", err
		}
		candidate, err := orderID(id)
		if err != nil {
			return "", ErrUnsafe
		}
		var exists bool
		if err = service.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tb_voucher_order WHERE id=?)
 OR EXISTS(SELECT 1 FROM tb_outbox WHERE event_id=?)`, candidate, rejectionID(id)).Scan(&exists).Error; err != nil {
			return "", err
		}
		if exists {
			continue
		}
		result, err := service.admission.Reserve(ctx, voucherID, userID, id, now, activity.BeginTime, activity.EndTime, activity.PublishVersion)
		if err != nil {
			return "", err
		}
		if result.Reused {
			if err := service.checkAcceptedIdentity(ctx, result.ID, userID, voucherID); err != nil {
				return "", err
			}
		}
		return result.ID, nil
	}
	return "", ErrUnsafe
}

func (service *Service) checkAcceptedIdentity(ctx context.Context, id, userID, voucherID string) error {
	parsed, err := orderID(id)
	if err != nil {
		return ErrUnsafe
	}
	var binding struct {
		UserID    uint64
		VoucherID uint64
	}
	if err = service.db.WithContext(ctx).Raw("SELECT user_id,voucher_id FROM tb_voucher_order WHERE id=?", parsed).Scan(&binding).Error; err != nil {
		return err
	}
	if binding.UserID != 0 && (decimal(binding.UserID) != userID || decimal(binding.VoucherID) != voucherID) {
		return ErrCollision
	}
	rejected, err := findRejection(ctx, service.db, id)
	if err != nil {
		return err
	}
	if rejected != nil && (rejected.UserID != userID || rejected.VoucherID != voucherID) {
		return ErrCollision
	}
	if service.redis == nil {
		return ErrUnsafe
	}
	reservation, err := service.redis.HGetAll(ctx, "order:reservation:"+id).Result()
	if err != nil {
		return err
	}
	if len(reservation) != 0 {
		if _, err := unsignedID(reservation["userId"]); err != nil {
			return ErrUnsafe
		}
		if _, err := unsignedID(reservation["voucherId"]); err != nil {
			return ErrUnsafe
		}
		if reservation["userId"] != userID || reservation["voucherId"] != voucherID {
			return ErrCollision
		}
	}
	// Reservation hashes expire before the accepted index. A missing hash does
	// not invent a terminal state or invalidate the remaining stable index.
	return nil
}
