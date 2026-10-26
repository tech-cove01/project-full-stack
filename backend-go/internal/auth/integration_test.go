package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// These tests use only explicitly configured, real dependencies. The caller
// must opt in and initialize the versioned schema; this suite never migrates,
// flushes Redis, truncates tables, or loads credentials from a .env file.
func TestIntegrationAuth(t *testing.T) {
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
	service := NewService(resources.ORM, resources.Redis, "development")
	router := integrationRouter(resources, cfg.ProbeTimeout, service)

	t.Run("code_login_session_and_logout", func(t *testing.T) {
		fixture := newIntegrationFixture(t, resources)
		code := fixture.requestCode(t, router)
		fixture.requireTTL(t, fixture.codeKey(), 115*time.Second, 120*time.Second)
		fixture.requireTTL(t, fixture.throttleKey(), 55*time.Second, 60*time.Second)

		before := fixture.snapshot(t, fixture.codeKey())
		throttled := integrationCall(t, router, http.MethodPost, "/user/code?phone="+url.QueryEscape(fixture.phone), nil, "")
		requireIntegrationFailure(t, throttled)
		fixture.requireUnchanged(t, fixture.codeKey(), before)

		wrong := "000000"
		if code == wrong {
			wrong = "999999"
		}
		badLogin := integrationCall(t, router, http.MethodPost, "/user/login", map[string]string{"phone": fixture.phone, "code": wrong}, "")
		requireIntegrationFailure(t, badLogin)
		fixture.requireUnchanged(t, fixture.codeKey(), before)
		fixture.requireUserCount(t, 0)

		// All requests share one valid code. Real Redis must consume it exactly
		// once before any competing request can create another session.
		const competitors = 12
		start := make(chan struct{})
		results := make(chan integrationReply, competitors)
		var workers sync.WaitGroup
		for range competitors {
			workers.Go(func() {
				<-start
				results <- integrationRequest(router, http.MethodPost, "/user/login", map[string]string{"phone": fixture.phone, "code": code}, "")
			})
		}
		close(start)
		workers.Wait()
		close(results)
		successes := 0
		var token string
		for reply := range results {
			if reply.decodeErr != nil {
				t.Fatal("concurrent login returned an invalid response envelope")
			}
			if reply.Success {
				successes++
				token = fixture.token(t, reply)
			} else if reply.status != http.StatusOK || reply.ErrorMsg == "" {
				t.Fatal("competing code consumption must return a clear business failure")
			}
		}
		if successes != 1 {
			t.Fatal("one verification code must create exactly one successful login")
		}
		fixture.requireMissing(t, fixture.codeKey())
		fixture.requireUserCount(t, 1)
		fixture.requireTTL(t, fixture.sessionKey(token), 25*24*time.Hour-5*time.Second, 25*24*time.Hour)

		userID := fixture.databaseUserID(t)
		for _, header := range []string{token, "Bearer " + token} {
			me := integrationCall(t, router, http.MethodGet, "/user/me", nil, header)
			fixture.requireUser(t, me, userID)
		}
		ctx, cancel := integrationContext()
		fields, err := resources.Redis.HGetAll(ctx, fixture.sessionKey(token)).Result()
		cancel()
		if err != nil || len(fields) != 3 || fields["id"] != userID || fields["icon"] != "" || fields["nickName"] == "" {
			t.Fatal("session must contain only the three Java-compatible string fields")
		}
		fixture.expire(t, fixture.sessionKey(token), 30*time.Second)
		fixture.requireUser(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, token), userID)
		fixture.requireTTL(t, fixture.sessionKey(token), 25*24*time.Hour-5*time.Second, 25*24*time.Hour)

		// Removing only this fixture's throttle allows a second legitimate login
		// without waiting a minute. It must reuse the persisted account.
		fixture.deleteKey(t, fixture.throttleKey())
		secondCode := fixture.requestCode(t, router)
		second := integrationCall(t, router, http.MethodPost, "/user/login", map[string]string{"phone": fixture.phone, "code": secondCode}, "")
		secondToken := fixture.token(t, second)
		if token == secondToken {
			t.Fatal("independent logins must receive independent random sessions")
		}
		fixture.requireUser(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, secondToken), userID)
		fixture.requireUserCount(t, 1)
		replay := integrationCall(t, router, http.MethodPost, "/user/login", map[string]string{"phone": fixture.phone, "code": secondCode}, "")
		requireIntegrationFailure(t, replay)

		logout := integrationCall(t, router, http.MethodPost, "/user/logout", nil, token)
		if logout.status != http.StatusOK || !logout.Success || logout.Data != nil {
			t.Fatal("logout must succeed with the empty success envelope")
		}
		fixture.requireMissing(t, fixture.sessionKey(token))
		requireIntegrationUnauthorized(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, token))
		fixture.requireUser(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, secondToken), userID)
	})

	t.Run("concurrent_first_login_reuses_unique_phone", func(t *testing.T) {
		fixture := newIntegrationFixture(t, resources)
		const competitors = 16
		start := make(chan struct{})
		errors := make(chan error, competitors)
		ids := make(chan string, competitors)
		var workers sync.WaitGroup
		for range competitors {
			workers.Go(func() {
				<-start
				ctx, cancel := integrationContext()
				defer cancel()
				user, err := service.users.FindOrCreate(ctx, fixture.phone, "user_concurrent")
				errors <- err
				ids <- user.ID
			})
		}
		close(start)
		workers.Wait()
		close(errors)
		close(ids)
		for err := range errors {
			if err != nil {
				t.Fatal("concurrent account creation must recover the unique-phone race")
			}
		}
		fixture.requireUserCount(t, 1)
		id := fixture.databaseUserID(t)
		for returnedID := range ids {
			if returnedID != id {
				t.Fatal("all first-login competitors must observe the same database ID")
			}
		}
	})

	t.Run("large_string_id_and_invalid_sessions", func(t *testing.T) {
		fixture := newIntegrationFixture(t, resources)
		const largeID = "9007199254740993"
		token := fixture.seedSession(t, map[string]string{"id": largeID, "nickName": "user_large", "icon": ""})
		me := integrationCall(t, router, http.MethodGet, "/user/me", nil, token)
		fixture.requireUser(t, me, largeID)
		if !bytes.Contains(me.Data, []byte(`"id":"9007199254740993"`)) {
			t.Fatal("large user IDs must remain exact decimal JSON strings")
		}
		for _, fields := range []map[string]string{
			{"id": "1", "icon": ""},
			{"id": "1", "nickName": "missing_icon"},
			{"id": "0", "nickName": "invalid", "icon": ""},
			{"id": "01", "nickName": "invalid", "icon": ""},
			{"id": "-1", "nickName": "invalid", "icon": ""},
			{"id": "18446744073709551616", "nickName": "invalid", "icon": ""},
			{"id": "not-a-number", "nickName": "invalid", "icon": ""},
		} {
			invalidToken := fixture.seedSession(t, fields)
			key := fixture.sessionKey(invalidToken)
			fixture.expire(t, key, 30*time.Second)
			before := fixture.snapshot(t, key)
			denied := integrationCall(t, router, http.MethodGet, "/user/me", nil, invalidToken)
			requireIntegrationSafeRejection(t, denied)
			fixture.requireUnchanged(t, key, before)
		}
		expired := fixture.seedSession(t, map[string]string{"id": "1", "nickName": "expired", "icon": ""})
		ctx, cancel := integrationContext()
		err := resources.Redis.PExpireAt(ctx, fixture.sessionKey(expired), time.Now().Add(-time.Second)).Err()
		cancel()
		if err != nil {
			t.Fatal("cannot arrange an expired session fixture")
		}
		requireIntegrationUnauthorized(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, expired))
		requireIntegrationUnauthorized(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, ""))
		requireIntegrationUnauthorized(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, fixture.randomToken(t)))
	})

	t.Run("production_never_returns_or_persists_a_code", func(t *testing.T) {
		fixture := newIntegrationFixture(t, resources)
		production := integrationRouter(resources, cfg.ProbeTimeout, NewService(resources.ORM, resources.Redis, "production"))
		reply := integrationCall(t, production, http.MethodPost, "/user/code?phone="+url.QueryEscape(fixture.phone), nil, "")
		requireIntegrationFailure(t, reply)
		if reply.Data != nil {
			t.Fatal("production must not return verification-code data")
		}
		fixture.requireMissing(t, fixture.codeKey(), fixture.throttleKey())
		fixture.requireUserCount(t, 0)
		fixture.setString(t, fixture.codeKey(), "123456", time.Minute)
		fixture.setString(t, fixture.throttleKey(), "1", time.Minute)
		beforeCode := fixture.snapshot(t, fixture.codeKey())
		beforeThrottle := fixture.snapshot(t, fixture.throttleKey())
		requireIntegrationFailure(t, integrationCall(t, production, http.MethodPost, "/user/code?phone="+url.QueryEscape(fixture.phone), nil, ""))
		fixture.requireUnchanged(t, fixture.codeKey(), beforeCode)
		fixture.requireUnchanged(t, fixture.throttleKey(), beforeThrottle)
	})

	t.Run("credentials_without_expiry_are_not_accepted", func(t *testing.T) {
		fixture := newIntegrationFixture(t, resources)
		fixture.setString(t, fixture.codeKey(), "123456", 0)
		beforeCode := fixture.snapshot(t, fixture.codeKey())
		requireIntegrationFailure(t, integrationCall(t, router, http.MethodPost, "/user/login", map[string]string{"phone": fixture.phone, "code": "123456"}, ""))
		afterCode := fixture.snapshot(t, fixture.codeKey())
		if afterCode.dump != beforeCode.dump || afterCode.ttl >= 0 {
			t.Fatal("a code without expiry must not be consumed or silently repaired")
		}
		fixture.requireUserCount(t, 0)

		token := fixture.randomToken(t)
		ctx, cancel := integrationContext()
		err := resources.Redis.HSet(ctx, fixture.sessionKey(token), map[string]string{"id": "9007199254740993", "nickName": "permanent", "icon": ""}).Err()
		cancel()
		if err != nil {
			t.Fatal("cannot arrange a session fixture without expiry")
		}
		beforeSession := fixture.snapshot(t, fixture.sessionKey(token))
		requireIntegrationUnauthorized(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, token))
		afterSession := fixture.snapshot(t, fixture.sessionKey(token))
		if afterSession.dump != beforeSession.dump || afterSession.ttl >= 0 {
			t.Fatal("a session without expiry must not authenticate or silently regain a lifetime")
		}
	})

	t.Run("wrong_redis_types_fail_without_partial_writes", func(t *testing.T) {
		for _, operation := range []string{"send_with_code_list", "send_with_throttle_hash", "login_with_code_list", "me_with_session_string"} {
			t.Run(operation, func(t *testing.T) {
				fixture := newIntegrationFixture(t, resources)
				ctx, cancel := integrationContext()
				defer cancel()
				var key, token string
				var seedErr error
				switch operation {
				case "send_with_code_list", "login_with_code_list":
					key = fixture.codeKey()
					seedErr = resources.Redis.RPush(ctx, key, "123456").Err()
				case "send_with_throttle_hash":
					key = fixture.throttleKey()
					seedErr = resources.Redis.HSet(ctx, key, "fixture", "wrong-type").Err()
				case "me_with_session_string":
					token = fixture.randomToken(t)
					key = fixture.sessionKey(token)
					seedErr = resources.Redis.Set(ctx, key, "wrong-type", time.Minute).Err()
				}
				if seedErr != nil {
					t.Fatal("cannot arrange wrong-type Redis fixture")
				}
				fixture.expire(t, key, time.Minute)
				before := fixture.snapshot(t, key)
				var reply integrationReply
				switch operation {
				case "send_with_code_list", "send_with_throttle_hash":
					reply = integrationCall(t, router, http.MethodPost, "/user/code?phone="+url.QueryEscape(fixture.phone), nil, "")
				case "login_with_code_list":
					reply = integrationCall(t, router, http.MethodPost, "/user/login", map[string]string{"phone": fixture.phone, "code": "123456"}, "")
				case "me_with_session_string":
					reply = integrationCall(t, router, http.MethodGet, "/user/me", nil, token)
				}
				requireIntegrationSafeRejection(t, reply)
				fixture.requireUnchanged(t, key, before)
				if key != fixture.codeKey() {
					fixture.requireMissing(t, fixture.codeKey())
				}
				if key != fixture.throttleKey() {
					fixture.requireMissing(t, fixture.throttleKey())
				}
				fixture.requireUserCount(t, 0)
			})
		}
	})

	t.Run("logout_cannot_be_resurrected_by_concurrent_refresh", func(t *testing.T) {
		fixture := newIntegrationFixture(t, resources)
		token := fixture.seedSession(t, map[string]string{"id": "9007199254740993", "nickName": "race", "icon": ""})
		const readers = 8
		start := make(chan struct{})
		firstRequests := make(chan struct{}, readers)
		invalid := make(chan bool, readers)
		var workers sync.WaitGroup
		for range readers {
			workers.Go(func() {
				<-start
				bad := false
				for request := range 30 {
					if request == 0 {
						firstRequests <- struct{}{}
					}
					reply := integrationRequest(router, http.MethodGet, "/user/me", nil, token)
					if reply.decodeErr != nil || (reply.status != http.StatusOK && reply.status != http.StatusUnauthorized) {
						bad = true
					}
				}
				invalid <- bad
			})
		}
		close(start)
		for range readers {
			<-firstRequests
		}
		logout := integrationCall(t, router, http.MethodPost, "/user/logout", nil, token)
		workers.Wait()
		close(invalid)
		if logout.status != http.StatusOK || !logout.Success {
			t.Fatal("concurrent logout must remove the current session")
		}
		for bad := range invalid {
			if bad {
				t.Fatal("concurrent refresh must yield only authenticated or expired responses")
			}
		}
		fixture.requireMissing(t, fixture.sessionKey(token))
		requireIntegrationUnauthorized(t, integrationCall(t, router, http.MethodGet, "/user/me", nil, token))
	})
}

