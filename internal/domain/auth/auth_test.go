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
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAuthFlow(t *testing.T) {
	masterKey := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"
	_ = os.Setenv("AUTH_MASTER_KEY", masterKey)
	defer func() { _ = os.Unsetenv("AUTH_MASTER_KEY") }()

	t.Run("POST /auth/grant", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("grant valid diterbitkan", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				clientID := uuid.New()
				mockSql.ExpectQuery(`(?i)INSERT.*grant_tokens`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(clientID))
				grant, err := svc.CreateGrantToken(context.Background(), clientID)
				assert.NoError(t, err)
				assert.NotEmpty(t, grant.Token)
				assert.False(t, grant.Used)
				assert.True(t, grant.ExpiresAt.After(time.Now()))
			})
			t.Run("grant valid digunakan satu kali", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				token := "test-grant-token-12345678901234567890123456789012"
				grantID := uuid.New()
				clientID := uuid.New()
				rows := sqlmock.NewRows([]string{"id", "api_client_id", "token", "expires_at", "used", "created_at"}).
					AddRow(grantID, clientID, token, time.Now().Add(10*time.Minute), false, time.Now())
				mockSql.ExpectQuery(`(?i)SELECT.*grant_tokens`).WillReturnRows(rows)
				mockSql.ExpectExec(`(?i)UPDATE.*grant_tokens.*SET.*used`).WillReturnResult(sqlmock.NewResult(1, 1))
				err := svc.ConsumeGrantToken(context.Background(), token)
				assert.NoError(t, err)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("grant yang sama tidak dapat digunakan kembali", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				// First query finds no unused grant (already used)
				mockSql.ExpectQuery(`(?i)SELECT.*grant_tokens`).WillReturnError(sqlmock.ErrCancelled)
				err := svc.ConsumeGrantToken(context.Background(), "already-used-token")
				assert.Error(t, err)
				assert.Equal(t, ErrGrantTokenInvalid, err)
			})
			t.Run("grant kedaluwarsa ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				token := "expired-grant-token"
				grantID := uuid.New()
				clientID := uuid.New()
				rows := sqlmock.NewRows([]string{"id", "api_client_id", "token", "expires_at", "used", "created_at"}).
					AddRow(grantID, clientID, token, time.Now().Add(-1*time.Hour), false, time.Now().Add(-2*time.Hour))
				mockSql.ExpectQuery(`(?i)SELECT.*grant_tokens`).WillReturnRows(rows)
				err := svc.ConsumeGrantToken(context.Background(), token)
				assert.Error(t, err)
				assert.Equal(t, ErrGrantTokenExpired, err)
			})
			t.Run("timestamp invalid ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				// Use invalid timestamp
				_, err := svc.ValidateHMAC(context.Background(), "some-key", "not-a-timestamp", "", "body")
				assert.Error(t, err)
				assert.Equal(t, ErrTimestampExpired, err)
				_ = mockSql
				_ = db
			})
			t.Run("signature invalid untuk listener ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				svc := NewService(db)
				apiKey := "mock-api-key-listener"
				timestamp := time.Now().Format(time.RFC3339)
				secretEnc, _ := encryptSecret("listener-secret-xyz")
				rows := sqlmock.NewRows([]string{"id", "app_name", "platform", "api_key", "api_secret_hash", "api_secret_enc", "is_active", "description", "created_at", "updated_at", "last_used_at"}).
					AddRow(uuid.New(), "Listener", "listener", apiKey, "hash", secretEnc, true, "desc", time.Now(), time.Now(), nil)
				mockSql.ExpectQuery(`(?i)SELECT.*api_clients`).WillReturnRows(rows)
				_, err := svc.ValidateHMAC(context.Background(), apiKey, timestamp, "wrong-signature", "body")
				assert.Error(t, err)
				assert.Equal(t, ErrInvalidSignature, err)
			})
		})
	})
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
