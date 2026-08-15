package user

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/auth"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/codecoffy/nitip-core/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	service Service
	db      *bun.DB
	redis   *cache.Redis
}

func NewHandler(service Service, db *bun.DB, redis *cache.Redis) *Handler {
	return &Handler{service: service, db: db, redis: redis}
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	// User profile & registration
	g := router.Group("/users")
	g.Post("/register", middleware.RateLimit(h.redis, 3, 1*time.Minute), h.Create)
	g.Post("/onboard/runner", middleware.RateLimit(h.redis, 3, 1*time.Minute), h.OnboardRunner)
	g.Post("/onboard/merchant", middleware.RateLimit(h.redis, 3, 1*time.Minute), h.OnboardMerchant)
	g.Get("/me", middleware.Protected(h.db, h.redis), h.GetMe)
	g.Get("/me/bank-account", middleware.Protected(h.db, h.redis), h.GetMyBankAccount)
	g.Post("/me/bank-account", middleware.Protected(h.db, h.redis), h.RegisterMyBankAccount)
	g.Post("/pin/setup", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 3, 1*time.Minute), h.SetupPin)
	g.Post("/pin/change", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.ChangePin)
	g.Post("/pin/verify", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.VerifyPin)
	g.Put("/home", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 10, 1*time.Minute), h.UpdateHome)
	g.Put("/profile", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 10, 1*time.Minute), h.UpdateProfile)
	g.Put("/fcm-token", middleware.Protected(h.db, h.redis), h.UpdateFcmToken)
	g.Put("/accepting-orders", middleware.Protected(h.db, h.redis), middleware.Role(RoleRunner), h.UpdateAcceptingOrders)
	g.Post("/location", middleware.Protected(h.db, h.redis), middleware.Role(RoleRunner), h.UpdateLocation)
	g.Post("/heartbeat", middleware.Protected(h.db, h.redis), middleware.Role(RoleRunner), h.Heartbeat)
	g.Get("/invitations/validate", h.ValidateInvitation)

	// Admin-only User Management
	adminUser := router.Group("/admin/users", middleware.Protected(h.db, h.redis), middleware.Role(RoleAdmin))
	adminUser.Get("/", h.AdminListUsers)
	adminUser.Post("/", h.AdminCreate)
	adminUser.Get("/all", h.List) // Maps to full list
	adminUser.Get("/:id", h.Get)
	adminUser.Delete("/:id", h.Delete)
	adminUser.Put("/:id/verify", h.AdminVerifyUser)
	adminUser.Put("/:id/profile", h.AdminUpdateProfile)
	adminUser.Put("/:id/trust", h.AdminUpdateTrust)
	adminUser.Put("/:id/suspend", h.AdminSuspendUser)
	adminUser.Post("/:id/unlock-pin", h.AdminUnlockPin)
	adminUser.Post("/:id/totp-disable", h.AdminDisableTOTP)
	adminUser.Post("/:id/bank-account", h.AdminRegisterBankAccount)
	adminUser.Get("/:id/bank-account", h.AdminGetBankAccount)
	adminUser.Post("/:id/reset-password", h.AdminResetPassword)
	adminUser.Post("/invitations", h.AdminCreateInvitation)
	adminUser.Get("/invitations", h.AdminListInvitations)

	// TOTP Management
	g.Post("/totp/setup", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 3, 1*time.Minute), h.SetupTOTP)
	g.Post("/totp/enable", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.EnableTOTP)
	g.Post("/totp/disable", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.DisableTOTP)

	authGroup := router.Group("/auth")
	authGroup.Post("/login", auth.RequireGrant(h.db), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.Login)
	authGroup.Post("/refresh", auth.RequireGrant(h.db), middleware.RateLimit(h.redis, 10, 1*time.Minute), h.Refresh)
	authGroup.Post("/logout", middleware.Protected(h.db, h.redis), h.Logout)

	// WebAuthn Passkeys
	authGroup.Post("/webauthn/register/begin", middleware.Protected(h.db, h.redis), h.WebAuthnRegisterBegin)
	authGroup.Post("/webauthn/register/finish", middleware.Protected(h.db, h.redis), h.WebAuthnRegisterFinish)
	authGroup.Post("/webauthn/login/begin", middleware.RateLimit(h.redis, 5, 1*time.Minute), h.WebAuthnLoginBegin)
	authGroup.Post("/webauthn/login/finish", middleware.RateLimit(h.redis, 5, 1*time.Minute), h.WebAuthnLoginFinish)
}

