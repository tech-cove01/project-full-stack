package notesearch

import (
	"bytes"
	"context"
	"database/sql"
	"strings"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"gorm.io/gorm"
)

// ApplyIgnoredLikes records only LIKE_CHANGED receipts in one SQL transaction.
// The search-control lock remains the rebuild barrier; no ES effect is skipped
// or batched here. Existing receipts must agree exactly before commit succeeds.
func (sync *Sync) ApplyIgnoredLikes(ctx context.Context, projection string, messages []communityprojection.Message) error {
	if len(messages) < 1 || len(messages) > 64 {
		return ErrSyncInvalid
	}
	events := make([]communityprojection.Event, 0, len(messages))
	byID := make(map[string][32]byte, len(messages))
	for _, message := range messages {
		event, err := decodeSearchMessage(projection, message)
		if err != nil || event.EventType != "LIKE_CHANGED" {
			return ErrSyncInvalid
		}
		if previous, exists := byID[event.EventID]; exists {
			if previous != event.Hash {
				return ErrSyncConflict
			}
			continue
		}
		byID[event.EventID] = event.Hash
		events = append(events, event)
	}
	if sync.db == nil || sync.client == nil {
		return ErrNeedsRebuild
	}
	return sync.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockSearchControl(tx); err != nil {
			return err
		}
		ids := make([]string, 0, len(events))
		arguments := make([]any, 0, len(events)*3)
		for _, event := range events {
			ids = append(ids, event.EventID)
			arguments = append(arguments, Projection, event.EventID, event.Hash[:])
		}
		query := `INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state) VALUES ` +
			strings.TrimSuffix(strings.Repeat("(?,?,?,'IGNORED'),", len(events)), ",") +
			` ON DUPLICATE KEY UPDATE event_id=event_id`
		if err := tx.Exec(query, arguments...).Error; err != nil {
			return err
		}
		var receipts []struct {
			EventID   string
			EventHash []byte
			State     string
		}
		if err := tx.Raw(`SELECT event_id,event_hash,state FROM tb_community_projection_event
			WHERE projection=? AND event_id IN ? FOR UPDATE`, Projection, ids).Scan(&receipts).Error; err != nil {
			return err
		}
		if len(receipts) != len(events) {
			return ErrSyncConflict
		}
		seen := make(map[string]bool, len(receipts))
		for _, receipt := range receipts {
			expected, exists := byID[receipt.EventID]
			if !exists || seen[receipt.EventID] || receipt.State != "IGNORED" || !bytes.Equal(receipt.EventHash, expected[:]) {
				return ErrSyncConflict
			}
			seen[receipt.EventID] = true
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
