package support

import (
	"strconv"
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
	// User routes (all authenticated roles except admin? but allow all for flexibility)
	userGroup := router.Group("/support", middleware.Protected(h.db, h.redis))
	userGroup.Get("/tickets", h.ListMyTickets)
	userGroup.Post("/tickets", h.CreateTicket)
	userGroup.Get("/tickets/:id", h.GetTicket)
	userGroup.Get("/tickets/:id/messages", h.GetMessages)
	userGroup.Post("/tickets/:id/messages", h.SendMessage)
	userGroup.Post("/tickets/:id/close", h.CloseTicket)
	userGroup.Post("/tickets/:id/reopen", h.ReopenTicket)
	userGroup.Get("/faq", h.ListFAQ)
	userGroup.Get("/faq/search", h.SearchFAQ)

	// CS routes
	csGroup := router.Group("/cs/support", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleCS, user.RoleAdmin))
	csGroup.Get("/queue", h.GetQueue)
	csGroup.Get("/tickets", h.ListAllTickets)
	csGroup.Get("/tickets/my-active", h.GetMyActiveTicket)
	csGroup.Post("/tickets/:id/claim", h.ClaimTicket)
	csGroup.Post("/tickets/:id/release", h.ReleaseTicket)
	csGroup.Post("/tickets/:id/resolve", h.ResolveTicket)
	csGroup.Get("/tickets/:id", h.GetTicketAdmin)
	csGroup.Get("/tickets/:id/messages", h.GetMessagesAdmin)
	csGroup.Post("/tickets/:id/messages", h.SendMessageCS)
	csGroup.Post("/tickets/:id/close", h.CloseTicketCS)

	// Admin FAQ management
	adminGroup := router.Group("/admin/support", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	adminGroup.Get("/tickets", h.ListAllTickets)
	adminGroup.Get("/tickets/:id", h.GetTicketAdmin)
	adminGroup.Get("/stats", h.GetStats)
	adminGroup.Post("/faq", h.CreateFAQ)
	adminGroup.Put("/faq/:id", h.UpdateFAQ)
	adminGroup.Delete("/faq/:id", h.DeleteFAQ)
	adminGroup.Get("/faq", h.ListAllFAQAdmin)
}

func (h *Handler) parsePagination(c *fiber.Ctx) (int, int) {
	limitStr := c.Query("limit", "20")
	offsetStr := c.Query("offset", "0")
	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return limit, offset
}

func (h *Handler) getUserID(c *fiber.Ctx) (uuid.UUID, error) {
	claims, ok := c.Locals("user").(*jwt.CustomClaims)
	if !ok {
		return uuid.Nil, fiber.NewError(401, "unauthorized")
	}
	return claims.UserID, nil
}

// User handlers

type createTicketPayload struct {
	OrderID     *uuid.UUID `json:"order_id"`
	Category    string     `json:"category" validate:"required,oneof=order_issue payment account merchant other general"`
	Title       string     `json:"title" validate:"required,min=5,max=255"`
	Description string     `json:"description" validate:"required,min=10,max=5000"`
	Priority    int        `json:"priority" validate:"omitempty,gte=1,lte=3"`
}

func (h *Handler) CreateTicket(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	var req createTicketPayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	ticket, err := h.service.CreateTicket(c.Context(), userID, CreateTicketRequest(req))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "tiket berhasil dibuat", ticket)
}

func (h *Handler) ListMyTickets(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	limit, offset := h.parsePagination(c)
	tickets, err := h.service.ListMyTickets(c.Context(), userID, limit, offset)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar tiket berhasil diambil", tickets)
}

func (h *Handler) GetTicket(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	ticket, err := h.service.GetTicketByID(c.Context(), id, userID, false)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, "detail tiket berhasil diambil", ticket)
}

func (h *Handler) GetMessages(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	afterIDStr := c.Query("after_id")
	var afterID *uuid.UUID
	if afterIDStr != "" {
		if parsed, err := uuid.Parse(afterIDStr); err == nil {
			afterID = &parsed
		}
	}
	afterTime := c.Query("after_time")
	var afterTimePtr *string
	if afterTime != "" {
		afterTimePtr = &afterTime
	}
	limitStr := c.Query("limit", "50")
	limit, _ := strconv.Atoi(limitStr)

	msgs, err := h.service.GetMessages(c.Context(), id, userID, false, afterID, afterTimePtr, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "pesan berhasil diambil", msgs)
}

type sendMessagePayload struct {
	Message    string `json:"message" validate:"required,min=1,max=2000"`
	IsInternal bool   `json:"is_internal"`
}

func (h *Handler) SendMessage(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	var req sendMessagePayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	// For user, is_internal must be false
	if req.IsInternal {
		return response.Forbidden(c, "user tidak bisa mengirim pesan internal")
	}
	msg, err := h.service.SendMessage(c.Context(), id, userID, SenderRoleUser, req.Message, false)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "pesan berhasil dikirim", msg)
}

func (h *Handler) CloseTicket(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.CloseTicket(c.Context(), id, userID, false)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "tiket berhasil ditutup", t)
}

func (h *Handler) ReopenTicket(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.ReopenTicket(c.Context(), id, userID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "tiket berhasil dibuka kembali", t)
}

// CS Handlers

func (h *Handler) GetQueue(c *fiber.Ctx) error {
	limit, offset := h.parsePagination(c)
	tickets, total, err := h.service.ListQueueTickets(c.Context(), limit, offset)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "antrian tiket berhasil diambil", fiber.Map{
		"tickets": tickets,
		"total":   total,
	})
}