// List godoc
// @Summary      List all users
// @Description  Retrieve a list of all non-deleted users
// @Tags         [Admin] User Management
// @Produce      json
// @Success      200  {object}  response.envelope{data=[]User}
// @Failure      500  {object}  response.envelope
// @Router       /users [get]
func (h *Handler) List(c *fiber.Ctx) error {
	users, err := h.service.GetAll(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar pengguna berhasil diambil", users)
}

// Get godoc
// @Summary      Get user by ID
// @Description  Retrieve a single user by their UUID
// @Tags         [Admin] User Management
// @Produce      json
// @Param        id   path      string  true  "User UUID"  Format(uuid)
// @Success      200  {object}  response.envelope{data=User}
// @Failure      400  {object}  response.envelope
// @Failure      404  {object}  response.envelope
// @Router       /users/{id} [get]
func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	// Get requester ID if available (optional for public info, but we want masking)
	var requesterID uuid.UUID
	if claims := jwt.GetClaims(c); claims != nil {
		requesterID = claims.UserID
	}

	user, err := h.service.GetByID(c.Context(), id, requesterID)
	if err != nil {
		return response.NotFound(c, "pengguna tidak ditemukan")
	}
	return response.Success(c, "data pengguna berhasil diambil", user)
}

// Create godoc
// @Summary      Register a new user
// @Description  Register a new user (requester or runner)
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      CreateUserRequest  true  "User payload"
// @Success      201   {object}  response.envelope{data=User}
// @Failure      400   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Failure      500   {object}  response.envelope
// @Router       /users/register [post]
func (h *Handler) Create(c *fiber.Ctx) error {
	var req CreateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	// Validate request fields
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	user, err := h.service.Create(c.Context(), req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, "pengguna berhasil didaftarkan", user)
}

