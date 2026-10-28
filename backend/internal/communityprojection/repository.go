package communityprojection

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"time"

	"gorm.io/gorm"
)

type Repository struct{ db *gorm.DB }

func New(db *gorm.DB) *Repository { return &Repository{db: db} }

// Apply commits the dedup receipt and the read-model change together. Errors
// never leave an APPLIED receipt for an absent effect; the caller must not
// commit a Kafka offset until success or a separately durable park operation.
func (repository *Repository) Apply(ctx context.Context, projection string, body []byte) error {
	if projection != Follow && projection != Feed {
		return ErrInvalid
	}
	event, err := Decode(body)
	if err != nil {
		return err
	}
	needed := (projection == Follow && event.EventType == "FOLLOW_CHANGED") || (projection == Feed && event.EventType == "NOTE_PUBLISHED")
	state := "IGNORED"
	if needed {
		state = "APPLIED"
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Rebuild holds this row FOR UPDATE while constructing/replacing the
		// group's read model. Apply never interleaves an incremental effect with
		// that snapshot or its atomic publication boundary.
		var control struct{ Projection string }
		if err := tx.Raw(`SELECT projection FROM tb_community_projection_control
 WHERE projection=? FOR SHARE`, projection).Scan(&control).Error; err != nil {
			return err
		}
		if control.Projection != projection {
			return ErrFactUnavailable
		}
		// This table has exactly the (projection,event_id) primary key. An exact
		// no-op duplicate UPDATE takes its exclusive record lock immediately;
		// INSERT -> duplicate error -> FOR UPDATE would upgrade shared duplicate
		// locks concurrently and can deadlock even for an already applied event.
		insert := tx.Exec(`INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state)
 VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE event_id=event_id`, projection, event.EventID, event.Hash[:], state)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			var existing struct {
				EventHash []byte
				State     string
			}
			if err := tx.Raw(`SELECT event_hash,state FROM tb_community_projection_event
 WHERE projection=? AND event_id=? FOR UPDATE`, projection, event.EventID).Scan(&existing).Error; err != nil {
				return err
			}
			if !bytes.Equal(existing.EventHash, event.Hash[:]) || existing.State != state {
				return ErrConflict
			}
			return nil
		}
		if insert.RowsAffected != 1 {
			return ErrConflict
		}
		if !needed {
			return nil
		}
		if projection == Follow {
			return applyFollow(ctx, tx, event)
		}
		return applyNote(ctx, tx, event)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func applyFollow(ctx context.Context, tx *gorm.DB, event Event) error {
	user, _ := strconv.ParseUint(event.UserID, 10, 64)
	target, _ := strconv.ParseUint(event.TargetID, 10, 64)
	var empty [32]byte
	// Materializing version zero serializes first application under RC; a plain
	// SELECT of an absent relation would leave concurrent inserts unprotected.
	if err := tx.WithContext(ctx).Exec(`INSERT INTO tb_community_follow_projection
 (user_id,target_id,active,version,event_hash,last_event_id) VALUES(?,?,0,0,?,'')
 ON DUPLICATE KEY UPDATE user_id=user_id`, user, target, empty[:]).Error; err != nil {
		return err
	}
	var row struct {
		Active      int
		Version     int
		EventHash   []byte
		LastEventID string
	}
	if err := tx.Raw(`SELECT active,version,event_hash,last_event_id FROM tb_community_follow_projection
 WHERE user_id=? AND target_id=? FOR UPDATE`, user, target).Scan(&row).Error; err != nil {
		return err
	}
	if event.Version < row.Version {
		return nil
	}
	if event.Version == row.Version {
		if (row.Active == 1) != event.Active {
			return ErrConflict
		}
		if row.LastEventID == "" && bytes.Equal(row.EventHash, empty[:]) {
			return tx.Exec(`UPDATE tb_community_follow_projection SET event_hash=?,last_event_id=?,updated_at=NOW(3)
 WHERE user_id=? AND target_id=?`, event.Hash[:], event.EventID, user, target).Error
		}
		if !bytes.Equal(row.EventHash, event.Hash[:]) {
			return ErrConflict
		}
		return nil
	}
	active := 0
	if event.Active {
		active = 1
	}
	result := tx.Exec(`UPDATE tb_community_follow_projection SET active=?,version=?,event_hash=?,last_event_id=?,updated_at=NOW(3)
 WHERE user_id=? AND target_id=? AND version=?`, active, event.Version, event.Hash[:], event.EventID, user, target, row.Version)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func applyNote(ctx context.Context, tx *gorm.DB, event Event) error {
	note, _ := strconv.ParseUint(event.NoteID, 10, 64)
	author, _ := strconv.ParseUint(event.AuthorID, 10, 64)
	var fact struct {
		ID             uint64
		UserID         uint64
		Status         string
		PublishVersion int
		CreateTime     time.Time
	}
	if err := tx.WithContext(ctx).Raw(`SELECT id,user_id,status,publish_version,create_time
 FROM tb_blog WHERE id=? FOR SHARE`, note).Scan(&fact).Error; err != nil {
		return err
	}
	if fact.ID == 0 || fact.UserID != author || fact.Status != "PUBLISHED" || fact.PublishVersion < event.Version || fact.CreateTime.IsZero() {
		return ErrFactUnavailable
	}
	var empty [32]byte
	if err := tx.Exec(`INSERT INTO tb_community_note_projection
 (note_id,author_id,version,event_hash,last_event_id,published_at) VALUES(?,?,0,?,'',?)
 ON DUPLICATE KEY UPDATE note_id=note_id`, note, author, empty[:], fact.CreateTime).Error; err != nil {
		return err
	}
	var row struct {
		AuthorID    uint64
		Version     int
		EventHash   []byte
		PublishedAt time.Time
		LastEventID string
	}
	if err := tx.Raw(`SELECT author_id,version,event_hash,published_at,last_event_id FROM tb_community_note_projection
 WHERE note_id=? FOR UPDATE`, note).Scan(&row).Error; err != nil {
		return err
	}
	if row.AuthorID != author {
		return ErrConflict
	}
	if event.Version < row.Version {
		return nil
	}
	if event.Version == row.Version {
		if !row.PublishedAt.Equal(fact.CreateTime) {
			return ErrConflict
		}
		if row.LastEventID == "" && bytes.Equal(row.EventHash, empty[:]) {
			return tx.Exec(`UPDATE tb_community_note_projection SET event_hash=?,last_event_id=?,updated_at=NOW(3)
 WHERE note_id=?`, event.Hash[:], event.EventID, note).Error
		}
		if !bytes.Equal(row.EventHash, event.Hash[:]) {
			return ErrConflict
		}
		return nil
	}
	result := tx.Exec(`UPDATE tb_community_note_projection SET author_id=?,version=?,event_hash=?,last_event_id=?,published_at=?,updated_at=NOW(3)
 WHERE note_id=? AND version=?`, author, event.Version, event.Hash[:], event.EventID, fact.CreateTime, note, row.Version)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
