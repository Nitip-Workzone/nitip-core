package kyc

import (
	"io"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/user"
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
	kyc.Get("/submissions", h.GetMySubmissions)

	admin := router.Group("/admin/kyc", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/pending", h.ListPending)
	admin.Post("/:id/review", h.Review)
	admin.Post("/reset-retry", h.ResetRetry)
}

// Submit godoc
// @Summary      Submit KYC documents
// @Description  Register identity verification documents
// @Tags         [Runner] KYC
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        target_level formData string false "Target level separuh or penuh"
// @Param        facebook_name formData string true "Facebook Name"
// @Param        facebook_screenshot formData file true "Facebook Screenshot"
// @Param        selfie formData file true "Selfie"
// @Param        id_card_number formData string false "ID Card Number (required for penuh)"
// @Param        id_card formData file false "ID Card Image (required for penuh)"
// @Success      201  {object}  response.envelope{data=KycSubmission}
// @Failure      400  {object}  response.envelope
// @Router       /kyc/submit [post]
func (h *Handler) Submit(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	targetLevel := c.FormValue("target_level")
	if targetLevel == "" {
		targetLevel = LevelSeparuh
	}
	if targetLevel != LevelSeparuh && targetLevel != LevelPenuh {
		return response.BadRequest(c, "target_level tidak valid")
	}

	// For penuh, only KTP is required; facebook/selfie are reused from separuh
	var fbScreenshotFile io.Reader
	var sf io.Reader
	var fbScreenshotFilename, selfieFilename string
	facebookName := strings.TrimSpace(c.FormValue("facebook_name"))
	var fbScreenshotHeader *multipart.FileHeader
	var selfieFile *multipart.FileHeader
	if targetLevel == LevelSeparuh {
		if facebookName == "" {
			return response.BadRequest(c, "nama profil facebook wajib diisi")
		}
		var err error
		fbScreenshotHeader, err = c.FormFile("facebook_screenshot")
		if err != nil {
			return response.BadRequest(c, "screenshot halaman profil facebook wajib diunggah")
		}
		if fbScreenshotHeader.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran screenshot facebook terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(fbScreenshotHeader) {
			return response.BadRequest(c, "screenshot facebook harus berupa file gambar (jpg, jpeg, png)")
		}
		selfieFile, err = c.FormFile("selfie")
		if err != nil {
			return response.BadRequest(c, "gambar selfie wajib diunggah")
		}
		if selfieFile.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran gambar selfie terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(selfieFile) {
			return response.BadRequest(c, "selfie harus berupa file gambar (jpg, jpeg, png)")
		}
		f, ferr := fbScreenshotHeader.Open()
		if ferr != nil {
			return response.InternalError(c, "gagal membuka screenshot facebook")
		}
		defer func() { _ = f.Close() }()
		fbScreenshotFile = f
		fbScreenshotFilename = fbScreenshotHeader.Filename
		sf2, serr := selfieFile.Open()
		if serr != nil {
			return response.InternalError(c, "gagal membuka gambar selfie")
		}
		defer func() { _ = sf2.Close() }()
		sf = sf2
		selfieFilename = selfieFile.Filename
	}

	// KTP handling: required for penuh, optional for separuh but if provided must be valid
	var ic io.Reader
	var idCardFilename string
	number := strings.TrimSpace(c.FormValue("id_card_number"))
	idCardFile, errCard := c.FormFile("id_card")
	hasIdCardFile := errCard == nil && idCardFile != nil
	if targetLevel == LevelPenuh {
		if number == "" {
			return response.BadRequest(c, "nomor KTP wajib diisi untuk upgrade penuh")
		}
		if !hasIdCardFile {
			return response.BadRequest(c, "foto KTP wajib diunggah untuk upgrade penuh")
		}
		if idCardFile.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran foto KTP terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(idCardFile) {
			return response.BadRequest(c, "foto KTP harus berupa file gambar (jpg, jpeg, png)")
		}
		f, errOpen := idCardFile.Open()
		if errOpen != nil {
			return response.InternalError(c, "gagal membuka foto KTP")
		}
		ic = f
		idCardFilename = idCardFile.Filename
		defer func() { _ = f.Close() }()
	} else {
		// separuh: id_card optional, but if provided must be valid, not silently ignored
		if hasIdCardFile || number != "" {
			if hasIdCardFile && number == "" {
				return response.BadRequest(c, "nomor KTP wajib diisi jika foto KTP disertakan")
			}
			if hasIdCardFile {
				if idCardFile.Size > 5*1024*1024 {
					return response.BadRequest(c, "ukuran foto KTP terlalu besar (maksimal 5MB)")
				}
				if !fileutil.IsImage(idCardFile) {
					return response.BadRequest(c, "foto KTP harus berupa file gambar (jpg, jpeg, png)")
				}
				f, errOpen := idCardFile.Open()
				if errOpen != nil {
					return response.InternalError(c, "gagal membuka foto KTP")
				}
				ic = f
				idCardFilename = idCardFile.Filename
				defer func() { _ = f.Close() }()
			}
		}
	}

	req := SubmitKycRequest{
		IdCardNumber:           number,
		IdCardFile:             ic,
		IdCardName:             idCardFilename,
		SelfieFile:             sf,
		SelfieName:             selfieFilename,
		FacebookName:           facebookName,
		FacebookScreenshotFile: fbScreenshotFile,
		FacebookScreenshotName: fbScreenshotFilename,
		TargetLevel:            targetLevel,
	}

	kyc, err := h.service.Submit(c.Context(), claims.UserID, req)
	if err != nil {
		msg := err.Error()
		low := strings.ToLower(msg)
		if strings.Contains(low, "pending untuk level") {
			return response.ConflictWithCode(c, msg, "KYC_PENDING_EXISTS")
		}
		if strings.Contains(low, "batas percobaan") {
			return response.ForbiddenWithCode(c, msg, "KYC_RETRY_LIMIT")
		}
		if strings.Contains(low, "sudah terverifikasi") || strings.Contains(low, "hanya tersedia untuk") {
			return response.ConflictWithCode(c, msg, "KYC_LEVEL_CONFLICT")
		}
		if strings.Contains(low, "target_level") {
			return response.BadRequest(c, msg)
		}
		return response.BadRequest(c, msg)
	}

	return response.Created(c, "KYC berhasil diajukan untuk ditinjau", kyc)
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
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	kyc, err := h.service.GetStatus(c.Context(), claims.UserID)
	if err != nil {
		return response.Success(c, "tidak ada pengajuan KYC ditemukan", nil)
	}

	return response.Success(c, "status KYC berhasil diambil", kyc)
}

// GetMySubmissions godoc
// @Summary      Get my KYC submissions
// @Tags         [Runner] KYC
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.envelope
// @Router       /kyc/submissions [get]
func (h *Handler) GetMySubmissions(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	subs, _, remaining, err := h.service.GetMySubmissions(c.Context(), claims.UserID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	// Also fetch user level
	// Use query via service? we return remaining and subs
	return response.Success(c, "daftar pengajuan KYC", fiber.Map{
		"submissions": subs,
		"remaining":   remaining,
	})
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

	return response.Success(c, "daftar pengajuan KYC tertunda berhasil diambil", results)
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
		return response.BadRequest(c, "ID KYC tidak valid")
	}

	var req ReviewRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	if !req.Approved && strings.TrimSpace(req.Note) == "" {
		return response.BadRequest(c, "alasan penolakan wajib diisi")
	}
	if err := h.service.Review(c.Context(), id, claims.UserID, req.Approved, req.Note); err != nil {
		msg := err.Error()
		low := strings.ToLower(msg)
		if strings.Contains(low, "sudah diproses") {
			return response.ConflictWithCode(c, msg, "KYC_ALREADY_PROCESSED")
		}
		if strings.Contains(low, "alasan penolakan") {
			return response.BadRequest(c, msg)
		}
		return response.BadRequest(c, msg)
	}
	msg := "kyc submission rejected"
	if req.Approved {
		msg = "kyc submission approved"
	}
	return response.Success(c, msg, nil)
}

type ResetRetryRequest struct {
	UserID      string `json:"user_id"`
	TargetLevel string `json:"target_level"`
}

// ResetRetry godoc
// @Summary [ADMIN] Reset retry limit
// @Tags [Admin] KYC Review
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body ResetRetryRequest true "Reset retry"
// @Success 200 {object} response.envelope
// @Router /admin/kyc/reset-retry [post]
func (h *Handler) ResetRetry(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	var req ResetRetryRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		return response.BadRequest(c, "user_id tidak valid")
	}
	if req.TargetLevel != LevelSeparuh && req.TargetLevel != LevelPenuh {
		return response.BadRequest(c, "target_level tidak valid")
	}
	// Audit old/new is handled in service
	if err := h.service.ResetRetry(c.Context(), uid, req.TargetLevel, claims.UserID); err != nil {
		msg := err.Error()
		low := strings.ToLower(msg)
		if strings.Contains(low, "tidak diperlukan") {
			return response.BadRequest(c, msg)
		}
		if strings.Contains(low, "tidak ditemukan") || strings.Contains(low, "tidak ada") {
			return response.NotFound(c, msg)
		}
		return response.BadRequest(c, msg)
	}
	remaining := time.Now()
	_ = remaining
	return response.Success(c, "retry direset", nil)
}
