package social

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type likeClampFixture struct {
	db                   *gorm.DB
	pool                 *sql.DB
	ctx                  context.Context
	author, reader, note uint64
	cleanupVerified      bool
}

func newLikeClampFixture(t *testing.T, cfg config.Config) *likeClampFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}),
		&gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("isolated MySQL unavailable")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(2)
	pool.SetMaxIdleConns(2)
	t.Cleanup(func() { _ = pool.Close() })
	f := &likeClampFixture{db: db, pool: pool, ctx: ctx}
	t.Cleanup(func() {
		if !f.cleanupVerified {
			t.Log("failed fixture retained; no SQL cleanup after unknown or invalid outcome")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		tx, err := pool.BeginTx(cleanup, nil)
		if err != nil {
			t.Error("exact clamp cleanup begin failed")
			return
		}
		defer tx.Rollback()
		first, second := likeEventID(f.note, f.reader, 1), likeEventID(f.note, f.reader, 2)
		steps := []struct {
			statement string
			args      []any
			rows      int64
		}{
			{"DELETE FROM tb_community_outbox WHERE event_id IN (?,?)", []any{first, second}, 2},
			{"DELETE FROM tb_blog_like WHERE blog_id=? AND user_id=?", []any{f.note, f.reader}, 1},
			{"DELETE FROM tb_blog WHERE id=? AND user_id=?", []any{f.note, f.author}, 1},
			{"DELETE FROM tb_user WHERE id IN (?,?)", []any{f.author, f.reader}, 2},
		}
		for _, step := range steps {
			result, err := tx.ExecContext(cleanup, step.statement, step.args...)
			if err != nil {
				t.Error("exact clamp cleanup statement failed")
				return
			}
			rows, err := result.RowsAffected()
			if err != nil || rows != step.rows {
				t.Error("exact clamp cleanup affected unexpected rows")
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Error("clamp cleanup commit uncertain; no retry")
			return
		}
		for _, table := range []string{"tb_user", "tb_blog", "tb_blog_like", "tb_community_outbox"} {
			var rows int
			if err := pool.QueryRowContext(cleanup, "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
				t.Error("clamp cleanup did not return the isolated fixture to empty")
			}
		}
	})
	for _, table := range []string{"tb_user", "tb_blog", "tb_blog_like", "tb_community_outbox"} {
		var rows int
		if err := pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("empty isolated fixture required; existing rows left untouched")
		}
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	user := func(prefix string) uint64 {
		var entropy [4]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			t.Fatal(err)
		}
		phone := prefix + fmt.Sprintf("%08d", binary.LittleEndian.Uint32(entropy[:])%100000000)
		result, err := tx.ExecContext(ctx, `INSERT INTO tb_user(phone,password,nick_name,icon) VALUES (?,'','like clamp integration','')`, phone)
		if err != nil {
			t.Fatal("owned clamp user seed failed")
		}
		id, err := result.LastInsertId()
		if err != nil || id <= 0 {
			t.Fatal("owned clamp user identity unavailable")
		}
		return uint64(id)
	}
	f.author, f.reader = user("155"), user("156")
	result, err := tx.ExecContext(ctx, `INSERT INTO tb_blog(shop_id,user_id,title,images,content,liked,status,publish_version)
		VALUES (0,?,'like clamp integration','','owned fixture',0,'PUBLISHED',1)`, f.author)
	if err != nil {
		t.Fatal("owned clamp note seed failed")
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		t.Fatal("owned clamp note identity unavailable")
	}
	f.note = uint64(id)
	// Register exact identities before seed commit, including uncertain outcomes.
	t.Logf("owned clamp fixture author=%d reader=%d note=%d interpolation=%t", f.author, f.reader, f.note, cfg.MySQL.InterpolateParams)
	if err := tx.Commit(); err != nil {
		t.Fatal("owned clamp seed commit uncertain; identities retained")
	}
	return f
}

