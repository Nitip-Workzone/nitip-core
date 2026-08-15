package user

import (
	"net/http"

	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

// Helper to convert fiber request to standard http request needed by webauthn library
func fiberReqToStdReq(c *fiber.Ctx) (*http.Request, error) {
	req := new(http.Request)
	if err := fasthttpadaptor.ConvertRequest(c.Context(), req, true); err != nil {
		return nil, err
	}
	// The body is read and replaced by fasthttpadaptor, but let's ensure body can be read
	req.Body = http.MaxBytesReader(nil, req.Body, 1024*1024)
	return req, nil
}

// WebAuthnRegisterBegin godoc
// @Summary      Begin WebAuthn Registration
// @Description  Starts the Passkey/Face ID registration process for the authenticated user
// @Tags         Auth
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=interface{}}
// @Router       /auth/webauthn/register/begin [post]
func (h *Handler) WebAuthnRegisterBegin(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses: token tidak valid")
	}

	options, err := h.service.WebAuthnRegisterBegin(c.Context(), userClaims.UserID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "Opsi registrasi WebAuthn berhasil dibuat", options)
}

// WebAuthnRegisterFinish godoc
// @Summary      Finish WebAuthn Registration
// @Description  Completes the Passkey registration by verifying the authenticator response
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope
// @Router       /auth/webauthn/register/finish [post]
func (h *Handler) WebAuthnRegisterFinish(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses: token tidak valid")
	}

	req, err := fiberReqToStdReq(c)
	if err != nil {
		return response.BadRequest(c, "Gagal memproses permintaan")
	}

	if err := h.service.WebAuthnRegisterFinish(c.Context(), userClaims.UserID, req); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "Passkey (Face ID/Fingerprint) berhasil didaftarkan", nil)
}

// WebAuthnLoginBegin godoc
// @Summary      Begin WebAuthn Login
// @Description  Starts the Passkey login process for a user
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        email  query  string  true  "Email or Phone Number"
// @Success      200  {object}  response.envelope{data=interface{}}
// @Router       /auth/webauthn/login/begin [post]
func (h *Handler) WebAuthnLoginBegin(c *fiber.Ctx) error {
	// Parse email from body since it's a POST
	type loginReq struct {
		Email string `json:"email"`
	}
	var req loginReq
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	options, err := h.service.WebAuthnLoginBegin(c.Context(), req.Email)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "Opsi login WebAuthn berhasil dibuat", options)
}

// WebAuthnLoginFinish godoc
// @Summary      Finish WebAuthn Login
// @Description  Completes the Passkey login and returns access token
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        email  query  string  true  "Email or Phone Number"
// @Success      200  {object}  response.envelope{data=LoginResponse}
// @Router       /auth/webauthn/login/finish [post]
func (h *Handler) WebAuthnLoginFinish(c *fiber.Ctx) error {
	// We need email from query since body is consumed by webauthn
	email := c.Query("email")
	if email == "" {
		return response.BadRequest(c, "email diperlukan di parameter query")
	}

	req, err := fiberReqToStdReq(c)
	if err != nil {
		return response.BadRequest(c, "Gagal memproses permintaan")
	}

	platform := c.Get("X-Platform", "web")
	res, err := h.service.WebAuthnLoginFinish(c.Context(), email, req, platform)
	if err != nil {
		return response.Unauthorized(c, err.Error())
	}

	return response.Success(c, "login dengan Face ID / Fingerprint berhasil", res)
}
