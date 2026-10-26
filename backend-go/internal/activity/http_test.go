package activity

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

func TestActivityRoutesAuthenticateAndKeepRecoveryClosed(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "activity-test", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { Register(routes, NewService(nil, nil), identity) })
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/merchant/activities", 401}, {http.MethodPost, "/merchant/activities", 401},
		{http.MethodPost, "/merchant/activities/1/retry-publication", 401}, {http.MethodPost, "/merchant/activities/1/offline", 401},
		{http.MethodPost, "/merchant/activities/1/reconcile?confirm=true", 404},
		{http.MethodGet, "/merchant/orders", 404}, {http.MethodGet, "/merchant/audits", 404},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("role", "OWNER")
		request.Header.Set("userId", "1")
		router.ServeHTTP(response, request)
		var body httpapi.Result
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Success || body.Data != nil {
			t.Fatalf("%s %s must reject forged identity with safe JSON", test.method, test.path)
		}
	}
}

func TestStrictCreateDecoderRejectsExtraWritableIdentityAndInvalidStock(t *testing.T) {
	base, _ := json.Marshal(validRequest())
	for _, body := range []string{
		strings.TrimSuffix(string(base), "}") + `,"merchantId":"1"}`,
		strings.TrimSuffix(string(base), "}") + `,"operatorUserId":"1"}`,
		strings.TrimSuffix(string(base), "}") + `,"title":"duplicate"}`,
		strings.Replace(string(base), `"shopId"`, `"ShopID"`, 1),
		strings.Replace(string(base), `"totalStock":100`, `"totalStock":1.5`, 1),
		strings.Replace(string(base), `"payValue":"1900"`, `"payValue":1900`, 1),
		string(base) + `{}`, strings.Repeat(" ", 16385) + string(base),
	} {
		request := httptest.NewRequest(http.MethodPost, "/merchant/activities", strings.NewReader(body))
		if _, err := decodeCreate(httptest.NewRecorder(), request); err == nil {
			t.Fatal("malformed or extra writable input must be rejected")
		}
	}
}

func TestActivityQueryFiltersValidateBeforeSQL(t *testing.T) {
	for _, query := range []string{"current=0", "current=1&current=2", "pageSize=51", "pageSize=-1", "shopId=01", "status=HIDDEN", "status=", "shopId=1&shopId=2"} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		if _, err := parseQuery(ctx); err == nil {
			t.Fatal("invalid pagination/filter must fail")
		}
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/?current=18446744073709551615&pageSize=50&shopId=9007199254740993&status=ACTIVE", nil)
	query, err := parseQuery(ctx)
	if err != nil || query.ShopID != "9007199254740993" || query.Status != "ACTIVE" {
		t.Fatal("valid large IDs and safe far pagination must stay exact")
	}
}
