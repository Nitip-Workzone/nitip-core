package wallet

import (
	"bytes"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"fmt"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/auth"
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
	wallets := router.Group("/wallets", middleware.Protected(h.db, h.redis))

	wallets.Get("/balance", h.GetBalance)
	wallets.Get("/transactions", h.GetTransactions)
	wallets.Get("/transactions/status", h.GetTransactionStatus)
	wallets.Post("/topup", h.TopUp)
	wallets.Get("/withdrawal-channels", h.GetWithdrawalChannels)
	wallets.Post("/withdraw/inquiry", h.Inquiry)
	wallets.Post("/withdraw", middleware.RateLimit(h.redis, 3, 1*time.Minute), h.Withdraw)

	admin := router.Group("/admin/wallets", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/system-balance", h.AdminGetSystemBalance)
	admin.Get("/withdrawals", h.AdminListWithdrawals)
	admin.Post("/topup/simulate-success", h.SimulateSuccess)
	admin.Post("/topup/:reference/finalize", h.AdminFinalizeTopUp)
	admin.Post("/withdrawals/simulate-success", h.SimulateSuccess) // Also allow admin to simulate
	admin.Post("/withdrawals/:id/approve", h.AdminApproveWithdrawal)

	// Public Webhooks
	webhooks := router.Group("/webhooks")
	webhooks.Post("/qris", h.WebhookQris)
	webhooks.Post("/disbursement", h.WebhookDisbursement)
	webhooks.Post("/midtrans", h.WebhookMidtrans)
	webhooks.Post("/payment-listener", h.WebhookPaymentListener)
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
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	userID := userClaims.UserID

	w, err := h.service.GetBalance(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "saldo berhasil diambil", w)
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
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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

	return response.Success(c, "riwayat transaksi berhasil diambil", txs)
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
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	userID := userClaims.UserID

	var req AmountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	wtx, err := h.service.InitiateTopUp(c.Context(), userID, req.Amount)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "top up berhasil dimulai, menunggu pembayaran", wtx)
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
		return response.BadRequest(c, "format permintaan tidak valid")
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

type AdminFinalizeTopUpRequest struct {
	NotificationID string `json:"notification_id"`
}

// AdminFinalizeTopUp godoc
// @Summary      [ADMIN] Finalize top up manually
// @Description  Confirm a pending wallet top-up manually, optionally registering notification_id to prevent duplicate webhook processing
// @Tags         [Admin] Finance
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        reference path      string                     true  "Top-Up Transaction Reference"
// @Param        body      body      AdminFinalizeTopUpRequest  false "Finalize payload"
// @Success      200       {object}  response.envelope{data=WalletTransaction}
// @Failure      400       {object}  response.envelope
// @Router       /admin/wallets/topup/{reference}/finalize [post]
func (h *Handler) AdminFinalizeTopUp(c *fiber.Ctx) error {
	reference := c.Params("reference")
	if reference == "" {
		return response.BadRequest(c, "parameter reference wajib diisi")
	}

	var req AdminFinalizeTopUpRequest
	_ = c.BodyParser(&req) // parse optional body

	if req.NotificationID != "" && h.redis != nil {
		cacheKey := fmt.Sprintf("payment_listener:processed:%s", req.NotificationID)
		exists, err := h.redis.Exists(c.Context(), cacheKey)
		if err == nil && exists {
			return response.BadRequest(c, "notifikasi/referensi pembayaran ini sudah pernah diproses")
		}
	}

	wtx, err := h.service.FinalizeTopUp(c.Context(), reference, req.NotificationID)
	if err != nil {
		return response.BadRequest(c, fmt.Sprintf("gagal memfinalisasi top-up: %v", err))
	}

	claims := jwt.GetClaims(c)
	var actorEmail string
	if claims != nil {
		actorEmail = claims.Email
	}
	log.Printf("[ADMIN_ACTION] Topup %s manually finalized by Admin %s", reference, actorEmail)

	return response.Success(c, "top-up berhasil difinalisasi secara manual", wtx)
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
	return response.Success(c, "saluran penarikan berhasil diambil", channels)
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
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	userID := userClaims.UserID

	var req WithdrawRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	wtx, err := h.service.RequestWithdrawal(c.Context(), userID, req.Amount, req.ChannelID, req.Pin, req.Metadata)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "penarikan berhasil diajukan", wtx)
}

