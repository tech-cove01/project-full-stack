package social

import (
	"context"
	"encoding/json"
	"errors"
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

func TestSocialRoutesRequireLogin(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "social-test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
			Register(routes, NewService(nil), identity)
		})
	for _, test := range []struct {
		method, path string
	}{
		{http.MethodPut, "/authors/2/follow"},
		{http.MethodGet, "/authors/2/relationship"},
		{http.MethodGet, "/feed/notes"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		var body httpapi.Result
		if response.Code != http.StatusUnauthorized || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Success {
			t.Fatalf("%s %s must require a session, got %d", test.method, test.path, response.Code)
		}
	}
}

func TestSetFollowRejectsSelfAndBadIdsBeforeSQL(t *testing.T) {
	service := NewService(nil)
	if _, err := service.SetFollow(context.Background(), "1", "1", true); !errors.Is(err, ErrSelf) {
		t.Fatal("following yourself must be rejected")
	}
	for _, pair := range [][2]string{{"0", "2"}, {"01", "2"}, {"1", "x"}, {"1", "0"}} {
		if _, err := service.SetFollow(context.Background(), pair[0], pair[1], true); !errors.Is(err, ErrInput) {
			t.Fatalf("ids %v must be rejected", pair)
		}
	}
}

func TestFeedCursorRequiresBothHalvesAndABoundedLimit(t *testing.T) {
	service := NewService(nil)
	for _, test := range []struct {
		cursorTime, cursorID string
		limit                int
	}{
		{"2026-10-27 10:00:00", "", 10},
		{"", "42", 10},
		{"not-a-time", "42", 10},
		{"2026-10-27 10:00:00", "0", 10},
		{"2026-10-27 10:00:00", "42", 51},
	} {
		if _, err := service.Feed(context.Background(), "1", test.cursorTime, test.cursorID,
			test.limit); !errors.Is(err, ErrPagination) {
			t.Fatalf("cursor %q/%q limit %d must be rejected",
				test.cursorTime, test.cursorID, test.limit)
		}
	}
}

func TestEventIDIsStablePerVersion(t *testing.T) {
	first := eventID(1, 2, 3)
	if first != "FOLLOW:1:2:3" {
		t.Fatalf("event id shape changed: %s", first)
	}
	if first == eventID(1, 2, 4) {
		t.Fatal("a new version must produce a new event id")
	}
}
