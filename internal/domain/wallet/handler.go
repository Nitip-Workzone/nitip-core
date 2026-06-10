package wallet

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
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
	wallets := router.Group("/wallets", middleware.Protected(h.db, h.redis))

	wallets.Get("/balance", h.GetBalance)
	wallets.Get("/transactions", h.GetTransactions)
	wallets.Get("/transactions/status", h.GetTransactionStatus)
	wallets.Post("/topup", h.TopUp)
	wallets.Get("/withdrawal-channels", h.GetWithdrawalChannels)
	wallets.Post("/withdraw/inquiry", h.Inquiry)
	wallets.Post("/withdraw", middleware.RateLimit(h.redis, 3, 1*time.Minute), h.Withdraw)

	admin := router.Group("/admin/wallets", middleware.Protected(h.db, h.redis), middleware.Role("admin"))
	admin.Get("/withdrawals", h.AdminListWithdrawals)
	admin.Post("/topup/simulate-success", h.SimulateSuccess)
	admin.Post("/withdrawals/simulate-success", h.SimulateSuccess) // Also allow admin to simulate
	admin.Post("/withdrawals/:id/approve", h.AdminApproveWithdrawal)

	// Public Webhooks
	webhooks := router.Group("/webhooks")
	webhooks.Post("/qris", h.WebhookQris)
	webhooks.Post("/disbursement", h.WebhookDisbursement)
}

// GetBalance godoc
// @Summary      Get wallet balance
// @Description  Retrieve the current user's wallet balance
// @Tags         [User] Finance
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=Wallet}
// @Router       /wallets/balance [get]
func (h *Handler) GetBalance(c *fiber.Ctx) error {
	userClaims := c.Locals("user").(*jwt.CustomClaims)
	userID := userClaims.UserID

	w, err := h.service.GetBalance(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "balance retrieved successfully", w)
}

// GetTransactions godoc
// @Summary      Get wallet transactions
// @Description  Retrieve the current user's wallet transaction history
// @Tags         [User] Finance
// @Produce      json
// @Security     BearerAuth
// @Param        page   query   int  false  "Page number"
// @Param        limit  query   int  false  "Items per page"
// @Success      200  {object}  response.envelope{data=[]WalletTransaction}
// @Router       /wallets/transactions [get]
func (h *Handler) GetTransactions(c *fiber.Ctx) error {
	userClaims := c.Locals("user").(*jwt.CustomClaims)
	userID := userClaims.UserID

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset := (page - 1) * limit

	txs, err := h.service.GetTransactions(c.Context(), userID, limit, offset)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "transactions retrieved successfully", txs)
}

type AmountRequest struct {
	Amount float64 `json:"amount" validate:"required,min=1"`
}

// TopUp godoc
// @Summary      Top Up Wallet
// @Description  Top up the user's wallet balance
// @Tags         [User] Finance
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      AmountRequest  true  "Top Up details"
// @Success      200   {object}  response.envelope{data=WalletTransaction}
// @Failure      400   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /wallets/topup [post]
func (h *Handler) TopUp(c *fiber.Ctx) error {
	userClaims := c.Locals("user").(*jwt.CustomClaims)
	userID := userClaims.UserID

	var req AmountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	wtx, err := h.service.InitiateTopUp(c.Context(), userID, req.Amount)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "top up initiated, pending payment", wtx)
}

type SimulateSuccessRequest struct {
	Reference string `json:"reference" validate:"required"`
}

