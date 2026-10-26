package shop

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// This suite requires 001-schema.sql and the explicit versioned migrations
// (including 003-trade.sql). It performs no DDL, global deletes or Redis writes.
// A real >2^53 ID advances the isolated table's AUTO_INCREMENT high-water mark;
// cleanup removes only exact fixture rows and never resets database sequences.
func TestIntegrationShop(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MYSQL_* and REDIS_* dependencies")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("integration configuration is incomplete or invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	resources, err := database.Open(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal("cannot open explicitly configured integration dependencies")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("integration dependency cleanup failed")
		}
	})
	router := httpapi.NewRouter(resources, cfg.ProbeTimeout, "shop-integration", slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
		Register(routes, NewService(resources.ORM, cfg.Location))
	})
	fixture := newShopFixture(t, resources, cfg.Location)

	t.Run("all_53_shops_are_reachable_without_duplicate_pages", func(t *testing.T) {
		var loaded []uint64
		for page := 1; page <= 7; page++ {
			path := "/shop/of/name?name=" + url.QueryEscape(fixture.prefix) + "&current=" + strconv.Itoa(page)
			status, body := request(t, router, http.MethodGet, path)
			rows := requireShopArray(t, status, body)
			if body.Total == nil || *body.Total != "53" {
				t.Fatal("every page must report the same exact matching-row string total")
			}
			expectedSize := 10
			if page == 6 {
				expectedSize = 3
			} else if page == 7 {
				expectedSize = 0
			}
			if len(rows) != expectedSize {
				t.Fatal("default ten-item paging must expose all 53 fixture shops and an empty final page")
			}
			for _, row := range rows {
				id := shopString(t, row, "id")
				parsed, err := strconv.ParseUint(id, 10, 64)
				if err != nil || parsed <= 1<<53 {
					t.Fatal("real large shop IDs must remain exact decimal strings")
				}
				loaded = append(loaded, parsed)
			}
		}
		if !reflect.DeepEqual(loaded, fixture.shopIDs) {
			t.Fatal("stable ID ordering must page through all fixture shops without duplicates or omissions")
		}
		path := "/shop/of/name?name=" + url.QueryEscape(fixture.prefix) + "&current=18446744073709551615&pageSize=50"
		status, body := request(t, router, http.MethodGet, path)
		if len(requireShopArray(t, status, body)) != 0 || body.Total == nil || *body.Total != "53" {
			t.Fatal("a valid enormous page number must return an empty page without offset overflow")
		}
	})

	t.Run("chinese_search_and_category_use_one_intersection", func(t *testing.T) {
		name := fixture.prefix + "交集"
		var wanted []string
		for index, shop := range fixture.shops {
			if strings.Contains(shop.name, name) && shop.typeID == fixture.typeIDs[0] {
				wanted = append(wanted, strconv.FormatUint(fixture.shopIDs[index], 10))
			}
		}
		for _, route := range []string{"/shop/of/name", "/shop/of/type"} {
			path := route + "?name=" + url.QueryEscape(name) + "&typeId=" + strconv.FormatUint(fixture.typeIDs[0], 10) + "&pageSize=50"
			status, body := request(t, router, http.MethodGet, path)
			rows := requireShopArray(t, status, body)
			var actual []string
			for _, row := range rows {
				actual = append(actual, shopString(t, row, "id"))
				if shopString(t, row, "typeId") != strconv.FormatUint(fixture.typeIDs[0], 10) {
					t.Fatal("combined search leaked a different category")
				}
			}
			if !reflect.DeepEqual(actual, wanted) || body.Total == nil || *body.Total != strconv.Itoa(len(wanted)) {
				t.Fatal("name and category must be applied together before count and pagination")
			}
		}
		path := "/shop/of/type?typeId=" + strconv.FormatUint(fixture.typeIDs[0], 10)
		status, body := request(t, router, http.MethodGet, path)
		if len(requireShopArray(t, status, body)) != 5 || body.Total == nil || *body.Total != "27" {
			t.Fatal("legacy type browsing must retain a default size of five and the exact category total")
		}
	})

	t.Run("literal_wildcards_and_injection_text_are_not_queries", func(t *testing.T) {
		for _, name := range []string{fixture.prefix + "字面%", fixture.prefix + "字面%百分号_", fixture.prefix + "字面%百分号_感叹!"} {
			status, body := request(t, router, http.MethodGet, "/shop/of/name?name="+url.QueryEscape(name))
			rows := requireShopArray(t, status, body)
			if len(rows) != 1 || shopString(t, rows[0], "id") != strconv.FormatUint(fixture.shopIDs[1], 10) || body.Total == nil || *body.Total != "1" {
				t.Fatal("percent, underscore and the escape character must be literal substring characters")
			}
		}
		for _, name := range []string{fixture.prefix + "' OR 1=1 --", fixture.prefix + "不存在"} {
			status, body := request(t, router, http.MethodGet, "/shop/of/name?name="+url.QueryEscape(name))
			if len(requireShopArray(t, status, body)) != 0 || body.Total == nil || *body.Total != "0" {
				t.Fatal("unmatched search text must stay an empty page without SQL interpretation")
			}
		}
		status, body := request(t, router, http.MethodGet, "/shop/of/name?typeId=18446744073709551615")
		if len(requireShopArray(t, status, body)) != 0 || body.Total == nil || *body.Total != "0" {
			t.Fatal("nonexistent category must produce an empty successful page")
		}
	})

	t.Run("detail_keeps_null_empty_yuan_and_local_time_contracts", func(t *testing.T) {
		for _, index := range []int{0, 1, 2} {
			status, body := request(t, router, http.MethodGet, "/shop/"+strconv.FormatUint(fixture.shopIDs[index], 10))
			row := requireShopObject(t, status, body)
			if shopString(t, row, "id") != strconv.FormatUint(fixture.shopIDs[index], 10) || shopString(t, row, "images") != "" {
				t.Fatal("detail must preserve large string IDs and empty images")
			}
			if _, exposed := row["merchantId"]; exposed {
				t.Fatal("public detail must not expose merchant identity")
			}
			if index == 0 {
				for _, absent := range []string{"area", "avgPrice", "openHours", "createTime", "updateTime"} {
					if _, present := row[absent]; present {
						t.Fatal("SQL NULL shop fields must be omitted from JSON")
					}
				}
			} else {
				price := "30"
				if index == 1 {
					price = "18446744073709551615"
				}
				if shopString(t, row, "avgPrice") != price || shopString(t, row, "createTime") != "2026-10-26T11:12:13" {
					t.Fatal("whole-yuan price or explicit Shanghai ISO time changed")
				}
			}
		}
		status, body := request(t, router, http.MethodGet, "/shop/18446744073709551615")
		if status != http.StatusOK || body.Success || body.ErrorMsg != "店铺不存在！" || body.Data != nil {
			t.Fatal("missing shop must preserve its existing business error")
		}
	})

	t.Run("categories_sort_by_sort_then_id_and_omit_null_fields", func(t *testing.T) {
		status, body := request(t, router, http.MethodGet, "/shop-type/list")
		rows := requireShopArray(t, status, body)
		var previousSort int64 = math.MinInt64
		var previousID uint64
		seen := make(map[uint64]bool)
		for _, row := range rows {
			id, err := strconv.ParseUint(shopString(t, row, "id"), 10, 64)
			var sort int64
			if err != nil || json.Unmarshal(row["sort"], &sort) != nil || sort < previousSort || (sort == previousSort && id <= previousID) {
				t.Fatal("category ordering must use the deterministic sort/id pair")
			}
			previousSort, previousID = sort, id
			for _, fixtureID := range fixture.typeIDs {
				if id == fixtureID {
					seen[id] = true
				}
			}
			if id == fixture.typeIDs[2] {
				if _, present := row["name"]; present {
					t.Fatal("NULL category name must be omitted")
				}
				if _, present := row["icon"]; present {
					t.Fatal("NULL category icon must be omitted")
				}
			}
			if _, present := row["createTime"]; present {
				t.Fatal("category timestamps must not be exposed")
			}
		}
		if len(seen) != 3 {
			t.Fatal("public category list did not contain the fixture categories")
		}
	})

	t.Run("only_enabled_active_vouchers_are_visible_without_stock_or_time_filter", func(t *testing.T) {
		status, body := request(t, router, http.MethodGet, "/voucher/list/"+strconv.FormatUint(fixture.shopIDs[0], 10))
		rows := requireShopArray(t, status, body)
		var ids []uint64
		for _, row := range rows {
			id, err := strconv.ParseUint(shopString(t, row, "id"), 10, 64)
			if err != nil || shopString(t, row, "shopId") != strconv.FormatUint(fixture.shopIDs[0], 10) {
				t.Fatal("public voucher IDs must remain exact strings belonging to the selected shop")
			}
			ids = append(ids, id)
			for _, absent := range []string{"subTitle", "status", "publishStatus", "merchantId", "createTime", "updateTime", "redeemToken"} {
				if _, present := row[absent]; present {
					t.Fatal("public voucher leaked SQL NULL or an unselected/internal field")
				}
			}
			if shopString(t, row, "rules") != "" || !strings.Contains(shopString(t, row, "beginTime"), "T") || strings.Contains(shopString(t, row, "beginTime"), "+08") {
				t.Fatal("voucher empty rules and local ISO time contract changed")
			}
		}
		if !reflect.DeepEqual(ids, fixture.activeVoucherIDs) || len(rows) != 4 {
			t.Fatal("visibility must include sold-out/future/expired ACTIVE vouchers and exclude unpublished/offline/disabled/normal vouchers")
		}
		if shopString(t, rows[0], "payValue") != "18446744073709551615" || shopString(t, rows[0], "actualValue") != "-9223372036854775808" {
			t.Fatal("unsigned pay cents and signed actual cents must not lose precision")
		}
		status, body = request(t, router, http.MethodGet, "/voucher/list/18446744073709551615")
		if len(requireShopArray(t, status, body)) != 0 {
			t.Fatal("unknown shop voucher list must remain an empty successful array")
		}
	})

	t.Run("invalid_parameters_and_coordinates_are_explicit_errors", func(t *testing.T) {
		for _, path := range []string{
			"/shop/of/name?current=0", "/shop/of/name?pageSize=51", "/shop/of/name?typeId=-1",
			"/shop/of/type", "/shop/of/type?typeId=1&x=120&y=30", "/shop/0", "/voucher/list/invalid",
		} {
			status, body := request(t, router, http.MethodGet, path)
			if status != http.StatusBadRequest || body.Success || body.ErrorMsg == "" || body.Data != nil {
				t.Fatal("invalid public query must return a clear safe 400 envelope")
			}
		}
	})
}

