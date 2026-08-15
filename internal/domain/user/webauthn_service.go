package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

var (
	webAuthn *webauthn.WebAuthn
)

func init() {
	// Initialize webauthn instance. In a real app, origins should come from config.
	origins := []string{"http://localhost:3000", "https://web.nihtip.com"}
	
	rpDisplayName := "Nitip Jo"
	rpID := "web.nihtip.com"
	if os.Getenv("APP_ENV") != "production" {
		rpID = "localhost"
	}

	wconfig := &webauthn.Config{
		RPDisplayName: rpDisplayName,
		RPID:          rpID,
		RPOrigins:     origins,
	}

	var err error
	webAuthn, err = webauthn.New(wconfig)
	if err != nil {
		log.Fatalf("Failed to create WebAuthn instance: %v", err)
	}
}

// saveSessionData stores WebAuthn SessionData into Redis temporarily
func (s *service) saveSessionData(ctx context.Context, prefix string, id string, sessionData *webauthn.SessionData) error {
	if s.redis == nil {
		return errors.New("redis is required for webauthn sessions")
	}
	key := fmt.Sprintf("webauthn:%s:%s", prefix, id)
	data, err := json.Marshal(sessionData)
	if err != nil {
		return err
	}
	// Expire session in 5 minutes
	return s.redis.Set(ctx, key, string(data), 5*time.Minute)
}

// loadSessionData retrieves WebAuthn SessionData from Redis
func (s *service) loadSessionData(ctx context.Context, prefix string, id string) (*webauthn.SessionData, error) {
	if s.redis == nil {
		return nil, errors.New("redis is required for webauthn sessions")
	}
	key := fmt.Sprintf("webauthn:%s:%s", prefix, id)
	data, err := s.redis.Get(ctx, key)
	if err != nil || data == "" {
		return nil, errors.New("sesi webauthn tidak ditemukan atau sudah kadaluarsa")
	}

	var sessionData webauthn.SessionData
	if err := json.Unmarshal([]byte(data), &sessionData); err != nil {
		return nil, err
	}

	// Delete after load to prevent replay
	_ = s.redis.Del(ctx, key)

	return &sessionData, nil
}

// WebAuthnRegisterBegin starts the passkey registration process
func (s *service) WebAuthnRegisterBegin(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, errors.New("pengguna tidak ditemukan")
	}

	existingCredsDB, _ := s.repo.FindWebAuthnCredentialsByUserID(ctx, userID)
	var exclusions []protocol.CredentialDescriptor
	for _, c := range existingCredsDB {
		exclusions = append(exclusions, protocol.CredentialDescriptor{
			Type:         protocol.PublicKeyCredentialType,
			CredentialID: c.ID,
		})
	}

	options, sessionData, err := webAuthn.BeginRegistration(user, webauthn.WithExclusions(exclusions))
	if err != nil {
		return nil, err
	}

	if err := s.saveSessionData(ctx, "register", userID.String(), sessionData); err != nil {
		return nil, err
	}

	return options, nil
}

// WebAuthnRegisterFinish verifies the response from the authenticator and saves the credential
func (s *service) WebAuthnRegisterFinish(ctx context.Context, userID uuid.UUID, parsedResponseRaw interface{}) error {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return errors.New("pengguna tidak ditemukan")
	}

	sessionData, err := s.loadSessionData(ctx, "register", userID.String())
	if err != nil {
		return err
	}

	parsedResponse, ok := parsedResponseRaw.(*http.Request)
	if !ok {
		return errors.New("invalid parsed response (requires *http.Request)")
	}

	credential, err := webAuthn.FinishRegistration(user, *sessionData, parsedResponse)
	if err != nil {
		return fmt.Errorf("gagal memvalidasi pendaftaran passkey: %w", err)
	}

	transports := make([]string, len(credential.Transport))
	for i, t := range credential.Transport {
		transports[i] = string(t)
	}

	newCred := &WebauthnCredential{
		ID:              credential.ID,
		UserID:          userID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		Transport:       transports,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		CloneWarning:    credential.Authenticator.CloneWarning,
	}

	if err := s.repo.CreateWebAuthnCredential(ctx, newCred); err != nil {
		return errors.New("gagal menyimpan passkey ke database")
	}

	return nil
}

