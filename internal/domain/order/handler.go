package order

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/internal/realtime"
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
	poolHub *realtime.PoolHub
}

func NewHandler(service Service, db *bun.DB, redis *cache.Redis, poolHub *realtime.PoolHub) *Handler {
	if poolHub == nil {
		poolHub = realtime.NewPoolHub()
	}
	return &Handler{service: service, db: db, redis: redis, poolHub: poolHub}
}

func (h *Handler) RegisterRoutes(router fiber.Router) {
	// All order routes are protected
	orders := router.Group("/orders", middleware.Protected(h.db, h.redis))

	// Requester routes
	orders.Post("/", middleware.RateLimit(h.redis, 5, 1*time.Minute), middleware.Role(user.RoleRequester), h.Create)
	orders.Post("/estimate-fee", h.GetFeeEstimate)
	// Merchant should not get 403 on /me - return empty list in service instead (avoid Flutter log spam)
	orders.Get("/me", middleware.Role(user.RoleRequester, user.RoleRunner, user.RoleMerchant), h.GetMyOrders)
	orders.Post("/:id/cancel", middleware.Role(user.RoleRequester, user.RoleRunner, user.RoleMerchant), h.Cancel)
	orders.Post("/:id/dispute", middleware.Role(user.RoleRequester), h.Dispute)
	orders.Post("/:id/refresh-qris", middleware.Role(user.RoleRequester), h.RefreshQRIS)

	// Merchant routes
	orders.Get("/merchant/orders", middleware.Role(user.RoleMerchant), h.GetMerchantOrders)
	orders.Post("/:id/merchant-accept", middleware.Role(user.RoleMerchant), h.MerchantAccept)
	orders.Post("/:id/merchant-ready", middleware.Role(user.RoleMerchant), h.MerchantReady)

	// Runner pool realtime (SSE)
	orders.Get("/pool/stream", middleware.Role(user.RoleRunner), h.PoolStream)
	// Merchant pool realtime (SSE)
	orders.Get("/merchant/stream", middleware.Role(user.RoleMerchant), h.MerchantPoolStream)

	// Runner endpoints
	orders.Get("/available", middleware.Role(user.RoleRunner), h.GetAvailableOrders)
	orders.Post("/:id/accept", middleware.Role(user.RoleRunner), h.Accept)
	orders.Post("/:id/pickup", middleware.Role(user.RoleRunner), h.Pickup)
	orders.Post("/:id/purchased", middleware.Role(user.RoleRunner), h.Purchased)
	orders.Post("/:id/complete", middleware.Role(user.RoleRunner), h.Complete)
	orders.Post("/:id/adjust-price", middleware.Role(user.RoleRunner), h.AdjustPrice)

	// Price Adjustment Approval (Requester)
	orders.Post("/:id/approve-adjustment", middleware.Role(user.RoleRequester, user.RoleRunner), h.ApproveAdjustment)
	orders.Post("/:id/reject-adjustment", middleware.Role(user.RoleRequester, user.RoleRunner), h.RejectAdjustment)

	// Admin/General
	orders.Get("/:id", h.Get)
	orders.Get("/:id/stream", h.Stream) // Status updates
	orders.Get("/:id/track", h.Track)   // Live location tracking
	admin := router.Group("/admin/orders", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/", h.AdminListOrders)
	admin.Get("/disputes", h.AdminListDisputes)
	admin.Post("/:id/cancel", h.AdminCancelOrder)
	admin.Post("/:id/resolve-dispute", h.AdminResolveDispute)
	admin.Post("/:id/resolve", h.AdminResolveDispute)
	admin.Post("/:id/pay", h.PayStub) // Dummy Payment Simulation
}

// Create godoc
// @Summary      Create a new order
// @Description  Create an order as a requester
// @Tags         [User] Order
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateOrderRequest  true  "Order payload"
// @Success      201   {object}  response.envelope{data=Order}
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      403   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /orders [post]
func (h *Handler) Create(c *fiber.Ctx) error {
	var req CreateOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	order, err := h.service.Create(c.Context(), claims.UserID, req)
	if err != nil {
		log.Printf("[ORDER_CREATE_ERROR] Failed to create order for User %s. Error: %v", claims.UserID, err)
		lowMsg := strings.ToLower(err.Error())
		if strings.Contains(lowMsg, "sql") ||
			strings.Contains(lowMsg, "constraint") ||
			strings.Contains(lowMsg, "foreign key") ||
			strings.Contains(lowMsg, "table") ||
			strings.Contains(lowMsg, "column") ||
			strings.Contains(lowMsg, "relation") ||
			strings.Contains(lowMsg, "db") {
			return response.InternalError(c, err.Error())
		}
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "pesanan berhasil dibuat", order)
}

// GetMyOrders godoc
// @Summary      Get user orders
// @Description  Get a list of orders related to the logged-in user (as requester or runner)
// For merchant, returns empty list to avoid 403 spam - merchant should use /merchant/orders
// @Tags         [Shared] Order View
// @Produce      json
// @Security     BearerAuth
// @Success      200   {object}  response.envelope{data=[]Order}
// @Failure      401   {object}  response.envelope
// @Router       /orders/me [get]
func (h *Handler) GetMyOrders(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	// Merchant calling /orders/me should get empty, not 403 or unrelated data
	if claims.Role == user.RoleMerchant {
		return response.Success(c, "daftar pesanan berhasil diambil", []interface{}{})
	}

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit", "15"))
	if limit < 1 {
		limit = 15
	}
	offset := (page - 1) * limit

	startDate := c.Query("start_date")
	endDate := c.Query("end_date")

	orders, err := h.service.GetByUser(c.Context(), claims.UserID, limit, offset, startDate, endDate)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar pesanan berhasil diambil", orders)
}

// Get godoc
// @Summary      Get order by ID
// @Description  Get specific order details
// @Tags         [Shared] Order View
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {object}  response.envelope{data=Order}
// @Failure      401   {object}  response.envelope
// @Failure      404   {object}  response.envelope
// @Router       /orders/{id} [get]
func (h *Handler) Get(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	order, err := h.service.GetByID(c.Context(), id, claims.UserID, claims.Role)
	if err != nil {
		return response.Forbidden(c, err.Error())
	}

	return response.Success(c, "detail pesanan berhasil diambil", order)
}

// Cancel godoc
// @Summary      Cancel an order
// @Description  Requester cancelling an order. Refunds or partial fees apply.
// @Tags         [User] Order
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Router       /orders/{id}/cancel [post]
func (h *Handler) Cancel(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	type CancelPayload struct {
		Reason string `json:"reason"`
	}
	var req CancelPayload
	_ = c.BodyParser(&req) // parse optional reason body

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.CancelOrder(c.Context(), id, claims.UserID, req.Reason); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "pesanan berhasil dibatalkan", nil)
}