// Delete godoc
// @Summary      Delete user by ID
// @Description  Soft-delete a user by their UUID
// @Tags         [Admin] User Management
// @Param        id   path  string  true  "User UUID"  Format(uuid)
// @Success      204
// @Failure      400  {object}  response.envelope
// @Failure      500  {object}  response.envelope
// @Router       /users/{id} [delete]
func (h *Handler) Delete(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	if err := h.service.Delete(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.NoContent(c)
}

// Login godoc
// @Summary      Login user
// @Description  Authenticate user and return JWT token. Requires a valid grant token from POST /auth/grant.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        X-Grant-Token  header    string           true  "Grant token from POST /auth/grant"
// @Param        body           body      LoginRequest     true  "Login credentials"
// @Success      200   {object}  response.envelope{data=LoginResponse}
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /auth/login [post]
func (h *Handler) Login(c *fiber.Ctx) error {
	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	platform := c.Get("X-Platform")
	res, err := h.service.Login(c.Context(), req, platform)
	if err != nil {
		return response.Unauthorized(c, err.Error())
	}

	return response.Success(c, "login berhasil", res)
}

// Refresh godoc
// @Summary      Refresh access token
// @Description  Use a refresh token to get a new access token. Requires a valid grant token from POST /auth/grant.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        X-Grant-Token  header    string           true  "Grant token from POST /auth/grant"
// @Param        body           body      RefreshRequest   true  "Refresh payload"
// @Success      200   {object}  response.envelope{data=LoginResponse}
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /auth/refresh [post]
func (h *Handler) Refresh(c *fiber.Ctx) error {
	var req RefreshRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	res, err := h.service.Refresh(c.Context(), req.RefreshToken)
	if err != nil {
		return response.Unauthorized(c, err.Error())
	}

	return response.Success(c, "token berhasil diperbarui", res)
}

// Logout godoc
// @Summary      Logout user
// @Description  Invalidate the user session (client should also delete the local token)
// @Tags         Auth
// @Produce      json
// @Security     BearerAuth
// @Success      200   {object}  response.envelope
// @Router       /auth/logout [post]
func (h *Handler) Logout(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	// Clear device_id in DB to invalidate the session
	_, err := h.db.NewUpdate().
		Table("users").
		Set("device_id = ?", nil).
		Set("token_version = token_version + 1"). // Increment version to invalidate all tokens
		Where("id = ?", userClaims.UserID).
		Exec(c.Context())

	if err != nil {
		return response.InternalError(c, "gagal melakukan logout")
	}

	// Clear Redis cache
	if h.redis != nil {
		cacheKey := fmt.Sprintf("user:session:v:%s", userClaims.UserID.String())
		_ = h.redis.Del(c.Context(), cacheKey)
	}

	return response.Success(c, "logout berhasil", nil)
}

// AdminListUsers godoc
// @Summary      [ADMIN] List users
// @Description  Retrieve users with filters (role, is_verified)
// @Tags         [Admin] User Management
// @Produce      json
// @Security     BearerAuth
// @Param        role         query   string  false  "Role filter"
// @Param        is_verified  query   bool    false  "Verification filter"
// @Success      200  {object}  response.envelope{data=[]User}
// @Router       /admin/users [get]
func (h *Handler) AdminListUsers(c *fiber.Ctx) error {
	role := c.Query("role")
	var isVerified *bool
	var isSuspended *bool

	if vStr := c.Query("is_verified"); vStr != "" {
		v, err := strconv.ParseBool(vStr)
		if err == nil {
			isVerified = &v
		}
	}

	if sStr := c.Query("is_suspended"); sStr != "" {
		s, err := strconv.ParseBool(sStr)
		if err == nil {
			isSuspended = &s
		}
	}

	users, err := h.service.GetAllWithFilters(c.Context(), role, isVerified, isSuspended)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar pengguna berhasil diambil", users)
}

// AdminVerifyUser godoc
// @Summary      [ADMIN] Verify user
// @Description  Toggle verification status of a user
// @Tags         [Admin] User Management
// @Produce      json
// @Security     BearerAuth
// @Param        id           path     string  true  "User UUID"  Format(uuid)
// @Param        is_verified  query    bool    true  "Verification status"
// @Success      200  {object}  response.envelope
// @Router       /admin/users/{id}/verify [put]
func (h *Handler) AdminVerifyUser(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	vStr := c.Query("is_verified")
	isVerified, err := strconv.ParseBool(vStr)
	if err != nil {
		return response.BadRequest(c, "nilai is_verified tidak valid")
	}

	claimsTmp := jwt.GetClaims(c)
	if claimsTmp == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claimsTmp.UserID
	if err := h.service.UpdateVerification(c.Context(), id, adminID, isVerified); err != nil {
		return response.BadRequest(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s updated verification for User %s to %v", adminID, id, isVerified)

	return response.Success(c, "status verifikasi pengguna berhasil diperbarui", nil)
}

// AdminUpdateTrust godoc
// @Summary      [ADMIN] Update User Trust Score
// @Description  Manually update a user's trust score
// @Tags         [Admin] User Management
// @Produce      json
// @Security     BearerAuth
// @Param        id     path     string  true  "User UUID"  Format(uuid)
// @Param        score  query    int     true  "New Trust Score"
// @Success      200  {object}  response.envelope
// @Router       /admin/users/{id}/trust [put]
func (h *Handler) AdminUpdateTrust(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	scoreStr := c.Query("score")
	score, err := strconv.Atoi(scoreStr)
	if err != nil {
		return response.BadRequest(c, "nilai skor tidak valid")
	}

	claimsTmp := jwt.GetClaims(c)
	if claimsTmp == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claimsTmp.UserID
	if err := h.service.UpdateTrustScore(c.Context(), id, adminID, score); err != nil {
		return response.BadRequest(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s updated trust score for User %s to %v", adminID, id, score)

	return response.Success(c, "skor kepercayaan pengguna berhasil diperbarui", nil)
}

// AdminSuspendUser godoc
// @Summary      [ADMIN] Suspend/Unsuspend user
// @Description  Toggle suspend status of a user with a reason
// @Tags         [Admin] User Management
// @Produce      json
// @Security     BearerAuth
// @Param        id            path     string  true  "User UUID"  Format(uuid)
// @Param        is_suspended  query    bool    true  "Suspend status"
// @Param        reason        query    string  false "Reason for suspend"
// @Success      200  {object}  response.envelope
// @Router       /admin/users/{id}/suspend [put]
func (h *Handler) AdminSuspendUser(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	sStr := c.Query("is_suspended")
	isSuspended, err := strconv.ParseBool(sStr)
	if err != nil {
		return response.BadRequest(c, "nilai is_suspended tidak valid")
	}

	reason := c.Query("reason")
	if isSuspended && reason == "" {
		return response.BadRequest(c, "alasan wajib diisi untuk suspensi")
	}

	claimsTmp := jwt.GetClaims(c)
	if claimsTmp == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claimsTmp.UserID
	if err := h.service.UpdateSuspendStatus(c.Context(), id, adminID, isSuspended, reason); err != nil {
		return response.BadRequest(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s updated suspend status for User %s to %v", adminID, id, isSuspended)

	return response.Success(c, "status penangguhan pengguna berhasil diperbarui", nil)
}

// GetMe godoc
// @Summary      Get current logged-in user profile
// @Description  Retrieve the profile of the currently authenticated user
// @Tags         [User] Profile
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=User}
// @Failure      401  {object}  response.envelope
// @Failure      404  {object}  response.envelope
// @Router       /users/me [get]
func (h *Handler) GetMe(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses: token tidak valid")
	}

	user, err := h.service.GetByID(c.Context(), userClaims.UserID, userClaims.UserID)
	if err != nil {
		return response.NotFound(c, "profil pengguna tidak ditemukan")
	}

	return response.Success(c, "profil berhasil diambil", user)
}

// UpdateHome godoc
// @Summary      Update user home location
// @Description  Update the home coordinates and address for the current authenticated user
// @Tags         [User] Profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpdateHomeRequest  true  "Home payload"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /users/home [put]
func (h *Handler) UpdateHome(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses: token tidak valid")
	}

	var req UpdateHomeRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.UpdateHome(c.Context(), userClaims.UserID, req); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "lokasi rumah berhasil diperbarui", nil)
}

// UpdateProfile godoc
// @Summary      Update user profile
// @Description  Update name, whatsapp number, and optionally avatar image
// @Tags         [User] Profile
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        name             formData  string  true   "Full Name"
// @Param        whatsapp_number  formData  string  true   "WhatsApp Number"
// @Param        home_address     formData  string  false  "Home Address"
// @Param        avatar           formData  file    false  "Avatar Image"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Router       /users/profile [put]
func (h *Handler) UpdateProfile(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses: token tidak valid")
	}

	var req UpdateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Handle Avatar file upload if present
	var avatarReader io.Reader
	var avatarFilename string

	file, err := c.FormFile("avatar")
	if err == nil {
		if file.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran gambar avatar terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(file) {
			return response.BadRequest(c, "avatar harus berupa file gambar (jpg, jpeg, png)")
		}
		f, err := file.Open()
		if err != nil {
			return response.InternalError(c, "gagal membuka gambar avatar")
		}
		defer func() { _ = f.Close() }()
		avatarReader = f
		avatarFilename = file.Filename
	}

	if err := h.service.UpdateProfile(c.Context(), userClaims.UserID, req, avatarReader, avatarFilename); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "profil berhasil diperbarui", nil)
}

// AdminUpdateProfile godoc
// @Summary      [ADMIN] Update any user's profile
// @Description  Update name, whatsapp number, and optionally avatar image for a user
// @Tags         [Admin] User Management
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id               path      string  true   "User ID" Format(uuid)
// @Param        name             formData  string  true   "Full Name"
// @Param        whatsapp_number  formData  string  true   "WhatsApp Number"
// @Param        home_address     formData  string  false  "Home Address"
// @Param        avatar           formData  file    false  "Avatar Image"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Router       /admin/users/{id}/profile [put]
func (h *Handler) AdminUpdateProfile(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	var req UpdateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Handle Avatar file upload if present
	var avatarReader io.Reader
	var avatarFilename string

	file, err := c.FormFile("avatar")
	if err == nil {
		if file.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran gambar avatar terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(file) {
			return response.BadRequest(c, "avatar harus berupa file gambar (jpg, jpeg, png)")
		}
		f, err := file.Open()
		if err != nil {
			return response.InternalError(c, "gagal membuka gambar avatar")
		}
		defer func() { _ = f.Close() }()
		avatarReader = f
		avatarFilename = file.Filename
	}

	if err := h.service.UpdateProfile(c.Context(), id, req, avatarReader, avatarFilename); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "profil pengguna berhasil diperbarui oleh admin", nil)
}

