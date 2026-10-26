package order

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestOrderViewSeparatesPendingFromDurableSnapshotsAndTokens(t *testing.T) {
	encoded, _ := json.Marshal(View{ID: "9007199254740993", State: Processing, Message: "订单处理中"})
	if string(encoded) != `{"id":"9007199254740993","state":"PROCESSING","message":"订单处理中","orderStatus":null}` {
		t.Fatal("pending must include null orderStatus without fabricated snapshots")
	}
	token := "owned-fixture-token"
	now := time.Date(2026, 10, 26, 1, 2, 3, 0, time.UTC)
	row := record{ID: math.MaxInt64, ShopID: math.MaxUint64, PayAmount: math.MaxUint64, Status: 2, RedeemToken: &token, CreateTime: now, PayDeadlineAt: now}
	detail := toView(row, true)
	list := toView(row, false)
	if detail.RedeemToken == nil || list.RedeemToken != nil || detail.ID != "9223372036854775807" || *detail.ShopID != "18446744073709551615" || *detail.PayAmount != "18446744073709551615" || *detail.CreateTime != "2026-10-26 09:02:03" {
		t.Fatal("owned detail, list privacy or exact ID/money/time contract changed")
	}
	for _, status := range []int{1, 3, 4, 6} {
		row.Status = status
		if view := toView(row, true); view.State != Created || view.OrderStatus == nil || *view.OrderStatus != status || view.RedeemToken != nil {
			t.Fatal("current DB terminal state must remain CREATED without a token")
		}
	}
}

func TestOrderIdentifiersAndPagingRejectOverflow(t *testing.T) {
	for _, id := range []string{"", "0", "01", "+1", "-1", "1.0", "9223372036854775808"} {
		if _, err := orderID(id); err == nil {
			t.Fatal("signed BIGINT order ID boundary was not enforced")
		}
	}
	if id, err := orderID("9223372036854775807"); err != nil || id != math.MaxInt64 {
		t.Fatal("valid signed BIGINT boundary must remain exact")
	}
	if _, present := pageOffset(Query{Current: math.MaxUint64, PageSize: 50}, 53); present {
		t.Fatal("far page overflow must return empty")
	}
	if n, present := pageOffset(Query{Current: 6, PageSize: 10}, 53); !present || n != 50 {
		t.Fatal("stable last page offset changed")
	}
}

func TestOrderRoutesAuthenticateAndLeaveFutureWritesClosed(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "order-test", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) { Register(routes, NewService(nil, nil, 15*time.Minute), identity) })
	for _, test := range []struct {
		method, path string
		status       int
	}{{http.MethodPost, "/voucher-order/seckill/1", 401}, {http.MethodGet, "/voucher-order/1", 401}, {http.MethodGet, "/voucher-order", 401}, {http.MethodPost, "/voucher-order/1/pay", 401}, {http.MethodPost, "/voucher-order/1/cancel", 404}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"userId":"1"}`))
		request.Header.Set("userId", "1")
		router.ServeHTTP(response, request)
		var result httpapi.Result
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Success || result.Data != nil {
			t.Fatal("forged identity or an unmigrated lifecycle write was accepted")
		}
	}
	for _, query := range []string{"current=0", "pageSize=51", "pageSize=2&pageSize=3", "current=01"} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		if _, err := parseQuery(ctx); err == nil {
			t.Fatal("invalid pagination must fail before SQL")
		}
	}
}