// AdminGetSystemBalance godoc
// @Summary      [ADMIN] Get system wallet balance summary
// @Description  Retrieve the platform's system wallet balance and service fee collection stats
// @Tags         [Admin] Finance
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.envelope{data=SystemBalanceSummary}
// @Router       /admin/wallets/system-balance [get]
func (h *Handler) AdminGetSystemBalance(c *fiber.Ctx) error {
	summary, err := h.service.GetSystemBalanceSummary(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "saldo sistem berhasil diambil", summary)
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

	return response.Success(c, "daftar penarikan tertunda berhasil diambil", txs)
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
		return response.BadRequest(c, "ID transaksi tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	if err := h.service.ApproveWithdrawal(c.Context(), txID, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "penarikan berhasil disetujui", nil)
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
	userClaims := jwt.GetClaims(c)
	if userClaims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
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

	return response.Success(c, "status transaksi berhasil diambil", wtx)
}

type WebhookQrisPayload struct {
	TrxID       string `json:"trx_id"`
	ReferenceID string `json:"reference_id"`
	Amount      int64  `json:"amount"`
	Status      string `json:"status"`
}

// verifyCallbackToken validates the webhook callback token using constant-time comparison.
// Token is read from config (environment variable WEBHOOK_CALLBACK_TOKEN).
func (h *Handler) verifyCallbackToken(c *fiber.Ctx) bool {
	expected := config.App.WebhookCallbackToken
	if expected == "" {
		if config.App.AppEnv == "production" {
			log.Fatal("[SECURITY] WEBHOOK_CALLBACK_TOKEN must be set in production")
		}
		return false
	}
	received := c.Get("X-Callback-Token")
	return subtle.ConstantTimeCompare([]byte(received), []byte(expected)) == 1
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
	if !h.verifyCallbackToken(c) {
		return response.Forbidden(c, "token callback tidak valid")
	}

	var payload WebhookQrisPayload
	if err := c.BodyParser(&payload); err != nil {
		return response.BadRequest(c, "data webhook tidak valid")
	}

	if payload.Status == "PAID" || payload.Status == "SUCCESS" {
		if OnPaymentSuccess != nil {
			err := OnPaymentSuccess(c.Context(), payload.TrxID)
			if err == nil {
				return response.Success(c, "webhook order diproses", nil)
			}
		}
		_, err := h.service.FinalizeTopUp(c.Context(), payload.TrxID, "")
		if err != nil {
			// Bisa di-ignore jika trx sudah success sebelumnya
			return response.BadRequest(c, err.Error())
		}
	}

	return response.Success(c, "webhook diproses", nil)
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
	if !h.verifyCallbackToken(c) {
		return response.Forbidden(c, "token callback tidak valid")
	}

	var req DisbursementWebhookRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	txID, err := uuid.Parse(req.TrxID)
	if err != nil {
		return response.BadRequest(c, "ID transaksi tidak valid")
	}

	status := StatusCompleted
	if req.Status != "SUCCESS" {
		status = StatusFailed
	}

	if err := h.service.FinalizeWithdrawal(c.Context(), txID, status); err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "webhook disbursement diproses", nil)
}

type MidtransNotification struct {
	TransactionTime   string `json:"transaction_time"`
	TransactionStatus string `json:"transaction_status"`
	StatusCode        string `json:"status_code"`
	SignatureKey      string `json:"signature_key"`
	PaymentType       string `json:"payment_type"`
	OrderID           string `json:"order_id"`
	GrossAmount       string `json:"gross_amount"`
}