// UpdateLocation godoc
// @Summary      [DISABLED] Update user live location
// @Description  [DISABLED FOR MVP V2] Runner updates their current latitude and longitude
// @Tags         [Runner] Trip
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      LocationUpdate  true  "Location coordinates"
// @Success      200   {object}  response.envelope
// @Router       /users/location [post]
func (h *Handler) UpdateLocation(c *fiber.Ctx) error {
	var req LocationUpdate
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.UpdateLocation(c.Context(), claims.UserID, req.Lat, req.Lng); err != nil {
		return response.InternalError(c, err.Error())
	}
	log.Printf("[HTTP_TRACKING] Received location from %s: %f, %f", claims.UserID, req.Lat, req.Lng)

	return response.Success(c, "lokasi berhasil diperbarui", nil)
}

// LocationUpdate represents the structure of the WS message
type LocationUpdate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Heartbeat godoc
// @Summary      Runner Heartbeat for Live Tracking
// @Description  Dedicated lightweight endpoint for runner location heartbeat. Only active when online, has active trip, active orders, and app foreground.
// @Tags         [Runner] Tracking
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      HeartbeatRequest  true  "Heartbeat payload"
// @Success      200  {object}  response.envelope{data=map[string]interface{}}
// @Failure      400  {object}  response.envelope
// @Failure      403  {object}  response.envelope
// @Router       /users/heartbeat [post]
func (h *Handler) Heartbeat(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	var req HeartbeatRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Validate heartbeat conditions server-side
	user, err := h.service.GetByID(c.Context(), claims.UserID, claims.UserID)
	if err != nil {
		return response.NotFound(c, "pengguna tidak ditemukan")
	}

	if !user.IsAcceptingOrders {
		return response.Forbidden(c, "heartbeat hanya diperbolehkan saat status online")
	}

	if !req.IsForeground {
		return response.Success(c, "heartbeat diabaikan karena aplikasi background", fiber.Map{
			"status": "ignored_background",
		})
	}

	if req.ActiveOrders == 0 && (req.TripID == nil || *req.TripID == "") {
		return response.Success(c, "heartbeat diabaikan karena tidak ada trip/order aktif", fiber.Map{
			"status": "ignored_no_task",
		})
	}

	if err := h.service.UpdateHeartbeat(c.Context(), claims.UserID, req); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "heartbeat berhasil", fiber.Map{
		"status": "ok",
		"lat":    req.Lat,
		"lng":    req.Lng,
	})
}

