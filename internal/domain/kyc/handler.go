package kyc

import (
	"strconv"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
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
	kyc := router.Group("/kyc", middleware.Protected(h.db, h.redis))
	kyc.Post("/submit", middleware.RateLimit(h.redis, 2, 1*time.Minute), h.Submit)
	kyc.Get("/me", h.GetMyStatus)

	admin := router.Group("/admin/kyc", middleware.Protected(h.db, h.redis), middleware.Role("admin"))
	admin.Get("/pending", h.ListPending)
	admin.Post("/:id/review", h.Review)
}

// Submit godoc
// @Summary      Submit KYC documents
// @Description  Register id card number and upload images for identity verification
// @Tags         [Runner] KYC
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id_card_number  formData  string  true  "ID Card Number"
// @Param        id_card         formData  file    true  "ID Card Image"
// @Param        selfie          formData  file    true  "Selfie Image"
// @Success      201  {object}  response.envelope{data=KycSubmission}
// @Failure      400  {object}  response.envelope
// @Router       /kyc/submit [post]
func (h *Handler) Submit(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)

	number := c.FormValue("id_card_number")
	if number == "" {
		return response.BadRequest(c, "id_card_number is required")
	}

	idCardFile, err := c.FormFile("id_card")
	if err != nil {
		return response.BadRequest(c, "id_card image is required")
	}
	if idCardFile.Size > 5*1024*1024 {
		return response.BadRequest(c, "id_card image is too large (max 5MB)")
	}
	if !fileutil.IsImage(idCardFile) {
		return response.BadRequest(c, "id_card must be an image file (jpg, jpeg, png)")
	}

	selfieFile, err := c.FormFile("selfie")
	if err != nil {
		return response.BadRequest(c, "selfie image is required")
	}
	if selfieFile.Size > 5*1024*1024 {
		return response.BadRequest(c, "selfie image is too large (max 5MB)")
	}
	if !fileutil.IsImage(selfieFile) {
		return response.BadRequest(c, "selfie must be an image file (jpg, jpeg, png)")
	}

	ic, err := idCardFile.Open()
	if err != nil {
		return response.InternalError(c, "failed to open id card image")
	}
	defer func() { _ = ic.Close() }()

	sf, err := selfieFile.Open()
	if err != nil {
		return response.InternalError(c, "failed to open selfie image")
	}
	defer func() { _ = sf.Close() }()

	req := SubmitKycRequest{
		IdCardNumber: number,
		IdCardFile:   ic,
		IdCardName:   idCardFile.Filename,
		SelfieFile:   sf,
		SelfieName:   selfieFile.Filename,
	}

	kyc, err := h.service.Submit(c.Context(), claims.UserID, req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "kyc submitted for review", kyc)
}

// GetMyStatus godoc
// @Summary      Get current user KYC status
// @Description  Retrieve the identity verification status for the logged-in user
// @Tags         [Runner] KYC
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=KycSubmission}
// @Failure      404  {object}  response.envelope
// @Router       /kyc/me [get]
func (h *Handler) GetMyStatus(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)

	kyc, err := h.service.GetStatus(c.Context(), claims.UserID)
	if err != nil {
		// Return success with null data instead of 404 to avoid Dio errors in mobile
		return response.Success(c, "no kyc submission found", nil)
	}

	return response.Success(c, "kyc status retrieved", kyc)
}

// ListPending godoc
// @Summary      [ADMIN] List pending KYC submissions
// @Description  Retrieve a paginated list of KYC submissions waiting for review
// @Tags         [Admin] KYC Review
// @Produce      json
// @Security     BearerAuth
// @Param        page   query   int  false  "Page number"
// @Param        limit  query   int  false  "Items per page"
// @Success      200  {object}  response.envelope{data=[]KycSubmission}
// @Router       /admin/kyc/pending [get]
func (h *Handler) ListPending(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset := (page - 1) * limit

	results, err := h.service.ListPending(c.Context(), offset, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "pending kyc submissions retrieved", results)
}

type ReviewRequest struct {
	Approved bool   `json:"approved"`
	Note     string `json:"note"`
}

// Review godoc
// @Summary      [ADMIN] Review a KYC submission
// @Description  Approve or reject a user's identity verification documents
// @Tags         [Admin] KYC Review
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string         true  "KYC ID"  Format(uuid)
// @Param        body  body      ReviewRequest  true  "Review decision"
// @Success      200  {object}  response.envelope
// @Failure      400  {object}  response.envelope
// @Router       /admin/kyc/{id}/review [post]
func (h *Handler) Review(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid kyc id")
	}

	var req ReviewRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	claims := c.Locals("user").(*jwt.CustomClaims)
	if err := h.service.Review(c.Context(), id, claims.UserID, req.Approved, req.Note); err != nil {
		return response.BadRequest(c, err.Error())
	}
	msg := "kyc submission rejected"
	if req.Approved {
		msg = "kyc submission approved"
	}
	return response.Success(c, msg, nil)
}