type integrationReply struct {
	Success   bool            `json:"success"`
	ErrorMsg  string          `json:"errorMsg"`
	Data      json.RawMessage `json:"data"`
	status    int
	decodeErr error
}

func integrationRouter(resources *database.Resources, timeout time.Duration, service *Service) *gin.Engine {
	return httpapi.NewRouter(resources, timeout, "integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		Register(routes, service)
	})
}

func integrationRequest(router http.Handler, method, path string, payload any, token string) integrationReply {
	body, err := json.Marshal(payload)
	if err != nil {
		return integrationReply{decodeErr: err}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("authorization", token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var reply integrationReply
	reply.decodeErr = json.Unmarshal(response.Body.Bytes(), &reply)
	reply.status = response.Code
	return reply
}

func integrationCall(t *testing.T, router http.Handler, method, path string, payload any, token string) integrationReply {
	t.Helper()
	reply := integrationRequest(router, method, path, payload, token)
	if reply.decodeErr != nil {
		t.Fatal("HTTP response must contain a valid JSON result envelope")
	}
	return reply
}

func requireIntegrationFailure(t *testing.T, reply integrationReply) {
	t.Helper()
	if reply.status != http.StatusOK || reply.Success || reply.ErrorMsg == "" || reply.Data != nil {
		t.Fatal("request must return an HTTP 200 business failure without data")
	}
}

func requireIntegrationUnauthorized(t *testing.T, reply integrationReply) {
	t.Helper()
	if reply.status != http.StatusUnauthorized || reply.Success || reply.ErrorMsg != "请先登录" || reply.Data != nil {
		t.Fatal("missing or expired identity must return the compatible HTTP 401 envelope")
	}
}

func requireIntegrationSafeRejection(t *testing.T, reply integrationReply) {
	t.Helper()
	if (reply.status != http.StatusUnauthorized && reply.status < 500) || reply.Success || reply.ErrorMsg == "" || reply.Data != nil {
		t.Fatal("invalid Redis data must fail closed without exposing identity or code data")
	}
}

type integrationFixture struct {
	resources *database.Resources
	phone     string
	mu        sync.Mutex
	keys      map[string]struct{}
}

func newIntegrationFixture(t *testing.T, resources *database.Resources) *integrationFixture {
	t.Helper()
	fixture := &integrationFixture{resources: resources, keys: make(map[string]struct{})}
	for range 10 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal("cannot create an independent integration fixture")
		}
		phone := []byte("15900000000")
		for index, value := range random {
			phone[index+3] = '0' + value%10
		}
		fixture.phone = string(phone)
		ctx, cancel := integrationContext()
		var count int64
		dbErr := resources.ORM.WithContext(ctx).Table("tb_user").Where("phone = ?", fixture.phone).Count(&count).Error
		existing, cacheErr := resources.Redis.Exists(ctx, fixture.codeKey(), fixture.throttleKey()).Result()
		cancel()
		if dbErr != nil || cacheErr != nil {
			t.Fatal("cannot check independent fixture ownership")
		}
		if count == 0 && existing == 0 {
			fixture.track(fixture.codeKey(), fixture.throttleKey())
			t.Cleanup(func() {
				ctx, cancel := integrationContext()
				defer cancel()
				fixture.mu.Lock()
				keys := make([]string, 0, len(fixture.keys))
				for key := range fixture.keys {
					keys = append(keys, key)
				}
				fixture.mu.Unlock()
				if len(keys) > 0 && resources.Redis.Del(ctx, keys...).Err() != nil {
					t.Error("cannot clean exact integration-owned Redis keys")
				}
				if resources.ORM.WithContext(ctx).Exec("DELETE FROM tb_user WHERE phone = ?", fixture.phone).Error != nil {
					t.Error("cannot clean the integration-owned account")
				}
			})
			return fixture
		}
	}
	t.Fatal("cannot allocate a fresh independent integration fixture")
	return nil
}