// StreamLocation godoc
// @Summary      [DISABLED] Stream real-time location (WebSocket)
// @Description  [DISABLED FOR MVP V2] WebSocket endpoint for runners to stream their live GPS coordinates
// @Tags         [Shared] Communications & Tracking
// @Security     BearerAuth
// @Router       /users/location/stream [get]
func (h *Handler) StreamLocation(c *websocket.Conn) {
	lc := c.Locals("user")
	if lc == nil {
		_ = c.Close()
		return
	}
	userClaims, ok := lc.(*jwt.CustomClaims)
	if !ok || userClaims == nil {
		_ = c.Close()
		return
	}
	if userClaims.Role != RoleRunner {
		_ = c.WriteMessage(websocket.TextMessage, []byte("Forbidden: only runners can stream location"))
		_ = c.Close()
		return
	}

	defer func() {
		// Clean up live location when disconnected
		// Optional: we can keep it for a while or remove immediately
		// For MVP, we'll keep it so users can still see the last known location
		_ = c.Close()
	}()

	// 2. Keep connection alive with Heartbeat (Ping/Pong)
	c.SetReadLimit(4096)
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))

	// Send pings periodically to keep the connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	for {
		var update LocationUpdate
		if err := c.ReadJSON(&update); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error reading websocket message: %v", err)
			}
			break
		}

		// Update Database & Redis GEO (Unified in Service)
		log.Printf("[WS_TRACKING] Received location from %s: %f, %f", userClaims.UserID, update.Lat, update.Lng)
		_ = h.service.UpdateLocation(context.Background(), userClaims.UserID, update.Lat, update.Lng)
	}
}

// SetupPin godoc
// @Summary      Setup user transaction PIN
// @Description  Set a 6-digit numeric PIN for transactions (first time only)
// @Tags         [User] Profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      SetupPinRequest  true  "PIN payload"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /users/pin/setup [post]
func (h *Handler) SetupPin(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req SetupPinRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.SetupPin(c.Context(), userClaims.UserID, req); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "PIN berhasil diatur", nil)
}

// ChangePin godoc
// @Summary      Change user transaction PIN
// @Description  Change 6-digit numeric PIN by verifying old PIN first
// @Tags         [User] Profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      ChangePinRequest  true  "Change PIN payload"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /users/pin/change [post]
func (h *Handler) ChangePin(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req ChangePinRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.ChangePin(c.Context(), userClaims.UserID, req); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "PIN berhasil diubah", nil)
}

// VerifyPin godoc
// @Summary      Verify user transaction PIN
// @Description  Verify 6-digit numeric PIN without changing it (for step verification)
// @Tags         [User] Profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      VerifyPinRequest  true  "Verify PIN payload"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /users/pin/verify [post]
func (h *Handler) VerifyPin(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req VerifyPinRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.VerifyPin(c.Context(), userClaims.UserID, req.Pin); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "PIN valid", nil)
}

// AdminUnlockPin godoc
// @Summary      [ADMIN] Unlock user PIN
// @Description  Reset PIN attempts and remove lockout status for a user
// @Tags         [Admin] User Management
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "User UUID"  Format(uuid)
// @Success      200  {object}  response.envelope
// @Failure      400  {object}  response.envelope
// @Router       /admin/users/{id}/unlock-pin [post]
func (h *Handler) AdminUnlockPin(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pengguna tidak valid")
	}

	if err := h.service.UnlockPin(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}

	claimsTmpUnlock := jwt.GetClaims(c)
	if claimsTmpUnlock == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claimsTmpUnlock.UserID
	log.Printf("[ADMIN_ACTION] Admin %s unlocked PIN for User %s", adminID, id)

	return response.Success(c, "PIN pengguna berhasil dibuka", nil)
}

// UpdateAcceptingOrders godoc
// @Summary      Toggle order acceptance status
// @Description  Allows runners to enable or disable matching for proximity orders (< 10km)
// @Tags         [Runner] Profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpdateAcceptingOrdersRequest  true  "Accepting Orders payload"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /users/accepting-orders [put]
func (h *Handler) UpdateAcceptingOrders(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req UpdateAcceptingOrdersRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if err := h.service.UpdateAcceptingOrders(c.Context(), userClaims.UserID, req.IsAcceptingOrders); err != nil {
		return response.BadRequest(c, err.Error())
	}

	status := "dinonaktifkan"
	if req.IsAcceptingOrders {
		status = "diaktifkan"
	}
	return response.Success(c, fmt.Sprintf("Penerimaan order berhasil %s", status), nil)
}

