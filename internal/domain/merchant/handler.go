package merchant

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

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

func (h *Handler) invalidateCache(ctx context.Context) {
	if h.redis != nil {
		_ = h.redis.DelByPattern(ctx, "merchants:nearby:*")
	}
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	// Public routes
	router.Get("/merchants", h.ListNearby)
	router.Get("/merchants/:id/menu", h.ListMenuPublic)

	// Merchant Owner routes
	owner := router.Group("/merchant", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleMerchant))
	owner.Get("/profile", h.GetProfile)
	owner.Post("/profile", h.CreateProfile)
	owner.Put("/profile", h.UpdateProfile)
	owner.Put("/status", h.UpdateStatus)
	owner.Get("/menu", h.ListMenuMerchant)
	owner.Post("/menu", h.CreateMenu)
	owner.Put("/menu/:id", h.UpdateMenu)
	owner.Put("/menu/:id/toggle", h.ToggleMenuAvailability)
	owner.Delete("/menu/:id", h.DeleteMenu)
	owner.Post("/menu/upload", h.UploadMenuImage)

	// Category routes (Makanan, Minuman) + image optional crop 1:1
	owner.Get("/categories", h.ListCategories)
	owner.Post("/categories", h.CreateCategory)
	owner.Put("/categories/:id", h.UpdateCategory)
	owner.Delete("/categories/:id", h.DeleteCategory)

	// Variant groups & options with image_url (varian bisa punya foto 600x600)
	owner.Get("/menu/:id/variants", h.ListVariantGroups)
	owner.Post("/menu/:id/variants", h.CreateVariantGroup)
	owner.Put("/menu/variants/:id", h.UpdateVariantGroup)
	owner.Delete("/menu/variants/:id", h.DeleteVariantGroup)
	owner.Post("/menu/variants/:id/options", h.CreateVariantOption)
	owner.Put("/menu/variants/options/:id", h.UpdateVariantOption)
	owner.Delete("/menu/variants/options/:id", h.DeleteVariantOption)

	// Topping groups & options with image_url (topping foto 400x400)
	owner.Get("/menu/:id/toppings", h.ListToppingGroups)
	owner.Post("/menu/:id/toppings", h.CreateToppingGroup)
	owner.Put("/menu/toppings/:id", h.UpdateToppingGroup)
	owner.Delete("/menu/toppings/:id", h.DeleteToppingGroup)
	owner.Post("/menu/toppings/:id/options", h.CreateToppingOption)
	owner.Put("/menu/toppings/options/:id", h.UpdateToppingOption)
	owner.Delete("/menu/toppings/options/:id", h.DeleteToppingOption)

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

	// Round coordinates to 3 decimal places to create a stable key (approx. 110m precision)
	roundedLat := math.Round(lat*1000) / 1000
	roundedLng := math.Round(lng*1000) / 1000
	cacheKey := fmt.Sprintf("merchants:nearby:%.3f:%.3f:%.2f", roundedLat, roundedLng, radius)

	// Check Redis cache first
	var merchants []Merchant
	if h.redis != nil {
		cachedData, err := h.redis.Get(c.Context(), cacheKey)
		if err == nil && cachedData != "" {
			if jsonErr := json.Unmarshal([]byte(cachedData), &merchants); jsonErr == nil {
				return response.Success(c, "daftar merchant terdekat berhasil diambil", merchants)
			}
		}
	}

	merchants, err = h.service.ListNearbyMerchants(c.Context(), lat, lng, radius)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	// Cache the result in Redis for 1 minute
	if h.redis != nil && len(merchants) > 0 {
		if cacheBytes, jsonErr := json.Marshal(merchants); jsonErr == nil {
			_ = h.redis.Set(c.Context(), cacheKey, cacheBytes, 1*time.Minute)
		}
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
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

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
		log.Printf("[ERROR] CreateProfile: %v", err)
		return response.InternalError(c, "gagal melengkapi profil merchant")
	}

	h.invalidateCache(c.Context())
	return response.Success(c, "profil merchant berhasil dilengkapi", m)
}

type updateProfileRequest struct {
	Name         string        `json:"name" validate:"required,min=2,max=100"`
	Description  string        `json:"description" validate:"omitempty,max=500"`
	Address      string        `json:"address" validate:"required,max=500"`
	Latitude     float64       `json:"latitude" validate:"required,latitude"`
	Longitude    float64       `json:"longitude" validate:"required,longitude"`
	Category     string        `json:"category" validate:"required,oneof=food laundry mart"`
	OpeningHours *OpeningHours `json:"opening_hours,omitempty"`
	ImageURL     *string       `json:"image_url,omitempty"`
	CoverURL     *string       `json:"cover_url,omitempty"`
}

