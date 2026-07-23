package merchant

import (
	"strconv"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
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
	// Public routes
	router.Get("/merchants", h.ListNearby)
	router.Get("/merchants/:id/menu", h.ListMenuPublic)

	// Merchant Owner routes
	owner := router.Group("/merchant", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleMerchant))
	owner.Get("/profile", h.GetProfile)
	owner.Post("/profile", h.CreateProfile)
	owner.Put("/status", h.UpdateStatus)
	owner.Get("/menu", h.ListMenuMerchant)
	owner.Post("/menu", h.CreateMenu)
	owner.Put("/menu/:id", h.UpdateMenu)
	owner.Delete("/menu/:id", h.DeleteMenu)
	owner.Post("/menu/upload", h.UploadMenuImage)

	// Admin routes
	admin := router.Group("/admin/merchants", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/", h.AdminList)
	admin.Post("/", h.AdminCreate)
	admin.Put("/:id", h.AdminUpdate)
	admin.Delete("/:id", h.AdminDelete)
}

func (h *Handler) ListNearby(c *fiber.Ctx) error {
	latStr := c.Query("lat")
	lngStr := c.Query("lng")
	radiusStr := c.Query("radius_km", "10.0")

	if latStr == "" || lngStr == "" {
		return response.BadRequest(c, "koordinat lat dan lng wajib disertakan")
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		return response.BadRequest(c, "lat tidak valid")
	}

	lng, err := strconv.ParseFloat(lngStr, 64)
	if err != nil {
		return response.BadRequest(c, "lng tidak valid")
	}

	radius, err := strconv.ParseFloat(radiusStr, 64)
	if err != nil {
		radius = 10.0
	}

	merchants, err := h.service.ListNearbyMerchants(c.Context(), lat, lng, radius)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar merchant terdekat berhasil diambil", merchants)
}

func (h *Handler) ListMenuPublic(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID merchant tidak valid")
	}

	menus, err := h.service.ListMenusByMerchantID(c.Context(), id, true)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar menu berhasil diambil", menus)
}

func (h *Handler) GetProfile(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.Success(c, "profil merchant tidak ditemukan untuk pengguna ini", nil)
	}
	return response.Success(c, "profil merchant berhasil diambil", m)
}

type createProfileRequest struct {
	Name        string  `json:"name" validate:"required"`
	Description string  `json:"description"`
	Address     string  `json:"address" validate:"required"`
	Latitude    float64 `json:"latitude" validate:"required"`
	Longitude   float64 `json:"longitude" validate:"required"`
	Category    string  `json:"category" validate:"required"`
}

func (h *Handler) CreateProfile(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)

	// Check if profile already exists to prevent duplicate profiles
	_, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err == nil {
		return response.BadRequest(c, "profil merchant sudah terdaftar")
	}

	var req createProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Create merchant with default autoConfirm=false, maxActiveOrders=5
	m, err := h.service.CreateMerchant(
		c.Context(),
		claims.UserID,
		req.Name,
		req.Description,
		req.Address,
		req.Latitude,
		req.Longitude,
		req.Category,
		false,
		5,
	)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "profil merchant berhasil dilengkapi", m)
}

type merchantUpdateStatusRequest struct {
	IsOpen          *bool `json:"is_open" validate:"required"`
	AutoConfirm     *bool `json:"auto_confirm" validate:"required"`
	MaxActiveOrders *int  `json:"max_active_orders" validate:"required,gt=0"`
}

func (h *Handler) UpdateStatus(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "profil merchant tidak ditemukan")
	}

	var req merchantUpdateStatusRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	m.IsOpen = *req.IsOpen
	m.AutoConfirm = *req.AutoConfirm
	m.MaxActiveOrders = *req.MaxActiveOrders

	_, err = h.service.UpdateMerchant(c.Context(), m.ID, m.Name, m.Description, m.Address, m.Latitude, m.Longitude, m.Category, m.MaxActiveOrders)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	// Double check open and autoconfirm states are correctly saved
	_, _ = h.service.ToggleOpenStatus(c.Context(), m.ID, *req.IsOpen)
	m, err = h.service.ToggleAutoConfirm(c.Context(), m.ID, *req.AutoConfirm)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "status merchant berhasil diperbarui", m)
}

func (h *Handler) ListMenuMerchant(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "profil merchant tidak ditemukan")
	}

	menus, err := h.service.ListMenusByMerchantID(c.Context(), m.ID, false)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar menu merchant berhasil diambil", menus)
}

