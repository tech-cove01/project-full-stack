package activity

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func validRequest() CreateRequest {
	return CreateRequest{ShopID: "9007199254740993", Title: "咖啡体验券", PayValue: "1900", ActualValue: "3000", TotalStock: 100,
		BeginTime: "2026-10-26T09:00:00+08:00", EndTime: "2026-10-26T18:00:00+08:00",
		RedeemBeginTime: "2026-10-26T09:00:00+08:00", RedeemEndTime: "2026-11-01T22:00:00+08:00"}
}

func TestPublishValidationRejectsUnsafeNumbersAndTimes(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*CreateRequest)
	}{
		{"empty_title", func(r *CreateRequest) { r.Title = " " }},
		{"long_title", func(r *CreateRequest) { r.Title = strings.Repeat("中", 256) }},
		{"malformed_shop", func(r *CreateRequest) { r.ShopID = "01" }},
		{"zero_money", func(r *CreateRequest) { r.PayValue = "0" }},
		{"signed_money", func(r *CreateRequest) { r.PayValue = "+1900" }},
		{"money_overflow", func(r *CreateRequest) { r.ActualValue = "9223372036854775808" }},
		{"discount_inverted", func(r *CreateRequest) { r.PayValue = "3001" }},
		{"zero_stock", func(r *CreateRequest) { r.TotalStock = 0 }},
		{"stock_too_large", func(r *CreateRequest) { r.TotalStock = 1000001 }},
		{"local_time", func(r *CreateRequest) { r.BeginTime = "2026-10-26T09:00:00" }},
		{"before_redis_epoch", func(r *CreateRequest) { r.BeginTime = "1970-01-01T00:00:00Z" }},
		{"fractional_time", func(r *CreateRequest) { r.BeginTime = "2026-10-26T09:00:00.1+08:00" }},
		{"inverted_sale", func(r *CreateRequest) { r.BeginTime = r.EndTime }},
		{"inverted_redeem", func(r *CreateRequest) { r.RedeemBeginTime = r.RedeemEndTime }},
		{"redeem_too_short", func(r *CreateRequest) { r.RedeemEndTime = r.EndTime }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			test.change(&request)
			if _, err := validate("request-key-1", request); err == nil {
				t.Fatal("invalid publication must fail before touching dependencies")
			}
		})
	}
	for _, key := range []string{"", "short", "spaces not permitted", strings.Repeat("a", 65)} {
		if _, err := validate(key, validRequest()); err == nil {
			t.Fatal("invalid request key was accepted")
		}
	}
	r := validRequest()
	r.PayValue = "9223372036854775807"
	r.ActualValue = r.PayValue
	if _, err := validate("request-key-1", r); err != nil {
		t.Fatal("the original signed 64-bit monetary boundary must remain supported")
	}
}

func TestCanonicalHashNormalizesBusinessTimeAndCannotCollideOnDelimiters(t *testing.T) {
	a := validRequest()
	b := a
	b.BeginTime = "2026-10-26T01:00:00Z"
	b.EndTime = "2026-10-26T10:00:00Z"
	b.RedeemBeginTime = "2026-10-26T01:00:00Z"
	b.RedeemEndTime = "2026-11-01T14:00:00Z"
	b.Title = "  咖啡体验券  "
	x, err := validate("request-key-1", a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := validate("request-key-1", b)
	if err != nil || x.hash != y.hash {
		t.Fatal("same instants and normalized text must have the same request identity")
	}
	subA, subB := "c", "b|c"
	a.Title = "a|b"
	a.SubTitle = &subA
	b = a
	b.Title = "a"
	b.SubTitle = &subB
	x, _ = validate("request-key-1", a)
	y, _ = validate("request-key-1", b)
	if x.hash == y.hash {
		t.Fatal("different title/subtitle payloads must not share a delimiter-based hash")
	}
}

func TestBusinessTimeUsesShanghaiIANAHistory(t *testing.T) {
	value, err := parseTime("1986-07-01T00:00:00Z")
	if err != nil || value.Format(time.RFC3339) != "1986-07-01T09:00:00+09:00" {
		t.Fatal("activity times must use the same IANA Shanghai history as the configured database location")
	}
}

func TestPaginationAvoidsOverflowAndSerializationKeepsExactContracts(t *testing.T) {
	if _, present := offset(Query{Current: math.MaxUint64, PageSize: 50}, 53); present {
		t.Fatal("far page must be empty without overflowing")
	}
	if n, present := offset(Query{Current: 6, PageSize: 10}, 53); !present || n != 50 {
		t.Fatal("last page offset changed")
	}
	row := activityRow{VoucherID: 9007199254740993, ShopID: math.MaxUint64, Title: "fixture", PayValue: math.MaxInt64, ActualValue: math.MaxInt64, PublishStatus: "INITIALIZING", BeginTime: time.Date(2026, 10, 26, 1, 2, 3, 0, time.UTC)}
	view := (&Service{}).toView(t.Context(), row)
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(body, &fields) != nil || fields["id"] != "9007199254740993" || fields["shopId"] != "18446744073709551615" || fields["payValue"] != "9223372036854775807" || fields["beginTime"] != "2026-10-26 09:02:03" || fields["ready"] != false {
		t.Fatal("ID, money, business time or readiness contract changed")
	}
	for _, absent := range []string{"subTitle", "redeemBeginTime", "merchantId", "rules"} {
		if _, exists := fields[absent]; exists {
			t.Fatal("null and private fields must be omitted")
		}
	}
}
