package audit

import (
	"strconv"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
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
	admin := router.Group("/admin/audits", middleware.Protected(h.db, h.redis), middleware.Role("admin"))
	admin.Get("/", h.AdminListAudits)
}

// AdminListAudits godoc
// @Summary      [ADMIN] List audit logs
// @Description  Retrieve a paginated and filtered list of system audit logs
// @Tags         [Admin] Audit Logs
// @Produce      json
// @Security     BearerAuth
// @Param        page    query     int     false  "Page number"
// @Param        limit   query     int     false  "Items per page"
// @Param        action  query     string  false  "Action type filter"
// @Success      200     {object}  response.envelope{data=[]AuditLog}
// @Router       /admin/audits [get]
func (h *Handler) AdminListAudits(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	action := c.Query("action", "")

	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	offset := (page - 1) * limit

	logs, total, err := h.service.List(c.Context(), offset, limit, action)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "audit logs retrieved successfully", fiber.Map{
		"logs":  logs,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}
