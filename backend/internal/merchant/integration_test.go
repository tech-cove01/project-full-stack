package merchant

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// This opt-in suite uses the existing versioned schema, real MySQL and Redis.
// It inserts only random, explicitly owned rows, then deletes their exact IDs
// and Redis keys. It never performs DDL, resets sequences, or flushes data.
// Real >2^53 IDs can advance the isolated tables' AUTO_INCREMENT high-water mark.
func TestIntegrationMerchant(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MYSQL_* and REDIS_* dependencies")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("integration configuration is incomplete or invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	resources, err := database.Open(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal("cannot open explicitly configured integration dependencies")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("integration dependency cleanup failed")
		}
	})
	identity := auth.NewService(resources.ORM, resources.Redis, "development")
	service := NewService(resources.ORM)
	access := NewAccess(resources.ORM)
	router := merchantRouter(resources, identity, service)
	fixture := newMerchantFixture(t, resources, router)
	ctx = context.Background()

	t.Run("workspace_preserves_mixed_roles_large_ids_and_empty_memberships", func(t *testing.T) {
		fixture.requireWorkspace(t, router, "mixed", []expectedMerchant{
			{"A", Owner, []string{"A1", "A2"}}, {"B", Staff, []string{"B1"}}, {"D", Owner, []string{}},
		})
		fixture.requireWorkspace(t, router, "staff", []expectedMerchant{{"A", Staff, []string{"A1"}}})
		fixture.requireWorkspace(t, router, "manager", []expectedMerchant{{"A", Manager, []string{"A1", "A2"}}})
		fixture.requireWorkspace(t, router, "ownerB", []expectedMerchant{{"B", Owner, []string{"B1", "B2"}}})
		fixture.requireWorkspace(t, router, "ordinary", []expectedMerchant{})
		fixture.requireWorkspace(t, router, "noScope", []expectedMerchant{{"A", Staff, []string{}}})
	})

	t.Run("visible_and_managed_ranges_do_not_merge_roles", func(t *testing.T) {
		visible, err := access.VisibleShopIDs(ctx, fixture.users["mixed"])
		if err != nil || !reflect.DeepEqual(visible, fixture.shopIDs("A1", "A2", "B1")) {
			t.Fatal("mixed identity must see its owner shops and only its explicitly scoped staff shop")
		}
		managed, err := access.ManagedShopIDs(ctx, fixture.users["mixed"])
		if err != nil || !reflect.DeepEqual(managed, fixture.shopIDs("A1", "A2")) {
			t.Fatal("owning merchant A must not grant management of staff merchant B")
		}
		for _, name := range []string{"staff", "ordinary", "noScope"} {
			ids, err := access.ManagedShopIDs(ctx, fixture.users[name])
			if err != nil || ids == nil || len(ids) != 0 {
				t.Fatal("staff and ordinary identities must have an empty management range")
			}
		}
	})

	t.Run("concrete_shop_permissions_bind_actual_merchant_and_role", func(t *testing.T) {
		for _, test := range []struct {
			user, shop string
			visible    bool
			managed    bool
		}{
			{"mixed", "A1", true, true}, {"mixed", "A2", true, true},
			{"mixed", "B1", true, false}, {"mixed", "B2", false, false},
			{"mixed", "C1", false, false}, {"staff", "A1", true, false},
			{"staff", "A2", false, false}, {"staff", "B1", false, false},
			{"manager", "A2", true, true}, {"manager", "B1", false, false},
			{"ownerB", "A1", false, false}, {"ordinary", "A1", false, false},
			{"noScope", "A1", false, false},
		} {
			membership, err := access.MembershipForShop(ctx, fixture.users[test.user], fixture.shops[test.shop])
			if test.visible {
				merchant := string(test.shop[0])
				if err != nil || membership.MerchantID != fixture.merchants[merchant] || membership.MemberID == "" {
					t.Fatal("concrete access must return the membership of the shop's actual merchant")
				}
			} else if !errors.Is(err, ErrForbidden) {
				t.Fatal("unauthorized concrete shop must return forbidden")
			}
			_, err = access.RequireManager(ctx, fixture.users[test.user], fixture.shops[test.shop])
			if (test.managed && err != nil) || (!test.managed && !errors.Is(err, ErrForbidden)) {
				t.Fatal("management must be decided for this exact shop, not the user's strongest role")
			}
		}
		if _, err := access.MembershipForShop(ctx, fixture.users["mixed"], "18446744073709551615"); !errors.Is(err, ErrForbidden) {
			t.Fatal("an absent shop must not acquire authorization")
		}
	})

	t.Run("untrusted_query_headers_and_extra_session_role_do_not_authorize", func(t *testing.T) {
		path := "/merchant/workspace?userId=" + fixture.users["mixed"] + "&merchantId=" + fixture.merchants["A"] + "&role=OWNER"
		reply := merchantCall(t, router, http.MethodGet, path, nil, fixture.tokens["ordinary"])
		var workspace Workspace
		if reply.status != http.StatusOK || !reply.Success || json.Unmarshal(reply.Data, &workspace) != nil || workspace.Merchants == nil || len(workspace.Merchants) != 0 {
			t.Fatal("query and headers must not replace the authenticated ordinary user")
		}
		key := "login:token:" + fixture.tokens["staff"]
		if resources.Redis.HSet(ctx, key, "role", "OWNER", "merchantId", fixture.merchants["B"]).Err() != nil {
			t.Fatal("cannot arrange irrelevant extra session fields")
		}
		fixture.requireWorkspace(t, router, "staff", []expectedMerchant{{"A", Staff, []string{"A1"}}})
		if _, err := access.RequireManager(ctx, fixture.users["staff"], fixture.shops["A1"]); !errors.Is(err, ErrForbidden) {
			t.Fatal("extra role data in a session must never grant management")
		}
	})

	t.Run("scope_revocation_is_effective_for_the_original_token", func(t *testing.T) {
		fixture.exec(t, "DELETE FROM tb_member_shop_scope WHERE member_id = ? AND shop_id = ?", fixture.members["mixedB"], fixture.shops["B1"])
		defer fixture.exec(t, "INSERT INTO tb_member_shop_scope (member_id, shop_id) VALUES (?, ?)", fixture.members["mixedB"], fixture.shops["B1"])
		fixture.requireMe(t, router, "mixed")
		fixture.requireWorkspace(t, router, "mixed", []expectedMerchant{{"A", Owner, []string{"A1", "A2"}}, {"B", Staff, []string{}}, {"D", Owner, []string{}}})
		if _, err := access.MembershipForShop(ctx, fixture.users["mixed"], fixture.shops["B1"]); !errors.Is(err, ErrForbidden) {
			t.Fatal("a revoked scope must cease granting access immediately")
		}
	})

	t.Run("shop_ownership_changes_invalidate_dirty_old_scopes", func(t *testing.T) {
		fixture.exec(t, "UPDATE tb_shop SET merchant_id = ? WHERE id = ?", fixture.merchants["C"], fixture.shops["A1"])
		defer fixture.exec(t, "UPDATE tb_shop SET merchant_id = ? WHERE id = ?", fixture.merchants["A"], fixture.shops["A1"])
		fixture.requireMe(t, router, "staff")
		fixture.requireWorkspace(t, router, "staff", []expectedMerchant{{"A", Staff, []string{}}})
		fixture.requireWorkspace(t, router, "mixed", []expectedMerchant{{"A", Owner, []string{"A2"}}, {"B", Staff, []string{"B1"}}, {"D", Owner, []string{}}})
		for _, user := range []string{"staff", "mixed"} {
			if _, err := access.MembershipForShop(ctx, fixture.users[user], fixture.shops["A1"]); !errors.Is(err, ErrForbidden) {
				t.Fatal("old owner membership and scopes must not follow a shop to another merchant")
			}
		}
	})

	t.Run("member_disable_and_role_downgrade_take_effect_without_relogin", func(t *testing.T) {
		fixture.exec(t, "UPDATE tb_merchant_member SET status = 'DISABLED' WHERE id = ?", fixture.members["mixedA"])
		defer fixture.exec(t, "UPDATE tb_merchant_member SET status = 'ACTIVE', role_code = 'OWNER' WHERE id = ?", fixture.members["mixedA"])
		fixture.requireMe(t, router, "mixed")
		fixture.requireWorkspace(t, router, "mixed", []expectedMerchant{{"B", Staff, []string{"B1"}}, {"D", Owner, []string{}}})
		if _, err := access.RequireManager(ctx, fixture.users["mixed"], fixture.shops["A1"]); !errors.Is(err, ErrForbidden) {
			t.Fatal("disabled membership must not retain old-token management")
		}
		fixture.exec(t, "UPDATE tb_merchant_member SET status = 'ACTIVE', role_code = 'STAFF' WHERE id = ?", fixture.members["mixedA"])
		fixture.requireMe(t, router, "mixed")
		fixture.requireWorkspace(t, router, "mixed", []expectedMerchant{{"A", Staff, []string{}}, {"B", Staff, []string{"B1"}}, {"D", Owner, []string{}}})
		ids, err := access.ManagedShopIDs(ctx, fixture.users["mixed"])
		if err != nil || len(ids) != 0 {
			t.Fatal("downgraded role must lose management immediately")
		}
	})

	t.Run("merchant_disable_removes_all_grants_but_not_login_identity", func(t *testing.T) {
		fixture.exec(t, "UPDATE tb_merchant SET status = 'DISABLED' WHERE id = ?", fixture.merchants["B"])
		defer fixture.exec(t, "UPDATE tb_merchant SET status = 'ACTIVE' WHERE id = ?", fixture.merchants["B"])
		fixture.requireMe(t, router, "mixed")
		fixture.requireMe(t, router, "ownerB")
		fixture.requireWorkspace(t, router, "mixed", []expectedMerchant{{"A", Owner, []string{"A1", "A2"}}, {"D", Owner, []string{}}})
		fixture.requireWorkspace(t, router, "ownerB", []expectedMerchant{})
		if _, err := access.RequireManager(ctx, fixture.users["ownerB"], fixture.shops["B1"]); !errors.Is(err, ErrForbidden) {
			t.Fatal("merchant disable must invalidate its owner's resource permissions")
		}
	})

	t.Run("access_uses_the_callers_transaction_handle", func(t *testing.T) {
		tx := resources.ORM.Begin()
		if tx.Error != nil {
			t.Fatal("cannot start isolated authorization transaction")
		}
		defer tx.Rollback()
		if tx.Exec("UPDATE tb_merchant_member SET status = 'DISABLED' WHERE id = ?", fixture.members["mixedA"]).Error != nil {
			t.Fatal("cannot prepare transaction-local membership change")
		}
		if _, err := NewAccess(tx).RequireManager(ctx, fixture.users["mixed"], fixture.shops["A1"]); !errors.Is(err, ErrForbidden) {
			t.Fatal("authorization must observe the supplied transaction's own writes")
		}
		if _, err := access.RequireManager(ctx, fixture.users["mixed"], fixture.shops["A1"]); err != nil {
			t.Fatal("uncommitted fixture change must not escape to the global handle")
		}
	})

	t.Run("workspace_uses_one_statement_and_database_errors_are_not_empty_permissions", func(t *testing.T) {
		counter := &queryCounter{Interface: logger.Default.LogMode(logger.Silent)}
		counted := NewService(resources.ORM.Session(&gorm.Session{Logger: counter}))
		if _, err := counted.Workspace(ctx, fixture.users["mixed"]); err != nil || counter.count.Load() != 1 {
			t.Fatal("workspace must resolve all memberships and shops with one database statement")
		}
		failedCtx, stop := context.WithCancel(ctx)
		stop()
		if _, err := access.RequireManager(failedCtx, fixture.users["mixed"], fixture.shops["A1"]); err == nil || errors.Is(err, ErrForbidden) {
			t.Fatal("a failed database query must not become forbidden or granted access")
		}
		if ids, err := access.VisibleShopIDs(failedCtx, fixture.users["mixed"]); err == nil || ids != nil {
			t.Fatal("failed visible-range reads must not return an authoritative empty list")
		}
		if ids, err := access.ManagedShopIDs(failedCtx, fixture.users["mixed"]); err == nil || ids != nil {
			t.Fatal("failed management-range reads must not return an authoritative empty list")
		}
		// A separate already-closed pool produces an actual driver error while
		// the original pool still authenticates the real Redis session.
		closed, err := gorm.Open(gormmysql.New(gormmysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("cannot arrange independent database-failure test pool")
		}
		closedSQL, err := closed.DB()
		if err != nil || closedSQL.Close() != nil {
			t.Fatal("cannot close independent database-failure test pool")
		}
		reply := merchantCall(t, merchantRouter(resources, identity, NewService(closed)), http.MethodGet, "/merchant/workspace", nil, fixture.tokens["mixed"])
		if reply.status != http.StatusServiceUnavailable || reply.Success || reply.ErrorMsg != "服务暂时无法处理请求，请稍后重试。" || reply.Data != nil {
			t.Fatal("actual workspace database failure must return safe 503 without an empty or stale workspace")
		}
	})

	t.Run("logout_revokes_original_workspace_session", func(t *testing.T) {
		reply := merchantCall(t, router, http.MethodPost, "/user/logout", nil, fixture.tokens["ordinary"])
		if reply.status != http.StatusOK || !reply.Success {
			t.Fatal("logout must revoke the current session")
		}
		reply = merchantCall(t, router, http.MethodGet, "/merchant/workspace", nil, fixture.tokens["ordinary"])
		if reply.status != http.StatusUnauthorized || reply.Success || reply.Data != nil {
			t.Fatal("logged-out token must not resolve a workspace")
		}
	})
}

