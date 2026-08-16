package store

import (
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
	// Public (authenticated) routes — accessible by requester, runner, merchant
	stores := router.Group("/stores", middleware.Protected(h.db, h.redis))
	stores.Get("/", h.ListActive)
	stores.Get("/nearby", h.ListNearby)

	// Admin-only routes
	admin := router.Group("/admin/stores", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/", h.AdminList)
	admin.Post("/", h.AdminCreate)
	admin.Put("/:id", h.AdminUpdate)
	admin.Delete("/:id", h.AdminDelete)
}

// ListActive godoc
// @Summary      List all active stores
// @Description  Returns all active tokoh/toko in the system (no location filter)
// @Tags         Shared
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=[]Store}
// @Router       /stores [get]
func (h *Handler) ListActive(c *fiber.Ctx) error {
	stores, err := h.service.GetActiveStores(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "daftar tokoh berhasil diambil", stores)
}

// ListNearby godoc
// @Summary      List nearby stores
// @Description  Returns active stores within a radius of the given coordinates, sorted by distance.
// @Tags         Shared
// @Produce      json
// @Security     BearerAuth
// @Param        lat     query  number  true   "Latitude"
// @Param        lng     query  number  true   "Longitude"
// @Param        radius  query  number  false  "Search radius in km (default 15, max 30)"
// @Param        limit   query  int     false  "Max results (default 20, max 20)"
// @Success      200  {object}  response.envelope{data=[]Store}
// @Router       /stores/nearby [get]
func (h *Handler) ListNearby(c *fiber.Ctx) error {
	lat := c.QueryFloat("lat", 0)
	lng := c.QueryFloat("lng", 0)
	if lat == 0 && lng == 0 {
		return response.BadRequest(c, "parameter lat dan lng wajib diisi")
	}

	radius := c.QueryFloat("radius", 15)
	if radius > 30 {
		radius = 30
	}
	limit := c.QueryInt("limit", 20)

	stores, err := h.service.GetNearbyStores(c.Context(), lat, lng, radius, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "tokoh terdekat berhasil diambil", stores)
}

// AdminList godoc
// @Summary      [Admin] List all stores
// @Description  Admin: List all tokoh including inactive ones
// @Tags         [Admin] Store Management
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=[]Store}
// @Router       /admin/stores [get]
func (h *Handler) AdminList(c *fiber.Ctx) error {
	stores, err := h.service.GetAllStores(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "semua tokoh berhasil diambil", stores)
}

// AdminCreate godoc
// @Summary      [Admin] Create a store
// @Description  Admin: Add a new tokoh to the directory
// @Tags         [Admin] Store Management
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateStoreRequest  true  "Store payload"
// @Success      201   {object}  response.envelope{data=Store}
// @Router       /admin/stores [post]
func (h *Handler) AdminCreate(c *fiber.Ctx) error {
	var req CreateStoreRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	s, err := h.service.CreateStore(c.Context(), req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, "tokoh berhasil ditambahkan", s)
}

// AdminUpdate godoc
// @Summary      [Admin] Update a store
// @Description  Admin: Update an existing tokoh's data
// @Tags         [Admin] Store Management
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  string              true  "Store ID"
// @Param        body  body  UpdateStoreRequest  true  "Store payload"
// @Success      200   {object}  response.envelope{data=Store}
// @Router       /admin/stores/{id} [put]
func (h *Handler) AdminUpdate(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tokoh tidak valid")
	}

	var req UpdateStoreRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	s, err := h.service.UpdateStore(c.Context(), id, req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "tokoh berhasil diperbarui", s)
}

// AdminDelete godoc
// @Summary      [Admin] Delete a store
// @Description  Admin: Permanently remove a tokoh from the directory
// @Tags         [Admin] Store Management
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Store ID"
// @Success      200  {object}  response.envelope
// @Router       /admin/stores/{id} [delete]
func (h *Handler) AdminDelete(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID tokoh tidak valid")
	}

	if err := h.service.DeleteStore(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "tokoh berhasil dihapus", nil)
}
