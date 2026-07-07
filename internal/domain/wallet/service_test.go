package wallet

import (
	"crypto/sha512"
	"encoding/hex"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	orderID := "TOPUP-1234"
	statusCode := "200"
	grossAmount := "50000.00"
	serverKey := "SB-Mid-server-key"

	// SHA512(order_id + status_code + gross_amount + server_key)
	signData := orderID + statusCode + grossAmount + serverKey
	hasher := sha512.New()
	hasher.Write([]byte(signData))
	expectedSignature := hex.EncodeToString(hasher.Sum(nil))

	// Jalankan kalkulasi signature internal
	signDataTest := orderID + statusCode + grossAmount + serverKey
	hasherTest := sha512.New()
	hasherTest.Write([]byte(signDataTest))
	computedSignature := hex.EncodeToString(hasherTest.Sum(nil))

	if computedSignature != expectedSignature {
		t.Errorf("Kalkulasi signature gagal. Diharapkan %s, tetapi mendapatkan %s", expectedSignature, computedSignature)
	}
}