type queryCounter struct {
	logger.Interface
	count atomic.Int32
}

func (counter *queryCounter) Trace(context.Context, time.Time, func() (string, int64), error) {
	counter.count.Add(1)
}

func merchantRouter(resources *database.Resources, identity *auth.Service, service *Service) http.Handler {
	return httpapi.NewRouter(resources, time.Second, "merchant-integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		auth.Register(routes, identity)
		Register(routes, service, identity)
	})
}

type merchantReply struct {
	status   int
	Success  bool            `json:"success"`
	ErrorMsg string          `json:"errorMsg"`
	Data     json.RawMessage `json:"data"`
}

func merchantCall(t *testing.T, handler http.Handler, method, path string, body any, token string) merchantReply {
	t.Helper()
	var input bytes.Buffer
	if body != nil && json.NewEncoder(&input).Encode(body) != nil {
		t.Fatal("cannot encode fixture request")
	}
	request := httptest.NewRequest(method, path, &input)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("authorization", token)
	request.Header.Set("userId", "1")
	request.Header.Set("role", "OWNER")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var reply merchantReply
	if json.Unmarshal(response.Body.Bytes(), &reply) != nil {
		t.Fatal("integration response must be a JSON envelope")
	}
	reply.status = response.Code
	return reply
}

type merchantFixture struct {
	resources *database.Resources
	prefix    string
	merchants map[string]string
	users     map[string]string
	phones    map[string]string
	members   map[string]string
	shops     map[string]string
	tokens    map[string]string
}

