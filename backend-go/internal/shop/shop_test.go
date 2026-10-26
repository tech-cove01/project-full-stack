package shop

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestPageOffsetReturnsEmptyBeforeOverflowing(t *testing.T) {
	for _, test := range []struct {
		current, size, total, offset uint64
		present                      bool
	}{
		{1, 10, 0, 0, false}, {1, 10, 53, 0, true}, {6, 10, 53, 50, true},
		{7, 10, 53, 0, false}, {math.MaxUint64, 50, 53, 0, false},
		{math.MaxUint64, 1, math.MaxUint64, math.MaxUint64 - 1, true},
		{math.MaxUint64, 50, math.MaxUint64, 0, false},
		{0, 10, 53, 0, false}, {1, 0, 53, 0, false},
	} {
		offset, present := pageOffset(Query{Current: test.current, PageSize: test.size}, test.total)
		if offset != test.offset || present != test.present {
			t.Fatal("pagination did not preserve the expected mathematical offset or empty-page boundary")
		}
	}
}

func TestLiteralSubstringFilterEscapesWildcardsAndBindsCategory(t *testing.T) {
	category := uint64(9007199254740993)
	where, parameters := shopFilter(Query{Name: `咖啡%_!\' OR 1=1`, TypeID: &category})
	if len(parameters) != 2 || parameters[0] != `%咖啡!%!_!!\' OR 1=1%` || parameters[1] != category {
		t.Fatal("substring escaping or category arguments changed")
	}
	if !strings.Contains(where, "name LIKE ? ESCAPE '!' AND type_id = ?") || strings.Contains(where, "咖啡") || strings.Contains(where, "OR 1=1") {
		t.Fatal("search and category must be bound parameters in the same AND query")
	}
	where, parameters = shopFilter(Query{})
	if where != " WHERE 1 = 1" || len(parameters) != 0 {
		t.Fatal("empty filtering should browse all shops")
	}
}

func TestPublicShopSerializationKeepsIDYuanNullAndEmptyStringSemantics(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	store := mysqlStore{location: location}
	price := uint64(math.MaxUint64)
	created := time.Date(2026, 10, 26, 3, 4, 5, 120_000_000, time.UTC)
	empty := ""
	shop := store.shop(shopRecord{
		ID: 9007199254740993, TypeID: math.MaxUint64, Name: "中文店铺", Images: "",
		AvgPrice: &price, OpenHours: &empty, CreateTime: &created,
	})
	encoded, err := json.Marshal(shop)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(encoded, &fields) != nil || fields["id"] != "9007199254740993" || fields["typeId"] != "18446744073709551615" || fields["avgPrice"] != "18446744073709551615" {
		t.Fatal("IDs and whole-yuan average prices must remain exact decimal strings")
	}
	if fields["images"] != "" || fields["openHours"] != "" || fields["createTime"] != "2026-10-26T11:04:05.12" {
		t.Fatal("empty strings and Shanghai local ISO timestamps changed")
	}
	for _, absent := range []string{"area", "updateTime", "merchantId", "distance"} {
		if _, present := fields[absent]; present {
			t.Fatal("null or private shop fields must be omitted")
		}
	}
	shop = store.shop(shopRecord{ID: 1, TypeID: 1, Name: "null price"})
	encoded, _ = json.Marshal(shop)
	if strings.Contains(string(encoded), "avgPrice") {
		t.Fatal("SQL NULL average price must remain absent, not a fabricated zero")
	}
}

func TestPublicTypeAndVoucherHaveOnlySelectedFields(t *testing.T) {
	typeJSON, _ := json.Marshal(ShopType{ID: "1", Sort: 0})
	if string(typeJSON) != `{"id":"1","sort":0}` {
		t.Fatal("null category fields and ignored timestamps must be omitted")
	}
	empty := ""
	encoded, _ := json.Marshal(Voucher{
		ID: "9007199254740993", ShopID: "2", Title: "voucher", Rules: &empty,
		PayValue: "18446744073709551615", ActualValue: "-9223372036854775808", Type: 1, Stock: 0,
	})
	var fields map[string]any
	if json.Unmarshal(encoded, &fields) != nil || fields["payValue"] != "18446744073709551615" || fields["actualValue"] != "-9223372036854775808" || fields["rules"] != "" {
		t.Fatal("voucher unsigned/signed cents and empty rules must stay exact strings")
	}
	for _, absent := range []string{"subTitle", "beginTime", "endTime", "createTime", "updateTime", "status", "merchantId", "publishStatus"} {
		if _, present := fields[absent]; present {
			t.Fatal("voucher response contains an unselected or internal field")
		}
	}
}