func (h *Handler) UpdateProfile(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "profil merchant tidak ditemukan")
	}

	var req updateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	updated, err := h.service.UpdateMerchantFull(c.Context(), m.ID, req.Name, req.Description, req.Address, req.Latitude, req.Longitude, req.Category, m.MaxActiveOrders, req.OpeningHours, req.ImageURL, req.CoverURL)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	h.invalidateCache(c.Context())
	return response.Success(c, "profil merchant berhasil diperbarui", updated)
}

type merchantUpdateStatusRequest struct {
	IsOpen          *bool `json:"is_open" validate:"required"`
	AutoConfirm     *bool `json:"auto_confirm" validate:"required"`
	MaxActiveOrders *int  `json:"max_active_orders" validate:"required,gt=0"`
}

func (h *Handler) UpdateStatus(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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

	h.invalidateCache(c.Context())
	return response.Success(c, "status merchant berhasil diperbarui", m)
}

func (h *Handler) ListMenuMerchant(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.Success(c, "profil merchant belum dikonfigurasi", []interface{}{})
	}

	menus, err := h.service.ListMenusByMerchantID(c.Context(), m.ID, false)
	if err != nil {
		log.Printf("[ERROR] ListMenuMerchant: %v", err)
		return response.InternalError(c, "gagal mengambil daftar menu merchant")
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
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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
		return response.BadRequest(c, "file harus berupa gambar (jpg, jpeg, png, webp)")
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

// Category handlers (Makanan, Minuman) + image optional crop 1:1 400px
type categoryRequest struct {
	Name      string `json:"name" validate:"required"`
	ImageURL  string `json:"image_url"`
	SortOrder int    `json:"sort_order"`
	IsActive  *bool  `json:"is_active"`
}

func (h *Handler) ListCategories(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "merchant tidak ditemukan")
	}
	list, err := h.service.ListCategoriesByMerchantID(c.Context(), m.ID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar kategori berhasil diambil", list)
}

func (h *Handler) CreateCategory(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	m, err := h.service.GetMerchantByOwnerID(c.Context(), claims.UserID)
	if err != nil {
		return response.NotFound(c, "merchant tidak ditemukan")
	}
	var req categoryRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	cat, err := h.service.CreateCategory(c.Context(), m.ID, req.Name, req.ImageURL, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "kategori berhasil ditambahkan", cat)
}

func (h *Handler) UpdateCategory(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req categoryRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	cat, err := h.service.UpdateCategory(c.Context(), id, req.Name, req.ImageURL, req.SortOrder, isActive)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "kategori berhasil diperbarui", cat)
}

func (h *Handler) DeleteCategory(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	if err := h.service.DeleteCategory(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "kategori berhasil dihapus", nil)
}

// Variant groups + options with image_url 600x600
type variantGroupRequest struct {
	Name       string `json:"name" validate:"required"`
	Type       string `json:"type" validate:"required"`
	IsRequired bool   `json:"is_required"`
	MinSelect  int    `json:"min_select"`
	MaxSelect  *int   `json:"max_select"`
	SortOrder  int    `json:"sort_order"`
}

type variantOptionRequest struct {
	Label       string  `json:"label" validate:"required"`
	PriceDelta  float64 `json:"price_delta"`
	ImageURL    string  `json:"image_url"`
	IsDefault   bool    `json:"is_default"`
	IsAvailable *bool   `json:"is_available"`
	SortOrder   int     `json:"sort_order"`
}

func (h *Handler) ListVariantGroups(c *fiber.Ctx) error {
	idStr := c.Params("id")
	menuID, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID menu tidak valid")
	}
	list, err := h.service.ListVariantGroupsByMenuID(c.Context(), menuID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar varian berhasil diambil", list)
}

func (h *Handler) CreateVariantGroup(c *fiber.Ctx) error {
	idStr := c.Params("id")
	menuID, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID menu tidak valid")
	}
	var req variantGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	g, err := h.service.CreateVariantGroup(c.Context(), menuID, req.Name, req.Type, req.IsRequired, req.MinSelect, req.MaxSelect, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "grup varian berhasil ditambahkan", g)
}

func (h *Handler) UpdateVariantGroup(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req variantGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	g, err := h.service.UpdateVariantGroup(c.Context(), id, req.Name, req.Type, req.IsRequired, req.MinSelect, req.MaxSelect, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "grup varian berhasil diperbarui", g)
}

func (h *Handler) DeleteVariantGroup(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	if err := h.service.DeleteVariantGroup(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "grup varian berhasil dihapus", nil)
}