func newMerchantFixture(t *testing.T, resources *database.Resources, router http.Handler) *merchantFixture {
	t.Helper()
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal("cannot allocate independent merchant fixture")
	}
	base := uint64(9_007_199_254_740_993) + binary.LittleEndian.Uint64(entropy[:])%1_000_000_000
	fixture := &merchantFixture{
		resources: resources, prefix: "go020_" + hex.EncodeToString(entropy[:]) + "_",
		merchants: map[string]string{}, users: map[string]string{}, phones: map[string]string{},
		members: map[string]string{}, shops: map[string]string{}, tokens: map[string]string{},
	}
	t.Cleanup(func() { fixture.cleanup(t) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := resources.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("cannot begin independent merchant fixture preparation")
	}
	defer tx.Rollback()
	for _, table := range []string{"tb_merchant", "tb_merchant_member", "tb_user", "tb_shop"} {
		var count int
		if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id BETWEEN ? AND ?", base, base+20).Scan(&count) != nil || count != 0 {
			t.Fatal("cannot establish exclusive ownership of large fixture IDs; retry with a fresh fixture")
		}
	}
	insert := func(query string, arguments ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, arguments...); err != nil {
			t.Fatal("cannot insert independent merchant fixture row")
		}
	}
	for index, name := range []string{"A", "B", "C", "D"} {
		id := decimal(base + uint64(index) + 1)
		insert("INSERT INTO tb_merchant (id, name) VALUES (?, ?)", id, fixture.prefix+name)
		fixture.merchants[name] = id
	}
	for index, name := range []string{"mixed", "staff", "manager", "ownerB", "ordinary", "noScope"} {
		id := decimal(base + uint64(index) + 1)
		phone := fmt.Sprintf("138%08d", (base+uint64(index))%100_000_000)
		insert("INSERT INTO tb_user (id, phone, nick_name) VALUES (?, ?, ?)", id, phone, "GO020"+name)
		fixture.users[name], fixture.phones[name] = id, phone
	}
	for index, name := range []string{"A1", "A2", "B1", "B2", "C1"} {
		id := decimal(base + uint64(index) + 1)
		insert(`INSERT INTO tb_shop (id, merchant_id, name, type_id, address, x, y)
			VALUES (?, ?, ?, 1, ?, 120.15, 30.28)`, id, fixture.merchants[string(name[0])], fixture.prefix+name, fixture.prefix+name+"路")
		fixture.shops[name] = id
	}
	for index, member := range []struct {
		name, user, merchant string
		role                 Role
	}{
		{"mixedA", "mixed", "A", Owner}, {"mixedB", "mixed", "B", Staff},
		{"staffA", "staff", "A", Staff}, {"managerA", "manager", "A", Manager},
		{"ownerB", "ownerB", "B", Owner}, {"noScopeA", "noScope", "A", Staff},
		{"mixedD", "mixed", "D", Owner},
	} {
		id := decimal(base + uint64(index) + 1)
		insert("INSERT INTO tb_merchant_member (id, merchant_id, user_id, role_code) VALUES (?, ?, ?, ?)", id, fixture.merchants[member.merchant], fixture.users[member.user], member.role)
		fixture.members[member.name] = id
	}
	for _, scope := range [][2]string{{"mixedB", "B1"}, {"mixedB", "C1"}, {"staffA", "A1"}, {"staffA", "B1"}} {
		insert("INSERT INTO tb_member_shop_scope (member_id, shop_id) VALUES (?, ?)", fixture.members[scope[0]], fixture.shops[scope[1]])
	}
	if tx.Commit() != nil {
		t.Fatal("cannot commit independent merchant fixture")
	}
	for _, name := range []string{"mixed", "staff", "manager", "ownerB", "ordinary", "noScope"} {
		codeReply := merchantCall(t, router, http.MethodPost, "/user/code?phone="+fixture.phones[name], nil, "")
		var code auth.DevelopmentCode
		if codeReply.status != http.StatusOK || !codeReply.Success || json.Unmarshal(codeReply.Data, &code) != nil || code.Code == "" {
			t.Fatal("fixture must obtain a real development login code")
		}
		login := merchantCall(t, router, http.MethodPost, "/user/login", auth.LoginForm{Phone: fixture.phones[name], Code: code.Code}, "")
		var token string
		if login.status != http.StatusOK || !login.Success || json.Unmarshal(login.Data, &token) != nil || len(token) != 32 {
			t.Fatal("fixture must establish a real authenticated Redis session")
		}
		fixture.tokens[name] = token
		fixture.requireMe(t, router, name)
		fields, err := resources.Redis.HGetAll(ctx, "login:token:"+token).Result()
		if err != nil || len(fields) != 3 || fields["id"] != fixture.users[name] {
			t.Fatal("real login must store identity only, not merchant roles")
		}
	}
	return fixture
}

