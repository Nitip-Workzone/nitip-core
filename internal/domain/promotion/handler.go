package promotion

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
	svc   Service
	db    *bun.DB
	redis *cache.Redis
}

func NewHandler(svc Service, db *bun.DB, redis *cache.Redis) *Handler {
	return &Handler{svc: svc, db: db, redis: redis}
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	admin := router.Group("/admin/promotions", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/", h.AdminList)
	admin.Post("/", h.AdminCreate)
	admin.Post("/calculate-preview", h.AdminCalculatePreview)
	admin.Get("/settlements", h.AdminSettlements)
	admin.Get("/:id", h.AdminGetByID)
	admin.Put("/:id", h.AdminUpdate)
	admin.Delete("/:id", h.AdminDelete)
	admin.Get("/:id/usages", h.AdminListUsages)
	admin.Get("/:id/stats", h.AdminStats)

	public := router.Group("/promotions")
	public.Get("/active", h.PublicActive)
	public.Post("/validate", h.PublicValidate)
}

func (h *Handler) AdminList(c *fiber.Ctx) error {
	var merchantID *uuid.UUID
	if mid := c.Query("merchant_id"); mid != "" {
		if parsed, err := uuid.Parse(mid); err == nil {
			merchantID = &parsed
		}
	}
	var isActive *bool
	if ia := c.Query("is_active"); ia != "" {
		if b, err := strconv.ParseBool(ia); err == nil {
			isActive = &b
		}
	}
	var firstPurchaseOnly *bool
	if fpo := c.Query("first_purchase_only"); fpo != "" {
		if b, err := strconv.ParseBool(fpo); err == nil {
			firstPurchaseOnly = &b
		}
	}
	search := c.Query("search")
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))

	list, total, err := h.svc.List(c.Context(), merchantID, isActive, search, firstPurchaseOnly, offset, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar promosi berhasil diambil", fiber.Map{"data": list, "total": total, "offset": offset, "limit": limit})
}

func (h *Handler) AdminGetByID(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "id tidak valid")
	}
	p, err := h.svc.GetByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, "promosi ditemukan", p)
}

func (h *Handler) AdminCreate(c *fiber.Ctx) error {
	var req CreatePromotionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "payload tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claims.UserID
	ip := c.IP()
	ua := c.Get("User-Agent")

	p, err := h.svc.CreatePromotion(c.Context(), adminID, req, ip, ua)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Created(c, "promosi berhasil dibuat", p)
}

func (h *Handler) AdminUpdate(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "id tidak valid")
	}
	var req UpdatePromotionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "payload tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claims.UserID
	ip := c.IP()
	ua := c.Get("User-Agent")

	p, err := h.svc.UpdatePromotion(c.Context(), adminID, id, req, ip, ua)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "promosi berhasil diperbarui", p)
}

func (h *Handler) AdminDelete(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "id tidak valid")
	}
	var req DeletePromotionRequest
	_ = c.BodyParser(&req)
	if req.AdminPassword == "" {
		req.AdminPassword = c.Query("admin_password")
		req.TotpCode = c.Query("totp_code")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	adminID := claims.UserID
	ip := c.IP()
	ua := c.Get("User-Agent")

	if err := h.svc.DeletePromotion(c.Context(), adminID, id, req, ip, ua); err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Success(c, "promosi dihapus/nonaktifkan", nil)
}

func (h *Handler) AdminListUsages(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "id tidak valid")
	}
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	list, total, err := h.svc.ListUsages(c.Context(), id, offset, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar penggunaan berhasil diambil", fiber.Map{"data": list, "total": total, "offset": offset, "limit": limit})
}

func (h *Handler) AdminStats(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "id tidak valid")
	}
	p, err := h.svc.GetByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	_, total, _ := h.svc.ListUsages(c.Context(), id, 0, 1)
	return response.Success(c, "statistik promosi", fiber.Map{
		"promotion":        p,
		"total_usages":     total,
		"budget_remaining": p.BudgetTotal - p.BudgetUsed,
	})
}

func (h *Handler) AdminCalculatePreview(c *fiber.Ctx) error {
	var req CalculatePreviewRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "payload tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	res, err := h.svc.CalculatePreview(c.Context(), req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "preview kalkulasi", res)
}

func (h *Handler) AdminSettlements(c *fiber.Ctx) error {
	var merchantID *uuid.UUID
	if mid := c.Query("merchant_id"); mid != "" {
		if parsed, err := uuid.Parse(mid); err == nil {
			merchantID = &parsed
		}
	}
	var from, to *time.Time
	if f := c.Query("from"); f != "" {
		if t, err := time.Parse("2006-01-02", f); err == nil {
			from = &t
		}
	}
	if tStr := c.Query("to"); tStr != "" {
		if t, err := time.Parse("2006-01-02", tStr); err == nil {
			end := t.Add(24*time.Hour - time.Second)
			to = &end
		}
	}
	res, err := h.svc.GetSettlement(c.Context(), merchantID, from, to)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "laporan settlement", res)
}

func (h *Handler) PublicActive(c *fiber.Ctx) error {
	var merchantID *uuid.UUID
	if mid := c.Query("merchant_id"); mid != "" {
		if parsed, err := uuid.Parse(mid); err == nil {
			merchantID = &parsed
		}
	}
	list, err := h.svc.GetActiveForMerchant(c.Context(), merchantID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "promo aktif", list)
}

func (h *Handler) PublicValidate(c *fiber.Ctx) error {
	var req ValidatePromotionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "payload tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	var userID *uuid.UUID
	if claims := jwt.GetClaims(c); claims != nil {
		uid := claims.UserID
		userID = &uid
	}
	res, err := h.svc.ValidateForCheckout(c.Context(), req, userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	if !res.Valid {
		return response.BadRequest(c, res.Message)
	}
	return response.Success(c, "voucher valid", res)
}
