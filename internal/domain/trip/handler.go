package trip

import (
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/codecoffy/nitip-core/pkg/validator"
	"github.com/gofiber/fiber/v2"
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
	trips := router.Group("/trips", middleware.Protected(h.db, h.redis))

	// Shared / Requester routes
	trips.Get("/", h.ListActive)

	// Runner specific routes
	trips.Post("/", middleware.Role(user.RoleRunner), h.Create)
	trips.Get("/me", middleware.Role(user.RoleRunner), h.GetMyTrips)
	trips.Post("/:id/start", middleware.Role(user.RoleRunner), h.Start)
	trips.Post("/:id/cancel", middleware.Role(user.RoleRunner), h.Cancel)
	trips.Post("/:id/complete", middleware.Role(user.RoleRunner), h.Complete)
}

// Create godoc
// @Summary      Create a new trip
// @Description  Declare a new journey as a runner
// @Tags         [Runner] Trip
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateTripRequest  true  "Trip payload"
// @Success      201   {object}  response.envelope{data=Trip}
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /trips [post]
func (h *Handler) Create(c *fiber.Ctx) error {
	var req CreateTripRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	claims := c.Locals("user").(*jwt.CustomClaims)

	trip, err := h.service.Create(c.Context(), claims.UserID, req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Created(c, "perjalanan berhasil dibuat", trip)
}

// GetMyTrips godoc
// @Summary      Get runner trips
// @Description  Retrieve all trips declared by the logged-in runner
// @Tags         [Runner] Trip
// @Produce      json
// @Security     BearerAuth
// @Success      200   {object}  response.envelope{data=[]Trip}
// @Router       /trips/me [get]
func (h *Handler) GetMyTrips(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)

	trips, err := h.service.GetByRunner(c.Context(), claims.UserID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar perjalanan berhasil diambil", trips)
}

// ListActive godoc
// @Summary      List all active trips
// @Description  Retrieve all active trips currently declared by runners
// @Tags         Shared
// @Produce      json
// @Security     BearerAuth
// @Success      200   {object}  response.envelope{data=[]Trip}
// @Router       /trips [get]
func (h *Handler) ListActive(c *fiber.Ctx) error {
	trips, err := h.service.ListActive(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar perjalanan aktif berhasil diambil", trips)
}

// Start godoc
// @Summary      Start a trip
// @Description  Mark a trip as started (in progress)
// @Tags         [Runner] Trip
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Trip ID"
// @Success      200   {object}  response.envelope
// @Router       /trips/{id}/start [post]
func (h *Handler) Start(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID perjalanan tidak valid")
	}

	claims := c.Locals("user").(*jwt.CustomClaims)

	if err := h.service.Start(c.Context(), id, claims.UserID); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "perjalanan berhasil dimulai", nil)
}

// Cancel godoc
// @Summary      Cancel a trip
// @Description  Mark a trip as cancelled
// @Tags         [Runner] Trip
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Trip ID"
// @Success      200   {object}  response.envelope
// @Router       /trips/{id}/cancel [post]
func (h *Handler) Cancel(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID perjalanan tidak valid")
	}

	claims := c.Locals("user").(*jwt.CustomClaims)

	if err := h.service.Cancel(c.Context(), id, claims.UserID); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "perjalanan berhasil dibatalkan", nil)
}

// Complete godoc
// @Summary      Complete a trip
// @Description  Mark a started trip as completed
// @Tags         [Runner] Trip
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Trip ID"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /trips/{id}/complete [post]
func (h *Handler) Complete(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID perjalanan tidak valid")
	}

	claims := c.Locals("user").(*jwt.CustomClaims)

	if err := h.service.Complete(c.Context(), id, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "perjalanan berhasil diselesaikan", nil)
}
