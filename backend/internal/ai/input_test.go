package ai

import "testing"

func TestSensitiveInputKeepsDecimalBusinessIDs(t *testing.T) {
	for _, text := range []string{
		"查我的订单 913812345678123456",
		"查我的订单 9138123456781234567",
		"活动 913812345678123456",
		"活动 9138123456781234567",
		"订单 18446744073709551615",
		"订单123456",
	} {
		if SensitiveInput(text) {
			t.Errorf("a decimal business ID must not be classified as a phone: %q", text)
		}
	}
}

func TestSensitiveInputStillRejectsCredentials(t *testing.T) {
	for _, text := range []string{
		"13812345678",
		"我的手机号13812345678，查停车规则",
		"联系电话 13812345678，请查单",
		"验证码：123456，查停车规则",
		"verification code: 123456",
		"sms code 123456",
		"token=0123456789abcdef0123456789abcdef",
		"token=FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF",
		"查订单 913812345678123456，同时手机号13812345678",
	} {
		if !SensitiveInput(text) {
			t.Errorf("credential-like input must remain rejected: %q", text)
		}
	}
}
