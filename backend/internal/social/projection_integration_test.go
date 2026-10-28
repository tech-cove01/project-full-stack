package social

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/note"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// These are read-path tests against the real schema and Repository.Apply, not
// Kafka-delivery evidence. Only owned notes/relations are altered to make stale,
// partial and unavailable read models deterministic; no shared table is renamed.
func TestIntegrationCommunityProjectionQueries(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated MySQL and Redis required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	connection, err := r.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("integration lock connection unavailable")
	}
	t.Cleanup(func() { _ = connection.Close() })
	var locked int
	if err = connection.QueryRowContext(ctx, "SELECT GET_LOCK('go072_integration',30)").Scan(&locked); err != nil || locked != 1 {
		t.Fatal("GO-072 integration lock unavailable")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var released int
		if connection.QueryRowContext(cleanupCtx, "SELECT RELEASE_LOCK('go072_integration')").Scan(&released) != nil || released != 1 {
			t.Error("integration lock release failed")
		}
	})
	reader, author, secondAuthor, otherReader, emptyAuthor := insertUser(t, r, "130"), insertUser(t, r, "131"),
		insertUser(t, r, "132"), insertUser(t, r, "133"), insertUser(t, r, "134")
	users := []uint64{reader, author, secondAuthor, otherReader, emptyAuthor}
	userID, authorID := strconv.FormatUint(reader, 10), strconv.FormatUint(author, 10)
	base := NewService(r.ORM)
	service := NewService(r.ORM).WithProjections()
	repository := communityprojection.New(r.ORM)
	ownedNotes := make([]uint64, 0)
	ownedEvents := make(map[string]bool)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// All targets/authors belong to these five fixture users. Record precise
		// event IDs before removing their outbox producers and dedup receipts.
		var events []string
		if r.ORM.WithContext(cleanupCtx).Raw(`SELECT event_id FROM tb_community_outbox
			WHERE (aggregate_type='FOLLOW' AND aggregate_id IN ?)
			OR (aggregate_type='BLOG' AND aggregate_id IN ?)`, idStrings(users), idStrings(ownedNotes)).Scan(&events).Error != nil {
			t.Error("owned event lookup failed during cleanup")
			return
		}
		for _, id := range events {
			ownedEvents[id] = true
		}
		for id := range ownedEvents {
			if r.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_community_projection_event WHERE event_id=?", id).Error != nil {
				t.Error("owned projection receipt cleanup failed")
			}
		}
		for _, command := range []struct {
			query string
			args  []any
		}{
			{"DELETE FROM tb_community_outbox WHERE event_id IN ?", []any{events}},
			{"DELETE FROM tb_community_follow_projection WHERE user_id IN ? AND target_id IN ?", []any{users, users}},
			{"DELETE FROM tb_community_note_projection WHERE note_id IN ?", []any{ownedNotes}},
			{"DELETE FROM tb_blog_like WHERE blog_id IN ?", []any{ownedNotes}},
			{"DELETE FROM tb_follow WHERE user_id IN ? AND follow_user_id IN ?", []any{users, users}},
			{"DELETE FROM tb_blog WHERE id IN ? AND user_id IN ?", []any{ownedNotes, users}},
			{"DELETE FROM tb_user WHERE id IN ?", []any{users}},
		} {
			if r.ORM.WithContext(cleanupCtx).Exec(command.query, command.args...).Error != nil {
				t.Error("owned projection query fixture cleanup failed")
			}
		}
		var remaining int64
		if r.ORM.WithContext(cleanupCtx).Raw(`SELECT
			(SELECT COUNT(*) FROM tb_follow WHERE user_id IN ? AND follow_user_id IN ?)+
			(SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id IN ? AND target_id IN ?)+
			(SELECT COUNT(*) FROM tb_blog WHERE id IN ?)+
			(SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id IN ?)+
			(SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN ?)+
			(SELECT COUNT(*) FROM tb_community_projection_event WHERE event_id IN ?)`,
			users, users, users, users, ownedNotes, ownedNotes, events, eventKeys(ownedEvents)).Scan(&remaining).Error != nil || remaining != 0 {
			t.Error("owned query fixture rows remain after cleanup")
		}
		keys := make([]string, 0, len(ownedNotes))
		for _, id := range ownedNotes {
			keys = append(keys, "community:counts:"+strconv.FormatUint(id, 10))
		}
		if len(keys) > 0 {
			if r.Redis.Del(cleanupCtx, keys...).Err() != nil {
				t.Error("owned counter cache cleanup failed")
			}
			if count, keyErr := r.Redis.Exists(cleanupCtx, keys...).Result(); keyErr != nil || count != 0 {
				t.Error("owned counter cache keys remain")
			}
		}
		t.Log("owned query fixtures removed; shared Kafka topic/groups and sequences preserved")
	})
	create := func(author uint64, title string) uint64 {
		t.Helper()
		view, createErr := note.NewService(r.ORM).Create(ctx, strconv.FormatUint(author, 10),
			note.CreateRequest{Title: title, Content: "GO-072真实查询夹具"})
		if createErr != nil {
			t.Fatal("owned real note creation failed")
		}
		id, _ := strconv.ParseUint(view.ID, 10, 64)
		ownedNotes = append(ownedNotes, id)
		return id
	}
	first, second, third := create(author, "相同时间第一篇"), create(author, "相同时间第二篇"), create(author, "相同时间第三篇")
	otherNote := create(secondAuthor, "尚未关注作者")
	sameTime := time.Now().In(zone()).Truncate(time.Second)
	if err = r.ORM.WithContext(ctx).Exec("UPDATE tb_blog SET create_time=?,update_time=? WHERE id IN ?", sameTime, sameTime,
		[]uint64{first, second, third, otherNote}).Error; err != nil {
		t.Fatal("owned same-time query fixture preparation failed")
	}
	if _, err = base.SetFollow(ctx, userID, authorID, true); err != nil {
		t.Fatal("owned follow failed")
	}
	if _, err = base.SetLike(ctx, userID, strconv.FormatUint(first, 10), true); err != nil {
		t.Fatal("owned like failed")
	}
	applyEvents := func() {
		t.Helper()
		var rows []struct {
			EventID          string
			EventType        string
			AggregateType    string
			AggregateID      string
			AggregateVersion int
			Payload          json.RawMessage
		}
		if r.ORM.WithContext(ctx).Raw(`SELECT event_id,event_type,aggregate_type,aggregate_id,aggregate_version,payload
			FROM tb_community_outbox WHERE
			(aggregate_type='FOLLOW' AND aggregate_id IN ?) OR
			(aggregate_type='BLOG' AND aggregate_id IN ?) ORDER BY id`, idStrings(users), idStrings(ownedNotes)).Scan(&rows).Error != nil {
			t.Fatal("owned event lookup failed")
		}
		for _, row := range rows {
			ownedEvents[row.EventID] = true
			if row.EventType != "FOLLOW_CHANGED" && row.EventType != "NOTE_PUBLISHED" {
				continue
			}
			body, marshalErr := json.Marshal(map[string]any{"eventId": row.EventID, "eventType": row.EventType,
				"aggregateType": row.AggregateType, "aggregateId": row.AggregateID, "version": row.AggregateVersion, "payload": row.Payload})
			projection := communityprojection.Follow
			if row.EventType == "NOTE_PUBLISHED" {
				projection = communityprojection.Feed
			}
			if marshalErr != nil || repository.Apply(ctx, projection, body) != nil {
				t.Fatal("owned committed event could not populate the read model")
			}
		}
	}
	applyEvents()
	expected := []uint64{third, second, first}
	checkFeed := func(t *testing.T, candidate *Service, source string, want []uint64) {
		t.Helper()
		page, queryErr := candidate.Feed(ctx, userID, "", "", 50)
		if queryErr != nil || page.Source != source || !reflect.DeepEqual(feedIDs(page), idStrings(want)) {
			t.Fatalf("unexpected guarded feed: source=%s items=%d error=%v", page.Source, len(page.Items), queryErr)
		}
		for _, item := range page.Items {
			if item.LikedByMe != (item.ID == strconv.FormatUint(first, 10)) {
				t.Fatal("the current user's liked state leaked or disappeared")
			}
		}
	}
	checkRelationship := func(t *testing.T, candidate *Service, source string, following bool, version int, count int64) {
		t.Helper()
		state, queryErr := candidate.Relationship(ctx, userID, authorID)
		if queryErr != nil || state.Source != source || state.Following != following || state.Version != version || state.FollowerCount != count {
			t.Fatalf("unexpected guarded relationship: source=%s following=%t version=%d count=%d error=%v",
				state.Source, state.Following, state.Version, state.FollowerCount, queryErr)
		}
	}
	t.Run("default_database_and_complete_projection_reads", func(t *testing.T) {
		checkRelationship(t, base, sourceDatabase, true, 1, 1)
		checkRelationship(t, service, sourceProjection, true, 1, 1)
		checkFeed(t, base, sourceDatabase, expected)
		checkFeed(t, service, sourceProjection, expected)
		checkFeed(t, NewService(r.ORM).WithProjections().WithCounts(communitycache.New(r.Redis, r.ORM)), sourceProjection, expected)
	})
	t.Run("complete_catalog_same_time_three_pages", func(t *testing.T) {
		var cursorTime, cursorID string
		var got []string
		for pageNo := 0; pageNo < 4; pageNo++ {
			page, queryErr := service.Feed(ctx, userID, cursorTime, cursorID, 1)
			if queryErr != nil || page.Source != sourceProjection {
				t.Fatal("projected page query failed")
			}
			got = append(got, feedIDs(page)...)
			if page.NextCursor == nil {
				break
			}
			cursorTime, cursorID = page.NextCursor.PublishedAt, page.NextCursor.ID
		}
		if !reflect.DeepEqual(got, idStrings(expected)) {
			t.Fatal("same-time projected pagination skipped or repeated a note")
		}
	})
	t.Run("mixed_historical_note_without_event_falls_back", func(t *testing.T) {
		legacy := insertNote(t, r, author, "无发布事件的历史查询夹具", sameTime.Add(-time.Hour))
		ownedNotes = append(ownedNotes, legacy)
		t.Cleanup(func() { _ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=? AND user_id=?", legacy, author).Error })
		checkFeed(t, service, sourceDatabase, append(append([]uint64{}, expected...), legacy))
	})
	t.Run("partial_catalog_gap_after_first_page_falls_back", func(t *testing.T) {
		if r.ORM.Exec("DELETE FROM tb_community_note_projection WHERE note_id=?", first).Error != nil ||
			r.ORM.Exec("DELETE FROM tb_community_projection_event WHERE projection=? AND event_id=?", communityprojection.Feed,
				"NOTE:"+strconv.FormatUint(first, 10)+":1:PUBLISHED").Error != nil {
			t.Fatal("owned partial catalog fixture failed")
		}
		t.Cleanup(applyEvents)
		page, queryErr := service.Feed(ctx, userID, "", "", 1)
		if queryErr != nil || page.Source != sourceDatabase || len(page.Items) != 1 || page.Items[0].ID != strconv.FormatUint(third, 10) {
			t.Fatal("a gap outside the first page was incorrectly treated as complete")
		}
		checkFeed(t, service, sourceDatabase, expected)
	})
	for _, mismatch := range []struct {
		name, update, restore string
		wrong, original       any
	}{
		{"author", "author_id", "author_id", secondAuthor, author},
		{"version", "version", "version", 0, 1},
		{"time", "published_at", "published_at", sameTime.Add(-time.Hour), sameTime},
	} {
		t.Run("catalog_"+mismatch.name+"_mismatch_falls_back", func(t *testing.T) {
			if r.ORM.Exec("UPDATE tb_community_note_projection SET "+mismatch.update+"=? WHERE note_id=?", mismatch.wrong, first).Error != nil {
				t.Fatal("owned catalog mismatch fixture failed")
			}
			t.Cleanup(func() {
				if r.ORM.Exec("UPDATE tb_community_note_projection SET "+mismatch.restore+"=? WHERE note_id=?", mismatch.original, first).Error != nil {
					t.Error("owned catalog mismatch restoration failed")
				}
			})
			checkFeed(t, service, sourceDatabase, expected)
		})
	}
	t.Run("draft_candidate_filtered_before_limit", func(t *testing.T) {
		draft := create(author, "投影已有但事实非公开")
		applyEvents()
		if r.ORM.Exec("UPDATE tb_blog SET status='DRAFT' WHERE id=? AND user_id=?", draft, author).Error != nil {
			t.Fatal("owned draft query fixture failed")
		}
		t.Cleanup(func() { _ = r.ORM.Exec("DELETE FROM tb_blog WHERE id=? AND user_id=?", draft, author).Error })
		page, queryErr := service.Feed(ctx, userID, "", "", 1)
		if queryErr != nil || page.Source != sourceProjection || len(page.Items) != 1 || page.Items[0].ID != strconv.FormatUint(third, 10) {
			t.Fatal("a non-public catalog candidate consumed the page limit")
		}
		checkFeed(t, service, sourceProjection, expected)
	})
	for _, mismatch := range []struct {
		name, column    string
		wrong, original int
	}{
		{"active", "active", 0, 1},
		{"version", "version", 0, 1},
	} {
		t.Run("follower_"+mismatch.name+"_mismatch_keeps_database_state_and_count", func(t *testing.T) {
			if r.ORM.Exec("UPDATE tb_community_follow_projection SET "+mismatch.column+"=? WHERE user_id=? AND target_id=?", mismatch.wrong, reader, author).Error != nil {
				t.Fatal("owned relationship mismatch fixture failed")
			}
			t.Cleanup(func() {
				if r.ORM.Exec("UPDATE tb_community_follow_projection SET "+mismatch.column+"=? WHERE user_id=? AND target_id=?", mismatch.original, reader, author).Error != nil {
					t.Error("owned relationship restoration failed")
				}
			})
			checkRelationship(t, service, sourceDatabase, true, 1, 1)
		})
	}
	t.Run("missing_active_follower_falls_back", func(t *testing.T) {
		if r.ORM.Exec("DELETE FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", reader, author).Error != nil ||
			r.ORM.Exec("DELETE FROM tb_community_projection_event WHERE projection=? AND event_id=?", communityprojection.Follow,
				eventID(reader, author, 1)).Error != nil {
			t.Fatal("owned missing follower fixture failed")
		}
		t.Cleanup(applyEvents)
		checkRelationship(t, service, sourceDatabase, true, 1, 1)
	})
	t.Run("extra_active_follower_cannot_inflate_count", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"eventId": eventID(otherReader, author, 1), "eventType": "FOLLOW_CHANGED",
			"aggregateType": "FOLLOW", "aggregateId": authorID, "version": 1,
			"payload": map[string]any{"userId": strconv.FormatUint(otherReader, 10), "followUserId": authorID, "active": "true", "version": 1}})
		ownedEvents[eventID(otherReader, author, 1)] = true
		if repository.Apply(ctx, communityprojection.Follow, body) != nil {
			t.Fatal("owned extra projection fixture failed")
		}
		t.Cleanup(func() {
			_ = r.ORM.Exec("DELETE FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", otherReader, author).Error
		})
		checkRelationship(t, service, sourceDatabase, true, 1, 1)
	})
	t.Run("missing_inactive_version_zero_also_falls_back", func(t *testing.T) {
		if _, followErr := base.SetFollow(ctx, strconv.FormatUint(otherReader, 10), authorID, false); followErr != nil {
			t.Fatal("owned inactive relation fixture failed")
		}
		t.Cleanup(func() {
			_ = r.ORM.Exec("DELETE FROM tb_follow WHERE user_id=? AND follow_user_id=?", otherReader, author).Error
		})
		checkRelationship(t, service, sourceDatabase, true, 1, 1)
	})
	t.Run("complete_empty_author_is_zero", func(t *testing.T) {
		state, queryErr := service.Relationship(ctx, userID, strconv.FormatUint(emptyAuthor, 10))
		if queryErr != nil || state.Source != sourceProjection || state.Following || state.Version != 0 || state.FollowerCount != 0 {
			t.Fatal("an exactly empty projection could not represent zero")
		}
	})
	for _, failure := range []struct {
		name, match string
		feed        bool
	}{
		{"missing_follow_table", "LEFT JOIN tb_community_follow_projection", false},
		{"follow_count_after_completeness", "SELECT COUNT(*) FROM tb_community_follow_projection", false},
		{"missing_catalog_table", "LEFT JOIN tb_community_note_projection", true},
		{"catalog_read_after_completeness", "INNER JOIN tb_community_note_projection", true},
	} {
		t.Run(failure.name+"_falls_back", func(t *testing.T) {
			// A separate ORM callback rewrites only the optional projection query
			// to an absent table. MySQL returns a real query error; business rows,
			// shared schema and successful response bodies are never mocked.
			faultDB, openErr := gorm.Open(gormmysql.New(gormmysql.Config{Conn: r.SQL, SkipInitializeWithVersion: true}),
				&gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
			if openErr != nil {
				t.Fatal("fault-isolated ORM unavailable")
			}
			hits := 0
			if faultDB.Callback().Row().Before("gorm:row").Register("go072_owned_missing_projection", func(tx *gorm.DB) {
				query := tx.Statement.SQL.String()
				if !strings.Contains(query, failure.match) {
					return
				}
				hits++
				query = strings.ReplaceAll(query, "tb_community_follow_projection", "go072_absent_owned_projection")
				query = strings.ReplaceAll(query, "tb_community_note_projection", "go072_absent_owned_projection")
				tx.Statement.SQL.Reset()
				tx.Statement.SQL.WriteString(query)
			}) != nil {
				t.Fatal("optional query fault registration failed")
			}
			candidate := NewService(faultDB).WithProjections()
			if failure.feed {
				checkFeed(t, candidate, sourceDatabase, expected)
			} else {
				checkRelationship(t, candidate, sourceDatabase, true, 1, 1)
			}
			if hits != 1 {
				t.Fatal("the controlled optional SQL fault was not reached exactly once")
			}
		})
	}
	t.Run("new_follow_and_unfollow_do_not_wait_for_graph", func(t *testing.T) {
		if _, followErr := base.SetFollow(ctx, userID, strconv.FormatUint(secondAuthor, 10), true); followErr != nil {
			t.Fatal("new owned follow failed")
		}
		checkFeed(t, service, sourceProjection, []uint64{otherNote, third, second, first})
		if _, followErr := base.SetFollow(ctx, userID, authorID, false); followErr != nil {
			t.Fatal("owned unfollow failed")
		}
		checkRelationship(t, service, sourceDatabase, false, 2, 0)
		checkFeed(t, service, sourceProjection, []uint64{otherNote})
		if _, followErr := base.SetFollow(ctx, userID, authorID, true); followErr != nil {
			t.Fatal("owned refollow failed")
		}
		checkRelationship(t, service, sourceDatabase, true, 3, 1)
		if _, followErr := base.SetFollow(ctx, userID, strconv.FormatUint(secondAuthor, 10), false); followErr != nil {
			t.Fatal("owned second-author unfollow failed")
		}
		checkFeed(t, service, sourceProjection, expected)
		applyEvents()
	})
	t.Run("one_repeatable_read_snapshot_and_next_request_guard", func(t *testing.T) {
		err := service.readSnapshot(ctx, func(snapshot *Service) error {
			var active int
			if queryErr := snapshot.db.Raw("SELECT active FROM tb_follow WHERE user_id=? AND follow_user_id=?", reader, author).Scan(&active).Error; queryErr != nil || active != 1 {
				return fmt.Errorf("snapshot could not establish the old active fact")
			}
			if _, writeErr := base.SetFollow(ctx, userID, authorID, false); writeErr != nil {
				return fmt.Errorf("concurrent owned unfollow failed")
			}
			applyEvents()
			state, queryErr := snapshot.relationship(ctx, reader, author)
			if queryErr != nil || state.Source != sourceProjection || !state.Following || state.Version != 3 || state.FollowerCount != 1 {
				return fmt.Errorf("relationship mixed two committed snapshots")
			}
			page, queryErr := snapshot.feedPage(ctx, reader, time.Time{}, 0, false, 50)
			if queryErr != nil || page.Source != sourceProjection || !reflect.DeepEqual(feedIDs(page), idStrings(expected)) {
				return fmt.Errorf("feed mixed two committed snapshots")
			}
			return nil
		})
		t.Cleanup(func() {
			if _, writeErr := base.SetFollow(ctx, userID, authorID, true); writeErr != nil {
				t.Error("owned snapshot fixture restoration failed")
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		checkRelationship(t, service, sourceProjection, false, 4, 0)
		checkFeed(t, service, sourceDatabase, []uint64{})
	})
}

func idStrings(ids []uint64) []string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, strconv.FormatUint(id, 10))
	}
	return values
}

func feedIDs(page FeedPage) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func eventKeys(events map[string]bool) []string {
	keys := make([]string, 0, len(events))
	for id := range events {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	return keys
}