// SimulateSuccess godoc
// @Summary      Simulate Top Up Success
// @Description  Development only: Simulate a successful payment callback for a pending top up
// @Tags         [Admin] Finance
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      SimulateSuccessRequest  true  "Simulate Success details"
// @Success      200   {object}  response.envelope{data=WalletTransaction}
// @Failure      400   {object}  response.envelope
// @Router       /admin/wallets/topup/simulate-success [post]
func (h *Handler) SimulateSuccess(c *fiber.Ctx) error {
	var req SimulateSuccessRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	wtxStatus, err := h.service.GetTransactionStatus(c.Context(), req.Reference)
	if err != nil {
		return response.BadRequest(c, "transaksi tidak ditemukan")
	}
	if wtxStatus.Status != StatusPending {
		return response.BadRequest(c, "simulasi hanya bisa dilakukan untuk transaksi yang berstatus PENDING")
	}

	// Menghubungi mock-qris untuk melakukan simulasi bayar (yang akan memicu webhook)
	payload := map[string]interface{}{
		"trx_id": req.Reference,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post("http://localhost:4000/api/qris/simulate-payment", "application/json", bytes.NewBuffer(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		return response.InternalError(c, "gagal menghubungi mock-qris untuk simulasi")
	}
	defer func() { _ = resp.Body.Close() }()

	// Beri waktu sejenak agar webhook asynchronous dari mock-qris sempat diproses oleh backend
	time.Sleep(100 * time.Millisecond)

	wtx, err := h.service.GetTransactionStatus(c.Context(), req.Reference)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "simulasi berhasil dikirim ke payment gateway", wtx)
}

// GetWithdrawalChannels godoc
// @Summary      Get active withdrawal channels
// @Description  List all allowed bank and e-wallet withdrawal channels with fees
// @Tags         [User] Finance
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=[]WithdrawalChannel}
// @Router       /wallets/withdrawal-channels [get]
func (h *Handler) GetWithdrawalChannels(c *fiber.Ctx) error {
	channels, err := h.service.GetWithdrawalChannels(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, "withdrawal channels retrieved", channels)
}

// Inquiry godoc
// @Summary      Inquiry Account Name
// @Description  Verify the account holder name before withdrawal
// @Tags         [User] Finance
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      InquiryAccountRequest  true  "Inquiry details"
// @Success      200   {object}  response.envelope{data=InquiryAccountResponse}
// @Router       /wallets/withdraw/inquiry [post]
func (h *Handler) Inquiry(c *fiber.Ctx) error {
	var req InquiryAccountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	res, err := h.service.InquiryAccount(c.Context(), req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "rekening terverifikasi", res)
}

type WithdrawRequest struct {
	Amount    float64                `json:"amount" validate:"required,min=10000"`
	ChannelID *uuid.UUID             `json:"channel_id" validate:"required"`
	Pin       string                 `json:"pin" validate:"required,len=6,numeric"`
	Metadata  map[string]interface{} `json:"metadata" validate:"required"`
}

// Withdraw godoc
// @Summary      Request Withdrawal
// @Description  Request a withdrawal from the user's wallet balance using a specific channel
// @Tags         [User] Finance
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      WithdrawRequest  true  "Withdrawal details"
// @Success      200   {object}  response.envelope{data=WalletTransaction}
// @Failure      400   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /wallets/withdraw [post]
func (h *Handler) Withdraw(c *fiber.Ctx) error {
	userClaims := c.Locals("user").(*jwt.CustomClaims)
	userID := userClaims.UserID

	var req WithdrawRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	wtx, err := h.service.RequestWithdrawal(c.Context(), userID, req.Amount, req.ChannelID, req.Pin, req.Metadata)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "withdrawal requested successfully", wtx)
}

// AdminListWithdrawals godoc
// @Summary      [ADMIN] List pending withdrawals
// @Description  List all pending wallet withdrawal requests globally
// @Tags         [Admin] Finance
// @Produce      json
// @Security     BearerAuth
// @Param        page   query   int  false  "Page number"
// @Param        limit  query   int  false  "Items per page"
// @Success      200  {object}  response.envelope{data=[]WalletTransaction}
// @Router       /admin/wallets/withdrawals [get]
func (h *Handler) AdminListWithdrawals(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset := (page - 1) * limit

	txs, err := h.service.GetPendingWithdrawals(c.Context(), limit, offset)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "pending withdrawals retrieved", txs)
}

// AdminApproveWithdrawal godoc
// @Summary      [ADMIN] Approve withdrawal
// @Description  Approve a pending withdrawal and finalize balance deduction
// @Tags         [Admin] Finance
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Transaction UUID" Format(uuid)
// @Success      200 {object}  response.envelope
// @Failure      400 {object}  response.envelope
// @Router       /admin/wallets/withdrawals/{id}/approve [post]
func (h *Handler) AdminApproveWithdrawal(c *fiber.Ctx) error {
	txID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid transaction id")
	}

	claims := c.Locals("user").(*jwt.CustomClaims)
	if err := h.service.ApproveWithdrawal(c.Context(), txID, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "withdrawal approved", nil)
}

