package order

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestRedeemTokenIsCryptographic32ByteHex(t *testing.T) {
	first, err := secureRedeemToken()
	if err != nil {
		t.Fatal("token entropy failed")
	}
	second, err := secureRedeemToken()
	if err != nil {
		t.Fatal("token entropy failed")
	}
	decoded, err := hex.DecodeString(first)
	if err != nil || len(decoded) != 32 || len(first) != 64 || first == second {
		t.Fatal("tokens must preserve 256-bit random input as distinct 64-character hex")
	}
}

func TestPayAuthenticatesAndLifecycleConflictHasSafeEnvelope(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "pay-test", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { Register(routes, NewService(nil, nil, 15*time.Minute), identity) })
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/voucher-order/1/pay", nil)
	request.Header.Set("userId", "1")
	router.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("request headers cannot manufacture a paying identity")
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	writeError(ctx, ErrConflict)
	if ctx.Writer.Status() != http.StatusConflict {
		t.Fatal("state/deadline conflicts require HTTP 409")
	}
	encoded, _ := json.Marshal(toView(record{ID: 1, Status: 4}, true))
	var body map[string]any
	_ = json.Unmarshal(encoded, &body)
	if _, present := body["redeemToken"]; present {
		t.Fatal("terminal state cannot expose a redeem token")
	}
}

func TestCancellationReasonUsesUnicodeCharactersAndDefault(t *testing.T) {
	if reason, err := cancelReason(" \t\u3000"); err != nil || reason != "USER_CANCEL" {
		t.Fatal("blank cancellation reason must receive the stable default")
	}
	reason, err := cancelReason("  " + strings.Repeat("猫🐱中", 30) + "  ")
	if err != nil || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) != 64 || reason != string([]rune(strings.Repeat("猫🐱中", 30))[:64]) {
		t.Fatal("reason truncation must preserve exactly 64 Unicode characters")
	}
	if _, err := cancelReason(string([]byte{0xff})); !errors.Is(err, ErrReason) {
		t.Fatal("invalid UTF8 must fail before DB changes")
	}
}