// AdminCreate godoc
// @Summary      [ADMIN] Create a new user manually
// @Description  Admin can create any user role by confirming their own password.
// @Tags         [Admin] User Management
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      AdminCreateUserRequest  true  "Create user payload"
// @Success      201   {object}  response.envelope{data=User}
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      500   {object}  response.envelope
// @Router       /admin/users [post]
func (h *Handler) AdminCreate(c *fiber.Ctx) error {
	adminClaims := jwt.GetClaims(c)
	if adminClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req AdminCreateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Verify currently logged in admin's password
	var adminUser User
	err := h.db.NewSelect().Model(&adminUser).Where("id = ?", adminClaims.UserID).Scan(c.Context())
	if err != nil {
		return response.InternalError(c, "gagal memverifikasi akun admin")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(adminUser.Password), []byte(req.AdminPassword)); err != nil {
		return response.BadRequest(c, "konfirmasi password admin salah")
	}

	user, err := h.service.AdminCreate(c.Context(), req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s created User %s (%s) with role %s", adminClaims.UserID, user.ID, user.Email, user.Role)

	return response.Created(c, "pengguna berhasil didaftarkan oleh admin", user)
}

type UpdateFcmTokenRequest struct {
	FcmToken string `json:"fcm_token" validate:"required"`
}

// UpdateFcmToken godoc
// @Summary      Update user FCM Device Token
// @Description  Update the Firebase Cloud Messaging device token for the current user
// @Tags         [User] Profile
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpdateFcmTokenRequest  true  "FCM token payload"
// @Success      200   {object}  response.envelope
// @Router       /users/fcm-token [put]
func (h *Handler) UpdateFcmToken(c *fiber.Ctx) error {
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req UpdateFcmTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	_, err := h.db.NewUpdate().
		Table("users").
		Set("fcm_token = ?", req.FcmToken).
		Where("id = ?", userClaims.UserID).
		Exec(c.Context())

	if err != nil {
		return response.InternalError(c, "gagal memperbarui FCM token")
	}

	return response.Success(c, "FCM token berhasil diperbarui", nil)
}

// GetMyBankAccount godoc
// @Summary      Get registered bank account
// @Description  Get the registered bank account for the current authenticated user
// @Tags         [User] Finance
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=UserBankAccount}
// @Router       /users/me/bank-account [get]
func (h *Handler) GetMyBankAccount(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	uba, err := h.service.GetBankAccount(c.Context(), claims.UserID)
	if err != nil {
		// Do not return 404 - allow client to handle empty state with 200
		return response.Success(c, "rekening belum didaftarkan", nil)
	}

	return response.Success(c, "rekening berhasil diambil", uba)
}

type RegisterMyBankAccountRequest struct {
	BankName    string `json:"bank_name" validate:"required"`
	AccountNo   string `json:"account_no" validate:"required"`
	AccountName string `json:"account_name" validate:"required"`
}

// RegisterMyBankAccount godoc
// @Summary      Register own bank account
// @Description  Register own bank account details. Only allowed once.
// @Tags         [User] Finance
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      RegisterMyBankAccountRequest true  "Registration details"
// @Success      200   {object}  response.envelope
// @Router       /users/me/bank-account [post]
func (h *Handler) RegisterMyBankAccount(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	var req RegisterMyBankAccountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.RegisterBankAccount(c.Context(), claims.UserID, req.BankName, req.AccountNo, req.AccountName); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "rekening Anda berhasil didaftarkan", nil)
}

type AdminRegisterBankAccountRequest struct {
	BankName      string `json:"bank_name" validate:"required"`
	AccountNo     string `json:"account_no" validate:"required"`
	AccountName   string `json:"account_name" validate:"required"`
	AdminPassword string `json:"admin_password" validate:"required"`
	TotpCode      string `json:"totp_code" validate:"required,len=6,numeric"`
}

// AdminRegisterBankAccount godoc
// @Summary      [ADMIN] Register user bank account
// @Description  Register or update a user's registered bank account. Highly restricted. Requires password and TOTP code.
// @Tags         [Admin] User Management
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                          true  "User ID"
// @Param        body  body      AdminRegisterBankAccountRequest true  "Registration details"
// @Success      200   {object}  response.envelope
// @Router       /admin/users/{id}/bank-account [post]
func (h *Handler) AdminRegisterBankAccount(c *fiber.Ctx) error {
	adminClaims := jwt.GetClaims(c)
	if adminClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID user tidak valid")
	}

	var req AdminRegisterBankAccountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.AdminRegisterBankAccount(c.Context(), targetID, req.BankName, req.AccountNo, req.AccountName, req.AdminPassword, req.TotpCode, adminClaims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "rekening pengguna berhasil didaftarkan oleh admin", nil)
}

