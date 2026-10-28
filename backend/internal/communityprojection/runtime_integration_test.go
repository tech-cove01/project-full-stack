package communityprojection

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationProjectionRecovery(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("explicit isolated dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	defer r.Close()
	conn, err := r.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("fixture lock unavailable")
	}
	defer conn.Close()
	var locked int
	if conn.QueryRowContext(ctx, "SELECT GET_LOCK('go072_integration',30)").Scan(&locked) != nil || locked != 1 {
		t.Fatal("fixture window unavailable")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('go072_integration')")
	var entropy [8]byte
	_, _ = rand.Read(entropy[:])
	base := uint64(9400000000000000) + binary.LittleEndian.Uint64(entropy[:])%1000000000
	user, target, note := strconv.FormatUint(base, 10), strconv.FormatUint(base+1, 10), strconv.FormatUint(base+2, 10)
	topic := "community.recovery.it." + user
	runtime := NewRuntime(New(r.ORM))
	exec := func(q string, a ...any) {
		t.Helper()
		if r.ORM.Exec(q, a...).Error != nil {
			t.Fatal("owned SQL mutation failed")
		}
	}
	count := func(q string, want int, a ...any) {
		t.Helper()
		var got int
		if r.ORM.Raw(q, a...).Scan(&got).Error != nil || got != want {
			t.Fatalf("durable invariant mismatch: want=%d got=%d", want, got)
		}
	}
	body := func(version int, active bool) []byte {
		value, _ := json.Marshal(map[string]any{"eventId": "FOLLOW:" + user + ":" + target + ":" + strconv.Itoa(version), "eventType": "FOLLOW_CHANGED", "aggregateType": "FOLLOW", "aggregateId": target, "version": version, "payload": map[string]any{"userId": user, "followUserId": target, "version": version, "active": active}})
		return value
	}
	message := Message{Topic: topic, Partition: 0, Offset: 0, Key: []byte(target), Body: body(1, true)}
	defer func() {
		exec("DELETE FROM tb_community_consumer_failure WHERE topic=?", topic)
		exec("DELETE FROM tb_community_projection_event WHERE event_id LIKE ? OR event_id=?", "FOLLOW:"+user+":"+target+":%", "NOTE:"+note+":1:PUBLISHED")
		exec("DELETE FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", user, target)
		exec("DELETE FROM tb_follow WHERE user_id=? AND follow_user_id=?", user, target)
		exec("DELETE FROM tb_community_note_projection WHERE note_id=?", note)
		exec("DELETE FROM tb_blog WHERE id=?", note)
	}()
	t.Run("nil_and_empty_poison_bytes_keep_one_immutable_digest", func(t *testing.T) {
		msg := Message{Topic: topic, Partition: 0, Offset: 3}
		if runtime.Park(ctx, Follow, msg, "apply_failed") != nil {
			t.Fatal("nil-byte poison could not be stored")
		}
		msg.Key, msg.Body = []byte{}, []byte{}
		if runtime.Park(ctx, Follow, msg, "apply_failed") != nil {
			t.Fatal("equivalent empty bytes changed the parked content hash")
		}
		if !errors.Is(runtime.Replay(ctx, Follow, topic, 0, 3), ErrInvalid) {
			t.Fatal("unchanged invalid poison was confused with tampered content")
		}
	})
	t.Run("park_preserves_poison_bytes_and_repeated_coordinates_require_identical_content", func(t *testing.T) {
		poison := message
		poison.Key = []byte(strings.Repeat("x", 2048))
		poison.Headers = make([]Header, 33)
		if !errors.Is(runtime.Apply(ctx, Follow, poison), ErrInvalid) {
			t.Fatal("domain poison accepted")
		}
		for range 2 {
			if runtime.Park(ctx, Follow, poison, "apply_failed") != nil {
				t.Fatal("poison was not durably parked")
			}
		}
		count("SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND state='PARKED' AND OCTET_LENGTH(message_key)=2048", 1, topic)
		poison.Body = []byte(`{}`)
		if !errors.Is(runtime.Park(ctx, Follow, poison, "apply_failed"), ErrConflict) {
			t.Fatal("same source was silently overwritten")
		}
		if !errors.Is(runtime.Replay(ctx, Follow, topic, 0, 0), ErrInvalid) {
			t.Fatal("invalid original record must stay parked")
		}
	})
	t.Run("replay_effect_and_resolution_roll_back_together_then_retry", func(t *testing.T) {
		message.Offset = 1
		if runtime.Park(ctx, Follow, message, "apply_failed") != nil {
			t.Fatal("cannot park owned event")
		}
		callback := "go072_replay_resolution_failure"
		if r.ORM.Callback().Raw().Before("gorm:raw").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Context.Value("go072-fault") == true && strings.Contains(tx.Statement.SQL.String(), "UPDATE tb_community_consumer_failure") {
				tx.AddError(errors.New("controlled resolution failure"))
			}
		}) != nil {
			t.Fatal("cannot install controlled barrier")
		}
		defer r.ORM.Callback().Raw().Remove(callback)
		fault := context.WithValue(ctx, "go072-fault", true)
		if runtime.Replay(fault, Follow, topic, 0, 1) == nil {
			t.Fatal("resolution failure reported success")
		}
		count("SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=?", 0, user, target)
		count("SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=? AND event_id=?", 0, Follow, "FOLLOW:"+user+":"+target+":1")
		count("SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND source_offset=1 AND state='PARKED'", 1, topic)
		for range 2 {
			if runtime.Replay(ctx, Follow, topic, 0, 1) != nil {
				t.Fatal("replay did not recover idempotently")
			}
		}
		count("SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND active=1 AND version=1", 1, user, target)
		count("SELECT COUNT(*) FROM tb_community_consumer_failure WHERE topic=? AND source_offset=1 AND state='RESOLVED'", 1, topic)
	})
	t.Run("missing_note_fact_recovers_from_original_record_without_changing_offsets", func(t *testing.T) {
		value, _ := json.Marshal(map[string]any{"eventId": "NOTE:" + note + ":1:PUBLISHED", "eventType": "NOTE_PUBLISHED", "aggregateType": "BLOG", "aggregateId": note, "version": 1, "payload": map[string]any{"noteId": note, "authorId": user, "publishVersion": 1}})
		msg := Message{Topic: topic, Partition: 0, Offset: 2, Key: []byte(note), Body: value}
		if !errors.Is(runtime.Apply(ctx, Feed, msg), ErrFactUnavailable) {
			t.Fatal("missing fact must not produce an effect")
		}
		if runtime.Park(ctx, Feed, msg, "apply_failed") != nil {
			t.Fatal("failed event was not durable")
		}
		exec("INSERT INTO tb_blog(id,shop_id,user_id,title,images,content,status,publish_version) VALUES(?,0,?,'owned recovery','','owned','PUBLISHED',1)", note, user)
		if runtime.Replay(ctx, Feed, topic, 0, 2) != nil {
			t.Fatal("original note record did not recover")
		}
		count("SELECT COUNT(*) FROM tb_community_note_projection WHERE note_id=? AND author_id=? AND version=1", 1, note, user)
	})
	t.Run("rebuild_fences_application_preserves_receipts_and_accepts_matching_bootstrap_event", func(t *testing.T) {
		exec("INSERT INTO tb_follow(user_id,follow_user_id,active,version) VALUES(?,?,1,3)", user, target)
		if runtime.Rebuild(ctx, Follow) != nil || runtime.Rebuild(ctx, Feed) != nil {
			t.Fatal("controlled snapshot rebuild failed")
		}
		if runtime.Repository.Apply(ctx, Follow, body(1, true)) != nil {
			t.Fatal("old durable receipt did not remain readable")
		}
		if runtime.Repository.Apply(ctx, Follow, body(2, false)) != nil {
			t.Fatal("old unseen event should be harmless")
		}
		if runtime.Repository.Apply(ctx, Follow, body(3, true)) != nil {
			t.Fatal("matching bootstrap version must adopt its first event hash")
		}
		count("SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND version=3 AND active=1", 1, user, target)
		barrier, release := make(chan struct{}), make(chan struct{})
		callback := "go072_rebuild_barrier"
		if r.ORM.Callback().Raw().After("gorm:raw").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Context.Value("go072-rebuild") == true && strings.Contains(tx.Statement.SQL.String(), "DELETE FROM tb_community_follow_projection") {
				close(barrier)
				<-release
			}
		}) != nil {
			t.Fatal("cannot install rebuild barrier")
		}
		defer r.ORM.Callback().Raw().Remove(callback)
		done := make(chan error, 1)
		go func() { done <- runtime.Rebuild(context.WithValue(ctx, "go072-rebuild", true), Follow) }()
		select {
		case <-barrier:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("rebuild did not enter exclusive window")
		}
		short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		err := runtime.Repository.Apply(short, Follow, body(4, false))
		stop()
		close(release)
		if <-done != nil {
			t.Fatal("rebuild failed after barrier release")
		}
		if err == nil {
			t.Fatal("incremental event passed an unpublished rebuild snapshot")
		}
		if runtime.Repository.Apply(ctx, Follow, body(4, false)) != nil {
			t.Fatal("blocked event did not recover")
		}
		count("SELECT COUNT(*) FROM tb_community_follow_projection WHERE user_id=? AND target_id=? AND version=4 AND active=0", 1, user, target)
		stats, err := runtime.Stats(ctx, Follow)
		if err != nil || stats.Parked < 1 || stats.Resolved < 1 {
			t.Fatal("durable failure counters are not observable")
		}
	})
}