func (fixture *integrationFixture) codeKey() string { return "login:code:" + fixture.phone }
func (fixture *integrationFixture) throttleKey() string {
	return "login:code:throttle:" + fixture.phone
}
func (fixture *integrationFixture) sessionKey(token string) string {
	return "login:token:" + token
}

func (fixture *integrationFixture) track(keys ...string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, key := range keys {
		fixture.keys[key] = struct{}{}
	}
}

func (fixture *integrationFixture) randomToken(t *testing.T) string {
	t.Helper()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("cannot create a session fixture")
	}
	token := hex.EncodeToString(random[:])
	fixture.track(fixture.sessionKey(token))
	return token
}

func (fixture *integrationFixture) token(t *testing.T, reply integrationReply) string {
	t.Helper()
	var token string
	if json.Unmarshal(reply.Data, &token) != nil {
		t.Fatal("successful login must return a token string")
	}
	fixture.track(fixture.sessionKey(token))
	if reply.status != http.StatusOK || !reply.Success || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(token) {
		t.Fatal("successful login must return a random 32-character hexadecimal token")
	}
	return token
}

func (fixture *integrationFixture) requestCode(t *testing.T, router http.Handler) string {
	t.Helper()
	reply := integrationCall(t, router, http.MethodPost, "/user/code?phone="+url.QueryEscape(fixture.phone), nil, "")
	var data struct {
		Code string `json:"code"`
		Mode string `json:"mode"`
	}
	if reply.status != http.StatusOK || !reply.Success || json.Unmarshal(reply.Data, &data) != nil || data.Mode != "development" || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(data.Code) {
		t.Fatal("development code endpoint must return six decimal digits and its explicit mode")
	}
	ctx, cancel := integrationContext()
	stored, err := fixture.resources.Redis.Get(ctx, fixture.codeKey()).Result()
	cancel()
	if err != nil || stored != data.Code {
		t.Fatal("development code must match the isolated Redis fixture")
	}
	return data.Code
}

