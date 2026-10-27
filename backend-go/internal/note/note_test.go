package note

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

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestNoteRoutesRequireLoginForWritesAndKeepDetailPublic(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "note-test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
			Register(routes, NewService(nil), identity)
		})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/notes", nil))
	var body httpapi.Result
	if response.Code != http.StatusUnauthorized || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Success {
		t.Fatalf("creating a note must require a session, got %d", response.Code)
	}
	// The detail route is public; a forged path parameter is a 404, not a 401.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/notes/0", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("an invalid note id must be 404 without a session, got %d", response.Code)
	}
}

func TestCreateRejectsInvalidInputBeforeStorage(t *testing.T) {
	service := NewService(nil)
	for _, request := range []CreateRequest{
		{Title: "   ", Content: "正文"},
		{Title: "标题", Content: ""},
		{Title: "标题", Content: "正文", Images: []string{"javascript:alert(1)"}},
		{Title: "标题", Content: "正文", Images: make([]string, maxImages+1)},
	} {
		if _, err := service.Create(context.Background(), "1", request); !errors.Is(err, ErrInput) {
			t.Fatalf("request %+v must be rejected before SQL", request)
		}
	}
	if _, err := service.Create(context.Background(), "not-a-number", CreateRequest{Title: "t", Content: "c"}); !errors.Is(err, ErrInput) {
		t.Fatal("a malformed session id must be rejected")
	}
}

func TestImageWhitelistAcceptsOnlyHttpURLs(t *testing.T) {
	images, err := validateImages([]string{"https://cdn.example.com/a.jpg", " http://cdn.example.com/b.png "})
	if err != nil || len(images) != 2 || images[1] != "http://cdn.example.com/b.png" {
		t.Fatalf("valid image URLs must be trimmed and kept, got %v %v", images, err)
	}
	for _, raw := range [][]string{
		{"ftp://cdn.example.com/a.jpg"},
		{""},
		{strings.Repeat("https://example.com/", 40)},
	} {
		if _, err = validateImages(raw); !errors.Is(err, ErrInput) {
			t.Fatalf("images %v must be rejected", raw)
		}
	}
}

func TestDetailRejectsNonCanonicalIDsWithoutSQL(t *testing.T) {
	service := NewService(nil)
	for _, value := range []string{"", "0", "01", "-1", "abc"} {
		if _, err := service.Detail(context.Background(), value); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %q must be a 404 before SQL", value)
		}
	}
}
