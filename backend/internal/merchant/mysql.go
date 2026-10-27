package merchant

import "context"

type workspaceRow struct {
	MerchantID   uint64
	MerchantName string
	RoleCode     Role
	ShopID       *uint64
	ShopName     *string
	ShopAddress  *string
}

// Workspace uses one statement so membership status, merchant status and shop
// scopes share one MySQL statement view, including memberships without shops.
func (service *Service) Workspace(ctx context.Context, userID string) (Workspace, error) {
	user, err := parseID(userID)
	if err != nil {
		return Workspace{}, err
	}
	var rows []workspaceRow
	err = service.db.WithContext(ctx).Raw(`
		SELECT m.id AS merchant_id, m.name AS merchant_name, mm.role_code,
		       s.id AS shop_id, s.name AS shop_name, s.address AS shop_address
		FROM tb_merchant_member mm
		JOIN tb_merchant m ON m.id = mm.merchant_id AND m.status = 'ACTIVE'
		LEFT JOIN tb_shop s ON s.merchant_id = mm.merchant_id
		  AND (mm.role_code IN ('OWNER', 'MANAGER') OR
		       (mm.role_code = 'STAFF' AND EXISTS (
		         SELECT 1 FROM tb_member_shop_scope sc
		         WHERE sc.member_id = mm.id AND sc.shop_id = s.id)))
		WHERE mm.user_id = ? AND mm.status = 'ACTIVE'
		  AND mm.role_code IN ('OWNER', 'MANAGER', 'STAFF')
		ORDER BY m.id ASC, s.id ASC`, user).Scan(&rows).Error
	if err != nil {
		return Workspace{}, err
	}
	return assembleWorkspace(rows), nil
}

func assembleWorkspace(rows []workspaceRow) Workspace {
	result := Workspace{Merchants: make([]MerchantSummary, 0)}
	for _, row := range rows {
		merchantID := decimal(row.MerchantID)
		index := len(result.Merchants) - 1
		if index < 0 || result.Merchants[index].ID != merchantID {
			result.Merchants = append(result.Merchants, MerchantSummary{
				ID: merchantID, Name: row.MerchantName, Role: row.RoleCode,
				Shops: make([]ShopSummary, 0),
			})
			index++
		}
		if row.ShopID != nil {
			shop := ShopSummary{ID: decimal(*row.ShopID)}
			if row.ShopName != nil {
				shop.Name = *row.ShopName
			}
			if row.ShopAddress != nil {
				shop.Address = *row.ShopAddress
			}
			result.Merchants[index].Shops = append(result.Merchants[index].Shops, shop)
		}
	}
	return result
}

// MembershipForShop authorizes the requested shop by its actual merchant,
// then the current member and explicit STAFF scope. A dirty cross-merchant
// scope cannot bypass the shop-to-member merchant join.
func (access *Access) MembershipForShop(ctx context.Context, userID, shopID string) (Membership, error) {
	user, err := parseID(userID)
	if err != nil {
		return Membership{}, err
	}
	shop, err := parseID(shopID)
	if err != nil {
		return Membership{}, err
	}
	var rows []struct {
		ID         uint64
		MerchantID uint64
		RoleCode   Role
	}
	err = access.db.WithContext(ctx).Raw(`
		SELECT mm.id, mm.merchant_id, mm.role_code
		FROM tb_shop s
		JOIN tb_merchant m ON m.id = s.merchant_id AND m.status = 'ACTIVE'
		JOIN tb_merchant_member mm ON mm.merchant_id = s.merchant_id
		  AND mm.user_id = ? AND mm.status = 'ACTIVE'
		WHERE s.id = ? AND (mm.role_code IN ('OWNER', 'MANAGER') OR
		  (mm.role_code = 'STAFF' AND EXISTS (
		    SELECT 1 FROM tb_member_shop_scope sc
		    WHERE sc.member_id = mm.id AND sc.shop_id = s.id)))`, user, shop).Scan(&rows).Error
	if err != nil {
		return Membership{}, err
	}
	if len(rows) != 1 {
		return Membership{}, ErrForbidden
	}
	row := rows[0]
	return Membership{MemberID: decimal(row.ID), MerchantID: decimal(row.MerchantID), Role: row.RoleCode}, nil
}

func (access *Access) RequireManager(ctx context.Context, userID, shopID string) (Membership, error) {
	membership, err := access.MembershipForShop(ctx, userID, shopID)
	if err != nil {
		return Membership{}, err
	}
	if !membership.IsManager() {
		return Membership{}, ErrForbidden
	}
	return membership, nil
}

func (access *Access) VisibleShopIDs(ctx context.Context, userID string) ([]string, error) {
	return access.shopIDs(ctx, userID, false)
}

// ManagedShopIDs deliberately excludes STAFF scopes even if the same user
// owns a different merchant. Use this range for management-only resources.
func (access *Access) ManagedShopIDs(ctx context.Context, userID string) ([]string, error) {
	return access.shopIDs(ctx, userID, true)
}

func (access *Access) shopIDs(ctx context.Context, userID string, management bool) ([]string, error) {
	user, err := parseID(userID)
	if err != nil {
		return nil, err
	}
	where := "mm.role_code IN ('OWNER', 'MANAGER')"
	if !management {
		where += ` OR (mm.role_code = 'STAFF' AND EXISTS (
			SELECT 1 FROM tb_member_shop_scope sc
			WHERE sc.member_id = mm.id AND sc.shop_id = s.id))`
	}
	var rows []struct{ ID uint64 }
	err = access.db.WithContext(ctx).Raw(`
		SELECT s.id FROM tb_shop s
		JOIN tb_merchant m ON m.id = s.merchant_id AND m.status = 'ACTIVE'
		JOIN tb_merchant_member mm ON mm.merchant_id = s.merchant_id
		  AND mm.user_id = ? AND mm.status = 'ACTIVE'
		WHERE (`+where+`) ORDER BY s.id ASC`, user).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, decimal(row.ID))
	}
	return ids, nil
}
