package user

import (
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/codecoffy/nitip-core/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type TOTPVerifyRequest struct {
	Code string `json:"code" validate:"required,len=6,numeric"`
}

// SetupTOTP handles generating a new TOTP secret and QR code for the authenticated user.
func (h *Handler) SetupTOTP(c *fiber.Ctx) error {
	userID, ok := c.Locals("user").(uuid.UUID)
	if !ok {
		return response.Unauthorized(c, "Tidak diizinkan")
	}

	secret, qrBase64, err := h.service.SetupTOTP(c.Context(), userID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "TOTP berhasil disiapkan", fiber.Map{
		"secret":    secret,
		"qr_base64": qrBase64,
	})
}

// EnableTOTP verifies the first code to enable TOTP on the account.
func (h *Handler) EnableTOTP(c *fiber.Ctx) error {
	userID, ok := c.Locals("user").(uuid.UUID)
	if !ok {
		return response.Unauthorized(c, "Tidak diizinkan")
	}

	var req TOTPVerifyRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.VerifyAndEnableTOTP(c.Context(), userID, req.Code); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "Autentikasi 2-Langkah (TOTP) berhasil diaktifkan", nil)
}

// DisableTOTP disables TOTP for the current user.
func (h *Handler) DisableTOTP(c *fiber.Ctx) error {
	userID, ok := c.Locals("user").(uuid.UUID)
	if !ok {
		return response.Unauthorized(c, "Tidak diizinkan")
	}

	var req TOTPVerifyRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.DisableTOTP(c.Context(), userID, req.Code); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "Autentikasi 2-Langkah (TOTP) berhasil dinonaktifkan", nil)
}

// AdminDisableTOTP disables TOTP for a specific user.
func (h *Handler) AdminDisableTOTP(c *fiber.Ctx) error {
	adminID, ok := c.Locals("user").(uuid.UUID)
	if !ok {
		return response.Unauthorized(c, "Tidak diizinkan")
	}

	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	if err := h.service.AdminDisableTOTP(c.Context(), targetID, adminID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "TOTP pengguna berhasil dinonaktifkan", nil)
}
