package banner

import (
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
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
	// Public routes
	router.Get("/banners", h.GetActiveBanners)

	// Admin routes
	admin := router.Group("/admin/banners", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/", h.AdminListBanners)
	admin.Post("/", h.AdminCreateBanner)
	admin.Post("/upload", h.AdminUploadBannerImage)
	admin.Put("/:id", h.AdminUpdateBanner)
	admin.Delete("/:id", h.AdminDeleteBanner)
}

func (h *Handler) GetActiveBanners(c *fiber.Ctx) error {
	banners, err := h.service.GetActiveBanners(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "banner aktif berhasil diambil", banners)
}

func (h *Handler) AdminListBanners(c *fiber.Ctx) error {
	banners, err := h.service.GetAllBanners(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "semua banner berhasil diambil", banners)
}

type createBannerRequest struct {
	Title       string  `json:"title" validate:"required"`
	ImageURL    string  `json:"image_url" validate:"required"`
	RedirectURL *string `json:"redirect_url"`
	IsActive    *bool   `json:"is_active" validate:"required"`
}

func (h *Handler) AdminCreateBanner(c *fiber.Ctx) error {
	var req createBannerRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	b, err := h.service.CreateBanner(c.Context(), req.Title, req.ImageURL, req.RedirectURL, *req.IsActive)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "banner berhasil dibuat", b)
}

type updateBannerRequest struct {
	Title       string  `json:"title" validate:"required"`
	ImageURL    string  `json:"image_url" validate:"required"`
	RedirectURL *string `json:"redirect_url"`
	IsActive    *bool   `json:"is_active" validate:"required"`
}

func (h *Handler) AdminUpdateBanner(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID banner tidak valid")
	}

	var req updateBannerRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	b, err := h.service.UpdateBanner(c.Context(), id, req.Title, req.ImageURL, req.RedirectURL, *req.IsActive)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "banner berhasil diperbarui", b)
}

func (h *Handler) AdminDeleteBanner(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID banner tidak valid")
	}

	if err := h.service.DeleteBanner(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "banner berhasil dihapus", nil)
}

func (h *Handler) AdminUploadBannerImage(c *fiber.Ctx) error {
	file, err := c.FormFile("image")
	if err != nil {
		return response.BadRequest(c, "file gambar tidak ditemukan")
	}

	if file.Size > 5*1024*1024 {
		return response.BadRequest(c, "ukuran file tidak boleh melebihi 5MB")
	}

	if !fileutil.IsImage(file) {
		return response.BadRequest(c, "file harus berupa gambar (jpg, jpeg, png)")
	}

	f, err := file.Open()
	if err != nil {
		return response.InternalError(c, "gagal membuka file gambar")
	}
	defer func() { _ = f.Close() }()

	path, err := h.service.UploadImage(c.Context(), file.Filename, f, file.Size, file.Header.Get("Content-Type"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "gambar berhasil diupload", fiber.Map{
		"url": path,
	})
}
