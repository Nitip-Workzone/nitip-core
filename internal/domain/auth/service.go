package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

const (
	grantTokenTTL    = 15 * time.Minute
	grantTokenLength = 48
	apiKeyLength     = 32
	apiSecretLength  = 48
)

var (
	ErrInvalidCredentials = errors.New("invalid API key or secret")
	ErrClientInactive     = errors.New("API client is inactive")
	ErrInvalidSignature   = errors.New("invalid HMAC signature")
	ErrTimestampExpired   = errors.New("request timestamp expired or invalid")
	ErrGrantTokenExpired  = errors.New("grant token has expired")
	ErrGrantTokenUsed     = errors.New("grant token has already been used")
	ErrGrantTokenInvalid  = errors.New("invalid grant token")
)

type Service struct {
	db *bun.DB
}

func NewService(db *bun.DB) *Service {
	return &Service{db: db}
}

// RegisterClient creates a new API client.
// Returns the plain-text secret (shown only once to admin).
// Internally stores: bcrypt hash (for fallback) + AES-encrypted secret (for HMAC verification).
func (s *Service) RegisterClient(ctx context.Context, appName, platform, description string) (*ApiClient, string, error) {
	apiKey, err := generateRandomString(apiKeyLength)
	if err != nil {
		return nil, "", fmt.Errorf("generate api key: %w", err)
	}

	apiSecret, err := generateRandomString(apiSecretLength)
	if err != nil {
		return nil, "", fmt.Errorf("generate api secret: %w", err)
	}

	// Store bcrypt hash (for fallback verification)
	secretHash, err := bcrypt.GenerateFromPassword([]byte(apiSecret), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash api secret: %w", err)
	}

	// Store AES-encrypted secret (for HMAC verification)
	secretEnc, err := encryptSecret(apiSecret)
	if err != nil {
		return nil, "", fmt.Errorf("encrypt api secret: %w", err)
	}

	client := &ApiClient{
		ID:            uuid.New(),
		AppName:       appName,
		Platform:      platform,
		ApiKey:        apiKey,
		ApiSecretHash: string(secretHash),
		ApiSecretEnc:  secretEnc,
		IsActive:      true,
		Description:   description,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	_, err = s.db.NewInsert().Model(client).Exec(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("insert api client: %w", err)
	}

	return client, apiSecret, nil
}

// ValidateHMAC verifies a request using HMAC-SHA256 signature.
//
// Client computes:
//
//	payload = timestamp + "." + SHA256(body)
//	signature = HMAC-SHA256(payload, api_secret)
//
// Server:
//  1. Lookup client by api_key
//  2. Decrypt stored api_secret
//  3. Recompute HMAC with same payload
//  4. Compare signatures (constant-time)
func (s *Service) ValidateHMAC(ctx context.Context, apiKey, timestamp, signature, body string) (*ApiClient, error) {
	// 1. Parse and validate timestamp
	reqTime, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return nil, ErrTimestampExpired
	}

	drift := time.Since(reqTime)
	if drift < 0 {
		drift = -drift
	}
	if drift > 5*time.Minute {
		return nil, ErrTimestampExpired
	}

	// 2. Look up client by api_key
	var client ApiClient
	err = s.db.NewSelect().
		Model(&client).
		Where("api_key = ?", apiKey).
		Where("is_active = true").
		Scan(ctx)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	// 3. Decrypt stored secret
	secret, err := decryptSecret(client.ApiSecretEnc)
	if err != nil {
		return nil, fmt.Errorf("server encryption error")
	}

	// 4. Compute expected signature
	bodyHash := sha256Hex(body)
	payload := timestamp + "." + bodyHash
	expected := hmacSHA256(payload, secret)

	// 5. Constant-time comparison
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return nil, ErrInvalidSignature
	}

	// 6. Update last_used_at
	now := time.Now()
	_, _ = s.db.NewUpdate().
		Model(&client).
		Set("last_used_at = ?", now).
		Where("id = ?", client.ID).
		Exec(ctx)

	return &client, nil
}

// CreateGrantToken issues a short-lived grant token for a validated client.
func (s *Service) CreateGrantToken(ctx context.Context, clientID uuid.UUID) (*GrantToken, error) {
	tokenStr, err := generateRandomString(grantTokenLength)
	if err != nil {
		return nil, fmt.Errorf("generate grant token: %w", err)
	}

	grant := &GrantToken{
		ID:          uuid.New(),
		ApiClientID: clientID,
		Token:       tokenStr,
		ExpiresAt:   time.Now().Add(grantTokenTTL),
		Used:        false,
		CreatedAt:   time.Now(),
	}

	_, err = s.db.NewInsert().Model(grant).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("insert grant token: %w", err)
	}

	return grant, nil
}

// ConsumeGrantToken validates and marks a grant token as used (one-time use).
func (s *Service) ConsumeGrantToken(ctx context.Context, token string) error {
	var grant GrantToken
	err := s.db.NewSelect().
		Model(&grant).
		Where("token = ?", token).
		Where("used = false").
		Scan(ctx)
	if err != nil {
		return ErrGrantTokenInvalid
	}

	if time.Now().After(grant.ExpiresAt) {
		return ErrGrantTokenExpired
	}

	_, err = s.db.NewUpdate().
		Table("grant_tokens").
		Set("used = true").
		Where("id = ?", grant.ID).
		Where("used = false").
		Exec(ctx)
	if err != nil {
		return ErrGrantTokenUsed
	}

	return nil
}

// CleanupExpiredGrantTokens removes expired tokens.
func (s *Service) CleanupExpiredGrantTokens(ctx context.Context) (int64, error) {
	res, err := s.db.NewDelete().
		Table("grant_tokens").
		Where("expires_at < ?", time.Now()).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListClients returns all registered API clients.
func (s *Service) ListClients(ctx context.Context) ([]ApiClient, error) {
	var clients []ApiClient
	err := s.db.NewSelect().
		Model(&clients).
		Order("created_at DESC").
		Scan(ctx)
	return clients, err
}

// DeactivateClient disables an API client.
func (s *Service) DeactivateClient(ctx context.Context, clientID uuid.UUID) error {
	_, err := s.db.NewUpdate().
		Table("api_clients").
		Set("is_active = false").
		Set("updated_at = ?", time.Now()).
		Where("id = ?", clientID).
		Exec(ctx)
	return err
}

// ── Crypto Helpers ────────────────────────────────────────

func getMasterKey() ([]byte, error) {
	keyHex := os.Getenv("AUTH_MASTER_KEY")
	if keyHex == "" {
		return nil, fmt.Errorf("AUTH_MASTER_KEY must be set (generate: openssl rand -hex 32)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("AUTH_MASTER_KEY must be exactly 64 hex chars (32 bytes)")
	}
	return key, nil
}

func encryptSecret(plaintext string) (string, error) {
	key, err := getMasterKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

func decryptSecret(encryptedHex string) (string, error) {
	key, err := getMasterKey()
	if err != nil {
		return "", err
	}

	ciphertext, err := hex.DecodeString(encryptedHex)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func hmacSHA256(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func sha256Hex(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func generateRandomString(length int) (string, error) {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}
