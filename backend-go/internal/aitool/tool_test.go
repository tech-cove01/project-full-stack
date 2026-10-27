package aitool

import "testing"

func TestParseOnlyTakesOverExplicitDataQuestions(t *testing.T) {
	for _, test := range []struct {
		message string
		kind    Kind
		id      string
	}{
		{"我的订单", KindOrders, ""},
		{"查询订单 652313753106251777", KindOrder, "652313753106251777"},
		{"订单 3 个", KindOrders, ""},
		{"活动 1038 什么时候开始", KindActivity, "1038"},
		{"活动规则是什么", "", ""},
		{"门店 3 的地址", KindShop, "3"},
		{"店铺 12", KindShop, "12"},
		{"门店地址怎么填写", "", ""},
		{"核销后可以退款吗", "", ""},
		{"", "", ""},
	} {
		kind, id := parse(test.message)
		if kind != test.kind || id != test.id {
			t.Fatalf("parse(%q) = %q,%q want %q,%q", test.message, kind, id, test.kind, test.id)
		}
	}
}

func TestStatusMessagesCoverEveryTerminalState(t *testing.T) {
	for status, want := range map[int]string{
		1: "待支付", 2: "已支付，待核销", 3: "已核销", 4: "已取消", 6: "已退款", 9: "状态未知",
	} {
		if got := statusMessage(status); got != want {
			t.Fatalf("status %d = %q, want %q", status, got, want)
		}
	}
}

func TestRouteIgnoresUnrelatedMessagesWithoutTouchingStorage(t *testing.T) {
	service := NewService(nil)
	result, err := service.Route(nil, 1, "核销后可以退款吗")
	if err != nil || result != nil {
		t.Fatal("a rules question must fall through to retrieval, not a data tool")
	}
}
