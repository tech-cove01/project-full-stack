// Package social owns community relations and the follow feed. MySQL is the
// fact source; Redis counters and projections are rebuilt from it, never the
// other way round.
package social

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/communitycache"
)

var (
	ErrInput      = errors.New("作者ID或关注状态不合法")
	ErrNotFound   = errors.New("用户不存在")
	ErrSelf       = errors.New("不能关注自己")
	ErrUnsafe     = errors.New("关注状态需要核对，请稍后重试")
	ErrPagination = errors.New("游标或分页参数不合法")
)

type Relationship struct {
	AuthorID      string `json:"authorId"`
	Following     bool   `json:"following"`
	Version       int    `json:"version"`
	FollowerCount int64  `json:"followerCount"`
}

type Service struct {
	db     *gorm.DB
	counts *communitycache.Cache
	likes  *communitycache.Likes
}

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// WithCounts attaches the page-card counter cache. Without it every read goes to
// MySQL, which keeps unit tests Redis-free.
func (service *Service) WithCounts(cache *communitycache.Cache) *Service {
	service.counts = cache
	return service
}

// WithLikes attaches the per-note like projection. It is a projection: a failure
// leaves MySQL authoritative and is fixed by the recount command.
func (service *Service) WithLikes(likes *communitycache.Likes) *Service {
	service.likes = likes
	return service
}

func positive(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != value {
		return 0, ErrInput
	}
	return parsed, nil
}

// enqueue writes to the community outbox. INSERT IGNORE makes a repeated write of
// the same version idempotent; a different version always gets its own row.
func enqueue(ctx context.Context, tx *gorm.DB, eventID, aggregate, aggregateID string,
	version int, eventType string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec(`INSERT IGNORE INTO tb_community_outbox
		(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
		VALUES (?,?,?,?,?,?)`, eventID, aggregate, aggregateID, version, eventType, string(body)).Error
}

// SetFollow is an expected-state write: repeating the current state changes
// nothing, while a real change bumps the version and emits one event in the same
// transaction as the fact.
func (service *Service) SetFollow(ctx context.Context, userID, authorID string, follow bool) (Relationship, error) {
	user, err := positive(userID)
	if err != nil {
		return Relationship{}, err
	}
	author, err := positive(authorID)
	if err != nil {
		return Relationship{}, err
	}
	if user == author {
		return Relationship{}, ErrSelf
	}
	var existing struct{ ID uint64 }
	if err = service.db.WithContext(ctx).Raw("SELECT id FROM tb_user WHERE id=?", author).Scan(&existing).Error; err != nil {
		return Relationship{}, err
	}
	if existing.ID == 0 {
		return Relationship{}, ErrNotFound
	}
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A missing row has no record lock under READ COMMITTED. Materialize an
		// inactive version-zero pair first: the unique key serializes simultaneous
		// first follows without turning harmless repeats into duplicate-key 500s.
		if err := tx.Exec(`INSERT INTO tb_follow(user_id,follow_user_id,active,version)
			VALUES (?,?,0,0) ON DUPLICATE KEY UPDATE id=id`, user, author).Error; err != nil {
			return err
		}
		var current struct {
			ID      uint64
			Active  int
			Version int
		}
		if err := tx.WithContext(ctx).Raw(`SELECT id,active,version FROM tb_follow
			WHERE user_id=? AND follow_user_id=? FOR UPDATE`, user, author).Scan(&current).Error; err != nil {
			return err
		}
		wanted := 0
		if follow {
			wanted = 1
		}
		if current.ID == 0 {
			return ErrUnsafe
		}
		if current.Active == wanted {
			// Repeating the current state must not bump the version, otherwise the
			// projection would apply a no-op event on every refresh.
			return nil
		}
		version := current.Version + 1
		result := tx.WithContext(ctx).Exec(`UPDATE tb_follow SET active=?,version=?
			WHERE id=? AND version=?`, wanted, version, current.ID, current.Version)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUnsafe
		}
		return enqueue(ctx, tx, eventID(user, author, version), "FOLLOW",
			strconv.FormatUint(author, 10), version, "FOLLOW_CHANGED",
			map[string]any{"userId": strconv.FormatUint(user, 10),
				"followUserId": strconv.FormatUint(author, 10),
				"active":       strconv.FormatBool(follow), "version": version})
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Relationship{}, err
	}
	return service.Relationship(ctx, userID, authorID)
}

func eventID(user, author uint64, version int) string {
	return "FOLLOW:" + strconv.FormatUint(user, 10) + ":" + strconv.FormatUint(author, 10) +
		":" + strconv.Itoa(version)
}

// Relationship reports the caller's own relation plus the public follower count.
func (service *Service) Relationship(ctx context.Context, userID, authorID string) (Relationship, error) {
	user, err := positive(userID)
	if err != nil {
		return Relationship{}, err
	}
	author, err := positive(authorID)
	if err != nil {
		return Relationship{}, err
	}
	var row struct {
		ID      uint64
		Active  int
		Version int
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT id,active,version FROM tb_follow
		WHERE user_id=? AND follow_user_id=?`, user, author).Scan(&row).Error; err != nil {
		return Relationship{}, err
	}
	var count int64
	if err = service.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_follow
		WHERE follow_user_id=? AND active=1`, author).Scan(&count).Error; err != nil {
		return Relationship{}, err
	}
	return Relationship{AuthorID: strconv.FormatUint(author, 10), Following: row.Active == 1,
		Version: row.Version, FollowerCount: count}, nil
}