// Accept godoc
// @Summary      Accept an active order
// @Description  Runner accepting a pending order
// @Tags         [Runner] Order Execution
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      403   {object}  response.envelope
// @Router       /orders/{id}/accept [post]
func (h *Handler) Accept(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.AcceptOrder(c.Context(), id, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "pesanan berhasil diterima", nil)
}

// Pickup godoc
// @Summary      Mark order as picked up
// @Description  Runner marks the order as picked up and moving to delivery phase
// @Tags         [Runner] Order Execution
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {object}  response.envelope
// @Router       /orders/{id}/pickup [post]
func (h *Handler) Pickup(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.PickupOrder(c.Context(), id, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "status pesanan diperbarui ke pengantaran", nil)
}

type CompletePayload struct {
	CompletionCode   string `json:"completion_code" validate:"required"`
	DeliveryImageURL string `json:"delivery_image_url" validate:"omitempty,url"`
}

// Complete godoc
// @Summary      Complete an order
// @Description  Runner completing an ongoing order by providing completion code from penitip and optional delivery image proof
// @Tags         [Runner] Order Execution
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id              path      string  true  "Order UUID"  Format(uuid)
// @Param        completion_code formData  string  true  "Completion Code"
// @Param        delivery_image  formData  file    false "Delivery Proof Image"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      403   {object}  response.envelope
// @Router       /orders/{id}/complete [post]
func (h *Handler) Complete(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	code := c.FormValue("completion_code")
	if code == "" {
		// Fallback to JSON body parser if they sent it as JSON for compatibility
		var jsonReq CompletePayload
		if err := c.BodyParser(&jsonReq); err == nil {
			code = jsonReq.CompletionCode
		}
	}
	// code can be empty if it is a force-completion (>30 mins delivering)
	// validation is deferred to the service layer.

	var deliveryReader io.Reader
	var deliveryFilename string
	file, err := c.FormFile("delivery_image")
	if err == nil {
		if file.Size > 5*1024*1024 {
			return response.BadRequest(c, "ukuran gambar bukti terlalu besar (maksimal 5MB)")
		}
		if !fileutil.IsImage(file) {
			return response.BadRequest(c, "bukti penyerahan harus berupa file gambar (jpg, jpeg, png)")
		}
		f, err := file.Open()
		if err != nil {
			return response.InternalError(c, "gagal membuka file bukti penyerahan")
		}
		defer func() { _ = f.Close() }()
		deliveryReader = f
		deliveryFilename = file.Filename
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.CompleteOrder(c.Context(), id, claims.UserID, code, deliveryReader, deliveryFilename); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "pesanan berhasil diselesaikan", nil)
}