func (fixture *integrationFixture) seedSession(t *testing.T, fields map[string]string) string {
	t.Helper()
	token := fixture.randomToken(t)
	ctx, cancel := integrationContext()
	defer cancel()
	if fixture.resources.Redis.HSet(ctx, fixture.sessionKey(token), fields).Err() != nil || fixture.resources.Redis.Expire(ctx, fixture.sessionKey(token), time.Minute).Err() != nil {
		t.Fatal("cannot create a Java-compatible session fixture")
	}
	return token
}

func (fixture *integrationFixture) requireUser(t *testing.T, reply integrationReply, id string) {
	t.Helper()
	var user map[string]json.RawMessage
	if reply.status != http.StatusOK || !reply.Success || json.Unmarshal(reply.Data, &user) != nil || len(user) != 3 {
		t.Fatal("authenticated me must return only the three public user fields")
	}
	var actualID, name, icon string
	if json.Unmarshal(user["id"], &actualID) != nil || json.Unmarshal(user["nickName"], &name) != nil || json.Unmarshal(user["icon"], &icon) != nil || actualID != id || name == "" || icon != "" {
		t.Fatal("public user fields must preserve string identity, nickname, and empty icon")
	}
}

func (fixture *integrationFixture) databaseUserID(t *testing.T) string {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	var row struct{ ID uint64 }
	if fixture.resources.ORM.WithContext(ctx).Table("tb_user").Select("id").Where("phone = ?", fixture.phone).Take(&row).Error != nil || row.ID == 0 {
		t.Fatal("login must persist one account with a MySQL-generated ID")
	}
	return strconv.FormatUint(row.ID, 10)
}

