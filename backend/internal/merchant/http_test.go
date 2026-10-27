package merchant

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestWorkspaceAuthenticatesWithoutOpeningOtherMerchantRoutes(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "merchant-test", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		Register(routes, NewService(nil), identity)
	})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/merchant/workspace", http.StatusUnauthorized},
		{http.MethodGet, "/merchant/workspace?userId=1&merchantId=1&role=OWNER", http.StatusUnauthorized},
		{http.MethodGet, "/merchant/activities", http.StatusNotFound},
		{http.MethodPost, "/merchant/activities", http.StatusNotFound},
		{http.MethodGet, "/merchant/orders", http.StatusNotFound},
		{http.MethodPost, "/merchant/orders/redeem", http.StatusNotFound},
		{http.MethodGet, "/merchant/audits", http.StatusNotFound},
		{http.MethodGet, "/merchant/knowledge", http.StatusNotFound},
		{http.MethodPost, "/merchant/workspace", http.StatusMethodNotAllowed},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("userId", "1")
		request.Header.Set("role", "OWNER")
		router.ServeHTTP(response, request)
		var result struct {
			Success  bool            `json:"success"`
			ErrorMsg string          `json:"errorMsg"`
			Data     json.RawMessage `json:"data"`
		}
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Success || result.ErrorMsg == "" || result.Data != nil {
			t.Fatalf("%s %s must reject with the expected safe error envelope", test.method, test.path)
		}
	}
}
