package merchantorder

import (
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

func TestMaskPhoneNeverEchoesShortValues(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"13800000000", "138****0000"},
		{"1234567", "123****4567"},
		{"123456", ""},
		{"", ""},
	} {
		if got := maskPhone(test.input); got != test.want {
			t.Fatalf("maskPhone(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestParseQueryRejectsValuesOutsideTheContract(t *testing.T) {
	for _, query := range []Query{
		{Current: 1, PageSize: 51},
		{Current: 1, PageSize: 10, Status: "5"},
		{Current: 1, PageSize: 10, Status: "0"},
		{Current: 1, PageSize: 10, OrderID: "0"},
		{Current: 1, PageSize: 10, OrderID: "1e3"},
		{Current: 1, PageSize: 10, ShopID: "-1"},
	} {
		if _, err := parseQuery(query); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("query %+v must be rejected", query)
		}
	}
	valid, err := parseQuery(Query{Current: 2, PageSize: 50, Status: "2", OrderID: "42", ShopID: "3"})
	if err != nil || valid.Current != 2 || valid.PageSize != 50 {
		t.Fatalf("valid query was rejected: %+v", valid)
	}
	defaults, err := parseQuery(Query{})
	if err != nil || defaults.Current != 1 || defaults.PageSize != 10 {
		t.Fatalf("zero pagination must fall back to the documented defaults: %+v", defaults)
	}
}

func TestPageOffsetKeepsEmptyPagesPredictable(t *testing.T) {
	if _, present := pageOffset(Query{Current: 1, PageSize: 10}, 0); present {
		t.Fatal("an empty result must not produce a page")
	}
	if _, present := pageOffset(Query{Current: 2, PageSize: 10}, 5); present {
		t.Fatal("a page beyond the total must not produce an offset")
	}
	offset, present := pageOffset(Query{Current: 3, PageSize: 10}, 21)
	if !present || offset != 20 {
		t.Fatalf("page 3 of 21 rows must start at 20, got %d", offset)
	}
}

func TestPlaceholdersNeverProduceEmptyTuples(t *testing.T) {
	if placeholders(0) != "" || placeholders(1) != "?" || placeholders(3) != "?,?,?" {
		t.Fatal("placeholder generation changed shape")
	}
}

func TestMerchantListsRequireLogin(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "merchantorder-test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
			Register(routes, NewService(nil), identity)
		})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/merchant/orders", http.StatusUnauthorized},
		{http.MethodGet, "/merchant/audits", http.StatusUnauthorized},
		{http.MethodGet, "/merchant/orders?userId=1&shopId=1", http.StatusUnauthorized},
		{http.MethodPost, "/merchant/orders", http.StatusMethodNotAllowed},
		{http.MethodPost, "/merchant/orders/redeem", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("userId", "1")
		router.ServeHTTP(response, request)
		var result struct {
			Success  bool            `json:"success"`
			ErrorMsg string          `json:"errorMsg"`
			Data     json.RawMessage `json:"data"`
		}
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &result) != nil ||
			result.Success || result.ErrorMsg == "" || result.Data != nil {
			t.Fatalf("%s %s must fail with a safe envelope, got %d", test.method, test.path, response.Code)
		}
	}
}
