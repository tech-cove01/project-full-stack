package aiknowledge

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

func TestKnowledgeRoutesRequireLogin(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "aiknowledge-test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
			Register(routes, NewService(nil, nil), identity)
		})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/merchant/knowledge?shopId=1", http.StatusUnauthorized},
		{http.MethodPost, "/merchant/knowledge", http.StatusUnauthorized},
		{http.MethodPut, "/merchant/knowledge/1", http.StatusUnauthorized},
		{http.MethodPost, "/merchant/knowledge/1/publish", http.StatusUnauthorized},
		{http.MethodPost, "/merchant/knowledge/1/disable", http.StatusUnauthorized},
		{http.MethodGet, "/merchant/knowledge", http.StatusUnauthorized},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("userId", "1")
		router.ServeHTTP(response, request)
		var body httpapi.Result
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Success {
			t.Fatalf("%s %s must reject without a session, got %d", test.method, test.path, response.Code)
		}
	}
}

func TestCleanTextRejectsBlankAndOversizedInput(t *testing.T) {
	if _, err := cleanText("   ", maxTitleRunes); err != ErrInput {
		t.Fatal("a blank title must be rejected")
	}
	long := ""
	for i := 0; i < maxTitleRunes+1; i++ {
		long += "标"
	}
	if _, err := cleanText(long, maxTitleRunes); err != ErrInput {
		t.Fatal("an oversized title must be rejected instead of truncated silently")
	}
	if clean, err := cleanText("  核销规则  ", maxTitleRunes); err != nil || clean != "核销规则" {
		t.Fatal("valid text must be trimmed and kept")
	}
}

func TestPositiveIDRejectsNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"", "0", "01", "-1", "1e3", " 1"} {
		if _, err := positive(value); err == nil {
			t.Fatalf("%q must not be accepted as an identifier", value)
		}
	}
	if id, err := positive("42"); err != nil || id != 42 {
		t.Fatal("a canonical id must parse")
	}
}
