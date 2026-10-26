package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/outbox"
)

type activityRow struct {
	VoucherID       uint64
	ShopID          uint64
	MerchantID      uint64
	ShopName        string
	Title           string
	SubTitle        *string
	PayValue        uint64
	ActualValue     int64
	TotalStock      int
	Stock           int
	PublishStatus   string
	PublishVersion  int
	BeginTime       time.Time
	EndTime         time.Time
	RedeemBeginTime *time.Time
	RedeemEndTime   *time.Time
}

const viewColumns = `sv.voucher_id, v.shop_id, sv.merchant_id, s.name AS shop_name,
 v.title, v.sub_title, v.pay_value, v.actual_value, sv.total_stock, sv.stock,
 sv.publish_status, sv.publish_version, sv.begin_time, sv.end_time,
 sv.redeem_begin_time, sv.redeem_end_time`
const viewFrom = ` FROM tb_seckill_voucher sv JOIN tb_voucher v ON v.id=sv.voucher_id
 JOIN tb_shop s ON s.id=v.shop_id`

// Authorization is part of every list statement. A previously resolved list
// of shop IDs is not reused after membership or scope is revoked.
const visibleWhere = ` WHERE EXISTS (
 SELECT 1 FROM tb_merchant m
 JOIN tb_merchant_member mm ON mm.merchant_id=m.id
 WHERE m.id=s.merchant_id AND m.status='ACTIVE' AND mm.user_id=? AND mm.status='ACTIVE'
 AND (mm.role_code IN ('OWNER','MANAGER') OR (mm.role_code='STAFF' AND EXISTS (
 SELECT 1 FROM tb_member_shop_scope sc WHERE sc.member_id=mm.id AND sc.shop_id=s.id))))`

func (service *Service) List(ctx context.Context, userID string, query Query) ([]View, uint64, error) {
	user, err := positiveID(userID)
	if err != nil {
		return nil, 0, err
	}
	if err = validateQuery(query); err != nil {
		return nil, 0, err
	}
	where, args := visibleWhere, []any{user}
	if query.ShopID != "" {
		shop, _ := positiveID(query.ShopID)
		where += " AND s.id=?"
		args = append(args, shop)
	}
	if query.Status != "" {
		where += " AND sv.publish_status=?"
		args = append(args, query.Status)
	}
	db := service.db.WithContext(ctx)
	var total uint64
	if err = db.Raw("SELECT COUNT(*)"+viewFrom+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]View, 0)
	start, present := offset(query, total)
	if !present {
		return items, total, nil
	}
	var rows []activityRow
	args = append(args, query.PageSize, start)
	if err = db.Raw("SELECT "+viewColumns+viewFrom+where+" ORDER BY sv.voucher_id DESC LIMIT ? OFFSET ?", args...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		items = append(items, service.toView(ctx, row))
	}
	return items, total, nil
}

func (service *Service) toView(ctx context.Context, row activityRow) View {
	view := View{ID: decimal(row.VoucherID), ShopID: decimal(row.ShopID), ShopName: row.ShopName,
		Title: row.Title, SubTitle: row.SubTitle, PayValue: decimal(row.PayValue), ActualValue: strconv.FormatInt(row.ActualValue, 10),
		TotalStock: row.TotalStock, Stock: row.Stock, PublishStatus: row.PublishStatus, PublishVersion: row.PublishVersion,
		BeginTime: displayTime(row.BeginTime), EndTime: displayTime(row.EndTime)}
	if row.RedeemBeginTime != nil {
		formatted := displayTime(*row.RedeemBeginTime)
		view.RedeemBeginTime = &formatted
	}
	if row.RedeemEndTime != nil {
		formatted := displayTime(*row.RedeemEndTime)
		view.RedeemEndTime = &formatted
	}
	if row.PublishStatus == "ACTIVE" {
		// A dependency outage must not fabricate readiness or hide known DB state.
		readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		view.Ready, _ = service.gate.Ready(readCtx, view.ID, row.PublishVersion)
		cancel()
	}
	return view
}

func displayTime(value time.Time) string { return value.In(businessZone).Format("2006-01-02 15:04:05") }

func (service *Service) view(ctx context.Context, id uint64) (View, error) {
	var row activityRow
	result := service.db.WithContext(ctx).Raw("SELECT "+viewColumns+viewFrom+" WHERE sv.voucher_id=?", id).Scan(&row)
	if result.Error != nil {
		return View{}, result.Error
	}
	if row.VoucherID == 0 {
		return View{}, ErrNotFound
	}
	return service.toView(ctx, row), nil
}

type membership struct{ merchantID uint64 }

