package communityprojection

import (
	"bytes"
	"context"
	"database/sql"
	"strings"

	"gorm.io/gorm"
)

// ApplyIgnoredLikes keeps every original source/envelope check and the rebuild
// fence, but amortizes ignored LIKE receipt writes across one durable DB commit.
// It does not apply read-model effects or accept another event kind.
func (runtime *Runtime) ApplyIgnoredLikes(ctx context.Context, projection string, messages []Message) error {
	if len(messages) < 1 || len(messages) > 64 || projection != Follow && projection != Feed {
		return ErrInvalid
	}
	events := make([]Event, 0, len(messages))
	expected := make(map[string][32]byte, len(messages))
	for _, message := range messages {
		if !validCoordinates(projection, message) || len(message.Key) > 1024 || len(message.Body) > 1<<20 || len(message.Headers) > 32 {
			return ErrInvalid
		}
		event, err := decodeCheckedEvent(message)
		if err != nil {
			return err
		}
		if event.EventType != "LIKE_CHANGED" {
			return ErrInvalid
		}
		if previous, exists := expected[event.EventID]; exists {
			if previous != event.Hash {
				return ErrConflict
			}
			continue
		}
		expected[event.EventID] = event.Hash
		events = append(events, event)
	}
	if runtime == nil || runtime.Repository == nil || runtime.db == nil {
		return ErrFactUnavailable
	}
	return runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var control struct{ Projection string }
		if err := tx.Raw(`SELECT projection FROM tb_community_projection_control
 WHERE projection=? FOR SHARE`, projection).Scan(&control).Error; err != nil {
			return err
		}
		if control.Projection != projection {
			return ErrFactUnavailable
		}
		placeholders := make([]string, len(events))
		args := make([]any, 0, len(events)*4)
		ids := make([]string, 0, len(events))
		for index, event := range events {
			placeholders[index] = "(?,?,?,?)"
			args = append(args, projection, event.EventID, event.Hash[:], "IGNORED")
			ids = append(ids, event.EventID)
		}
		if err := tx.Exec(`INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state) VALUES `+
			strings.Join(placeholders, ",")+` ON DUPLICATE KEY UPDATE event_id=event_id`, args...).Error; err != nil {
			return err
		}
		var rows []struct {
			EventID   string
			EventHash []byte
			State     string
		}
		if err := tx.Raw(`SELECT event_id,event_hash,state FROM tb_community_projection_event
 WHERE projection=? AND event_id IN ? FOR UPDATE`, projection, ids).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(expected) {
			return ErrConflict
		}
		for _, row := range rows {
			hash, exists := expected[row.EventID]
			if !exists || row.State != "IGNORED" || !bytes.Equal(row.EventHash, hash[:]) {
				return ErrConflict
			}
			delete(expected, row.EventID)
		}
		if len(expected) != 0 {
			return ErrConflict
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
