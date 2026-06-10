package systemconfig

import (
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/codecoffy/nitip-core/pkg/validator"
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
	admin := router.Group("/admin/configs", middleware.Protected(h.db, h.redis), middleware.Role("admin"))
	admin.Get("/", h.AdminListConfigs)
	admin.Put("/:key", h.AdminUpdateConfig)

	// Public configurations (accessible by mobile & web)
	router.Get("/configs/public", h.GetPublicConfig)
}

// AdminListConfigs godoc
// @Summary      [ADMIN] List all configs
// @Description  Retrieve all dynamic configuration values
// @Tags         [Admin] System Config
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=[]Config}
// @Router       /admin/configs [get]
func (h *Handler) AdminListConfigs(c *fiber.Ctx) error {
	cfgs, err := h.service.GetAll(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "configs retrieved successfully", cfgs)
}

type updateConfigRequest struct {
	Value       string `json:"value" validate:"required"`
	Description string `json:"description"`
}

// AdminUpdateConfig godoc
// @Summary      [ADMIN] Update a config value
// @Description  Set or update a specific configuration key
// @Tags         [Admin] System Config
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        key   path      string               true  "Config Key"
// @Param        body  body      updateConfigRequest  true  "New config details"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /admin/configs/{key} [put]
func (h *Handler) AdminUpdateConfig(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return response.BadRequest(c, "invalid config key")
	}

	var req updateConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.SetValue(c.Context(), key, req.Value, req.Description); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "config updated successfully", nil)
}

// GetPublicConfig godoc
// @Summary      Get public system config
// @Description  Retrieve public configurations without authentication
// @Tags         Shared
// @Produce      json
// @Success      200  {object}  response.envelope
// @Router       /configs/public [get]
func (h *Handler) GetPublicConfig(c *fiber.Ctx) error {
	return response.Success(c, "public configurations retrieved successfully", fiber.Map{
		"kyc_verification_required": !config.App.BypassKYCValidation,
	})
}