type PurchasePayload struct {
	ReceiptURL string `json:"receipt_url" validate:"required,url"`
}

// Purchased godoc
// @Summary      Mark order as purchased
// @Description  Runner marks the order as purchased and uploads a receipt file
// @Tags         [Runner] Order Execution
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string  true  "Order UUID"  Format(uuid)
// @Param        receipt  formData  file    true  "Receipt Image file"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      401   {object}  response.envelope
// @Failure      403   {object}  response.envelope
// @Router       /orders/{id}/purchased [post]
func (h *Handler) Purchased(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	file, err := c.FormFile("receipt")
	if err != nil {
		return response.BadRequest(c, "file gambar kwitansi (receipt) wajib diunggah")
	}
	if file.Size > 5*1024*1024 {
		return response.BadRequest(c, "ukuran file gambar kwitansi terlalu besar (maksimal 5MB)")
	}
	if !fileutil.IsImage(file) {
		return response.BadRequest(c, "kwitansi harus berupa file gambar (jpg, jpeg, png)")
	}

	f, err := file.Open()
	if err != nil {
		return response.InternalError(c, "gagal membuka file kwitansi")
	}
	defer func() { _ = f.Close() }()

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.SubmitPurchaseReceipt(c.Context(), id, claims.UserID, f, file.Filename); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "fase pembelian diperbarui", nil)
}

// Stream godoc
// @Summary      Stream order status updates
// @Description  Server-Sent Events (SSE) endpoint to listen for order status changes in real-time
// @Tags         [Shared] Communications & Tracking
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {string}  string  "SSE Stream"
// @Router       /orders/{id}/stream [get]
func (h *Handler) Stream(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("X-Accel-Buffering", "no")

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	// Single query for initial state + permission check
	initialOrder, err := h.service.GetByID(c.Context(), id, claims.UserID, claims.Role)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Event-driven low-burden SSE: subscribe to PoolHub cell "order:{id}"
		// Zero DB queries while idle, only push on status change.
		cellKey := fmt.Sprintf("order:%s", id.String())
		clientID := fmt.Sprintf("order_stream:%s:%d", id.String(), time.Now().UnixNano())

		sseClient := &realtime.SSEClient{
			ID:       clientID,
			CellKeys: []string{cellKey},
			Ch:       make(chan realtime.PoolEvent, 16),
			Done:     make(chan struct{}),
		}

		if h.poolHub != nil {
			h.poolHub.RegisterSSE(sseClient)
			defer h.poolHub.UnregisterSSE(clientID)
		}

		// Send initial status immediately (1 query already done)
		initMsg := "data: {\"status\": \"" + initialOrder.Status + "\", \"type\": \"init\"}\n\n"
		_, _ = w.WriteString(initMsg)
		_ = w.Flush()

		// Heartbeat every 25s (battery friendly vs 20s pool)
		heartbeatTicker := time.NewTicker(25 * time.Second)
		defer heartbeatTicker.Stop()

		// Terminal statuses – short-lived connection then close
		if initialOrder.Status == StatusCompleted || initialOrder.Status == StatusCancelled || initialOrder.Status == StatusExpired {
			return
		}

		for {
			select {
			case <-sseClient.Done:
				return
			case <-heartbeatTicker.C:
				if err := realtime.WriteSSEHeartbeat(w); err != nil {
					return
				}
			case ev := <-sseClient.Ch:
				// Write event data – ev.Data may contain status
				if err := realtime.WriteSSEEvent(w, ev); err != nil {
					return
				}
				// End stream if terminal status reached
				if ev.Type == realtime.EventOrderCompleted || ev.Type == realtime.EventOrderCancelled || ev.Type == realtime.EventOrderExpired {
					// Give client time to receive
					time.Sleep(100 * time.Millisecond)
					return
				}
			}
		}
	})

	return nil
}