// WebAuthnLoginBegin starts the passkey login process
func (s *service) WebAuthnLoginBegin(ctx context.Context, email string) (interface{}, error) {
	var user *User
	var err error

	if email == "" {
		return nil, errors.New("email atau nomor telepon diperlukan")
	}

	user, err = s.repo.FindByEmail(ctx, email)
	if err != nil || user == nil {
		sanitizedWa := sanitizeWhatsappNumber(email)
		user, err = s.repo.FindByWhatsappNumber(ctx, sanitizedWa)
		if (err != nil || user == nil) && sanitizedWa != email {
			user, err = s.repo.FindByWhatsappNumber(ctx, email)
		}
	}

	if err != nil || user == nil {
		return nil, errors.New("pengguna tidak ditemukan")
	}

	existingCredsDB, err := s.repo.FindWebAuthnCredentialsByUserID(ctx, user.ID)
	if err != nil || len(existingCredsDB) == 0 {
		return nil, errors.New("pengguna belum mengatur Face ID / Fingerprint")
	}

	options, sessionData, err := webAuthn.BeginLogin(user)
	if err != nil {
		return nil, err
	}

	if err := s.saveSessionData(ctx, "login", user.ID.String(), sessionData); err != nil {
		return nil, err
	}

	return options, nil
}

// Wrapper struct for overriding WebAuthnCredentials method
type webAuthnUserWrapper struct {
	*User
	creds []webauthn.Credential
}

// Wrapper method
func (w *webAuthnUserWrapper) WebAuthnCredentials() []webauthn.Credential {
	return w.creds
}

// WebAuthnLoginFinish verifies the passkey signature and returns login tokens
func (s *service) WebAuthnLoginFinish(ctx context.Context, email string, parsedResponseRaw interface{}, platform string) (*LoginResponse, error) {
	var user *User
	var err error

	// Find user
	user, err = s.repo.FindByEmail(ctx, email)
	if err != nil || user == nil {
		sanitizedWa := sanitizeWhatsappNumber(email)
		user, err = s.repo.FindByWhatsappNumber(ctx, sanitizedWa)
		if (err != nil || user == nil) && sanitizedWa != email {
			user, err = s.repo.FindByWhatsappNumber(ctx, email)
		}
	}

	if err != nil || user == nil {
		return nil, errors.New("pengguna tidak ditemukan")
	}

	sessionData, err := s.loadSessionData(ctx, "login", user.ID.String())
	if err != nil {
		return nil, err
	}

	parsedResponse, ok := parsedResponseRaw.(*http.Request)
	if !ok {
		return nil, errors.New("invalid parsed response (requires *http.Request)")
	}

	existingCredsDB, _ := s.repo.FindWebAuthnCredentialsByUserID(ctx, user.ID)
	var existingCreds []webauthn.Credential
	for _, c := range existingCredsDB {
		existingCreds = append(existingCreds, c.ToWebAuthnCredential())
	}

	wrapper := &webAuthnUserWrapper{User: user, creds: existingCreds}
	
	credential, err := webAuthn.FinishLogin(wrapper, *sessionData, parsedResponse)
	if err != nil {
		return nil, fmt.Errorf("gagal memvalidasi Face ID / Fingerprint: %w", err)
	}

	// Update credential sign count in DB
	for _, c := range existingCredsDB {
		if string(c.ID) == string(credential.ID) {
			c.SignCount = credential.Authenticator.SignCount
			c.UpdatedAt = time.Now()
			_ = s.repo.UpdateWebAuthnCredential(ctx, &c)
			break
		}
	}

	// --- Proceed with standard Login token generation ---
	
	// Unique Device Session Handling
	deviceId := "webauthn-device"

	user.TokenVersion++
	user.DeviceId = &deviceId
	user.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, user); err != nil {
		return nil, errors.New("gagal memperbarui sesi")
	}

	if s.redis != nil {
		key := fmt.Sprintf("user:session:v:%s", user.ID.String())
		_ = s.redis.Set(ctx, key, user.TokenVersion, 24*time.Hour)
	}

	token, err := jwt.GenerateToken(user.ID, user.Email, user.Role, user.IsVerified, *user.DeviceId, user.TokenVersion)
	if err != nil {
		return nil, errors.New("gagal membuat token akses")
	}

	refreshToken, err := jwt.GenerateRefreshToken(user.ID, *user.DeviceId, user.TokenVersion)
	if err != nil {
		return nil, errors.New("gagal membuat token penyegar")
	}

	user.ComputeHasPin()
	s.signAvatar(ctx, user)
	return &LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         user,
	}, nil
}