// GetTransactionStatus godoc
// @Summary      Get transaction status by reference
// @Description  Check the status of a wallet transaction using its reference code
// @Tags         [User] Finance
// @Produce      json
// @Security     BearerAuth
// @Param        reference  query   string  true  "Transaction reference code"
// @Success      200  {object}  response.envelope{data=WalletTransaction}
// @Failure      400  {object}  response.envelope
// @Router       /wallets/transactions/status [get]
func (h *Handler) GetTransactionStatus(c *fiber.Ctx) error {
	reference := c.Query("reference")
	if reference == "" {
		return response.BadRequest(c, "parameter 'reference' wajib diisi")
	}

	// Pastikan transaksi milik user yang sedang login
	userClaims := c.Locals("user").(*jwt.CustomClaims)
	userID := userClaims.UserID

	wtx, err := h.service.GetTransactionStatus(c.Context(), reference)
	if err != nil {
		return response.NotFound(c, err.Error())
	}

	// Verifikasi wallet milik user
	w, err := h.service.GetBalance(c.Context(), userID)
	if err != nil || w.ID != wtx.WalletID {
		return response.Forbidden(c, "akses ditolak")
	}

	return response.Success(c, "transaction status retrieved", wtx)
}

type WebhookQrisPayload struct {
	TrxID       string `json:"trx_id"`
	ReferenceID string `json:"reference_id"`
	Amount      int64  `json:"amount"`
	Status      string `json:"status"`
}

// WebhookQris godoc
// @Summary      QRIS Webhook Callback
// @Description  Receive payment status updates from mock-qris
// @Tags         [Public] Webhook
// @Accept       json
// @Produce      json
// @Param        X-Callback-Token  header    string              true  "Callback Security Token"
// @Param        body              body      WebhookQrisPayload  true  "Webhook Payload"
// @Success      200               {object}  response.envelope
// @Router       /webhooks/qris [post]
func (h *Handler) WebhookQris(c *fiber.Ctx) error {
	// Security: Verify Callback Token
	token := c.Get("X-Callback-Token")
	if token != "nitip-secure-callback-token" {
		return response.Forbidden(c, "invalid callback token")
	}

	var payload WebhookQrisPayload
	if err := c.BodyParser(&payload); err != nil {
		return response.BadRequest(c, "invalid webhook payload")
	}

	if payload.Status == "PAID" || payload.Status == "SUCCESS" {
		_, err := h.service.FinalizeTopUp(c.Context(), payload.TrxID)
		if err != nil {
			// Bisa di-ignore jika trx sudah success sebelumnya
			return response.BadRequest(c, err.Error())
		}
	}

	return response.Success(c, "webhook processed", nil)
}

type DisbursementWebhookRequest struct {
	TrxID  string `json:"trx_id"`
	Status string `json:"status"`
}

// WebhookDisbursement godoc
// @Summary      Disbursement Webhook Callback
// @Description  Receive disbursement status updates from payment gateway
// @Tags         [Public] Webhook
// @Accept       json
// @Produce      json
// @Param        X-Callback-Token  header    string                      true  "Callback Security Token"
// @Param        body              body      DisbursementWebhookRequest  true  "Webhook Payload"
// @Success      200               {object}  response.envelope
// @Router       /webhooks/disbursement [post]
func (h *Handler) WebhookDisbursement(c *fiber.Ctx) error {
	// Security: Verify Callback Token
	token := c.Get("X-Callback-Token")
	if token != "nitip-secure-callback-token" {
		return response.Forbidden(c, "invalid callback token")
	}

	var req DisbursementWebhookRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	txID, err := uuid.Parse(req.TrxID)
	if err != nil {
		return response.BadRequest(c, "invalid transaction id")
	}

	status := StatusCompleted
	if req.Status != "SUCCESS" {
		status = StatusFailed
	}

	if err := h.service.FinalizeWithdrawal(c.Context(), txID, status); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "disbursement webhook processed", nil)
}