type AdminPayRequest struct {
	NotificationID string `json:"notification_id"`
	Reason         string `json:"reason"`
}

// PayStub godoc
// @Summary      Simulate order payment / Confirm payment manually
// @Description  Allows admin to mark order as paid manually, optionally registering notification_id to prevent duplicate webhook processing
// @Tags         [Admin] Order Management
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Param        body body  AdminPayRequest false "Payment details"
// @Success      200   {object}  response.envelope
// @Router       /admin/orders/{id}/pay [post]
func (h *Handler) PayStub(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req AdminPayRequest
	_ = c.BodyParser(&req) // parse optional body

	if req.NotificationID != "" && h.redis != nil {
		cacheKey := fmt.Sprintf("payment_listener:processed:%s", req.NotificationID)
		exists, err := h.redis.Exists(c.Context(), cacheKey)
		if err == nil && exists {
			return response.BadRequest(c, "notifikasi/referensi pembayaran ini sudah pernah diproses")
		}
	}

	// Call service to update payment status to escrow/paid
	err = h.service.UpdatePaymentStatus(c.Context(), id, PaymentEscrow)
	if err != nil {
		return response.BadRequest(c, fmt.Sprintf("gagal memperbarui pembayaran: %v", err))
	}

	// Register notification_id in Redis if provided
	if req.NotificationID != "" && h.redis != nil {
		cacheKey := fmt.Sprintf("payment_listener:processed:%s", req.NotificationID)
		_ = h.redis.Set(c.Context(), cacheKey, "processed", 24*time.Hour)
	}

	claims := jwt.GetClaims(c)
	actorEmail := "Unknown Admin"
	var actorID *uuid.UUID
	if claims != nil {
		actorEmail = claims.Email
		actorID = &claims.UserID
	}
	log.Printf("[ADMIN_ACTION] Order %s manually paid by Admin %s with reason/remark: %s", id, actorEmail, req.Reason)

	// Write to audit_logs
	auditLog := &audit.AuditLog{
		UserID:     actorID,
		Action:     "MANUAL_ORDER_PAY",
		Resource:   "order",
		ResourceID: id.String(),
		NewValues:  map[string]interface{}{"status": "paid", "notification_id": req.NotificationID, "reason": req.Reason},
		IPAddress:  c.IP(),
		UserAgent:  c.Get("User-Agent"),
	}
	_, _ = h.db.NewInsert().Model(auditLog).Exec(c.Context())

	return response.Success(c, "konfirmasi pembayaran manual berhasil", nil)
}

// AdminListOrders godoc
// @Summary      [ADMIN] List orders
// @Description  Retrieve orders with optional status filter
// @Tags         [Admin] Order Management
// @Produce      json
// @Security     BearerAuth
// @Param        status  query   string  false  "Order status filter"
// @Param        page    query   int     false  "Page number"
// @Param        limit   query   int     false  "Items per page"
// @Success      200  {object}  response.envelope{data=[]Order}
// @Router       /admin/orders [get]
func (h *Handler) AdminListOrders(c *fiber.Ctx) error {
	status := c.Query("status")

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit", "20"))

	offset := (page - 1) * limit

	orders, err := h.service.GetAllWithFilters(c.Context(), status, offset, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar pesanan berhasil diambil", orders)
}

type DisputePayload struct {
	Reason   string `json:"reason" validate:"required"`
	ProofURL string `json:"proof_url" validate:"required,url"`
}

// Dispute godoc
// @Summary      Open a dispute against an order
// @Description  Requester can flag a completed order if an issue arised
// @Tags         [User] Order
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  string          true  "Order UUID"  Format(uuid)
// @Param        body  body  DisputePayload  true  "Dispute details"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /orders/{id}/dispute [post]
func (h *Handler) Dispute(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req DisputePayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.DisputeOrder(c.Context(), id, claims.UserID, req.Reason, req.ProofURL); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "status pesanan dinaikkan ke sengketa", nil)
}