// lockManager uses current reads in a fixed shop -> merchant -> member order.
// Shared row locks remain held until the business transaction commits; a
// concurrent revocation must finish before authorization or wait for the write.
func lockManager(ctx context.Context, tx *gorm.DB, user, shop uint64) (membership, error) {
	db := tx.WithContext(ctx)
	var s struct {
		ID         uint64
		MerchantID uint64
	}
	if err := db.Raw("SELECT id,merchant_id FROM tb_shop WHERE id=? FOR SHARE", shop).Scan(&s).Error; err != nil {
		return membership{}, err
	}
	if s.ID == 0 || s.MerchantID == 0 {
		return membership{}, ErrForbidden
	}
	var m struct {
		ID     uint64
		Status string
	}
	if err := db.Raw("SELECT id,status FROM tb_merchant WHERE id=? FOR SHARE", s.MerchantID).Scan(&m).Error; err != nil {
		return membership{}, err
	}
	if m.ID == 0 || m.Status != "ACTIVE" {
		return membership{}, ErrForbidden
	}
	var mm struct {
		ID       uint64
		RoleCode string
		Status   string
	}
	if err := db.Raw(`SELECT id,role_code,status FROM tb_merchant_member
 WHERE merchant_id=? AND user_id=? FOR SHARE`, m.ID, user).Scan(&mm).Error; err != nil {
		return membership{}, err
	}
	if mm.ID == 0 || mm.Status != "ACTIVE" || (mm.RoleCode != "OWNER" && mm.RoleCode != "MANAGER") {
		return membership{}, ErrForbidden
	}
	return membership{merchantID: m.ID}, nil
}

type voucherInsert struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	ShopID      uint64
	Title       string
	SubTitle    *string
	Rules       *string
	PayValue    uint64
	ActualValue int64
	Type        uint8
	Status      uint8
}

func (voucherInsert) TableName() string { return "tb_voucher" }

type seckillInsert struct {
	VoucherID          uint64 `gorm:"primaryKey;autoIncrement:false"`
	MerchantID         uint64
	Stock              int
	TotalStock         int
	PublishStatus      string
	PublishVersion     int
	BeginTime          time.Time
	EndTime            time.Time
	RedeemBeginTime    time.Time
	RedeemEndTime      time.Time
	PublishRequestKey  string
	PublishRequestHash string
}

func (seckillInsert) TableName() string { return "tb_seckill_voucher" }

