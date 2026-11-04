package social

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

// ErrNoteNotFound also covers a note that is not published: a draft and a
// missing row must be indistinguishable to a like caller.
var ErrNoteNotFound = errors.New("笔记不存在")

type LikeState struct {
	NoteID  string `json:"noteId"`
	Liked   bool   `json:"liked"`
	Version int    `json:"version"`
	Likes   int64  `json:"likes"`
}

// SetLike is an expected-state write. The note row is locked first, which
// serializes the counter update with concurrent likes on the same note; repeating
// the current state changes nothing and emits no event.
func (service *Service) SetLike(ctx context.Context, userID, noteID string, liked bool) (result LikeState, returnedErr error) {
	ctx, finish := taildiag.StartLike(ctx, service.likeObservations, false)
	span := taildiag.Begin(ctx, taildiag.LikeService, 0)
	defer func() {
		span.End(returnedErr != nil)
		finish(returnedErr != nil)
	}()
	user, err := positive(userID)
	if err != nil {
		return LikeState{}, err
	}
	note, err := positive(noteID)
	if err != nil {
		return LikeState{}, err
	}
	transactionSpan := taildiag.Begin(ctx, taildiag.LikeTransaction, 0)
	defer transactionSpan.End(true)
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var blog struct {
			ID     uint64
			UserID uint64
			Status string
		}
		if err := tx.WithContext(ctx).Raw(`SELECT id,user_id,status FROM tb_blog
			WHERE id=? FOR UPDATE`, note).Scan(&blog).Error; err != nil {
			return err
		}
		if blog.ID == 0 || blog.Status != "PUBLISHED" {
			return ErrNoteNotFound
		}
		var existing struct {
			ID      uint64
			Active  int
			Version int
		}
		if err := tx.WithContext(ctx).Raw(`SELECT id,active,version FROM tb_blog_like
			WHERE blog_id=? AND user_id=? FOR UPDATE`, note, user).Scan(&existing).Error; err != nil {
			return err
		}
		wanted := 0
		if liked {
			wanted = 1
		}
		if existing.ID == 0 {
			if wanted == 0 {
				return nil
			}
			if err := tx.WithContext(ctx).Exec(`INSERT INTO tb_blog_like
				(blog_id,user_id,active,version) VALUES (?,?,?,1)`, note, user, wanted).Error; err != nil {
				return err
			}
			if wanted == 1 {
				if err := tx.WithContext(ctx).Exec(
					`UPDATE tb_blog SET liked=liked+1 WHERE id=?`, note).Error; err != nil {
					return err
				}
			}
			return enqueue(ctx, tx, likeEventID(note, user, 1), "BLOG",
				strconv.FormatUint(note, 10), 1, "LIKE_CHANGED",
				map[string]any{"blogId": strconv.FormatUint(note, 10),
					"userId": strconv.FormatUint(user, 10),
					"liked":  liked, "version": 1})
		}
		if existing.Active == wanted {
			return nil
		}
		version := existing.Version + 1
		result := tx.WithContext(ctx).Exec(`UPDATE tb_blog_like SET active=?,version=?
			WHERE id=? AND version=?`, wanted, version, existing.ID, existing.Version)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUnsafe
		}
		var counter *gorm.DB
		if wanted == 0 {
			// liked is unsigned: protect its input before subtraction. An outer
			// GREATEST cannot catch underflow from evaluating liked + (-1).
			counter = tx.WithContext(ctx).Exec(`UPDATE tb_blog SET liked=GREATEST(liked,1)-1 WHERE id=?`, note)
		} else {
			counter = tx.WithContext(ctx).Exec(`UPDATE tb_blog SET liked=GREATEST(0,liked+?) WHERE id=?`, 1, note)
		}
		if counter.Error != nil {
			return counter.Error
		}
		return enqueue(ctx, tx, likeEventID(note, user, version), "BLOG",
			strconv.FormatUint(note, 10), version, "LIKE_CHANGED",
			map[string]any{"blogId": strconv.FormatUint(note, 10),
				"userId": strconv.FormatUint(user, 10),
				"liked":  liked, "version": version})
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	transactionSpan.End(err != nil)
	if err != nil {
		return LikeState{}, err
	}
	// The transaction has committed. Finish its derived updates even when the
	// caller disconnects, but keep that synchronous work bounded. A commit error
	// still returns above: an ambiguous database acknowledgement needs reconciliation.
	postCommitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	// The counter changed, so the cached copy must go; the next read rebuilds it.
	if service.counts != nil {
		invalidateSpan := taildiag.Begin(postCommitCtx, taildiag.LikeInvalidate, 0)
		defer invalidateSpan.End(true)
		invalidateErr := service.counts.Invalidate(postCommitCtx, note)
		invalidateSpan.End(invalidateErr != nil)
	}
	stateSpan := taildiag.Begin(postCommitCtx, taildiag.LikeStateRead, 0)
	defer stateSpan.End(true)
	state, err := service.LikeState(postCommitCtx, userID, noteID)
	stateSpan.End(err != nil)
	if err != nil {
		return LikeState{}, err
	}
	// Read the current committed relation, not the original requested state. The
	// projection version fences a delayed completion behind a later unlike.
	if service.likes != nil && state.Version > 0 {
		markSpan := taildiag.Begin(postCommitCtx, taildiag.LikeMark, 0)
		defer markSpan.End(true)
		_, markErr := service.likes.Mark(postCommitCtx, note, user, state.Liked, state.Version)
		markSpan.End(markErr != nil)
		if markErr != nil {
			// The projection is repairable, but the failure must be visible.
			slog.Warn("like bitmap update failed", "note_id", note, "user_id", user, "error", markErr)
		}
	}
	return state, nil
}

func likeEventID(note, user uint64, version int) string {
	return "LIKE:" + strconv.FormatUint(note, 10) + ":" + strconv.FormatUint(user, 10) +
		":" + strconv.Itoa(version)
}

// LikeState returns the caller's own like and the public counter. A missing like
// row is reported as "not liked, version 0" instead of an error.
func (service *Service) LikeState(ctx context.Context, userID, noteID string) (LikeState, error) {
	user, err := positive(userID)
	if err != nil {
		return LikeState{}, err
	}
	note, err := positive(noteID)
	if err != nil {
		return LikeState{}, err
	}
	var row struct {
		Liked   int64
		Active  int
		Version int
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT b.liked, COALESCE(l.active,0) AS active,
		COALESCE(l.version,0) AS version
		FROM tb_blog b LEFT JOIN tb_blog_like l ON l.blog_id=b.id AND l.user_id=?
		WHERE b.id=? AND b.status='PUBLISHED'`, user, note).Scan(&row).Error; err != nil {
		return LikeState{}, err
	}
	if row.Liked == 0 && row.Active == 0 && row.Version == 0 {
		// Distinguish "no likes yet" from "no such note" with one cheap existence check.
		var exists struct{ ID uint64 }
		if err = service.db.WithContext(ctx).Raw(`SELECT id FROM tb_blog
			WHERE id=? AND status='PUBLISHED'`, note).Scan(&exists).Error; err != nil {
			return LikeState{}, err
		}
		if exists.ID == 0 {
			return LikeState{}, ErrNoteNotFound
		}
	}
	state := LikeState{NoteID: strconv.FormatUint(note, 10), Liked: row.Active == 1,
		Version: row.Version, Likes: row.Liked}
	// This query already loaded the authoritative public count. A recently reset
	// set may contain only the newest changed user, so never replace the fact with
	// a partial or stale projection. Feed caching remains independently bounded.
	return state, nil
}