type expectedMerchant struct {
	name  string
	role  Role
	shops []string
}

func (fixture *merchantFixture) requireWorkspace(t *testing.T, router http.Handler, user string, expected []expectedMerchant) {
	t.Helper()
	reply := merchantCall(t, router, http.MethodGet, "/merchant/workspace", nil, fixture.tokens[user])
	var actual Workspace
	if reply.status != http.StatusOK || !reply.Success || json.Unmarshal(reply.Data, &actual) != nil || actual.Merchants == nil || len(actual.Merchants) != len(expected) {
		t.Fatal("workspace must return the exact current authorized membership array")
	}
	for index, want := range expected {
		merchant := actual.Merchants[index]
		if merchant.ID != fixture.merchants[want.name] || merchant.Name != fixture.prefix+want.name || merchant.Role != want.role || merchant.Shops == nil || len(merchant.Shops) != len(want.shops) {
			t.Fatal("workspace merchant role, ordering, identity or empty shop array was incorrect")
		}
		for index, name := range want.shops {
			shop := merchant.Shops[index]
			if shop.ID != fixture.shops[name] || shop.Name != fixture.prefix+name || shop.Address != fixture.prefix+name+"路" {
				t.Fatal("workspace exposed an unauthorized shop or lost exact string identity/order")
			}
		}
	}
}

