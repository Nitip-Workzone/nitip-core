package notification

import (
	"strconv"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/codecoffy/nitip-core/pkg/response"
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
	g := router.Group("/notifications", middleware.Protected(h.db, h.redis))
	g.Get("/", h.List)
	g.Get("/unread-count", h.UnreadCount)
	g.Put("/:id/read", h.MarkAsRead)
	g.Put("/read-all", h.MarkAllAsRead)
}

// List godoc
// @Summary      List notifications
// @Description  Retrieve paginated notifications for the logged-in user
// @Tags         [User] Notifications
// @Produce      json
// @Security     BearerAuth
// @Param        limit   query   int  false  "Limit"
// @Param        offset  query   int  false  "Offset"
// @Success      200  {object}  response.envelope{data=[]Notification}
// @Router       /notifications [get]
func (h *Handler) List(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	userID := claims.UserID

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))

	notifications, err := h.service.GetUserNotifications(c.Context(), userID, limit, offset)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "notifications retrieved", notifications)
}

// UnreadCount godoc
// @Summary      Get unread notification count
// @Description  Get the number of unread notifications for the user
// @Tags         [User] Notifications
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=map[string]int}
// @Router       /notifications/unread-count [get]
func (h *Handler) UnreadCount(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	userID := claims.UserID

	count, err := h.service.GetUnreadCount(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "unread count retrieved", fiber.Map{
		"unread_count": count,
	})
}

// MarkAsRead godoc
// @Summary      Mark notification as read
// @Description  Update a specific notification status to read
// @Tags         [User] Notifications
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Notification UUID" Format(uuid)
// @Success      200  {object}  response.envelope
// @Router       /notifications/{id}/read [put]
func (h *Handler) MarkAsRead(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	userID := claims.UserID

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID notifikasi tidak valid")
	}

	if err := h.service.MarkAsRead(c.Context(), id, userID); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "Notification marked as read", nil)
}

// MarkAllAsRead godoc
// @Summary      Mark all notifications as read
// @Description  Update all user's notifications to read status
// @Tags         [User] Notifications
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope
// @Router       /notifications/read-all [put]
func (h *Handler) MarkAllAsRead(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	userID := claims.UserID

	if err := h.service.MarkAllAsRead(c.Context(), userID); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "All notifications marked as read", nil)
}