// AdminListDisputes godoc
// @Summary      [ADMIN] List all disputed orders
// @Description  Retrieve a paginated list of disputed orders
// @Tags         [Admin] Order Management
// @Produce      json
// @Security     BearerAuth
// @Param        page   query   int  false  "Page number"
// @Param        limit  query   int  false  "Items per page"
// @Success      200    {object}  response.envelope{data=[]Order}
// @Router       /admin/orders/disputes [get]
func (h *Handler) AdminListDisputes(c *fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset := (page - 1) * limit

	orders, err := h.service.GetAllWithFilters(c.Context(), StatusDisputed, offset, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar pesanan bersengketa berhasil diambil", orders)
}

type ResolvePayload struct {
	Side string `json:"side" validate:"required,oneof=requester runner"`
}

// AdminResolveDispute godoc
// @Summary      [ADMIN] Resolve an order dispute
// @Description  Assigns the escrow funds back to the requester or to the runner based on investigation
// @Tags         [Admin] Order Management
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  string          true  "Order UUID"  Format(uuid)
// @Param        body  body  ResolvePayload  true  "Resolution details"
// @Success      200   {object}  response.envelope
// @Failure      400   {object}  response.envelope
// @Failure      422   {object}  response.envelope{errors=[]response.ValidationError}
// @Router       /admin/orders/{id}/resolve-dispute [post]
// @Router       /admin/orders/{id}/resolve [post]
func (h *Handler) AdminResolveDispute(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req ResolvePayload
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}
	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	if err := h.service.ResolveDispute(c.Context(), id, req.Side); err != nil {
		return response.BadRequest(c, err.Error())
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	log.Printf("[ADMIN_ACTION] Dispute resolved by %s for Order %s with side %s", claims.Email, id, req.Side)

	return response.Success(c, "sengketa diselesaikan dan escrow dialihkan", nil)
}

// AdminCancelOrder godoc
// @Summary      [ADMIN] Force cancel an order
// @Description  Allows an admin to forcefully cancel an order, bypassing normal states
// @Tags         [Admin] Order Management
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200  {object}  response.envelope
// @Failure      400  {object}  response.envelope
// @Router       /admin/orders/{id}/cancel [post]
type AdminCancelOrderRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) AdminCancelOrder(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req AdminCancelOrderRequest
	_ = c.BodyParser(&req)

	if err := h.service.ForceCancelOrder(c.Context(), id); err != nil {
		return response.BadRequest(c, err.Error())
	}

	claims := jwt.GetClaims(c)
	actorEmail := "Unknown Admin"
	var actorID *uuid.UUID
	if claims != nil {
		actorEmail = claims.Email
		actorID = &claims.UserID
	}
	log.Printf("[ADMIN_ACTION] Order %s forcefully cancelled by Admin %s with reason/remark: %s", id, actorEmail, req.Reason)

	// Write to audit_logs
	auditLog := &audit.AuditLog{
		UserID:     actorID,
		Action:     audit.ActionOrderCancel,
		Resource:   "order",
		ResourceID: id.String(),
		NewValues:  map[string]interface{}{"status": "cancelled", "reason": req.Reason},
		IPAddress:  c.IP(),
		UserAgent:  c.Get("User-Agent"),
	}
	_, _ = h.db.NewInsert().Model(auditLog).Exec(c.Context())

	return response.Success(c, "pesanan berhasil dibatalkan", nil)
}