func requireShopArray(t *testing.T, status int, body reply) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if status != http.StatusOK || !body.Success || json.Unmarshal(body.Data, &rows) != nil || rows == nil {
		t.Fatal("public list must return an HTTP 200 successful JSON array, including empty arrays")
	}
	return rows
}

func requireShopObject(t *testing.T, status int, body reply) map[string]json.RawMessage {
	t.Helper()
	var row map[string]json.RawMessage
	if status != http.StatusOK || !body.Success || json.Unmarshal(body.Data, &row) != nil || row == nil {
		t.Fatal("public detail must return an HTTP 200 successful JSON object")
	}
	return row
}

func shopString(t *testing.T, row map[string]json.RawMessage, field string) string {
	t.Helper()
	var value string
	if json.Unmarshal(row[field], &value) != nil {
		t.Fatal("public string field was absent or was encoded as a JSON number")
	}
	return value
}

type fixtureShop struct {
	name   string
	typeID uint64
}

type shopFixture struct {
	prefix           string
	merchantID       uint64
	typeIDs          []uint64
	shopIDs          []uint64
	shops            []fixtureShop
	voucherIDs       []uint64
	activeVoucherIDs []uint64
}

func newShopFixture(t *testing.T, resources *database.Resources, location *time.Location) *shopFixture {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("cannot allocate an independent shop fixture")
	}
	fixture := &shopFixture{prefix: "go011_" + hex.EncodeToString(random[:]) + "_"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Fail clearly if the caller forgot the explicit versioned migration.
	rows, err := resources.SQL.QueryContext(ctx, "SELECT publish_status FROM tb_seckill_voucher LIMIT 0")
	if err != nil {
		t.Fatal("shop integration requires the existing explicit 003-trade.sql migration")
	}
	_ = rows.Close()
	tx, err := resources.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("cannot begin independent fixture preparation")
	}
	defer tx.Rollback()
	t.Cleanup(func() { fixture.cleanup(t, resources.SQL) })
	insert := func(query string, arguments ...any) uint64 {
		t.Helper()
		result, err := tx.ExecContext(ctx, query, arguments...)
		if err != nil {
			t.Fatal("cannot insert an independent shop fixture row")
		}
		id, err := result.LastInsertId()
		if err != nil || id <= 0 {
			t.Fatal("fixture insert did not receive a database ID")
		}
		return uint64(id)
	}
	fixture.merchantID = insert("INSERT INTO tb_merchant (name) VALUES (?)", fixture.prefix+"商家")
	for _, label := range []string{"分类甲", "分类乙"} {
		fixture.typeIDs = append(fixture.typeIDs, insert("INSERT INTO tb_shop_type (name, icon, sort) VALUES (?, '', ?)", fixture.prefix+label, 1_000_000))
	}
	fixture.typeIDs = append(fixture.typeIDs, insert("INSERT INTO tb_shop_type (name, icon, sort) VALUES (NULL, NULL, ?)", 999_999))

	// Reserve a collision-free explicit block so every real fixture shop tests
	// exact JSON IDs beyond JavaScript's integer range without touching old rows.
	var base uint64
	for range 10 {
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal("cannot allocate a real large-ID fixture")
		}
		base = 9_007_199_254_740_993 + binary.LittleEndian.Uint64(random[:])%1_000_000_000
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tb_shop WHERE id BETWEEN ? AND ?", base, base+52).Scan(&count); err != nil {
			t.Fatal("cannot establish independent large-ID fixture ownership")
		}
		if count == 0 {
			break
		}
		base = 0
	}
	if base == 0 {
		t.Fatal("cannot reserve independent large-ID fixture rows")
	}
	for index := range 53 {
		name := fixture.prefix + "普通餐馆" + fmt.Sprintf("%02d", index)
		if index%3 == 0 {
			name = fixture.prefix + "交集咖啡" + fmt.Sprintf("%02d", index)
		}
		if index == 1 {
			name = fixture.prefix + "字面%百分号_感叹!"
		} else if index == 2 {
			name = fixture.prefix + "字面X百分号Y感叹Z"
		}
		typeID := fixture.typeIDs[index%2]
		var area, price, hours, created, updated any = "示例商圈", "30", "09:00-21:00", "2026-10-26 11:12:13", "2026-10-26 11:12:13"
		if index == 0 {
			area, price, hours, created, updated = nil, nil, nil, nil, nil
		} else if index == 1 {
			price = "18446744073709551615"
		}
		id := base + uint64(index)
		if _, err := tx.ExecContext(ctx, `INSERT INTO tb_shop
			(id, merchant_id, name, type_id, images, area, address, x, y, avg_price, sold, comments, score, open_hours, create_time, update_time)
			VALUES (?, ?, ?, ?, '', ?, ?, 120.15, 30.28, ?, 0, 0, 48, ?, ?, ?)`,
			id, fixture.merchantID, name, typeID, area, fixture.prefix+"路", price, hours, created, updated); err != nil {
			t.Fatal("cannot insert the independent real large-ID shop fixture")
		}
		fixture.shopIDs = append(fixture.shopIDs, id)
		fixture.shops = append(fixture.shops, fixtureShop{name: name, typeID: typeID})
	}
	now := time.Now().In(location).Truncate(time.Second)
	for index, publication := range []string{"ACTIVE", "ACTIVE", "ACTIVE", "ACTIVE", "OFFLINE", "INITIALIZING", "ACTIVE", "", "ACTIVE"} {
		shopID := fixture.shopIDs[0]
		if index == 8 {
			shopID = fixture.shopIDs[1]
		}
		status, kind := 1, 1
		if index == 6 {
			status = 2
		} else if index == 7 {
			kind = 0
		}
		pay, actual := "1900", "3000"
		if index == 0 {
			pay, actual = "18446744073709551615", "-9223372036854775808"
		}
		id := insert(`INSERT INTO tb_voucher (shop_id, title, sub_title, rules, pay_value, actual_value, type, status)
			VALUES (?, ?, NULL, '', ?, ?, ?, ?)`, shopID, fixture.prefix+"活动"+strconv.Itoa(index), pay, actual, kind, status)
		fixture.voucherIDs = append(fixture.voucherIDs, id)
		if publication == "" {
			continue
		}
		stock := 50
		if index == 1 {
			stock = 0
		}
		begin, end := now.Add(-time.Hour), now.Add(time.Hour)
		if index == 2 {
			begin, end = now.Add(24*time.Hour), now.Add(48*time.Hour)
		} else if index == 3 {
			begin, end = now.Add(-48*time.Hour), now.Add(-24*time.Hour)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tb_seckill_voucher
			(voucher_id, merchant_id, stock, total_stock, publish_status, publish_version, begin_time, end_time)
			VALUES (?, ?, ?, 50, ?, 1, ?, ?)`, id, fixture.merchantID, stock, publication,
			begin.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal("cannot insert the versioned activity fixture")
		}
		if index < 4 {
			fixture.activeVoucherIDs = append(fixture.activeVoucherIDs, id)
		}
	}
	if tx.Commit() != nil {
		t.Fatal("cannot commit independent fixture preparation")
	}
	return fixture
}

func (fixture *shopFixture) cleanup(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, target := range []struct {
		table, column string
		ids           []uint64
	}{
		{"tb_seckill_voucher", "voucher_id", fixture.voucherIDs},
		{"tb_voucher", "id", fixture.voucherIDs},
		{"tb_shop", "id", fixture.shopIDs},
		{"tb_shop_type", "id", fixture.typeIDs},
		{"tb_merchant", "id", []uint64{fixture.merchantID}},
	} {
		if len(target.ids) == 0 || (len(target.ids) == 1 && target.ids[0] == 0) {
			continue
		}
		arguments := make([]any, len(target.ids))
		for index, id := range target.ids {
			arguments[index] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(arguments)), ",")
		if _, err := db.ExecContext(ctx, "DELETE FROM "+target.table+" WHERE "+target.column+" IN ("+placeholders+")", arguments...); err != nil {
			t.Error("cannot clean the exact integration-owned shop fixture rows")
		}
	}
}
