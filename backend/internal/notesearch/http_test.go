package notesearch

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicSearchInputAndDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, NewService(nil, nil))
	for _, item := range []struct {
		path   string
		status int
	}{{"/notes/search", 400}, {"/notes/search?q=coffee&limit=0", 400}, {"/notes/search?q=coffee&limit=21", 400}, {"/notes/search?q=coffee&sort=bad", 400}, {"/notes/search?q=coffee&shopId=01", 400}, {"/notes/search?q=coffee", 503}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", item.path, nil))
		if response.Code != item.status {
			t.Fatalf("%s status%d want%d", item.path, response.Code, item.status)
		}
	}
}
