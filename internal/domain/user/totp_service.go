package user

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/skip2/go-qrcode"
)

// SetupTOTP generates a new TOTP secret for the user and returns the base32 secret and a base64 encoded PNG QR code.
// The user is not fully protected until they verify the secret via VerifyAndEnableTOTP.
func (s *service) SetupTOTP(ctx context.Context, id uuid.UUID) (string, string, error) {
	u, err := s.GetByID(ctx, id, id) // self query
	if err != nil {
		return "", "", errors.New("pengguna tidak ditemukan")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Nitip App",
		AccountName: u.Email,
	})
	if err != nil {
		return "", "", fmt.Errorf("gagal membuat kunci TOTP: %w", err)
	}

	secret := key.Secret()

	// Generate QR code PNG
	png, err := qrcode.Encode(key.URL(), qrcode.Medium, 256)
	if err != nil {
		return "", "", fmt.Errorf("gagal membuat QR Code: %w", err)
	}
	base64QR := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	// Save the secret temporarily or permanently, but leave TotpEnabled as false
	u.TotpSecret = &secret
	u.TotpEnabled = false
	u.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, u); err != nil {
		return "", "", fmt.Errorf("gagal menyimpan konfigurasi: %w", err)
	}

	return secret, base64QR, nil
}

// VerifyAndEnableTOTP verifies a code against the stored secret and enables TOTP for the user.
func (s *service) VerifyAndEnableTOTP(ctx context.Context, id uuid.UUID, code string) error {
	u, err := s.GetByID(ctx, id, id)
	if err != nil {
		return errors.New("pengguna tidak ditemukan")
	}

	if u.TotpSecret == nil || *u.TotpSecret == "" {
		return errors.New("TOTP belum dikonfigurasi")
	}

	valid := totp.Validate(code, *u.TotpSecret)
	if !valid {
		return errors.New("kode TOTP tidak valid")
	}

	u.TotpEnabled = true
	u.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, u); err != nil {
		return fmt.Errorf("gagal mengaktifkan TOTP: %w", err)
	}

	return nil
}

// DisableTOTP allows a user to disable their own TOTP using a valid code.
func (s *service) DisableTOTP(ctx context.Context, id uuid.UUID, code string) error {
	u, err := s.GetByID(ctx, id, id)
	if err != nil {
		return errors.New("pengguna tidak ditemukan")
	}

	if !u.TotpEnabled || u.TotpSecret == nil {
		return errors.New("TOTP tidak aktif")
	}

	valid := totp.Validate(code, *u.TotpSecret)
	if !valid {
		return errors.New("kode TOTP tidak valid")
	}

	u.TotpEnabled = false
	u.TotpSecret = nil
	u.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, u); err != nil {
		return fmt.Errorf("gagal menonaktifkan TOTP: %w", err)
	}

	return nil
}

// AdminDisableTOTP allows an admin to disable TOTP for another user (e.g., if they lost access).
func (s *service) AdminDisableTOTP(ctx context.Context, id uuid.UUID, adminID uuid.UUID) error {
	// Verify admin role
	admin, err := s.GetByID(ctx, adminID, adminID)
	if err != nil || admin.Role != RoleAdmin {
		return errors.New("tidak memiliki hak akses admin")
	}

	u, err := s.GetByID(ctx, id, adminID)
	if err != nil {
		return errors.New("pengguna tidak ditemukan")
	}

	u.TotpEnabled = false
	u.TotpSecret = nil
	u.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, u); err != nil {
		return fmt.Errorf("gagal menghapus TOTP pengguna: %w", err)
	}

	return nil
}