func (fixture *integrationFixture) requireUserCount(t *testing.T, want int64) {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	var count int64
	if fixture.resources.ORM.WithContext(ctx).Table("tb_user").Where("phone = ?", fixture.phone).Count(&count).Error != nil || count != want {
		t.Fatal("integration fixture must have the expected unique-phone account count")
	}
}

type integrationSnapshot struct {
	dump string
	ttl  time.Duration
}

func (fixture *integrationFixture) snapshot(t *testing.T, key string) integrationSnapshot {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	dump, err := fixture.resources.Redis.Dump(ctx, key).Result()
	ttl, ttlErr := fixture.resources.Redis.PTTL(ctx, key).Result()
	if err != nil || ttlErr != nil {
		t.Fatal("cannot snapshot integration-owned Redis data")
	}
	return integrationSnapshot{dump: dump, ttl: ttl}
}

func (fixture *integrationFixture) requireUnchanged(t *testing.T, key string, before integrationSnapshot) {
	t.Helper()
	after := fixture.snapshot(t, key)
	if after.dump != before.dump || after.ttl > before.ttl || after.ttl <= 0 {
		t.Fatal("failed operation must preserve existing Redis data and must not renew its expiry")
	}
}

func (fixture *integrationFixture) requireTTL(t *testing.T, key string, minimum, maximum time.Duration) {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	ttl, err := fixture.resources.Redis.PTTL(ctx, key).Result()
	if err != nil || ttl < minimum || ttl > maximum {
		t.Fatal("Redis expiry must match the documented code, throttle, or session lifetime")
	}
}

func (fixture *integrationFixture) requireMissing(t *testing.T, keys ...string) {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	existing, err := fixture.resources.Redis.Exists(ctx, keys...).Result()
	if err != nil || existing != 0 {
		t.Fatal("operation must not retain or create unexpected integration-owned Redis keys")
	}
}

func (fixture *integrationFixture) expire(t *testing.T, key string, ttl time.Duration) {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	if fixture.resources.Redis.Expire(ctx, key, ttl).Err() != nil {
		t.Fatal("cannot arrange integration-owned Redis expiry")
	}
}

func (fixture *integrationFixture) deleteKey(t *testing.T, key string) {
	t.Helper()
	ctx, cancel := integrationContext()
	defer cancel()
	if fixture.resources.Redis.Del(ctx, key).Err() != nil {
		t.Fatal("cannot remove the exact integration-owned key")
	}
}

func (fixture *integrationFixture) setString(t *testing.T, key, value string, ttl time.Duration) {
	t.Helper()
	fixture.track(key)
	ctx, cancel := integrationContext()
	defer cancel()
	if fixture.resources.Redis.Set(ctx, key, value, ttl).Err() != nil {
		t.Fatal("cannot arrange integration-owned Redis string")
	}
}

func integrationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
