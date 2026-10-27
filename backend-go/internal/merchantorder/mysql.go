package merchantorder

import (
	"context"
	"time"
)

type orderRow struct {
	ID            int64
	ShopID        uint64
	ShopName      string
	UserID        uint64
	Phone         *string
	VoucherID     uint64
	VoucherTitle  string
	Status        int
	PayAmount     uint64
	CreateTime    time.Time
	PayTime       *time.Time
	UseTime       *time.Time
	RefundTime    *time.Time
	RedeemEndTime *time.Time
}

type auditRow struct {
	ID             uint64
	ShopID         *uint64
	OperatorUserID uint64
	OperatorType   string
	Action         string
	ResourceID     *string
	Detail         *string
	CreateTime     time.Time
}

// List returns orders the caller may see through their concrete shop scopes. A
// shopId outside that scope yields an empty page instead of a distinguishable
// "forbidden", so the endpoint cannot be used to probe other shops.
func (service *Service) List(ctx context.Context, userID string, query Query) ([]View, uint64, error) {
	query, err := parseQuery(query)
	if err != nil {
		return nil, 0, err
	}
	visible, err := service.access().VisibleShopIDs(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	items := make([]View, 0)
	if len(visible) == 0 {
		return items, 0, nil
	}
	if query.ShopID != "" && !contains(visible, query.ShopID) {
		return items, 0, nil
	}
	args := make([]any, 0, len(visible)+4)
	for _, id := range visible {
		args = append(args, id)
	}
	where := " WHERE o.shop_id IN (" + placeholders(len(visible)) + ")"
	if query.ShopID != "" {
		where += " AND o.shop_id=?"
		args = append(args, query.ShopID)
	}
	if query.Status != "" {
		where += " AND o.status=?"
		args = append(args, query.Status)
	}
	if query.OrderID != "" {
		where += " AND o.id=?"
		args = append(args, query.OrderID)
	}
	from := `
	 FROM tb_voucher_order o
	 JOIN tb_shop s ON s.id=o.shop_id
	 JOIN tb_user u ON u.id=o.user_id
	 JOIN tb_voucher v ON v.id=o.voucher_id` + where
	db := service.db.WithContext(ctx)
	var total uint64
	if err = db.Raw("SELECT COUNT(*)"+from, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, present := pageOffset(query, total)
	if !present {
		return items, total, nil
	}
	var rows []orderRow
	pageArgs := append(append([]any{}, args...), query.PageSize, offset)
	if err = db.Raw(`SELECT o.id,o.shop_id,s.name AS shop_name,o.user_id,u.phone,
		o.voucher_id,v.title AS voucher_title,o.status,o.pay_amount,
		o.create_time,o.pay_time,o.use_time,o.refund_time,o.redeem_end_time`+from+
		` ORDER BY o.create_time DESC,o.id DESC LIMIT ? OFFSET ?`, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		phone := ""
		if row.Phone != nil {
			phone = maskPhone(*row.Phone)
		}
		items = append(items, View{
			ID: decimal(uint64(row.ID)), ShopID: decimal(row.ShopID), ShopName: row.ShopName,
			UserID: decimal(row.UserID), Phone: phone, VoucherID: decimal(row.VoucherID),
			VoucherTitle: row.VoucherTitle, Status: row.Status, PayAmount: decimal(row.PayAmount),
			CreateTime: formattedTime(&row.CreateTime), PayTime: formattedTime(row.PayTime),
			UseTime: formattedTime(row.UseTime), RefundTime: formattedTime(row.RefundTime),
			RedeemEndTime: formattedTime(row.RedeemEndTime),
		})
	}
	return items, total, nil
}

// Audits uses the management scope only: a STAFF scope of another merchant must
// not widen the audit trail, even when the same account manages some merchant.
func (service *Service) Audits(ctx context.Context, userID string, query Query) ([]AuditView, uint64, error) {
	query, err := parseQuery(query)
	if err != nil {
		return nil, 0, err
	}
	managed, err := service.access().ManagedShopIDs(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	if len(managed) == 0 {
		return nil, 0, ErrForbidden
	}
	items := make([]AuditView, 0)
	if query.ShopID != "" && !contains(managed, query.ShopID) {
		return items, 0, nil
	}
	args := make([]any, 0, len(managed)+2)
	for _, id := range managed {
		args = append(args, id)
	}
	where := " WHERE shop_id IN (" + placeholders(len(managed)) + ")"
	if query.ShopID != "" {
		where += " AND shop_id=?"
		args = append(args, query.ShopID)
	}
	if query.Action != "" {
		where += " AND action=?"
		args = append(args, query.Action)
	}
	db := service.db.WithContext(ctx)
	var total uint64
	if err = db.Raw("SELECT COUNT(*) FROM tb_audit_log"+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, present := pageOffset(query, total)
	if !present {
		return items, total, nil
	}
	var rows []auditRow
	pageArgs := append(append([]any{}, args...), query.PageSize, offset)
	if err = db.Raw(`SELECT id,shop_id,operator_user_id,operator_type,action,resource_id,
		CAST(detail AS CHAR) AS detail,create_time FROM tb_audit_log`+where+
		` ORDER BY id DESC LIMIT ? OFFSET ?`, pageArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		item := AuditView{
			ID: decimal(row.ID), OperatorUserID: decimal(row.OperatorUserID),
			OperatorType: row.OperatorType, Action: row.Action, ResourceID: row.ResourceID,
			Detail: row.Detail, CreateTime: *formattedTime(&row.CreateTime),
		}
		if row.ShopID != nil {
			shop := decimal(*row.ShopID)
			item.ShopID = &shop
		}
		items = append(items, item)
	}
	return items, total, nil
}