func (f *likeClampFixture) assertFacts(t *testing.T, active, version int, likes int64, eventCount int) {
	t.Helper()
	var gotActive, gotVersion int
	var gotLikes int64
	if err := f.pool.QueryRowContext(f.ctx, `SELECT l.active,l.version,b.liked FROM tb_blog b
		JOIN tb_blog_like l ON l.blog_id=b.id AND l.user_id=? WHERE b.id=?`, f.reader, f.note).
		Scan(&gotActive, &gotVersion, &gotLikes); err != nil || gotActive != active || gotVersion != version || gotLikes != likes {
		t.Fatalf("committed clamp facts differ: active=%d version=%d likes=%d error=%v", gotActive, gotVersion, gotLikes, err)
	}
	rows, err := f.pool.QueryContext(f.ctx, `SELECT event_id,aggregate_type,aggregate_version,event_type,CAST(payload AS CHAR),delivery_status,attempts
		FROM tb_community_outbox WHERE aggregate_id=? ORDER BY aggregate_version`, strconv.FormatUint(f.note, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var eventID, aggregate, eventType, body, delivery string
		var eventVersion, attempts int
		if err := rows.Scan(&eventID, &aggregate, &eventVersion, &eventType, &body, &delivery, &attempts); err != nil {
			t.Fatal(err)
		}
		seen++
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		var noteID, userID string
		if len(payload) != 4 || json.Unmarshal(payload["blogId"], &noteID) != nil || json.Unmarshal(payload["userId"], &userID) != nil ||
			noteID != strconv.FormatUint(f.note, 10) || userID != strconv.FormatUint(f.reader, 10) ||
			string(payload["version"]) != strconv.Itoa(seen) || string(payload["liked"]) != strconv.FormatBool(seen == 1) ||
			eventID != likeEventID(f.note, f.reader, seen) || aggregate != "BLOG" || eventVersion != seen || eventType != "LIKE_CHANGED" || delivery != "PENDING" || attempts != 0 {
			t.Fatal("committed clamp Outbox event differs")
		}
	}
	if err := rows.Err(); err != nil || seen != eventCount {
		t.Fatalf("clamp Outbox count differs: got=%d expected=%d error=%v", seen, eventCount, err)
	}
}

func TestIntegrationLikeCounterClamp(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("registered isolated MySQL required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	if !regexp.MustCompile(`^cap10_relay_it_[a-f0-9]{12}$`).MatchString(cfg.MySQL.Database) {
		t.Skip("independent CAP10 fixture required before connecting")
	}
	for _, interpolate := range []bool{false, true} {
		if !t.Run(strconv.FormatBool(interpolate), func(t *testing.T) {
			local := cfg
			local.MySQL.InterpolateParams = interpolate
			f := newLikeClampFixture(t, local)
			service := NewService(f.db)
			reader, note := strconv.FormatUint(f.reader, 10), strconv.FormatUint(f.note, 10)
			state, err := service.SetLike(f.ctx, reader, note, true)
			if err != nil || !state.Liked || state.Version != 1 || state.Likes != 1 {
				t.Fatalf("first committed like failed: state=%+v error=%v", state, err)
			}
			f.assertFacts(t, 1, 1, 1, 1)
			// Controlled drift belongs only to this note; no event or relation is changed.
			drift := f.db.WithContext(f.ctx).Exec("UPDATE tb_blog SET liked=0 WHERE id=?", f.note)
			if drift.Error != nil || drift.RowsAffected != 1 {
				t.Fatal("controlled owned counter drift failed")
			}
			f.assertFacts(t, 1, 1, 0, 1)
			state, err = service.SetLike(f.ctx, reader, note, false)
			if err != nil || state.Liked || state.Version != 2 || state.Likes != 0 {
				t.Fatalf("unlike must commit while counter stays zero: state=%+v error=%v", state, err)
			}
			f.assertFacts(t, 0, 2, 0, 2)
			state, err = service.SetLike(f.ctx, reader, note, false)
			if err != nil || state.Liked || state.Version != 2 || state.Likes != 0 {
				t.Fatalf("repeated unlike must stay a noop: state=%+v error=%v", state, err)
			}
			f.assertFacts(t, 0, 2, 0, 2)
			f.cleanupVerified = true
		}) {
			return // Preserve the first failed mode; no automatic second attempt.
		}
	}
}
