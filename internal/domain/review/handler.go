package review

import (
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/middleware"
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
	Rating  int    `json:"rating" validate:"required,min=1,max=5"`
	Comment string `json:"comment" validate:"omitempty,max=500"`
}

// SubmitReview godoc
// @Summary      Submit a review
// @Description  Allows the Requester to rate the Runner after order is completed
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

	reviewerIDStr := c.Locals("userId").(string)
	reviewerID, _ := uuid.Parse(reviewerIDStr)

	if err := h.service.SubmitReview(c.Context(), orderID, reviewerID, req.Rating, req.Comment); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "review submitted and trust score updated", nil)
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
		return response.NotFound(c, "ulasan tidak ditemukan atau belum diberikan")
	}

	return response.Success(c, "review retrieved", rv)
}