type AdminResetPasswordRequest struct {
	NewPassword   string `json:"new_password" validate:"required,min=8,max=72"`
	AdminPassword string `json:"admin_password" validate:"required"`
	TotpCode      string `json:"totp_code" validate:"required,len=6,numeric"`
}

// AdminResetPassword godoc
// @Summary      [ADMIN] Reset user password
// @Description  Reset a user's password. Highly restricted. Requires admin password + TOTP. Revokes all user sessions.
// @Tags         [Admin] User Management
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                     true  "User ID"
// @Param        body  body      AdminResetPasswordRequest  true  "Reset details"
// @Success      200   {object}  response.envelope
// @Router       /admin/users/{id}/reset-password [post]
func (h *Handler) AdminResetPassword(c *fiber.Ctx) error {
	adminClaims := jwt.GetClaims(c)
	if adminClaims == nil {
		return response.Unauthorized(c, "tidak memiliki akses")
	}

	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID user tidak valid")
	}

	var req AdminResetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.AdminResetPassword(c.Context(), targetID, req.NewPassword, req.AdminPassword, req.TotpCode, adminClaims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "password pengguna berhasil direset oleh admin", nil)
}

// AdminGetBankAccount godoc
// @Summary      [ADMIN] Get user bank account
// @Description  Get a user's registered bank account details by their ID.
// @Tags         [Admin] User Management
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string  true  "User ID"
// @Success      200   {object}  response.envelope{data=UserBankAccount}
// @Router       /admin/users/{id}/bank-account [get]
func (h *Handler) AdminGetBankAccount(c *fiber.Ctx) error {
	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID user tidak valid")
	}

	uba, err := h.service.GetBankAccount(c.Context(), targetID)
	if err != nil {
		// Return 200 with nil data so UI can display empty state without error toast / red Network entry
		return response.Success(c, "rekening pengguna belum didaftarkan", nil)
	}

	return response.Success(c, "rekening pengguna berhasil diambil", uba)
}