// WebhookMidtrans godoc
// @Summary      Midtrans Webhook Callback
// @Description  Receive payment notifications from Midtrans
// @Tags         [Public] Webhook
// @Accept       json
// @Produce      json
// @Param        body              body      MidtransNotification  true  "Webhook Payload"
// @Success      200               {object}  response.envelope
// @Router       /webhooks/midtrans [post]
func (h *Handler) WebhookMidtrans(c *fiber.Ctx) error {
	// Handle empty/ping body request from Midtrans dashboard
	if len(c.Body()) == 0 {
		log.Println("[MIDTRANS-WEBHOOK] Empty body received (likely a test ping). Returning 200 OK.")
		return response.Success(c, "ping webhook diterima", nil)
	}

	log.Printf("[MIDTRANS-WEBHOOK] Incoming Content-Type: %s", c.Get("Content-Type"))
	log.Printf("[MIDTRANS-WEBHOOK] Incoming Raw Body: %s", string(c.Body()))

	var payload MidtransNotification
	if err := c.BodyParser(&payload); err != nil {
		log.Printf("[MIDTRANS-WEBHOOK] BodyParser error: %v", err)
		return response.BadRequest(c, "data webhook tidak valid")
	}

	// Handle empty/ping request from Midtrans dashboard
	if payload.OrderID == "" || payload.SignatureKey == "" || strings.HasPrefix(payload.OrderID, "payment_notif_test_") {
		log.Println("[MIDTRANS-WEBHOOK] Ping/Test request received. Returning 200 OK.")
		return response.Success(c, "ping webhook diterima", nil)
	}

	// Verify Signature: SHA512(order_id + status_code + gross_amount + server_key)
	serverKey := config.App.MidtransServerKey
	signData := payload.OrderID + payload.StatusCode + payload.GrossAmount + serverKey
	hasher := sha512.New()
	hasher.Write([]byte(signData))
	computedSign := hex.EncodeToString(hasher.Sum(nil))

	if computedSign != payload.SignatureKey {
		return response.Forbidden(c, "kunci tanda tangan tidak valid")
	}

	// If paid, finalize the payment
	if payload.TransactionStatus == "settlement" || payload.TransactionStatus == "capture" {
		if OnPaymentSuccess != nil {
			err := OnPaymentSuccess(c.Context(), payload.OrderID)
			if err == nil {
				return response.Success(c, "webhook order diproses", nil)
			}
		}
		_, err := h.service.FinalizeTopUp(c.Context(), payload.OrderID, "")
		if err != nil {
			// Transaction might be processed already or not found
			return response.BadRequest(c, err.Error())
		}
	}

	return response.Success(c, "webhook diproses", nil)
}

type WebhookPaymentListenerPayload struct {
	NotificationID string  `json:"notification_id" validate:"required"`
	Amount         float64 `json:"amount" validate:"required,min=1"`
	Timestamp      int64   `json:"timestamp"`
}

