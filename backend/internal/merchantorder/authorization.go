package merchantorder

import (
	"context"

	"github.com/cityloop/fullstack-go/internal/merchant"
	"gorm.io/gorm"
)

// Hold authorization facts for the complete business transaction. The initial
// nonlocking check is only a fast rejection; it cannot authorize a write after
// a concurrent revocation. Lock shop -> merchant -> member -> optional scope,
// before catalog/inventory/order, matching activity management's lock order.
func lockMutationMembership(ctx context.Context, tx *gorm.DB, user, shop uint64, manager bool) (uint64, error) {
	db := tx.WithContext(ctx)
	var s struct{ ID, MerchantID uint64 }
	if err := db.Raw("SELECT id,merchant_id FROM tb_shop WHERE id=? FOR SHARE", shop).Scan(&s).Error; err != nil {
		return 0, err
	}
	if s.ID == 0 || s.MerchantID == 0 {
		return 0, merchant.ErrForbidden
	}
	var m struct {
		ID     uint64
		Status string
	}
	if err := db.Raw("SELECT id,status FROM tb_merchant WHERE id=? FOR SHARE", s.MerchantID).Scan(&m).Error; err != nil {
		return 0, err
	}
	if m.ID == 0 || m.Status != "ACTIVE" {
		return 0, merchant.ErrForbidden
	}
	var member struct {
		ID               uint64
		RoleCode, Status string
	}
	if err := db.Raw("SELECT id,role_code,status FROM tb_merchant_member WHERE merchant_id=? AND user_id=? FOR SHARE", m.ID, user).Scan(&member).Error; err != nil {
		return 0, err
	}
	if member.ID == 0 || member.Status != "ACTIVE" {
		return 0, merchant.ErrForbidden
	}
	if member.RoleCode == "OWNER" || member.RoleCode == "MANAGER" {
		return m.ID, nil
	}
	if manager || member.RoleCode != "STAFF" {
		return 0, merchant.ErrForbidden
	}
	var scope struct{ MemberID uint64 }
	if err := db.Raw("SELECT member_id FROM tb_member_shop_scope WHERE member_id=? AND shop_id=? FOR SHARE", member.ID, shop).Scan(&scope).Error; err != nil {
		return 0, err
	}
	if scope.MemberID == 0 {
		return 0, merchant.ErrForbidden
	}
	return m.ID, nil
}
