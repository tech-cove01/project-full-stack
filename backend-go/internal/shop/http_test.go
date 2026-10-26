package shop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

type repositoryStub struct {
	list     func(context.Context, Query) ([]Shop, uint64, error)
	detail   func(context.Context, uint64) (*Shop, error)
	types    func(context.Context) ([]ShopType, error)
	vouchers func(context.Context, uint64) ([]Voucher, error)
}

func (store repositoryStub) List(ctx context.Context, query Query) ([]Shop, uint64, error) {
	return store.list(ctx, query)
}
func (store repositoryStub) Detail(ctx context.Context, id uint64) (*Shop, error) {
	return store.detail(ctx, id)
}
func (store repositoryStub) Types(ctx context.Context) ([]ShopType, error) {
	return store.types(ctx)
}
func (store repositoryStub) Vouchers(ctx context.Context, id uint64) ([]Voucher, error) {
	return store.vouchers(ctx, id)
}

func testRouter(store repository) *gin.Engine {
	return httpapi.NewRouter(nil, time.Second, "shop-unit", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		Register(routes, &Service{store: store})
	})
}

type reply struct {
	Success  bool            `json:"success"`
	ErrorMsg string          `json:"errorMsg"`
	Data     json.RawMessage `json:"data"`
	Total    *string         `json:"total"`
}

func request(t *testing.T, router http.Handler, method, path string) (int, reply) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	var body reply
	if json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal("public operation must return a JSON Result")
	}
	return response.Code, body
}

func TestPublicListBindsCombinedFiltersAndKeepsLegacyDefaultSizes(t *testing.T) {
	for _, test := range []struct {
		path                  string
		name                  string
		typeID, current, size uint64
	}{
		{"/shop/of/name", "", 0, 1, 10},
		{"/shop/of/type?typeId=2", "", 2, 1, 5},
		{"/shop/of/name?name=%20%E5%92%96%E5%95%A1%20&typeId=9007199254740993&current=6&pageSize=50", "咖啡", 9007199254740993, 6, 50},
		{"/shop/of/type?typeId=2&name=%E5%92%96%E5%95%A1&current=18446744073709551615&pageSize=1", "咖啡", 2, 18446744073709551615, 1},
	} {
		t.Run(test.path, func(t *testing.T) {
			store := repositoryStub{list: func(_ context.Context, query Query) ([]Shop, uint64, error) {
				if query.Name != test.name || query.Current != test.current || query.PageSize != test.size || (test.typeID == 0 && query.TypeID != nil) || (test.typeID != 0 && (query.TypeID == nil || *query.TypeID != test.typeID)) {
					t.Fatal("public filter parsing differs from the combined browsing contract")
				}
				return nil, 0, nil
			}}
			status, body := request(t, testRouter(store), http.MethodGet, test.path)
			if status != http.StatusOK || !body.Success || string(body.Data) != "[]" || body.Total == nil || *body.Total != "0" {
				t.Fatal("empty public page must contain an empty array and string total")
			}
		})
	}
}

func TestInvalidFiltersAndUnsupportedCoordinatesFailBeforeSQL(t *testing.T) {
	router := testRouter(nil)
	for _, path := range []string{
		"/shop/of/name?current=0", "/shop/of/name?current=-1", "/shop/of/name?current=1.5",
		"/shop/of/name?current=", "/shop/of/name?current=1&current=2", "/shop/of/name?current=18446744073709551616",
		"/shop/of/name?pageSize=0", "/shop/of/name?pageSize=51", "/shop/of/name?pageSize=1&pageSize=2",
		"/shop/of/name?typeId=0", "/shop/of/name?typeId=", "/shop/of/name?typeId=abc",
		"/shop/of/type", "/shop/of/type?typeId=1&typeId=2", "/shop/0", "/shop/not-an-id", "/voucher/list/-1",
		"/shop/of/name?name=" + url.QueryEscape(strings.Repeat("店", 129)),
	} {
		status, body := request(t, router, http.MethodGet, path)
		if status != http.StatusBadRequest || body.Success || body.ErrorMsg == "" || body.Data != nil {
			t.Fatal("invalid browsing parameters must fail before database access")
		}
	}
	for _, suffix := range []string{"&x=120&y=30", "&x=", "&y=30"} {
		status, body := request(t, router, http.MethodGet, "/shop/of/type?typeId=1"+suffix)
		if status != http.StatusBadRequest || body.ErrorMsg != "附近坐标查询尚未迁移" {
			t.Fatal("legacy coordinates must be explicitly rejected rather than silently ignored")
		}
	}
}

