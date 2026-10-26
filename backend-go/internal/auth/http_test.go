package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func unitRouter(service *Service) *gin.Engine {
	return httpapi.NewRouter(nil, time.Second, "auth-unit", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		Register(routes, service)
	})
}

func unitRequest(t *testing.T, router http.Handler, method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("authorization", token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal("response was not a JSON object")
	}
	return response, envelope
}

func TestUserRoutesAuthenticateOnlyMigratedProtectedOperations(t *testing.T) {
	router := unitRouter(&Service{})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/user/me", http.StatusUnauthorized},
		{http.MethodPost, "/user/logout", http.StatusUnauthorized},
		{http.MethodGet, "/user/123", http.StatusNotFound},
		{http.MethodPost, "/user/sign", http.StatusNotFound},
		{http.MethodGet, "/merchant/workspace", http.StatusNotFound},
		{http.MethodPost, "/user/me", http.StatusMethodNotAllowed},
	} {
		response, body := unitRequest(t, router, test.method, test.path, "", "")
		if response.Code != test.status || body["success"] != false || body["errorMsg"] == nil {
			t.Fatalf("unexpected protected/unsupported route behavior: %s %s status=%d", test.method, test.path, response.Code)
		}
		if _, present := body["data"]; present {
			t.Fatal("error envelope must omit null data")
		}
	}
}

func TestMeAcceptsRawOrBearerAndKeepsLargeIDAsString(t *testing.T) {
	token := strings.Repeat("a", 32)
	identity := User{ID: "18446744073709551615", NickName: "fixture", Icon: ""}
	service := &Service{sessions: sessionStub{refresh: func(_ context.Context, presented string) (*User, error) {
		if presented != token {
			return nil, nil
		}
		return &identity, nil
	}}}
	router := unitRouter(service)
	for _, header := range []string{token, "Bearer " + token} {
		response, body := unitRequest(t, router, http.MethodGet, "/user/me?userId=1&role=OWNER", header, "")
		user, ok := body["data"].(map[string]any)
		if response.Code != http.StatusOK || body["success"] != true || !ok || user["id"] != identity.ID || user["icon"] != "" {
			t.Fatal("me must use the server identity with exact decimal string ID and empty icon")
		}
		if len(user) != 3 {
			t.Fatal("me exposed fields outside the public User DTO")
		}
	}
	for _, header := range []string{"bearer " + token, "Bearer  " + token, token + " ", "not-a-token"} {
		response, _ := unitRequest(t, router, http.MethodGet, "/user/me", header, "")
		if response.Code != http.StatusUnauthorized {
			t.Fatal("noncontract token header format must be unauthorized")
		}
	}
}

func TestLogoutDeletesExactlyPresentedSessionAndOmitsNullData(t *testing.T) {
	token := strings.Repeat("b", 32)
	deleted := ""
	service := &Service{sessions: sessionStub{
		refresh: func(context.Context, string) (*User, error) { return &User{ID: "1", NickName: "fixture"}, nil },
		delete:  func(_ context.Context, key string) error { deleted = key; return nil },
	}}
	response, body := unitRequest(t, unitRouter(service), http.MethodPost, "/user/logout", "Bearer "+token, "")
	if response.Code != http.StatusOK || body["success"] != true || len(body) != 1 || deleted != token {
		t.Fatal("logout must revoke only the current token and return the Java NON_NULL envelope")
	}
}

func TestLoginBodyIsBoundedAndIgnoresClientIdentityFields(t *testing.T) {
	service := &Service{
		users: usersFunc(func(_ context.Context, phone, _ string) (User, error) {
			if phone != "13800000000" {
				t.Fatal("login did not bind the validated phone")
			}
			return User{ID: "42", NickName: "server"}, nil
		}),
		sessions: sessionStub{
			consume: func(context.Context, string, string) (bool, error) { return true, nil },
			create: func(_ context.Context, _ string, user User) (bool, error) {
				if user.ID != "42" {
					t.Fatal("client JSON overrode the database identity")
				}
				return true, nil
			},
		},
	}
	router := unitRouter(service)
	response, body := unitRequest(t, router, http.MethodPost, "/user/login", "", `{"phone":"13800000000","code":"123456","id":"1","userId":"1","role":"OWNER"}`)
	if response.Code != http.StatusOK || body["success"] != true {
		t.Fatal("legacy login JSON must accept unrelated fields without using them as identity")
	}
	for _, malformed := range []string{`{`, `{"phone":1}`, `{} {}`, `{"extra":"` + strings.Repeat("x", 4096) + `"}`} {
		response, _ := unitRequest(t, router, http.MethodPost, "/user/login", "", malformed)
		if response.Code != http.StatusBadRequest {
			t.Fatal("malformed or oversized login JSON must be rejected")
		}
	}
}

func TestPublicErrorsNeverExposeStorageDiagnostics(t *testing.T) {
	service := &Service{sessions: sessionStub{refresh: func(context.Context, string) (*User, error) {
		return nil, errors.New("private-password private-SQL")
	}}}
	response, body := unitRequest(t, unitRouter(service), http.MethodGet, "/user/me", strings.Repeat("c", 32), "")
	if response.Code != http.StatusServiceUnavailable || body["success"] != false || strings.Contains(response.Body.String(), "private") {
		t.Fatal("storage failures must be safe 5xx envelopes")
	}
}

func TestCurrentUserReturnsValueCopyAndRequestContextCancels(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if _, ok := CurrentUser(c); ok {
		t.Fatal("request began with an unexpected identity")
	}
	c.Set(identityKey, User{ID: "42", NickName: "server"})
	user, ok := CurrentUser(c)
	user.ID = "1"
	unchanged, _ := CurrentUser(c)
	if !ok || unchanged.ID != "42" {
		t.Fatal("request identity must be returned by value")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodGet, "/user/me", nil).WithContext(ctx)
	bounded, finish := requestContext(c)
	defer finish()
	cancel()
	if !errors.Is(bounded.Err(), context.Canceled) {
		t.Fatal("auth work must inherit HTTP cancellation")
	}
}
