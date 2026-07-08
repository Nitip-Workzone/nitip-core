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
	g.Get("/me", middleware.Protected(h.db, h.redis), h.GetMe)
	g.Post("/pin/setup", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 3, 1*time.Minute), h.SetupPin)
	g.Post("/pin/change", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.ChangePin)
	g.Post("/pin/verify", middleware.Protected(h.db, h.redis), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.VerifyPin)
	g.Put("/home", middleware.Protected(h.db, h.redis), h.UpdateHome)
	g.Put("/profile", middleware.Protected(h.db, h.redis), h.UpdateProfile)
	g.Put("/accepting-orders", middleware.Protected(h.db, h.redis), middleware.Role(RoleRunner), h.UpdateAcceptingOrders)
	// Removed for MVP v2 - Live GPS dynamic tracking is no longer used
	// g.Post("/location", middleware.Protected(h.db, h.redis), middleware.Role(RoleRunner), h.UpdateLocation)
	// g.Get("/location/stream", middleware.Protected(h.db, h.redis), websocket.New(h.StreamLocation))

	// Admin-only User Management
	adminUser := router.Group("/admin/users", middleware.Protected(h.db, h.redis), middleware.Role(RoleAdmin))
	adminUser.Get("/", h.AdminListUsers)
	adminUser.Get("/all", h.List) // Maps to full list
	adminUser.Get("/:id", h.Get)
	adminUser.Delete("/:id", h.Delete)
	adminUser.Put("/:id/verify", h.AdminVerifyUser)
	adminUser.Put("/:id/profile", h.AdminUpdateProfile)
	adminUser.Put("/:id/trust", h.AdminUpdateTrust)
	adminUser.Put("/:id/suspend", h.AdminSuspendUser)
	adminUser.Post("/:id/unlock-pin", h.AdminUnlockPin)

	authGroup := router.Group("/auth")
	authGroup.Post("/login", auth.RequireGrant(h.db), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.Login)
	authGroup.Post("/refresh", auth.RequireGrant(h.db), middleware.RateLimit(h.redis, 10, 1*time.Minute), h.Refresh)
	authGroup.Post("/logout", middleware.Protected(h.db, h.redis), h.Logout)
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
	return response.Success(c, "users retrieved successfully", users)
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
		return response.BadRequest(c, "invalid user id")
	}

	// Get requester ID if available (optional for public info, but we want masking)
	var requesterID uuid.UUID
	if claims, ok := c.Locals("user").(*jwt.CustomClaims); ok {
		requesterID = claims.UserID
	}

	user, err := h.service.GetByID(c.Context(), id, requesterID)
	if err != nil {
		return response.NotFound(c, "user not found")
	}
	return response.Success(c, "user retrieved successfully", user)
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
	return response.Created(c, "user created successfully", user)
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
		return response.BadRequest(c, "invalid user id")
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

	return response.Success(c, "login successful", res)
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

	return response.Success(c, "token refreshed successfully", res)
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access")
	}

	// Clear device_id in DB to invalidate the session
	_, err := h.db.NewUpdate().
		Table("users").
		Set("device_id = ?", nil).
		Set("token_version = token_version + 1"). // Increment version to invalidate all tokens
		Where("id = ?", userClaims.UserID).
		Exec(c.Context())

	if err != nil {
		return response.InternalError(c, "Gagal melakukan logout")
	}

	// Clear Redis cache
	if h.redis != nil {
		cacheKey := fmt.Sprintf("user:session:v:%s", userClaims.UserID.String())
		_ = h.redis.Del(c.Context(), cacheKey)
	}

	return response.Success(c, "logout successful", nil)
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
	return response.Success(c, "users retrieved successfully", users)
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
		return response.BadRequest(c, "invalid user id")
	}

	vStr := c.Query("is_verified")
	isVerified, err := strconv.ParseBool(vStr)
	if err != nil {
		return response.BadRequest(c, "invalid is_verified value")
	}

	adminID := c.Locals("user").(*jwt.CustomClaims).UserID
	if err := h.service.UpdateVerification(c.Context(), id, adminID, isVerified); err != nil {
		return response.BadRequest(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s updated verification for User %s to %v", adminID, id, isVerified)

	return response.Success(c, "user verification status updated", nil)
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
		return response.BadRequest(c, "invalid user id")
	}

	scoreStr := c.Query("score")
	score, err := strconv.Atoi(scoreStr)
	if err != nil {
		return response.BadRequest(c, "invalid score value")
	}

	adminID := c.Locals("user").(*jwt.CustomClaims).UserID
	if err := h.service.UpdateTrustScore(c.Context(), id, adminID, score); err != nil {
		return response.BadRequest(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s updated trust score for User %s to %v", adminID, id, score)

	return response.Success(c, "user trust score updated", nil)
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
		return response.BadRequest(c, "invalid user id")
	}

	sStr := c.Query("is_suspended")
	isSuspended, err := strconv.ParseBool(sStr)
	if err != nil {
		return response.BadRequest(c, "invalid is_suspended value")
	}

	reason := c.Query("reason")
	if isSuspended && reason == "" {
		return response.BadRequest(c, "reason is required for suspension")
	}

	adminID := c.Locals("user").(*jwt.CustomClaims).UserID
	if err := h.service.UpdateSuspendStatus(c.Context(), id, adminID, isSuspended, reason); err != nil {
		return response.BadRequest(c, err.Error())
	}

	log.Printf("[ADMIN_ACTION] Admin %s updated suspend status for User %s to %v", adminID, id, isSuspended)

	return response.Success(c, "user suspend status updated", nil)
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
	// 1. Get claims from context (set by Protected middleware)
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access: invalid token claims")
	}

	// 2. Fetch user from DB to get latest non-sensitive info
	user, err := h.service.GetByID(c.Context(), userClaims.UserID, userClaims.UserID)
	if err != nil {
		return response.NotFound(c, "User profile not found")
	}

	// 3. Optional: Passive location update if X-Location header is present
	if loc := c.Get("X-Location"); loc != "" {
		var lat, lng float64
		if n, _ := fmt.Sscanf(loc, "%f,%f", &lat, &lng); n == 2 {
			_ = h.service.UpdateLocation(c.Context(), userClaims.UserID, lat, lng)
		}
	}

	// Note: Password field is already excluded via json:"-" tags in User model.
	return response.Success(c, "Profile retrieved successfully", user)
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access: invalid token claims")
	}

	var req UpdateHomeRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.UpdateHome(c.Context(), userClaims.UserID, req); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "home location updated successfully", nil)
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access: invalid token claims")
	}

	var req UpdateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
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
			return response.BadRequest(c, "avatar image is too large (max 5MB)")
		}
		if !fileutil.IsImage(file) {
			return response.BadRequest(c, "avatar must be an image file (jpg, jpeg, png)")
		}
		f, err := file.Open()
		if err != nil {
			return response.InternalError(c, "failed to open avatar image")
		}
		defer func() { _ = f.Close() }()
		avatarReader = f
		avatarFilename = file.Filename
	}

	if err := h.service.UpdateProfile(c.Context(), userClaims.UserID, req, avatarReader, avatarFilename); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "profile updated successfully", nil)
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
		return response.BadRequest(c, "invalid user id")
	}

	var req UpdateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
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
			return response.BadRequest(c, "avatar image is too large (max 5MB)")
		}
		if !fileutil.IsImage(file) {
			return response.BadRequest(c, "avatar must be an image file (jpg, jpeg, png)")
		}
		f, err := file.Open()
		if err != nil {
			return response.InternalError(c, "failed to open avatar image")
		}
		defer func() { _ = f.Close() }()
		avatarReader = f
		avatarFilename = file.Filename
	}

	if err := h.service.UpdateProfile(c.Context(), id, req, avatarReader, avatarFilename); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "user profile updated successfully by admin", nil)
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
		return response.BadRequest(c, "invalid request body")
	}

	claims := c.Locals("user").(*jwt.CustomClaims)

	if err := h.service.UpdateLocation(c.Context(), claims.UserID, req.Lat, req.Lng); err != nil {
		return response.InternalError(c, err.Error())
	}
	log.Printf("[HTTP_TRACKING] Received location from %s: %f, %f", claims.UserID, req.Lat, req.Lng)

	return response.Success(c, "location updated successfully", nil)
}

// LocationUpdate represents the structure of the WS message
type LocationUpdate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// StreamLocation godoc
// @Summary      [DISABLED] Stream real-time location (WebSocket)
// @Description  [DISABLED FOR MVP V2] WebSocket endpoint for runners to stream their live GPS coordinates
// @Tags         [Shared] Communications & Tracking
// @Security     BearerAuth
// @Router       /users/location/stream [get]
func (h *Handler) StreamLocation(c *websocket.Conn) {
	userClaims := c.Locals("user").(*jwt.CustomClaims)
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access")
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access")
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access")
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
		return response.BadRequest(c, "invalid user id")
	}

	if err := h.service.UnlockPin(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}

	adminID := c.Locals("user").(*jwt.CustomClaims).UserID
	log.Printf("[ADMIN_ACTION] Admin %s unlocked PIN for User %s", adminID, id)

	return response.Success(c, "user PIN unlocked successfully", nil)
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
	userClaims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return response.Unauthorized(c, "Unauthorized access")
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