func (h *Handler) ListAllTickets(c *fiber.Ctx) error {
	status := c.Query("status")
	category := c.Query("category")
	search := c.Query("search")
	assignedCS := c.Query("assigned_cs_id")
	var assignedCSID *uuid.UUID
	if assignedCS != "" {
		if parsed, err := uuid.Parse(assignedCS); err == nil {
			assignedCSID = &parsed
		}
	}
	limit, offset := h.parsePagination(c)
	tickets, total, err := h.service.ListAllTickets(c.Context(), status, category, search, assignedCSID, limit, offset)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar tiket berhasil diambil", fiber.Map{
		"tickets": tickets,
		"total":   total,
	})
}

func (h *Handler) GetMyActiveTicket(c *fiber.Ctx) error {
	csID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	t, err := h.service.GetActiveTicketByCS(c.Context(), csID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	if t == nil {
		return response.Success(c, "tidak ada tiket aktif", nil)
	}
	return response.Success(c, "tiket aktif berhasil diambil", t)
}

func (h *Handler) ClaimTicket(c *fiber.Ctx) error {
	csID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.ClaimTicket(c.Context(), id, csID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "tiket berhasil diambil", t)
}

func (h *Handler) ReleaseTicket(c *fiber.Ctx) error {
	csID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.ReleaseTicket(c.Context(), id, csID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "tiket berhasil dilepas", t)
}

func (h *Handler) ResolveTicket(c *fiber.Ctx) error {
	csID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.ResolveTicket(c.Context(), id, csID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "tiket berhasil diselesaikan", t)
}

func (h *Handler) GetTicketAdmin(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.GetTicketByID(c.Context(), id, userID, true)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, "detail tiket berhasil diambil", t)
}

func (h *Handler) GetMessagesAdmin(c *fiber.Ctx) error {
	userID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	afterIDStr := c.Query("after_id")
	var afterID *uuid.UUID
	if afterIDStr != "" {
		if parsed, err := uuid.Parse(afterIDStr); err == nil {
			afterID = &parsed
		}
	}
	afterTime := c.Query("after_time")
	var afterTimePtr *string
	if afterTime != "" {
		afterTimePtr = &afterTime
	}
	limitStr := c.Query("limit", "50")
	limit, _ := strconv.Atoi(limitStr)

	msgs, err := h.service.GetMessages(c.Context(), id, userID, true, afterID, afterTimePtr, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "pesan berhasil diambil", msgs)
}

func (h *Handler) SendMessageCS(c *fiber.Ctx) error {
	csID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	var req sendMessagePayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	msg, err := h.service.SendMessage(c.Context(), id, csID, SenderRoleCS, req.Message, req.IsInternal)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "pesan berhasil dikirim", msg)
}

func (h *Handler) CloseTicketCS(c *fiber.Ctx) error {
	csID, err := h.getUserID(c)
	if err != nil {
		return response.Unauthorized(c, "unauthorized")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID tiket tidak valid")
	}
	t, err := h.service.CloseTicket(c.Context(), id, csID, true)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "tiket berhasil ditutup", t)
}

func (h *Handler) GetStats(c *fiber.Ctx) error {
	stats, err := h.service.GetStats(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "statistik berhasil diambil", fiber.Map{
		"queue_count":        stats["queue"],
		"open_count":         stats["open"],
		"queued_count":       stats["queued"],
		"total_count":        stats["total"],
		"assigned_count":     stats["assigned"],
		"in_progress_count":  stats["in_progress"],
		"waiting_user_count": stats["waiting_user"],
		"resolved_count":     stats["resolved"],
		"closed_count":       stats["closed"],
	})
}

// FAQ handlers

func (h *Handler) ListFAQ(c *fiber.Ctx) error {
	faqs, err := h.service.ListFAQ(c.Context(), true)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar FAQ berhasil diambil", faqs)
}

func (h *Handler) SearchFAQ(c *fiber.Ctx) error {
	query := c.Query("q")
	category := c.Query("category")
	limitStr := c.Query("limit", "20")
	limit, _ := strconv.Atoi(limitStr)
	faqs, err := h.service.SearchFAQ(c.Context(), query, category, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "pencarian FAQ berhasil", faqs)
}

func (h *Handler) ListAllFAQAdmin(c *fiber.Ctx) error {
	faqs, err := h.service.ListFAQ(c.Context(), false)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar FAQ berhasil diambil", faqs)
}

type createFAQPayload struct {
	Category string `json:"category" validate:"required"`
	Question string `json:"question" validate:"required,min=5,max=255"`
	Answer   string `json:"answer" validate:"required,min=10"`
	Keywords string `json:"keywords"`
	IsActive *bool  `json:"is_active"`
}

func (h *Handler) CreateFAQ(c *fiber.Ctx) error {
	var req createFAQPayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	faq, err := h.service.CreateFAQ(c.Context(), CreateFAQRequest(req))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "FAQ berhasil dibuat", faq)
}

type updateFAQPayload struct {
	Category *string `json:"category"`
	Question *string `json:"question" validate:"omitempty,min=5,max=255"`
	Answer   *string `json:"answer" validate:"omitempty,min=10"`
	Keywords *string `json:"keywords"`
	IsActive *bool   `json:"is_active"`
}

func (h *Handler) UpdateFAQ(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID FAQ tidak valid")
	}
	var req updateFAQPayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	faq, err := h.service.UpdateFAQ(c.Context(), id, UpdateFAQRequest(req))
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "FAQ berhasil diperbarui", faq)
}

func (h *Handler) DeleteFAQ(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID FAQ tidak valid")
	}
	if err := h.service.DeleteFAQ(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "FAQ berhasil dihapus", nil)
}

// Ensure time import used (for swagger gen may need)
var _ = time.Now