func (h *Handler) CreateVariantOption(c *fiber.Ctx) error {
	idStr := c.Params("id")
	groupID, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req variantOptionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	isAvail := true
	if req.IsAvailable != nil {
		isAvail = *req.IsAvailable
	}
	o, err := h.service.CreateVariantOption(c.Context(), groupID, req.Label, req.PriceDelta, req.ImageURL, req.IsDefault, isAvail, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "opsi varian berhasil ditambahkan", o)
}

func (h *Handler) UpdateVariantOption(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req variantOptionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	isAvail := true
	if req.IsAvailable != nil {
		isAvail = *req.IsAvailable
	}
	o, err := h.service.UpdateVariantOption(c.Context(), id, req.Label, req.PriceDelta, req.ImageURL, req.IsDefault, isAvail, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "opsi varian berhasil diperbarui", o)
}

func (h *Handler) DeleteVariantOption(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	if err := h.service.DeleteVariantOption(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "opsi varian berhasil dihapus", nil)
}

// Topping groups + options with image_url 400x400
type toppingGroupRequest struct {
	VariantOptionID *uuid.UUID `json:"variant_option_id"`
	Name            string     `json:"name" validate:"required"`
	Type            string     `json:"type" validate:"required"`
	IsRequired      bool       `json:"is_required"`
	MinSelect       int        `json:"min_select"`
	MaxSelect       *int       `json:"max_select"`
	SortOrder       int        `json:"sort_order"`
}

type toppingOptionRequest struct {
	Label       string  `json:"label" validate:"required"`
	PriceDelta  float64 `json:"price_delta"`
	ImageURL    string  `json:"image_url"`
	IsAvailable *bool   `json:"is_available"`
	SortOrder   int     `json:"sort_order"`
}

func (h *Handler) ListToppingGroups(c *fiber.Ctx) error {
	idStr := c.Params("id")
	menuID, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID menu tidak valid")
	}
	list, err := h.service.ListToppingGroupsByMenuID(c.Context(), menuID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar topping berhasil diambil", list)
}

func (h *Handler) CreateToppingGroup(c *fiber.Ctx) error {
	idStr := c.Params("id")
	menuID, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID menu tidak valid")
	}
	var req toppingGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	g, err := h.service.CreateToppingGroup(c.Context(), menuID, req.VariantOptionID, req.Name, req.Type, req.IsRequired, req.MinSelect, req.MaxSelect, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "grup topping berhasil ditambahkan", g)
}

func (h *Handler) UpdateToppingGroup(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req toppingGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	g, err := h.service.UpdateToppingGroup(c.Context(), id, req.Name, req.Type, req.IsRequired, req.MinSelect, req.MaxSelect, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "grup topping berhasil diperbarui", g)
}

func (h *Handler) DeleteToppingGroup(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	if err := h.service.DeleteToppingGroup(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "grup topping berhasil dihapus", nil)
}

func (h *Handler) CreateToppingOption(c *fiber.Ctx) error {
	idStr := c.Params("id")
	groupID, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req toppingOptionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}
	isAvail := true
	if req.IsAvailable != nil {
		isAvail = *req.IsAvailable
	}
	o, err := h.service.CreateToppingOption(c.Context(), groupID, req.Label, req.PriceDelta, req.ImageURL, isAvail, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "opsi topping berhasil ditambahkan", o)
}

func (h *Handler) UpdateToppingOption(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	var req toppingOptionRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format tidak valid")
	}
	isAvail := true
	if req.IsAvailable != nil {
		isAvail = *req.IsAvailable
	}
	o, err := h.service.UpdateToppingOption(c.Context(), id, req.Label, req.PriceDelta, req.ImageURL, isAvail, req.SortOrder)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "opsi topping berhasil diperbarui", o)
}

func (h *Handler) DeleteToppingOption(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tidak valid")
	}
	if err := h.service.DeleteToppingOption(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "opsi topping berhasil dihapus", nil)
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

	h.invalidateCache(c.Context())
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

	h.invalidateCache(c.Context())
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

	h.invalidateCache(c.Context())
	return response.Success(c, "merchant berhasil dihapus oleh admin", nil)
}

func (h *Handler) ToggleMenuAvailability(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "id menu tidak valid")
	}

	type toggleRequest struct {
		IsAvailable bool `json:"is_available"`
	}
	var req toggleRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	menu, err := h.service.ToggleMenuAvailability(c.Context(), id, req.IsAvailable)
	if err != nil {
		log.Printf("[ERROR] ToggleMenuAvailability: %v", err)
		return response.InternalError(c, "gagal mengubah ketersediaan menu")
	}

	return response.Success(c, "status ketersediaan menu berhasil diperbarui", menu)
}
