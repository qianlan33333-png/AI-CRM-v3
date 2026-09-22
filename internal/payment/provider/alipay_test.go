package provider

import "testing"

func TestAlipayDisabledIsFailClosedWithoutCredentials(t *testing.T) {
	provider, err := NewAlipay(AlipayConfig{})
	if err != nil || provider.Enabled() {
		t.Fatalf("disabled provider=%v err=%v", provider, err)
	}
}

func TestAlipayConfigRequiresHTTPSAndOneVerificationMode(t *testing.T) {
	base := AlipayConfig{Enabled: true, AppID: "2021007100657802", PrivateKey: "private", NotifyURL: "https://example.test/notify", ReturnURL: "https://example.test/return", AlipayPublicKey: "public"}
	if !base.valid() {
		t.Fatal("public-key configuration should be valid")
	}
	badURL := base
	badURL.NotifyURL = "http://example.test/notify"
	if badURL.valid() {
		t.Fatal("non-HTTPS notify URL must be rejected")
	}
	both := base
	both.AppCertPath, both.AlipayCertPath, both.AlipayRootPath = "app.crt", "ali.crt", "root.crt"
	if both.valid() {
		t.Fatal("mixed public-key and certificate configuration must be rejected")
	}
	incompleteCert := base
	incompleteCert.AlipayPublicKey = ""
	incompleteCert.AppCertPath = "app.crt"
	if incompleteCert.valid() {
		t.Fatal("incomplete certificate configuration must be rejected")
	}
}

func TestAlipayWebPayRequestValidation(t *testing.T) {
	if !validWebPayRequest(WebPayRequest{MerchantOrderNo: "order-1", Subject: "商品", TotalAmount: "9.90"}) {
		t.Fatal("valid web payment request rejected")
	}
	for _, request := range []WebPayRequest{{Subject: "商品", TotalAmount: "9.90"}, {MerchantOrderNo: "order", TotalAmount: "9.90"}, {MerchantOrderNo: "order", Subject: "商品"}} {
		if validWebPayRequest(request) {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
}