func TestClosedWritesAreForbiddenAndUnknownRoutesStayUnavailable(t *testing.T) {
	router := testRouter(nil)
	for _, path := range []string{"/shop", "/shop/1", "/shop/of/name", "/shop/of/type", "/shop-type", "/shop-type/list", "/voucher", "/voucher/seckill", "/voucher/list/1", "/%73hop", "/%76oucher/seckill"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			status, body := request(t, router, method, path)
			if status != http.StatusForbidden || body.Success || body.ErrorMsg != "商家写接口暂未开放" {
				t.Fatalf("known legacy write was not forbidden: method=%s path=%s", method, path)
			}
		}
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/shop/1/unknown"}, {http.MethodPost, "/shop-type/unknown"},
		{http.MethodPost, "/voucher/unknown"}, {http.MethodGet, "/voucher/123"},
		{http.MethodGet, "/shop/1/unknown"}, {http.MethodPost, "/shop;matrix=1"},
	} {
		status, _ := request(t, router, test.method, test.path)
		if status != http.StatusNotFound {
			t.Fatal("unimplemented paths must keep 404 behavior")
		}
	}
}

func TestMissingShopAndEmptyCategoryOrVoucherListsRemainDistinct(t *testing.T) {
	router := testRouter(repositoryStub{
		detail:   func(context.Context, uint64) (*Shop, error) { return nil, nil },
		types:    func(context.Context) ([]ShopType, error) { return nil, nil },
		vouchers: func(context.Context, uint64) ([]Voucher, error) { return nil, nil },
	})
	status, body := request(t, router, http.MethodGet, "/shop/9007199254740993")
	if status != http.StatusOK || body.Success || body.ErrorMsg != "店铺不存在！" || body.Data != nil {
		t.Fatal("nonexistent shop must preserve its business error contract")
	}
	for _, path := range []string{"/shop-type/list", "/voucher/list/9007199254740993"} {
		status, body := request(t, router, http.MethodGet, path)
		if status != http.StatusOK || !body.Success || string(body.Data) != "[]" || body.Total != nil {
			t.Fatal("empty category/voucher lists must be arrays without a fabricated total")
		}
	}
}

func TestDatabaseErrorsStaySafeAndRequestCancellationPropagates(t *testing.T) {
	failure := errors.New("private DSN password and SQL")
	router := testRouter(repositoryStub{
		list:     func(context.Context, Query) ([]Shop, uint64, error) { return nil, 0, failure },
		detail:   func(context.Context, uint64) (*Shop, error) { return nil, failure },
		types:    func(context.Context) ([]ShopType, error) { return nil, failure },
		vouchers: func(context.Context, uint64) ([]Voucher, error) { return nil, failure },
	})
	for _, path := range []string{"/shop/of/name", "/shop/1", "/shop-type/list", "/voucher/list/1"} {
		status, body := request(t, router, http.MethodGet, path)
		if status != http.StatusServiceUnavailable || body.Success || strings.Contains(body.ErrorMsg, "private") || body.Data != nil {
			t.Fatal("dependency errors must return safe 5xx envelopes")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/shop/of/name", nil).WithContext(ctx)
	bounded, finish := browseContext(c)
	defer finish()
	cancel()
	if !errors.Is(bounded.Err(), context.Canceled) {
		t.Fatal("browse work did not inherit HTTP request cancellation")
	}
}
