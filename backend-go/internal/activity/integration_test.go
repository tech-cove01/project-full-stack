package activity

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/activitypub"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

type faultKey struct{}

// These tests share a named lock with activitypub/outbox integration because
// their real relay may otherwise consume this suite's intentionally queued rows.
// Only exact random fixture IDs/keys are written or removed; no DDL or flushes.
func TestIntegrationActivity(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MYSQL_* and REDIS_* dependencies")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("incomplete integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	resources, err := database.Open(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal("cannot open explicitly configured integration dependencies")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	lockConn, err := resources.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var acquired int
	if err = lockConn.QueryRowContext(context.Background(), "SELECT GET_LOCK('go021_outbox_integration',30)").Scan(&acquired); err != nil || acquired != 1 {
		_ = lockConn.Close()
		t.Fatal("cannot acquire shared activity integration lock")
	}
	t.Cleanup(func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('go021_outbox_integration')")
		_ = lockConn.Close()
	})
	identity := auth.NewService(resources.ORM, resources.Redis, "development")
	service := NewService(resources.ORM, resources.Redis)
	router := httpapi.NewRouter(resources, time.Second, "activity-integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { auth.Register(routes, identity); Register(routes, service, identity) })
	f := newActivityFixture(t, resources, router)
	var created View
	t.Run("create_lists_exact_strings_and_commits_single_audit_and_outbox", func(t *testing.T) {
		created = f.create(t, "mixed", "A1", "first-publish-key")
		if created.PublishStatus != "INITIALIZING" || created.PublishVersion != 0 || created.Ready || created.Stock != 100 || created.PayValue != "1900" || created.ActualValue != "3000" {
			t.Fatal("creation must return durable closed preparation state")
		}
		for _, check := range []struct {
			query string
			args  []any
		}{
			{"SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id=? AND merchant_id=? AND stock=100 AND total_stock=100", []any{created.ID, f.merchants["A"]}},
			{"SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND operator_user_id=? AND action='ACTIVITY_CREATE' AND operator_type='MERCHANT'", []any{created.ID, f.users["mixed"]}},
			{"SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ACTIVITY' AND aggregate_id=? AND event_type='ACTIVITY_PREPARE' AND aggregate_version=0 AND delivery_status='PENDING'", []any{created.ID}},
		} {
			f.count(t, check.query, check.args, 1)
		}
		reply := f.call(t, "mixed", http.MethodGet, "/merchant/activities?shopId="+f.shops["A1"], nil, "", context.Background())
		var rows []View
		if reply.Code != 200 || !reply.Success || reply.Total == nil || *reply.Total != "1" || json.Unmarshal(reply.Data, &rows) != nil || len(rows) != 1 || rows[0].ID != created.ID {
			t.Fatal("activity list must serialize exact string IDs/total and the committed resource")
		}
	})
	t.Run("same_key_concurrent_requests_return_one_activity_and_changed_body_conflicts", func(t *testing.T) {
		const count = 8
		start := make(chan struct{})
		var wait sync.WaitGroup
		replies := make([]activityReply, count)
		for i := range count {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				<-start
				request := f.request("A1")
				replies[i] = f.call(t, "mixed", http.MethodPost, "/merchant/activities", request, "concurrent-publish-key", context.Background())
			}(i)
		}
		close(start)
		wait.Wait()
		id := ""
		for _, reply := range replies {
			var view View
			if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &view) != nil || view.ID == "" {
				t.Fatal("all same-payload concurrent requests must find the committed winner")
			}
			if id == "" {
				id = view.ID
			}
			if view.ID != id {
				t.Fatal("concurrent publication produced different activities")
			}
		}
		f.count(t, "SELECT COUNT(*) FROM tb_seckill_voucher WHERE merchant_id=? AND publish_request_key=?", []any{f.merchants["A"], "concurrent-publish-key"}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ACTIVITY_CREATE'", []any{id}, 1)
		changed := f.request("A1")
		changed.Title += "changed"
		reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities", changed, "concurrent-publish-key", context.Background())
		if reply.Code != 409 || reply.Success {
			t.Fatal("same key with another canonical payload must conflict")
		}
	})
	t.Run("mixed_roles_cross_shop_and_old_token_revocation_reject_without_writes", func(t *testing.T) {
		other := f.create(t, "foreign", "B1", "foreign-publish-key")
		for _, user := range []string{"mixed", "staff", "ordinary"} {
			r := f.request("B1")
			before := f.activityCount(t)
			if reply := f.call(t, user, http.MethodPost, "/merchant/activities", r, "forbidden-publish-key", context.Background()); reply.Code != 403 || reply.Success {
				t.Fatal("management rights must follow the concrete shop")
			}
			for _, action := range []string{"retry-publication", "offline"} {
				if reply := f.call(t, user, http.MethodPost, "/merchant/activities/"+other.ID+"/"+action, nil, "", context.Background()); reply.Code != 403 || reply.Success {
					t.Fatal("cross-shop mutation must reject before any Redis or DB effects")
				}
			}
			if f.activityCount(t) != before {
				t.Fatal("rejected publication changed activity count")
			}
		}
		f.exec(t, "UPDATE tb_merchant_member SET status='DISABLED' WHERE id=?", f.members["mixedA"])
		t.Cleanup(func() { f.exec(t, "UPDATE tb_merchant_member SET status='ACTIVE' WHERE id=?", f.members["mixedA"]) })
		for _, action := range []string{"retry-publication", "offline"} {
			if reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+created.ID+"/"+action, nil, "", context.Background()); reply.Code != 403 {
				t.Fatal("an existing login token must not retain revoked management rights")
			}
		}
		if reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities", f.request("A1"), "revoked-publish-key", context.Background()); reply.Code != 403 {
			t.Fatal("revoked membership must reject new publication")
		}
		me := f.call(t, "mixed", http.MethodGet, "/user/me", nil, "", context.Background())
		if me.Code != 200 || !me.Success {
			t.Fatal("authorization revocation must not manufacture a login failure")
		}
		f.exec(t, "UPDATE tb_merchant_member SET status='ACTIVE' WHERE id=?", f.members["mixedA"])
	})
	t.Run("waiting_authorization_read_observes_committed_member_revocation", func(t *testing.T) {
		// This subtest also runs independently: it creates no activity and no
		// PREPARE event, so observing a blocked write never needs a worker.
		counts := func() [3]int {
			var result [3]int
			marks, shops := placeholders(f.shops)
			queries := []struct {
				sql       string
				arguments []any
			}{
				{"SELECT COUNT(*) FROM tb_voucher WHERE shop_id IN (" + marks + ")", shops},
				{"SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id IN (?,?)", []any{f.merchants["A"], f.merchants["B"]}},
				{`SELECT COUNT(*) FROM tb_outbox o JOIN tb_seckill_voucher sv
 ON o.aggregate_type='ACTIVITY' AND o.aggregate_id=CAST(sv.voucher_id AS CHAR)
 WHERE sv.merchant_id IN (?,?)`, []any{f.merchants["A"], f.merchants["B"]}},
			}
			for i, query := range queries {
				if err := resources.SQL.QueryRow(query.sql, query.arguments...).Scan(&result[i]); err != nil {
					t.Fatal("cannot observe owned activity/audit/outbox baseline")
				}
			}
			return result
		}
		before := counts()
		revoker, err := resources.SQL.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			t.Fatal("cannot begin owned revocation transaction")
		}
		defer revoker.Rollback()
		changed, err := revoker.ExecContext(context.Background(), "UPDATE tb_merchant_member SET status='DISABLED' WHERE id=?", f.members["mixedA"])
		if err != nil {
			t.Fatal("cannot hold the uncommitted membership revocation")
		}
		if affected, err := changed.RowsAffected(); err != nil || affected != 1 {
			t.Fatal("revocation must lock exactly the owned membership")
		}
		t.Cleanup(func() { f.exec(t, "UPDATE tb_merchant_member SET status='ACTIVE' WHERE id=?", f.members["mixedA"]) })

		arrived := make(chan struct{}, 1)
		matchMemberRead := func(db *gorm.DB) bool {
			statement := strings.ToUpper(strings.Join(strings.Fields(db.Statement.SQL.String()), " "))
			return db.Statement.Context.Value(faultKey{}) == "waiting-revocation" &&
				strings.Contains(statement, "FROM TB_MERCHANT_MEMBER") &&
				(strings.Contains(statement, "FOR SHARE") || strings.Contains(statement, "FOR UPDATE"))
		}
		// Raw(...).Scan executes through GORM's Row callback, rather than its
		// Raw/Exec callback. Observe entry, then assert the HTTP operation cannot
		// finish while the other transaction still owns the authorization row.
		beforeName := "go021a_observe_member_read_entry"
		if err := resources.ORM.Callback().Row().Before("gorm:row").Register(beforeName, func(db *gorm.DB) {
			if matchMemberRead(db) {
				select {
				case arrived <- struct{}{}:
				default:
				}
			}
		}); err != nil {
			t.Fatal("cannot observe authorization SQL entry")
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Row().Remove(beforeName) })
		result, done := make(chan activityReply, 1), make(chan struct{})
		go func() {
			defer close(done)
			result <- f.call(t, "mixed", http.MethodPost, "/merchant/activities", f.request("A1"), "waiting-revocation-key", context.WithValue(context.Background(), faultKey{}, "waiting-revocation"))
		}()
		t.Cleanup(func() {
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("waiting publication did not finish before fixture cleanup")
			}
		})
		select {
		case <-arrived:
		case reply := <-result:
			t.Fatalf("publication finished before its current authorization read: HTTP %d", reply.Code)
		case <-time.After(3 * time.Second):
			t.Fatal("publication never reached the observed member authorization SQL")
		}
		select {
		case <-result:
			t.Fatal("publication returned while revocation still held the member row lock")
		case <-time.After(75 * time.Millisecond): // Arrival was established by the SQL callback above.
		}
		if err := revoker.Commit(); err != nil {
			t.Fatal("cannot commit the observed membership revocation")
		}
		select {
		case reply := <-result:
			if reply.Code != http.StatusForbidden || reply.Success || len(reply.Data) != 0 {
				t.Fatal("waiting old-token publication must reject the newly committed revocation")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("publication did not resume after revocation committed")
		}
		if after := counts(); after != before {
			t.Fatal("revoked waiting publication wrote a voucher, audit or outbox event")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_seckill_voucher WHERE merchant_id=? AND publish_request_key=?", []any{f.merchants["A"], "waiting-revocation-key"}, 0)
	})
	t.Run("lists_enforce_staff_scope_filters_stable_pagination_and_empty_arrays", func(t *testing.T) {
		f.create(t, "mixed", "A2", "second-shop-key")
		f.create(t, "foreign", "B2", "second-foreign-key")
		for _, user := range []string{"mixed", "staff", "ordinary"} {
			reply := f.call(t, user, http.MethodGet, "/merchant/activities?current=1&pageSize=1", nil, "", context.Background())
			var rows []View
			if reply.Code != 200 || !reply.Success || reply.Total == nil || json.Unmarshal(reply.Data, &rows) != nil || rows == nil {
				t.Fatal("all list outcomes must contain an array and exact total")
			}
			if user == "ordinary" && (len(rows) != 0 || *reply.Total != "0") {
				t.Fatal("ordinary user must see no activities")
			}
			if user == "staff" && len(rows) > 0 && rows[0].ShopID != f.shops["A1"] {
				t.Fatal("staff list leaked an unscoped shop")
			}
			if user == "mixed" && len(rows) > 0 && rows[0].ShopID == f.shops["B2"] {
				t.Fatal("owner of A must not gain staff-unscoped B2 visibility")
			}
		}
		for _, path := range []string{"/merchant/activities?shopId=" + f.shops["B2"], "/merchant/activities?current=18446744073709551615&pageSize=50", "/merchant/activities?status=OFFLINE"} {
			reply := f.call(t, "mixed", http.MethodGet, path, nil, "", context.Background())
			var rows []View
			if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &rows) != nil || rows == nil || len(rows) != 0 {
				t.Fatal("unauthorized filter, far page or empty status must remain an empty list")
			}
		}
	})
	t.Run("audit_or_outbox_failure_rolls_back_activity_voucher_and_success_records", func(t *testing.T) {
		createFault := "go021a_fault_create"
		rawFault := "go021a_fault_raw"
		if err := resources.ORM.Callback().Create().Before("gorm:create").Register(createFault, func(db *gorm.DB) {
			if db.Statement.Context.Value(faultKey{}) == "audit" && db.Statement.Table == "tb_audit_log" {
				db.AddError(errors.New("injected fixture audit failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := resources.ORM.Callback().Raw().Before("gorm:raw").Register(rawFault, func(db *gorm.DB) {
			if db.Statement.Context.Value(faultKey{}) == "outbox" && strings.Contains(db.Statement.SQL.String(), "INSERT INTO tb_outbox") {
				db.AddError(errors.New("injected fixture outbox failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = resources.ORM.Callback().Create().Remove(createFault)
			_ = resources.ORM.Callback().Raw().Remove(rawFault)
		})
		for _, fault := range []string{"audit", "outbox"} {
			before := f.activityCount(t)
			reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities", f.request("A1"), "rollback-"+fault+"-key", context.WithValue(context.Background(), faultKey{}, fault))
			if reply.Code != 503 || reply.Success || f.activityCount(t) != before {
				t.Fatal("audit/outbox failure must abort the complete creation transaction")
			}
			f.count(t, "SELECT COUNT(*) FROM tb_seckill_voucher WHERE merchant_id=? AND publish_request_key=?", []any{f.merchants["A"], "rollback-" + fault + "-key"}, 0)
		}
	})
	t.Run("offline_synchronously_closes_once_and_retry_never_resets_stock", func(t *testing.T) {
		active := f.create(t, "mixed", "A1", "active-offline-key")
		request := f.request("A1")
		begin, _ := parseTime(request.BeginTime)
		end, _ := parseTime(request.EndTime)
		gate := activitypub.NewGate(resources.Redis)
		if err := gate.Prepare(context.Background(), active.ID, 1, 100, begin, end); err != nil {
			t.Fatal(err)
		}
		f.exec(t, "UPDATE tb_seckill_voucher SET publish_status='ACTIVE',publish_version=1,stock=97 WHERE voucher_id=?", active.ID)
		if err := gate.Open(context.Background(), active.ID, 1); err != nil {
			t.Fatal(err)
		}
		if err := resources.Redis.Set(context.Background(), "seckill:stock:"+active.ID, 97, 0).Err(); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+active.ID+"/retry-publication", nil, "", context.Background())
			if reply.Code != 200 || !reply.Success {
				t.Fatal("safe publication retry should succeed")
			}
		}
		if stock, _ := resources.Redis.Get(context.Background(), "seckill:stock:"+active.ID).Int(); stock != 97 {
			t.Fatal("publication retry reset sold/reserved Redis stock")
		}
		for range 2 {
			reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+active.ID+"/offline", nil, "", context.Background())
			var view View
			if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &view) != nil || view.PublishStatus != "OFFLINE" || view.PublishVersion != 2 || view.Ready {
				t.Fatal("offline must return only after a synchronous one-way close")
			}
		}
		if ready, _ := gate.Ready(context.Background(), active.ID, 1); ready {
			t.Fatal("offline returned while old gate was open")
		}
		if err := gate.Open(context.Background(), active.ID, 1); !errors.Is(err, activitypub.ErrStale) {
			t.Fatal("a delayed old event must never undo offline")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ACTIVITY_OFFLINE'", []any{active.ID}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND event_type='ACTIVITY_CLOSE'", []any{active.ID}, 1)
		if reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+active.ID+"/retry-publication", nil, "", context.Background()); reply.Code != 409 {
			t.Fatal("offline activities cannot be republished")
		}
	})
	t.Run("offline_audit_rollback_keeps_gate_closed_and_same_action_converges", func(t *testing.T) {
		callback := "go021a_offline_audit_failure"
		if err := resources.ORM.Callback().Create().Before("gorm:create").Register(callback, func(db *gorm.DB) {
			if db.Statement.Context.Value(faultKey{}) == "offline-audit" && db.Statement.Table == "tb_audit_log" {
				db.AddError(errors.New("injected offline audit failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resources.ORM.Callback().Create().Remove(callback) })
		gate := activitypub.NewGate(resources.Redis)
		for _, status := range []string{"INITIALIZING", "ACTIVE"} {
			view := f.create(t, "mixed", "A1", "offline-rollback-"+status)
			version := 0
			if status == "ACTIVE" {
				request := f.request("A1")
				begin, _ := parseTime(request.BeginTime)
				end, _ := parseTime(request.EndTime)
				if err := gate.Prepare(context.Background(), view.ID, 1, 100, begin, end); err != nil {
					t.Fatal(err)
				}
				f.exec(t, "UPDATE tb_seckill_voucher SET publish_status='ACTIVE',publish_version=1 WHERE voucher_id=?", view.ID)
				if err := gate.Open(context.Background(), view.ID, 1); err != nil {
					t.Fatal(err)
				}
				version = 1
			}
			reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+view.ID+"/offline", nil, "", context.WithValue(context.Background(), faultKey{}, "offline-audit"))
			if reply.Code != 503 || reply.Success {
				t.Fatal("failed DB audit cannot report offline success")
			}
			f.count(t, "SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id=? AND publish_status=? AND publish_version=?", []any{view.ID, status, version}, 1)
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ACTIVITY_OFFLINE'", []any{view.ID}, 0)
			f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND event_type='ACTIVITY_CLOSE'", []any{view.ID}, 0)
			if state, err := resources.Redis.HGet(context.Background(), "seckill:gate:"+view.ID, "state").Result(); err != nil || state != "CLOSED" {
				t.Fatal("DB failure after Redis close must leave admission closed")
			}
			if status == "INITIALIZING" {
				request := f.request("A1")
				begin, _ := parseTime(request.BeginTime)
				end, _ := parseTime(request.EndTime)
				if err := gate.Prepare(context.Background(), view.ID, 1, 100, begin, end); !errors.Is(err, activitypub.ErrUnsafe) {
					t.Fatal("old prepare must not undo a safety close after DB rollback")
				}
			}
			reply = f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+view.ID+"/offline", nil, "", context.Background())
			var converged View
			if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &converged) != nil || converged.PublishStatus != "OFFLINE" || converged.PublishVersion != version+1 {
				t.Fatal("same offline action must complete the DB transition without reopening")
			}
			f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ACTIVITY_OFFLINE'", []any{view.ID}, 1)
		}
	})
	t.Run("redis_close_wrong_type_rejects_without_db_audit_or_outbox_change", func(t *testing.T) {
		view := f.create(t, "mixed", "A1", "offline-wrongtype-key")
		key := "seckill:gate:" + view.ID
		if err := resources.Redis.Set(context.Background(), key, "wrong-type", 0).Err(); err != nil {
			t.Fatal(err)
		}
		reply := f.call(t, "mixed", http.MethodPost, "/merchant/activities/"+view.ID+"/offline", nil, "", context.Background())
		if reply.Code != 503 || reply.Success {
			t.Fatal("Redis close failure must not report offline success")
		}
		f.count(t, "SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id=? AND publish_status='INITIALIZING' AND publish_version=0", []any{view.ID}, 1)
		f.count(t, "SELECT COUNT(*) FROM tb_audit_log WHERE resource_id=? AND action='ACTIVITY_OFFLINE'", []any{view.ID}, 0)
		f.count(t, "SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id=? AND event_type='ACTIVITY_CLOSE'", []any{view.ID}, 0)
		if value, _ := resources.Redis.Get(context.Background(), key).Result(); value != "wrong-type" {
			t.Fatal("failed close mutated an incompatible Redis key")
		}
	})
}

type activityReply struct {
	Code     int
	Success  bool            `json:"success"`
	Data     json.RawMessage `json:"data"`
	Total    *string         `json:"total"`
	ErrorMsg string          `json:"errorMsg"`
}
type activityFixture struct {
	resources                                        *database.Resources
	router                                           http.Handler
	users, phones, tokens, merchants, shops, members map[string]string
	prefix                                           string
}

func newActivityFixture(t *testing.T, resources *database.Resources, router http.Handler) *activityFixture {
	t.Helper()
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	base := uint64(9100000000000000) + (binary.BigEndian.Uint64(seed[:])%1000000000000)*16
	f := &activityFixture{resources: resources, router: router, users: map[string]string{}, phones: map[string]string{}, tokens: map[string]string{}, merchants: map[string]string{}, shops: map[string]string{}, members: map[string]string{}, prefix: fmt.Sprintf("GO021-%x-", seed[:4])}
	t.Cleanup(func() { f.cleanup(t) })
	for i, name := range []string{"A", "B"} {
		id := decimal(base + uint64(i) + 1)
		f.exec(t, "INSERT INTO tb_merchant(id,name) VALUES(?,?)", id, f.prefix+name)
		f.merchants[name] = id
	}
	for i, name := range []string{"mixed", "staff", "ordinary", "foreign"} {
		id := decimal(base + uint64(i) + 1)
		phone := fmt.Sprintf("139%08d", (base/16+uint64(i))%100000000)
		f.exec(t, "INSERT INTO tb_user(id,phone,nick_name) VALUES(?,?,?)", id, phone, f.prefix+name)
		f.users[name] = id
		f.phones[name] = phone
	}
	for i, name := range []string{"A1", "A2", "B1", "B2"} {
		id := decimal(base + uint64(i) + 1)
		f.exec(t, "INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(?,?,?,1,?,120.1,30.2)", id, f.merchants[string(name[0])], f.prefix+name, f.prefix+name+"路")
		f.shops[name] = id
	}
	for i, member := range []struct{ name, user, merchant, role string }{{"mixedA", "mixed", "A", "OWNER"}, {"mixedB", "mixed", "B", "STAFF"}, {"staffA", "staff", "A", "STAFF"}, {"foreignB", "foreign", "B", "OWNER"}} {
		id := decimal(base + uint64(i) + 1)
		f.exec(t, "INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES(?,?,?,?)", id, f.merchants[member.merchant], f.users[member.user], member.role)
		f.members[member.name] = id
	}
	for _, scope := range [][2]string{{"mixedB", "B1"}, {"staffA", "A1"}, {"staffA", "B2"}} {
		f.exec(t, "INSERT INTO tb_member_shop_scope(member_id,shop_id) VALUES(?,?)", f.members[scope[0]], f.shops[scope[1]])
	}
	for name, phone := range f.phones {
		reply := f.call(t, "", http.MethodPost, "/user/code?phone="+phone, nil, "", context.Background())
		var code auth.DevelopmentCode
		if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &code) != nil || code.Code == "" {
			t.Fatal("fixture login code failed")
		}
		reply = f.call(t, "", http.MethodPost, "/user/login", auth.LoginForm{Phone: phone, Code: code.Code}, "", context.Background())
		var token string
		if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &token) != nil || token == "" {
			t.Fatal("fixture real login failed")
		}
		f.tokens[name] = token
	}
	return f
}

func (f *activityFixture) request(shop string) CreateRequest {
	r := validRequest()
	r.ShopID = f.shops[shop]
	r.Title = f.prefix + "活动"
	return r
}
func (f *activityFixture) create(t *testing.T, user, shop, key string) View {
	t.Helper()
	reply := f.call(t, user, http.MethodPost, "/merchant/activities", f.request(shop), key, context.Background())
	var view View
	if reply.Code != 200 || !reply.Success || json.Unmarshal(reply.Data, &view) != nil || view.ID == "" {
		t.Fatalf("fixture publication failed: HTTP %d %s", reply.Code, reply.ErrorMsg)
	}
	return view
}
func (f *activityFixture) call(t *testing.T, user, method, path string, body any, key string, ctx context.Context) activityReply {
	t.Helper()
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, input).WithContext(ctx)
	if user != "" {
		request.Header.Set("authorization", f.tokens[user])
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	var result activityReply
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal("invalid HTTP JSON response")
	}
	result.Code = response.Code
	return result
}
func (f *activityFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.resources.SQL.ExecContext(ctx, query, args...); err != nil {
		t.Fatal("owned integration fixture mutation failed", err)
	}
}
func (f *activityFixture) count(t *testing.T, query string, args []any, want int) {
	t.Helper()
	var got int
	if err := f.resources.SQL.QueryRow(query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("persistent invariant expected %d, observed %d (%v)", want, got, err)
	}
}
func (f *activityFixture) activityCount(t *testing.T) int {
	t.Helper()
	var count int
	shops, args := placeholders(f.shops)
	if err := f.resources.SQL.QueryRow("SELECT COUNT(*) FROM tb_voucher WHERE shop_id IN ("+shops+")", args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func placeholders(values map[string]string) (string, []any) {
	args := make([]any, 0, len(values))
	for _, id := range values {
		args = append(args, id)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(args)), ","), args
}
func (f *activityFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shopPlaceholders, shopArgs := placeholders(f.shops)
	ids := []string{}
	if len(shopArgs) > 0 {
		rows, err := f.resources.SQL.QueryContext(ctx, "SELECT id FROM tb_voucher WHERE shop_id IN ("+shopPlaceholders+")", shopArgs...)
		if err != nil {
			t.Error("cleanup cannot enumerate owned activities")
		} else {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			_ = rows.Close()
		}
	}
	keys := []string{}
	for _, phone := range f.phones {
		keys = append(keys, "login:code:"+phone, "login:code:throttle:"+phone)
	}
	for _, token := range f.tokens {
		keys = append(keys, "login:token:"+token)
	}
	for _, id := range ids {
		for _, prefix := range []string{"seckill:gate:", "seckill:stock:", "seckill:begin:", "seckill:end:"} {
			keys = append(keys, prefix+id)
		}
		for _, statement := range []string{"DELETE FROM tb_outbox WHERE aggregate_type='ACTIVITY' AND aggregate_id=?", "DELETE FROM tb_seckill_voucher WHERE voucher_id=?", "DELETE FROM tb_voucher WHERE id=?"} {
			if _, err := f.resources.SQL.ExecContext(ctx, statement, id); err != nil {
				t.Error("cannot remove exact owned activity row")
			}
		}
	}
	if len(keys) > 0 {
		if err := f.resources.Redis.Del(ctx, keys...).Err(); err != nil {
			t.Error("cannot remove exact owned Redis keys")
		}
	}
	for _, target := range []struct {
		table, column string
		ids           map[string]string
	}{{"tb_audit_log", "merchant_id", f.merchants}, {"tb_member_shop_scope", "member_id", f.members}, {"tb_merchant_member", "id", f.members}, {"tb_shop", "id", f.shops}, {"tb_merchant", "id", f.merchants}, {"tb_user", "id", f.users}} {
		if len(target.ids) == 0 {
			continue
		}
		marks, args := placeholders(target.ids)
		if _, err := f.resources.SQL.ExecContext(ctx, "DELETE FROM "+target.table+" WHERE "+target.column+" IN ("+marks+")", args...); err != nil {
			t.Error("cannot remove exact owned fixture rows")
		}
	}
}
