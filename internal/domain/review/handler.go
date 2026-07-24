package review

import (
	"database/sql"
	"errors"
	"strings"
	"time"

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
	// Tied closely to the orders endpoint layout logically
	ordersGrp := router.Group("/orders", middleware.Protected(h.db, h.redis))
	ordersGrp.Post("/:id/review", middleware.Role(user.RoleRequester), middleware.RateLimit(h.redis, 5, 1*time.Minute), h.SubmitReview)
	ordersGrp.Get("/:id/review", h.GetReview)
}

type SubmitReviewRequest struct {
	RunnerRating    int    `json:"runner_rating" validate:"required,min=1,max=5"`
	RunnerComment   string `json:"runner_comment" validate:"omitempty,max=500"`
	MerchantRating  *int   `json:"merchant_rating" validate:"omitempty,min=1,max=5"`
	MerchantComment string `json:"merchant_comment" validate:"omitempty,max=500"`
}

// SubmitReview godoc
// @Summary      Submit a review
// @Description  Allows the Requester to rate the Runner (and optionally Merchant) after order is completed
// @Tags         [User] Order
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  string  true  "Order UUID" Format(uuid)
// @Param        body  body  SubmitReviewRequest  true  "Review payload"
// @Success      200 {object} response.envelope
// @Failure      400 {object} response.envelope
// @Failure      422 {object} response.envelope{errors=[]response.ValidationError}
// @Router       /orders/{id}/review [post]
func (h *Handler) SubmitReview(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req SubmitReviewRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Ambil claims dari middleware Protected (key: "user"), bukan "userId"
	claims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok || claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	reviewerID := claims.UserID

	if err := h.service.SubmitReview(c.Context(), orderID, reviewerID, req.RunnerRating, req.RunnerComment, req.MerchantRating, req.MerchantComment); err != nil {
		lowMsg := strings.ToLower(err.Error())
		if strings.Contains(lowMsg, "duplicate") || strings.Contains(lowMsg, "unique") {
			return response.BadRequest(c, "pesanan ini sudah diulas")
		}
		if strings.Contains(lowMsg, "sql") ||
			strings.Contains(lowMsg, "constraint") ||
			strings.Contains(lowMsg, "foreign key") {
			return response.InternalError(c, err.Error())
		}
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "ulasan berhasil dikirim dan skor kepercayaan diperbarui", nil)
}

// GetReview godoc
// @Summary      Get review by order
// @Description  View the review left on a specific order
// @Tags         [Shared] Order View
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Order UUID" Format(uuid)
// @Success      200 {object} response.envelope{data=Review}
// @Failure      404 {object} response.envelope
// @Router       /orders/{id}/review [get]
func (h *Handler) GetReview(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	rv, err := h.service.GetReviewByOrder(c.Context(), orderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return response.Success(c, "ulasan tidak ditemukan atau belum diberikan", nil)
		}
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "ulasan berhasil diambil", rv)
}