// GetAvailableOrders godoc
// @Summary      List available orders for runners
// @Description  Retrieve all orders that are current pending or matching for a potential runner
// @Tags         [Runner] Order Execution
// @Produce      json
// @Security     BearerAuth
// @Success      200   {object}  response.envelope{data=[]Order}
// @Router       /orders/available [get]
func (h *Handler) GetAvailableOrders(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	orders, err := h.service.GetAvailableOrders(c.Context(), claims.UserID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar pesanan tersedia berhasil diambil", orders)
}

// PoolStream godoc
// @Summary      Realtime pool stream for runners (SSE)
// @Description  SSE stream delivering new orders nearby runner. Query token via ?token= also supported for EventSource.
// @Tags         [Runner] Order Execution
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        lat  query  number  false  "Runner latitude"
// @Param        lng  query  number  false  "Runner longitude"
// @Param        radius query number false "Radius km (default 15)"
// @Success      200  {string}  string  "SSE Stream"
// @Router       /orders/pool/stream [get]
func (h *Handler) PoolStream(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	runnerID := claims.UserID.String()

	lat, _ := strconv.ParseFloat(c.Query("lat", "0"), 64)
	lng, _ := strconv.ParseFloat(c.Query("lng", "0"), 64)
	radiusKm, _ := strconv.ParseFloat(c.Query("radius", "15"), 64)
	if radiusKm <= 0 || radiusKm > 50 {
		radiusKm = 15
	}

	// If lat/lng not provided via query, try redis stored location — with timeout ctx not Background (P1)
	if lat == 0 && lng == 0 && h.redis != nil {
		hKey := fmt.Sprintf("runner:loc:%s", runnerID)
		if client := h.redis.Client(); client != nil {
			ctxTimeout, cancel := context.WithTimeout(c.Context(), 2*time.Second)
			if vals, err := client.HGetAll(ctxTimeout, hKey).Result(); err == nil && len(vals) > 0 {
				if v, ok := vals["lat"]; ok {
					lat, _ = strconv.ParseFloat(v, 64)
				}
				if v, ok := vals["lng"]; ok {
					lng, _ = strconv.ParseFloat(v, 64)
				}
			}
			cancel()
		}
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("X-Accel-Buffering", "no")

	// Increment connection counter with timeout ctx (was Background)
	if h.redis != nil {
		ctxTimeout, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		_, _ = h.redis.IncrCounter(ctxTimeout, "sse:connections")
		cancel()
	}

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		clientID := fmt.Sprintf("runner:%s:%d", runnerID, time.Now().UnixNano())

		// Subscribe cells
		var cellKeys []string
		if lat != 0 && lng != 0 {
			cellKeys = realtime.NeighborCells(lat, lng)
		} else {
			// fallback: single generic cell if no location
			cellKeys = []string{"global"}
		}

		sseClient := &realtime.SSEClient{
			ID:       clientID,
			CellKeys: cellKeys,
			Lat:      lat,
			Lng:      lng,
			RadiusKm: radiusKm,
			Ch:       make(chan realtime.PoolEvent, 64),
			Done:     make(chan struct{}),
		}

		h.poolHub.RegisterSSE(sseClient)
		defer func() {
			h.poolHub.UnregisterSSE(clientID)
			if h.redis != nil {
				if cli := h.redis.Client(); cli != nil {
					ctxTimeout, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_, _ = cli.Decr(ctxTimeout, "pool:counter:sse:connections").Result()
					cancel()
				}
			}
		}()

		// Immediate heartbeat
		_ = realtime.WriteSSEHeartbeat(w)

		// On connect, send initial available orders snapshot as event?
		// To keep payload small, frontend should still call GET /orders/available once
		// We send just connected info
		_ = realtime.WriteSSEEvent(w, realtime.PoolEvent{
			Type:      "connected",
			Timestamp: time.Now().UnixMilli(),
			Data:      map[string]interface{}{"cells": cellKeys, "radius": radiusKm},
		})

		heartbeatTicker := time.NewTicker(20 * time.Second)
		defer heartbeatTicker.Stop()

		for {
			select {
			case <-sseClient.Done:
				return
			case <-heartbeatTicker.C:
				if err := realtime.WriteSSEHeartbeat(w); err != nil {
					return
				}
			case ev := <-sseClient.Ch:
				if err := realtime.WriteSSEEvent(w, ev); err != nil {
					return
				}
			}
		}
	})

	return nil
}