type createMenuRequest struct {
	Name        string  `json:"name" validate:"required"`
	Description string  `json:"description"`
	Price       float64 `json:"price" validate:"required,gt=0"`
	ImageURL    string  `json:"image_url"`
	IsAvailable *bool   `json:"is_available" validate:"required"`
}

func (h *Handler) CreateMenu(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "profil merchant tidak ditemukan")
	}

	var req createMenuRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	menu, err := h.service.CreateMenu(c.Context(), m.ID, req.Name, req.Description, req.Price, req.ImageURL, *req.IsAvailable)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "menu berhasil ditambahkan", menu)
}

type updateMenuRequest struct {
	Name        string  `json:"name" validate:"required"`
	Description string  `json:"description"`
	Price       float64 `json:"price" validate:"required,gt=0"`
	ImageURL    string  `json:"image_url"`
	IsAvailable *bool   `json:"is_available" validate:"required"`
}

func (h *Handler) UpdateMenu(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "profil merchant tidak ditemukan")
	}

	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID menu tidak valid")
	}

	menu, err := h.service.GetMenuByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, err.Error())
	}

	if menu.MerchantID != m.ID {
		return response.Forbidden(c, "Anda tidak memiliki akses ke menu ini")
	}

	var req updateMenuRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	menu, err = h.service.UpdateMenu(c.Context(), id, req.Name, req.Description, req.Price, req.ImageURL, *req.IsAvailable)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "menu berhasil diperbarui", menu)
}

func (h *Handler) DeleteMenu(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "profil merchant tidak ditemukan")
	}

	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID menu tidak valid")
	}

	menu, err := h.service.GetMenuByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, err.Error())
	}

	if menu.MerchantID != m.ID {
		return response.Forbidden(c, "Anda tidak memiliki akses ke menu ini")
	}

	if err := h.service.DeleteMenu(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "menu berhasil dihapus", nil)
}

func (h *Handler) UploadMenuImage(c *fiber.Ctx) error {
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

	path, err := h.service.UploadMenuImage(c.Context(), file.Filename, f, file.Size, file.Header.Get("Content-Type"))
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "gambar menu berhasil diupload", fiber.Map{
		"url": path,
	})
}

// Admin Implementation

func (h *Handler) AdminList(c *fiber.Ctx) error {
	merchants, err := h.service.ListAllMerchants(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar semua merchant berhasil diambil", merchants)
}

type adminCreateRequest struct {
	OwnerID         uuid.UUID `json:"owner_id" validate:"required"`
	Name            string    `json:"name" validate:"required"`
	Description     string    `json:"description"`
	Address         string    `json:"address"`
	Latitude        float64   `json:"latitude" validate:"required"`
	Longitude       float64   `json:"longitude" validate:"required"`
	Category        string    `json:"category" validate:"required"`
	AutoConfirm     bool      `json:"auto_confirm"`
	MaxActiveOrders int       `json:"max_active_orders" validate:"required,gt=0"`
}

func (h *Handler) AdminCreate(c *fiber.Ctx) error {
	var req adminCreateRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	m, err := h.service.CreateMerchant(c.Context(), req.OwnerID, req.Name, req.Description, req.Address, req.Latitude, req.Longitude, req.Category, req.AutoConfirm, req.MaxActiveOrders)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "merchant berhasil dibuat oleh admin", m)
}

type adminUpdateRequest struct {
	Name            string  `json:"name" validate:"required"`
	Description     string  `json:"description"`
	Address         string  `json:"address"`
	Latitude        float64 `json:"latitude" validate:"required"`
	Longitude       float64 `json:"longitude" validate:"required"`
	Category        string  `json:"category" validate:"required"`
	MaxActiveOrders int     `json:"max_active_orders" validate:"required,gt=0"`
}

func (h *Handler) AdminUpdate(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID merchant tidak valid")
	}

	var req adminUpdateRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	m, err := h.service.UpdateMerchant(c.Context(), id, req.Name, req.Description, req.Address, req.Latitude, req.Longitude, req.Category, req.MaxActiveOrders)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "merchant berhasil diperbarui oleh admin", m)
}

func (h *Handler) AdminDelete(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID merchant tidak valid")
	}

	if err := h.service.DeleteMerchant(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "merchant berhasil dihapus oleh admin", nil)
}