// WebhookPaymentListener godoc
// @Summary      Payment Listener Webhook Callback
// @Description  Receive payment notifications from Android Listener
// @Tags         [Public] Webhook
// @Accept       json
// @Produce      json
// @Param        X-API-Key    header    string                        true  "API Key"
// @Param        X-Timestamp  header    string                        true  "Timestamp"
// @Param        X-Signature  header    string                        true  "HMAC Signature"
// @Param        body         body      WebhookPaymentListenerPayload true  "Webhook Payload"
// @Success      200          {object}  response.envelope
// @Router       /webhooks/payment-listener [post]
func (h *Handler) WebhookPaymentListener(c *fiber.Ctx) error {
	apiKey := c.Get("X-API-Key")
	timestamp := c.Get("X-Timestamp")
	signature := c.Get("X-Signature")

	if apiKey == "" || timestamp == "" || signature == "" {
		return response.Unauthorized(c, "header wajib tidak lengkap: X-API-Key, X-Timestamp, X-Signature")
	}

	authSvc := auth.NewService(h.db)
	_, err := authSvc.ValidateHMAC(c.Context(), apiKey, timestamp, signature, string(c.Body()))
	if err != nil {
		log.Printf("[PAYMENT_LISTENER_WEBHOOK] HMAC Validation Failed: %v", err)
		return response.Unauthorized(c, err.Error())
	}

	var payload WebhookPaymentListenerPayload
	if err := c.BodyParser(&payload); err != nil {
		return response.BadRequest(c, "data webhook tidak valid")
	}

	if errs := validator.Validate(payload); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	// Idempotency: check if notification_id already exists in redis to avoid double-processing
	cacheKey := fmt.Sprintf("payment_listener:processed:%s", payload.NotificationID)
	exists, err := h.redis.Exists(c.Context(), cacheKey)
	if err == nil && exists {
		log.Printf("[PAYMENT_LISTENER_WEBHOOK] Duplicate notification ignored: %s", payload.NotificationID)
		return response.Success(c, "notifikasi duplikat diabaikan", nil)
	}

	// Lock by payment amount to serialize matching and prevent race conditions (double match of same order/topup)
	var lockToken string
	if h.redis != nil {
		lockKey := fmt.Sprintf("lock:payment_listener:amount:%.2f", payload.Amount)
		lockToken, err = h.redis.AcquireLock(c.Context(), lockKey, 5*time.Second)
		if err != nil || lockToken == "" {
			return response.InternalError(c, "proses pembayaran dengan nominal ini sedang berlangsung, silakan coba sesaat lagi")
		}
		defer func() { _ = h.redis.ReleaseLock(c.Context(), lockKey, lockToken) }()
	}

	log.Printf("[PAYMENT_LISTENER_WEBHOOK] Received payment notification. Amount: %.2f, ID: %s", payload.Amount, payload.NotificationID)

	// 1. Try finding a pending direct order matching this total payment amount
	var orderObj struct {
		ID uuid.UUID `bun:"id"`
	}
	err = h.db.NewSelect().
		Table("orders").
		Column("id").
		Where("payment_status = 'unpaid'").
		Where("total_payment = ?", payload.Amount).
		Where("status != 'cancelled'").
		Order("created_at ASC"). // match oldest pending order first
		Limit(1).
		Scan(c.Context(), &orderObj)

	if err == nil && orderObj.ID != uuid.Nil {
		// Found matching order! Process success callback.
		if OnPaymentSuccess != nil {
			err = OnPaymentSuccess(c.Context(), orderObj.ID.String())
			if err != nil {
				log.Printf("[PAYMENT_LISTENER_WEBHOOK] Failed to update order %s payment status: %v", orderObj.ID.String(), err)
				return response.InternalError(c, "gagal memproses pembayaran order")
			}
		}
		// Save to redis idempotency key with 24 hours TTL
		_ = h.redis.Set(c.Context(), cacheKey, "processed", 24*time.Hour)
		log.Printf("[PAYMENT_LISTENER_WEBHOOK] Order %s successfully paid via listener!", orderObj.ID.String())
		return response.Success(c, "pembayaran order berhasil diproses", map[string]interface{}{
			"type":     "order",
			"order_id": orderObj.ID,
		})
	}

	// 2. If no order matches, check if it matches a pending wallet top-up transaction
	var walletTx struct {
		ID        uuid.UUID `bun:"id"`
		Reference string    `bun:"reference"`
	}
	err = h.db.NewSelect().
		Table("wallet_transactions").
		Column("id", "reference").
		Where("type = 'TOP_UP'").
		Where("status = 'pending'").
		Where("amount + pg_fee = ?", payload.Amount).
		Order("created_at ASC"). // match oldest pending topup first
		Limit(1).
		Scan(c.Context(), &walletTx)

	if err == nil && walletTx.ID != uuid.Nil {
		// Found matching topup transaction! Finalize it. Pass the notification ID to FinalizeTopUp
		_, err = h.service.FinalizeTopUp(c.Context(), walletTx.Reference, payload.NotificationID)
		if err != nil {
			log.Printf("[PAYMENT_LISTENER_WEBHOOK] Failed to finalize wallet transaction %s: %v", walletTx.Reference, err)
			return response.InternalError(c, "gagal memproses top-up")
		}
		// Save to redis idempotency key with 24 hours TTL
		_ = h.redis.Set(c.Context(), cacheKey, "processed", 24*time.Hour)
		log.Printf("[PAYMENT_LISTENER_WEBHOOK] Wallet Topup %s successfully finalized via listener!", walletTx.Reference)
		return response.Success(c, "top-up wallet berhasil diproses", map[string]interface{}{
			"type":      "topup",
			"reference": walletTx.Reference,
		})
	}

	log.Printf("[PAYMENT_LISTENER_WEBHOOK] No pending transaction matches amount %.2f", payload.Amount)
	return response.NotFound(c, "tidak ada transaksi pending yang cocok dengan nominal tersebut")
}
