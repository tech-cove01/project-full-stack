// Package orderreview stores accepted orders whose database outcome could not be
// confirmed. It is the only reason the Stream consumer may acknowledge a message
// without a committed order: the row is durable, it blocks rebuilds, and it is
// the input of the controlled replay command.
package orderreview

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

var ErrNotDurable = errors.New("order review record could not be written")

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// Record inserts or refreshes the OPEN row for an order. Repeated failures keep
// one row; a replayed message that fails again returns REQUEUED to OPEN so it can
// be replayed again. An error means the outcome is still not durable.
func (repository *Repository) Record(ctx context.Context, orderID, userID, voucherID, messageID string,
	attempts int, cause error) error {
	reason := "unknown outcome"
	if cause != nil && cause.Error() != "" {
		reason = cause.Error()
	}
	if runes := []rune(reason); len(runes) > 240 {
		reason = string(runes[:240])
	}
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing struct{ UserID, VoucherID string }
		if err := tx.Raw("SELECT user_id,voucher_id FROM tb_order_review WHERE order_id=? FOR UPDATE", orderID).Scan(&existing).Error; err != nil {
			return err
		}
		if existing.UserID != "" && (existing.UserID != userID || existing.VoucherID != voucherID) {
			return ErrNotDurable
		}
		return tx.Exec(`INSERT INTO tb_order_review
		(order_id, voucher_id, user_id, stream_message_id, attempts, reason, status)
		VALUES (?, ?, ?, ?, ?, ?, 'OPEN')
		ON DUPLICATE KEY UPDATE
			stream_message_id = VALUES(stream_message_id),
			attempts = GREATEST(attempts, VALUES(attempts)),
			reason = VALUES(reason),
			status = IF(status = 'REQUEUED', 'OPEN', status),
			updated_at = NOW(3)`,
			orderID, voucherID, userID, messageID, attempts, reason).Error
	})
	if err != nil {
		return ErrNotDurable
	}
	return nil
}

// Resolve closes the row once the order exists or was definitively rejected.
func (repository *Repository) Resolve(ctx context.Context, orderID string) error {
	return repository.db.WithContext(ctx).Exec(`UPDATE tb_order_review
		SET status = 'RESOLVED', resolved_at = NOW(3), updated_at = NOW(3)
		WHERE order_id = ? AND status <> 'RESOLVED'`, orderID).Error
}

// Open reports whether the activity still has unresolved or replay-claimed rows.
func (repository *Repository) Open(ctx context.Context, voucherID string) (bool, error) {
	var count uint64
	err := repository.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_order_review
		WHERE voucher_id = ? AND status IN ('OPEN', 'REQUEUED')`, voucherID).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Claim marks a review replay before publishing. REQUEUED remains replayable:
// a previous command may have exited after this commit but before XADD, or lost
// its XADD reply. Repeating the same IDs is safe because creation is idempotent.
// RESOLVED is never replayable. The row stays a rebuild blocker until resolved.
func (repository *Repository) Claim(ctx context.Context, orderID string) (bool, error) {
	claimed := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row struct{ Status string }
		if err := tx.Raw("SELECT status FROM tb_order_review WHERE order_id=? FOR UPDATE", orderID).Scan(&row).Error; err != nil {
			return err
		}
		if row.Status != "OPEN" && row.Status != "REQUEUED" {
			return nil
		}
		if err := tx.Exec("UPDATE tb_order_review SET status='REQUEUED',resolved_at=NULL,updated_at=NOW(3) WHERE order_id=?", orderID).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return claimed, err
}

// Release puts a claimed row back to OPEN when publishing the message failed.
func (repository *Repository) Release(ctx context.Context, orderID string) error {
	return repository.db.WithContext(ctx).Exec(`UPDATE tb_order_review
		SET status = 'OPEN', updated_at = NOW(3)
		WHERE order_id = ? AND status = 'REQUEUED'`, orderID).Error
}

type Row struct {
	OrderID   string
	VoucherID string
	UserID    string
	Attempts  int
	Status    string
	Reason    string
}

// List returns recent rows for the operator command.
func (repository *Repository) List(ctx context.Context, limit int) ([]Row, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows := make([]Row, 0)
	err := repository.db.WithContext(ctx).Raw(`SELECT order_id, voucher_id, user_id, attempts, status, reason
		FROM tb_order_review ORDER BY created_at DESC LIMIT ?`, limit).Scan(&rows).Error
	return rows, err
}

// Load reads one row so a replay can rebuild the original stream message.
func (repository *Repository) Load(ctx context.Context, orderID string) (Row, bool, error) {
	var rows []Row
	if err := repository.db.WithContext(ctx).Raw(`SELECT order_id, voucher_id, user_id, attempts, status, reason
		FROM tb_order_review WHERE order_id = ?`, orderID).Scan(&rows).Error; err != nil {
		return Row{}, false, err
	}
	if len(rows) != 1 {
		return Row{}, false, nil
	}
	return rows[0], true, nil
}