// MerchantPoolStream godoc
// @Summary      Realtime pool stream for merchants (SSE)
// @Description  SSE stream for merchant incoming orders
// @Tags         [Runner] Order Execution
// @Produce      text/event-stream
// @Security     BearerAuth
// @Success      200  {string}  string  "SSE Stream"
// @Router       /orders/merchant/stream [get]
func (h *Handler) MerchantPoolStream(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	userID := claims.UserID.String()

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")
	c.Set("X-Accel-Buffering", "no")

	if h.redis != nil {
		ctxTimeout, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		_, _ = h.redis.IncrCounter(ctxTimeout, "sse:connections")
		cancel()
	}

	// Capture request context for use inside StreamWriter goroutine (P0 fix: avoid Background in SSE)
	reqCtx := c.Context()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		clientID := fmt.Sprintf("merchant_user:%s:%d", userID, time.Now().UnixNano())

		cellKeys := []string{"merchant:global", "merchant:user:" + userID}

		// Expand to actual merchant IDs — use timeout ctx (was Background)
		if h.service != nil {
			ctxTimeout, cancel := context.WithTimeout(reqCtx, 3*time.Second)
			if merchants, err := h.service.GetMerchantOrders(ctxTimeout, claims.UserID); err == nil {
				seen := map[string]bool{}
				for _, o := range merchants {
					if o.MerchantID != nil {
						k := "merchant:" + o.MerchantID.String()
						if !seen[k] {
							seen[k] = true
							cellKeys = append(cellKeys, k)
						}
					}
				}
			}
			cancel()
		}

		sseClient := &realtime.SSEClient{
			ID:       clientID,
			CellKeys: cellKeys,
			Ch:       make(chan realtime.PoolEvent, 64),
			Done:     make(chan struct{}),
		}

		h.poolHub.RegisterSSE(sseClient)
		defer func() {
			h.poolHub.UnregisterSSE(clientID)
			if h.redis != nil {
				if cli := h.redis.Client(); cli != nil {
					ctxTimeout, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_, _ = cli.Decr(ctxTimeout, "pool:counter:sse:connections").Result()
					cancel()
				}
			}
		}()

		_ = realtime.WriteSSEHeartbeat(w)
		_ = realtime.WriteSSEEvent(w, realtime.PoolEvent{
			Type:      "connected",
			Timestamp: time.Now().UnixMilli(),
			Data:      map[string]interface{}{"cells": cellKeys},
		})

		heartbeatTicker := time.NewTicker(20 * time.Second)
		defer heartbeatTicker.Stop()

		for {
			select {
			case <-sseClient.Done:
				return
			case <-heartbeatTicker.C:
				if err := realtime.WriteSSEHeartbeat(w); err != nil {
					return
				}
			case ev := <-sseClient.Ch:
				if err := realtime.WriteSSEEvent(w, ev); err != nil {
					return
				}
			}
		}
	})

	return nil
}

// Track godoc
// @Summary      Live tracking stream (SSE)
// @Description  SSE stream for runner location, status, and ETA. Shared with order participants.
// @Tags         [Shared] Communications & Tracking
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200  {string}  string  "SSE Stream"
// @Router       /orders/{id}/track [get]
func (h *Handler) Track(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	order, err := h.service.GetByID(c.Context(), id, claims.UserID, claims.Role)
	if err != nil {
		return response.NotFound(c, "pesanan tidak ditemukan")
	}

	isAuthorized := claims.Role == user.RoleAdmin ||
		order.RequesterID == claims.UserID ||
		(order.RunnerID != nil && *order.RunnerID == claims.UserID)

	if !isAuthorized && claims.Role == user.RoleMerchant && order.MerchantID != nil {
		exists, err := h.db.NewSelect().
			Table("merchants").
			Where("id = ? AND owner_id = ?", order.MerchantID, claims.UserID).
			Exists(c.Context())
		if err == nil && exists {
			isAuthorized = true
		}
	}

	if !isAuthorized {
		return response.Forbidden(c, "anda tidak memiliki akses untuk melacak pesanan ini")
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")

	// P0 #5 FIX: Use detached ctx with timeout + check client disconnect via w flush error + done channel via context cancel
	// Fiber's BodyStreamWriter runs in separate goroutine — we must respect request context cancellation
	reqCtx := c.Context()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Use request context for DB calls, not Background() — prevents leak + allows cancel propagation
		ctx := reqCtx

		for {
			// Check if client disconnected (Fiber ctx Done)
			select {
			case <-ctx.Done():
				return
			default:
			}

			state, err := h.service.GetTrackingState(ctx, id)
			if err != nil {
				// Log via logger not fmt.Printf to avoid docker log fill (P1)
				// Silently return — client will reconnect via EventSource
				return
			}

			msg := "data: {\"lat\": " + strconv.FormatFloat(state.Lat, 'f', 6, 64) +
				", \"lng\": " + strconv.FormatFloat(state.Lng, 'f', 6, 64) +
				", \"distance\": " + strconv.FormatFloat(state.Distance, 'f', 2, 64) +
				", \"eta\": " + strconv.Itoa(state.ETA) +
				", \"status\": \"" + state.Status + "\"" +
				", \"visible\": " + strconv.FormatBool(state.Visible) + "}\n\n"

			if _, err := w.WriteString(msg); err != nil {
				return
			}

			if err := w.Flush(); err != nil {
				return
			}

			// Sleep with context check to allow fast exit on disconnect
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	})

	return nil
}

