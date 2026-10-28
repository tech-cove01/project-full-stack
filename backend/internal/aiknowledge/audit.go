package aiknowledge

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
)

func audit(ctx context.Context, tx *gorm.DB, user, document uint64, shopID, merchantID, action string, version int) error {
	detail, _ := json.Marshal(map[string]any{"version": version})
	return tx.WithContext(ctx).Exec(`INSERT INTO tb_audit_log(merchant_id,shop_id,operator_user_id,operator_type,action,resource_id,detail)
 VALUES(?,?,?,'MERCHANT',?,?,?)`, merchantID, shopID, user, action, decimal(document), string(detail)).Error
}