func (h *Handler) OnboardRunner(c *fiber.Ctx) error {
	token := c.FormValue("token")
	name := c.FormValue("name")
	email := c.FormValue("email")
	password := c.FormValue("password")
	whatsappNumber := c.FormValue("whatsapp_number")
	idCardNumber := c.FormValue("id_card_number")
	if idCardNumber == "" {
		idCardNumber = "0000000000000000"
	}

	if token == "" || name == "" || email == "" || password == "" || whatsappNumber == "" {
		return response.BadRequest(c, "semua kolom pendaftaran wajib diisi")
	}

	if idCardNumber != "0000000000000000" && len(idCardNumber) != 16 {
		return response.BadRequest(c, "nomor KTP harus berjumlah 16 digit")
	}

	var ic io.Reader
	var idCardFilename string
	idCardFile, err := c.FormFile("id_card")
	if err == nil {
		if idCardFile.Size > 20*1024*1024 {
			return response.BadRequest(c, "ukuran foto KTP terlalu besar (maksimal 20MB)")
		}
		if !fileutil.IsImage(idCardFile) {
			return response.BadRequest(c, "file KTP harus berupa gambar (jpg, jpeg, png)")
		}
		opened, err := idCardFile.Open()
		if err != nil {
			return response.InternalError(c, "gagal memproses file KTP")
		}
		defer func() { _ = opened.Close() }()
		ic = opened
		idCardFilename = idCardFile.Filename
	}

	selfieFile, err := c.FormFile("selfie")
	if err != nil {
		return response.BadRequest(c, "foto selfie wajib diunggah")
	}
	if selfieFile.Size > 20*1024*1024 {
		return response.BadRequest(c, "ukuran foto selfie terlalu besar (maksimal 20MB)")
	}
	if !fileutil.IsImage(selfieFile) {
		return response.BadRequest(c, "file selfie harus berupa gambar (jpg, jpeg, png)")
	}

	sf, err := selfieFile.Open()
	if err != nil {
		return response.InternalError(c, "gagal memproses file selfie")
	}
	defer func() { _ = sf.Close() }()

	req := OnboardRunnerRequest{
		Token:          token,
		Name:           name,
		Email:          email,
		Password:       password,
		WhatsappNumber: whatsappNumber,
		IdCardNumber:   idCardNumber,
		IdCardFile:     ic,
		IdCardFilename: idCardFilename,
		SelfieFile:     sf,
		SelfieFilename: selfieFile.Filename,
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	user, err := h.service.OnboardRunner(c.Context(), req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "pendaftaran Runner berhasil, mohon tunggu peninjauan dokumen oleh admin", user)
}

func (h *Handler) OnboardMerchant(c *fiber.Ctx) error {
	token := c.FormValue("token")
	name := c.FormValue("name")
	email := c.FormValue("email")
	password := c.FormValue("password")
	whatsappNumber := c.FormValue("whatsapp_number")
	merchantName := c.FormValue("merchant_name")
	description := c.FormValue("description")
	address := c.FormValue("address")
	latitudeStr := c.FormValue("latitude")
	longitudeStr := c.FormValue("longitude")
	category := c.FormValue("category")
	monthlySalesRange := c.FormValue("monthly_sales_range")
	averageItemPriceStr := c.FormValue("average_item_price")

	if token == "" || name == "" || email == "" || password == "" || whatsappNumber == "" || merchantName == "" || address == "" || latitudeStr == "" || longitudeStr == "" || category == "" || monthlySalesRange == "" || averageItemPriceStr == "" {
		return response.BadRequest(c, "semua kolom pendaftaran merchant wajib diisi")
	}

	latitude, err := strconv.ParseFloat(latitudeStr, 64)
	if err != nil {
		return response.BadRequest(c, "format koordinat latitude tidak valid")
	}

	longitude, err := strconv.ParseFloat(longitudeStr, 64)
	if err != nil {
		return response.BadRequest(c, "format koordinat longitude tidak valid")
	}

	averageItemPrice, err := strconv.ParseFloat(averageItemPriceStr, 64)
	if err != nil {
		return response.BadRequest(c, "format harga barang tidak valid")
	}

	photoFile, err := c.FormFile("photo")
	if err != nil {
		return response.BadRequest(c, "foto tempat usaha wajib diunggah")
	}
	if photoFile.Size > 5*1024*1024 {
		return response.BadRequest(c, "ukuran foto tempat usaha terlalu besar (maksimal 5MB)")
	}
	if !fileutil.IsImage(photoFile) {
		return response.BadRequest(c, "file foto usaha harus berupa gambar (jpg, jpeg, png)")
	}

	pf, err := photoFile.Open()
	if err != nil {
		return response.InternalError(c, "gagal memproses foto usaha")
	}
	defer func() { _ = pf.Close() }()

	// Cover file is optional
	var cf io.Reader
	var coverFilename string
	coverFile, err := c.FormFile("cover")
	if err == nil && coverFile != nil {
		if coverFile.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran foto sampul terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(coverFile) {
			return response.BadRequest(c, "file foto sampul harus berupa gambar (jpg, jpeg, png)")
		}
		openedCover, err := coverFile.Open()
		if err != nil {
			return response.InternalError(c, "gagal memproses foto sampul")
		}
		defer func() { _ = openedCover.Close() }()
		cf = openedCover
		coverFilename = coverFile.Filename
	}

	req := OnboardMerchantRequest{
		Token:             token,
		Name:              name,
		Email:             email,
		Password:          password,
		WhatsappNumber:    whatsappNumber,
		MerchantName:      merchantName,
		Description:       description,
		Address:           address,
		Latitude:          latitude,
		Longitude:         longitude,
		Category:          category,
		MonthlySalesRange: monthlySalesRange,
		AverageItemPrice:  averageItemPrice,
		PhotoFile:         pf,
		PhotoFilename:     photoFile.Filename,
		CoverFile:         cf,
		CoverFilename:     coverFilename,
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	user, err := h.service.OnboardMerchant(c.Context(), req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "pendaftaran Merchant berhasil, mohon tunggu kurasi oleh tim admin", user)
}

type CreateInvitationRequest struct {
	PhoneNumber string `json:"phone_number" validate:"required,min=9,max=15"`
	Role        string `json:"role"         validate:"required,oneof=runner merchant"`
}

func (h *Handler) AdminCreateInvitation(c *fiber.Ctx) error {
	var req CreateInvitationRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "body request tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "tidak diotorisasi")
	}
	actorID := claims.UserID

	invite, err := h.service.CreateInvitation(c.Context(), actorID, req.PhoneNumber, req.Role)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "undangan pendaftaran berhasil dibuat", invite)
}

func (h *Handler) AdminListInvitations(c *fiber.Ctx) error {
	invites, err := h.service.ListInvitations(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar undangan pendaftaran berhasil diambil", invites)
}

func (h *Handler) ValidateInvitation(c *fiber.Ctx) error {
	token := c.Query("token")
	if token == "" {
		return response.BadRequest(c, "token pendaftaran wajib disertakan")
	}

	invite, err := h.service.ValidateInvitation(c.Context(), token)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "token pendaftaran valid", invite)
}