type AdjustmentRequest struct {
	AdjustedCost float64 `json:"adjusted_cost" validate:"required,gt=0"`
	Reason       string  `json:"reason" validate:"required"`
}

// AdjustPrice godoc
// @Summary      Request a price adjustment
// @Description  Runner requests a higher price for an order due to store prices
// @Tags         [Runner] Order Execution
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string             true  "Order UUID"  Format(uuid)
// @Param        body  body      AdjustmentRequest  true  "Adjustment payload"
// @Success      200   {object}  response.envelope
// @Router       /orders/{id}/adjust-price [post]
func (h *Handler) AdjustPrice(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req AdjustmentRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.RequestPriceAdjustment(c.Context(), id, claims.UserID, req.AdjustedCost, req.Reason); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "pengajuan penyesuaian harga berhasil", nil)
}

// ApproveAdjustment godoc
// @Summary      Approve a price adjustment
// @Description  Requester approves the adjusted price. May trigger additional escrow hold.
// @Tags         [User] Order
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {object}  response.envelope
// @Router       /orders/{id}/approve-adjustment [post]
func (h *Handler) ApproveAdjustment(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.ApprovePriceAdjustment(c.Context(), id, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "penyesuaian harga disetujui", nil)
}

type RejectAdjustmentRequest struct {
	CancelOrder bool `json:"cancel_order"`
}

// RejectAdjustment godoc
// @Summary      Reject a price adjustment
// @Description  Requester rejects the adjusted price. Optionally cancels the order.
// @Tags         [User] Order
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string                   true  "Order UUID"  Format(uuid)
// @Param        body  body      RejectAdjustmentRequest  true  "Reject payload"
// @Success      200   {object}  response.envelope
// @Router       /orders/{id}/reject-adjustment [post]
func (h *Handler) RejectAdjustment(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	var req RejectAdjustmentRequest
	_ = c.BodyParser(&req) // Optional body

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}

	if err := h.service.RejectPriceAdjustment(c.Context(), id, claims.UserID, req.CancelOrder); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "penyesuaian harga ditolak", nil)
}

// GetFeeEstimate godoc
// @Summary      Estimate delivery fee
// @Description  Calculate estimated delivery fee based on distance, weight, and volume
// @Tags         [User] Order
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      EstimateFeeRequest  true  "Fee estimation details"
// @Success      200   {object}  response.envelope{data=EstimateFeeResponse}
// @Router       /orders/estimate-fee [post]
func (h *Handler) GetFeeEstimate(c *fiber.Ctx) error {
	var req EstimateFeeRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "format permintaan tidak valid")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	resp, err := h.service.EstimateFee(c.Context(), req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "estimasi biaya berhasil diambil", resp)
}

func (h *Handler) RefreshQRIS(c *fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return response.BadRequest(c, "ID order tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	order, err := h.service.RefreshQRIS(c.Context(), id, claims.UserID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "QRIS berhasil diperbarui", order)
}

func (h *Handler) GetMerchantOrders(c *fiber.Ctx) error {
	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	orders, err := h.service.GetMerchantOrders(c.Context(), claims.UserID)
	if err != nil {
		return response.Success(c, "daftar pesanan merchant kosong", []interface{}{})
	}
	return response.Success(c, "daftar pesanan merchant berhasil diambil", orders)
}

func (h *Handler) MerchantAccept(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	if err := h.service.MerchantAcceptOrder(c.Context(), id, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "pesanan berhasil diterima oleh merchant", nil)
}

func (h *Handler) MerchantReady(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	claims := jwt.GetClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "sesi tidak valid")
	}
	if err := h.service.MerchantReadyOrder(c.Context(), id, claims.UserID); err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "pesanan ditandai siap diambil", nil)
}