func (service *Service) Create(ctx context.Context, userID, key string, request CreateRequest) (View, error) {
	user, err := positiveID(userID)
	if err != nil {
		return View{}, err
	}
	validated, err := validate(key, request)
	if err != nil {
		return View{}, err
	}
	var id uint64
	create := func(tx *gorm.DB) error {
		member, err := lockManager(ctx, tx, user, validated.shopID)
		if err != nil {
			return err
		}
		var existing struct {
			VoucherID          uint64
			PublishRequestHash string
		}
		if err = tx.WithContext(ctx).Raw(`SELECT voucher_id,publish_request_hash FROM tb_seckill_voucher
 WHERE merchant_id=? AND publish_request_key=?`, member.merchantID, key).Scan(&existing).Error; err != nil {
			return err
		}
		if existing.VoucherID != 0 {
			if existing.PublishRequestHash != validated.hash {
				return ErrConflict
			}
			id = existing.VoucherID
			return nil
		}
		v := voucherInsert{ShopID: validated.shopID, Title: validated.request.Title, SubTitle: validated.request.SubTitle, Rules: validated.request.Rules,
			PayValue: uint64(validated.payValue), ActualValue: validated.actualValue, Type: 1, Status: 1}
		if err = tx.WithContext(ctx).Create(&v).Error; err != nil {
			return err
		}
		sv := seckillInsert{VoucherID: v.ID, MerchantID: member.merchantID, Stock: request.TotalStock, TotalStock: request.TotalStock,
			PublishStatus: "INITIALIZING", PublishVersion: 0, BeginTime: validated.begin, EndTime: validated.end,
			RedeemBeginTime: validated.redeemBegin, RedeemEndTime: validated.redeemEnd, PublishRequestKey: key, PublishRequestHash: validated.hash}
		if err = tx.WithContext(ctx).Create(&sv).Error; err != nil {
			return err
		}
		if err = audit(ctx, tx, member.merchantID, validated.shopID, user, "ACTIVITY_CREATE", v.ID, map[string]any{
			"title": validated.request.Title, "totalStock": request.TotalStock, "beginTime": displayTime(validated.begin), "endTime": displayTime(validated.end)}); err != nil {
			return err
		}
		if err = enqueue(ctx, tx, v.ID, 0, "PREPARE"); err != nil {
			return err
		}
		id = v.ID
		return nil
	}
	err = service.db.WithContext(ctx).Transaction(create, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	// A competing same-key INSERT may win after our initial query. Roll back
	// our voucher, then query the winner in a new transaction with fresh auth.
	var duplicate *mysqldriver.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			member, err := lockManager(ctx, tx, user, validated.shopID)
			if err != nil {
				return err
			}
			var existing struct {
				VoucherID          uint64
				PublishRequestHash string
			}
			if err = tx.WithContext(ctx).Raw(`SELECT voucher_id,publish_request_hash FROM tb_seckill_voucher WHERE merchant_id=? AND publish_request_key=?`, member.merchantID, key).Scan(&existing).Error; err != nil {
				return err
			}
			if existing.VoucherID == 0 {
				return duplicate
			}
			if existing.PublishRequestHash != validated.hash {
				return ErrConflict
			}
			id = existing.VoucherID
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

type auditInsert struct {
	MerchantID     uint64
	ShopID         uint64
	OperatorUserID uint64
	OperatorType   string
	Action         string
	ResourceID     string
	Detail         json.RawMessage
}

func (auditInsert) TableName() string { return "tb_audit_log" }

func audit(ctx context.Context, tx *gorm.DB, merchant, shop, user uint64, action string, id uint64, detail map[string]any) error {
	body, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Create(&auditInsert{MerchantID: merchant, ShopID: shop, OperatorUserID: user, OperatorType: "MERCHANT", Action: action, ResourceID: decimal(id), Detail: body}).Error
}

func enqueue(ctx context.Context, tx *gorm.DB, id uint64, version int, action string) error {
	payload := map[string]any{"activityId": decimal(id)}
	if action != "PREPARE" {
		payload["version"] = version
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return outbox.Enqueue(ctx, tx, outbox.Event{EventID: "ACTIVITY:" + decimal(id) + ":" + strconv.Itoa(version) + ":" + action,
		AggregateType: "ACTIVITY", AggregateID: decimal(id), AggregateVersion: version, EventType: "ACTIVITY_" + action, Payload: body})
}

// mutate authorizes against the resource's real shop before locking its
// activity row. This order matches create and never trusts a request shopId.
func (service *Service) mutate(ctx context.Context, userID, activityID string, offline bool) (View, error) {
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
		var voucher struct{ ShopID uint64 }
		if err = tx.Raw("SELECT shop_id FROM tb_voucher WHERE id=? FOR SHARE", id).Scan(&voucher).Error; err != nil {
			return err
		}
		if voucher.ShopID != located.ShopID {
			return ErrForbidden
		}
		var activity struct {
			VoucherID      uint64
			MerchantID     uint64
			PublishStatus  string
			PublishVersion int
		}
		if err = tx.Raw("SELECT voucher_id,merchant_id,publish_status,publish_version FROM tb_seckill_voucher WHERE voucher_id=? FOR UPDATE", id).Scan(&activity).Error; err != nil {
			return err
		}
		if activity.VoucherID == 0 {
			return ErrNotFound
		}
		if activity.MerchantID != member.merchantID {
			return ErrForbidden
		}
		if activity.PublishStatus == "OFFLINE" {
			if offline {
				return nil
			}
			return ErrOffline
		}
		if activity.PublishStatus != "INITIALIZING" && activity.PublishStatus != "ACTIVE" {
			return ErrUnsafe
		}
		if !offline {
			action := "PREPARE"
			if activity.PublishStatus == "ACTIVE" {
				action = "OPEN"
			}
			// Duplicate DONE events stay DONE. An explicit retry cannot recreate
			// missing Redis state or undo a safety close after a failed commit.
			return enqueue(ctx, tx, id, activity.PublishVersion, action)
		}
		next, err := nextVersion(activity.PublishVersion)
		if err != nil {
			return err
		}
		closeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err = service.gate.Close(closeCtx, activityID, next)
		cancel()
		if err != nil {
			return err
		}
		changed := tx.Table("tb_seckill_voucher").Where("voucher_id=? AND publish_version=?", id, activity.PublishVersion).Updates(map[string]any{"publish_status": "OFFLINE", "publish_version": next})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrUnsafe
		}
		if err = audit(ctx, tx, member.merchantID, located.ShopID, user, "ACTIVITY_OFFLINE", id, map[string]any{"version": next}); err != nil {
			return err
		}
		return enqueue(ctx, tx, id, next, "CLOSE")
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

func (service *Service) Retry(ctx context.Context, userID, id string) (View, error) {
	return service.mutate(ctx, userID, id, false)
}
func (service *Service) Offline(ctx context.Context, userID, id string) (View, error) {
	return service.mutate(ctx, userID, id, true)
}