func (fixture *merchantFixture) requireMe(t *testing.T, router http.Handler, user string) {
	t.Helper()
	reply := merchantCall(t, router, http.MethodGet, "/user/me", nil, fixture.tokens[user])
	var identity auth.User
	if reply.status != http.StatusOK || !reply.Success || json.Unmarshal(reply.Data, &identity) != nil || identity.ID != fixture.users[user] {
		t.Fatal("the original login token must remain authenticated while business permissions change")
	}
}

func (fixture *merchantFixture) shopIDs(names ...string) []string {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		ids = append(ids, fixture.shops[name])
	}
	return ids
}

func (fixture *merchantFixture) exec(t *testing.T, query string, arguments ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := fixture.resources.SQL.ExecContext(ctx, query, arguments...)
	if err != nil {
		t.Fatal("cannot mutate exact owned authorization fixture")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal("fixture mutation must affect exactly its one owned row")
	}
}

func (fixture *merchantFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	keys := make([]string, 0)
	for _, phone := range fixture.phones {
		keys = append(keys, "login:code:"+phone, "login:code:throttle:"+phone)
	}
	for _, token := range fixture.tokens {
		keys = append(keys, "login:token:"+token)
	}
	if len(keys) > 0 && fixture.resources.Redis.Del(ctx, keys...).Err() != nil {
		t.Error("cannot delete exact merchant fixture Redis keys")
	}
	if len(keys) > 0 {
		remaining, err := fixture.resources.Redis.Exists(ctx, keys...).Result()
		if err != nil || remaining != 0 {
			t.Error("merchant fixture Redis cleanup must leave none of its exact keys")
		}
	}
	for _, target := range []struct {
		table, column string
		ids           map[string]string
	}{
		{"tb_member_shop_scope", "member_id", fixture.members},
		{"tb_merchant_member", "id", fixture.members}, {"tb_shop", "id", fixture.shops},
		{"tb_merchant", "id", fixture.merchants}, {"tb_user", "id", fixture.users},
	} {
		if len(target.ids) == 0 {
			continue
		}
		arguments := make([]any, 0, len(target.ids))
		for _, id := range target.ids {
			arguments = append(arguments, id)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(arguments)), ",")
		if _, err := fixture.resources.SQL.ExecContext(ctx, "DELETE FROM "+target.table+" WHERE "+target.column+" IN ("+placeholders+")", arguments...); err != nil {
			t.Error("cannot delete exact merchant fixture database rows")
		}
		var remaining int
		if err := fixture.resources.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+target.table+" WHERE "+target.column+" IN ("+placeholders+")", arguments...).Scan(&remaining); err != nil || remaining != 0 {
			t.Error("merchant fixture database cleanup must leave none of its exact rows")
		}
	}
}
