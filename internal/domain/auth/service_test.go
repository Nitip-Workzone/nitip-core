package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func TestAuth(t *testing.T) {
	masterKey := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"
	_ = os.Setenv("AUTH_MASTER_KEY", masterKey)
	defer func() { _ = os.Unsetenv("AUTH_MASTER_KEY") }()

	t.Run("ValidateHMAC", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("web dengan signature kosong diterima sesuai kontrak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				apiKey := "mock-api-key-web"
				timestamp := time.Now().Format(time.RFC3339)
				secretEnc, err := encryptSecret("web-secret-key-123456789")
				assert.NoError(t, err)
				rows := sqlmock.NewRows([]string{"id", "app_name", "platform", "api_key", "api_secret_hash", "api_secret_enc", "is_active", "description", "created_at", "updated_at", "last_used_at"}).
					AddRow("00000000-0000-0000-0000-000000000001", "Web Client", "web", apiKey, "hash", secretEnc, true, "desc", time.Now(), time.Now(), nil)
				mockSql.ExpectQuery(`(?i)SELECT .* FROM "api_clients"`).
					WillReturnRows(rows)
				mockSql.ExpectExec(`(?i)UPDATE "api_clients"`).
					WillReturnResult(sqlmock.NewResult(1, 1))
				client, err := svc.ValidateHMAC(context.Background(), apiKey, timestamp, "", "body")
				assert.NoError(t, err)
				assert.NotNil(t, client)
				assert.Equal(t, "web", client.Platform)
			})
			t.Run("listener dengan signature benar diterima", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				apiKey := "mock-api-key-listener"
				apiSecret := "some-very-secret-key-1234567890123"
				timestamp := time.Now().Format(time.RFC3339)
				secretEnc, err := encryptSecret(apiSecret)
				assert.NoError(t, err)
				rows := sqlmock.NewRows([]string{"id", "app_name", "platform", "api_key", "api_secret_hash", "api_secret_enc", "is_active", "description", "created_at", "updated_at", "last_used_at"}).
					AddRow("00000000-0000-0000-0000-000000000002", "Listener Client", "listener", apiKey, "hash", secretEnc, true, "desc", time.Now(), time.Now(), nil)
				mockSql.ExpectQuery(`(?i)SELECT .* FROM "api_clients"`).
					WillReturnRows(rows)
				bodyHash := sha256Hex("body")
				payload := timestamp + "." + bodyHash
				mac := hmac.New(sha256.New, []byte(apiSecret))
				mac.Write([]byte(payload))
				signature := hex.EncodeToString(mac.Sum(nil))
				mockSql.ExpectExec(`(?i)UPDATE "api_clients"`).
					WillReturnResult(sqlmock.NewResult(1, 1))
				client, err := svc.ValidateHMAC(context.Background(), apiKey, timestamp, signature, "body")
				assert.NoError(t, err)
				assert.NotNil(t, client)
				assert.Equal(t, "listener", client.Platform)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("listener dengan signature kosong ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				apiKey := "mock-api-key-listener"
				timestamp := time.Now().Format(time.RFC3339)
				secretEnc, err := encryptSecret("listener-secret-key-123456789")
				assert.NoError(t, err)
				rows := sqlmock.NewRows([]string{"id", "app_name", "platform", "api_key", "api_secret_hash", "api_secret_enc", "is_active", "description", "created_at", "updated_at", "last_used_at"}).
					AddRow("00000000-0000-0000-0000-000000000002", "Listener Client", "listener", apiKey, "hash", secretEnc, true, "desc", time.Now(), time.Now(), nil)
				mockSql.ExpectQuery(`(?i)SELECT .* FROM "api_clients"`).
					WillReturnRows(rows)
				client, err := svc.ValidateHMAC(context.Background(), apiKey, timestamp, "", "body")
				assert.Error(t, err)
				assert.Nil(t, client)
				assert.Equal(t, ErrInvalidSignature, err)
			})
		})
	})
}
