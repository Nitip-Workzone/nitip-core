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
	// All order routes are protected
	orders := router.Group("/orders", middleware.Protected(h.db, h.redis))

	// Requester routes
	orders.Post("/", middleware.RateLimit(h.redis, 5, 1*time.Minute), middleware.Role(user.RoleRequester), h.Create)
	orders.Post("/estimate-fee", h.GetFeeEstimate)
	orders.Get("/me", middleware.Role(user.RoleRequester, user.RoleRunner), h.GetMyOrders)
	orders.Post("/:id/cancel", middleware.Role(user.RoleRequester), h.Cancel)
	orders.Post("/:id/dispute", middleware.Role(user.RoleRequester), h.Dispute)
	orders.Post("/:id/refresh-qris", middleware.Role(user.RoleRequester), h.RefreshQRIS)

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

	claims := c.Locals("user").(*jwt.CustomClaims)

	order, err := h.service.Create(c.Context(), claims.UserID, req)
	if err != nil {
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
// @Tags         [Shared] Order View
// @Produce      json
// @Security     BearerAuth
// @Success      200   {object}  response.envelope{data=[]Order}
// @Failure      401   {object}  response.envelope
// @Router       /orders/me [get]
func (h *Handler) GetMyOrders(c *fiber.Ctx) error {
	claims := c.Locals("user").(*jwt.CustomClaims)

	// Fetch all orders where the user is either the requester or the runner
	orders, err := h.service.GetByUser(c.Context(), claims.UserID)
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

	claims := c.Locals("user").(*jwt.CustomClaims)
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

	claims := c.Locals("user").(*jwt.CustomClaims)

	if err := h.service.CancelOrder(c.Context(), id, claims.UserID); err != nil {
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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)

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
	if code == "" {
		return response.BadRequest(c, "kode konfirmasi (completion_code) wajib diisi")
	}

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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// MVP: Simple poller masquerading as SSE for simplicity.
		// In production, use Redis Pub/Sub to trigger events instead of polling.
		lastStatus := ""
		for {
			order, err := h.service.GetByID(c.Context(), id, claims.UserID, claims.Role)
			if err != nil {
				return
			}

			if order.Status != lastStatus {
				lastStatus = order.Status
				msg := "data: {\"status\": \"" + order.Status + "\"}\n\n"
				_, writeErr := w.WriteString(msg)
				_ = w.Flush()
				if writeErr != nil {
					return
				}
			}

			if lastStatus == StatusCompleted || lastStatus == StatusCancelled {
				return // End stream
			}

			time.Sleep(3 * time.Second)
		}
	})

	return nil
}

// PayStub godoc
// @Summary      Simulate order payment
// @Description  Stub endpoint to simulate payment Gateway callback setting order to escrow
// @Tags         [Admin] Order Management
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID"  Format(uuid)
// @Success      200   {object}  response.envelope
// @Router       /admin/orders/{id}/pay [post]
func (h *Handler) PayStub(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	// For MVP, literally just call service to update payment status to escrow/paid
	// This would normally be executed by a Midtrans webhook handler
	err = h.service.UpdatePaymentStatus(c.Context(), id, PaymentEscrow)
	if err != nil {
		return response.InternalError(c, "gagal memperbarui pembayaran")
	}

	return response.Success(c, "simulasi pembayaran berhasil", nil)
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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)
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
func (h *Handler) AdminCancelOrder(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "ID pesanan tidak valid")
	}

	if err := h.service.ForceCancelOrder(c.Context(), id); err != nil {
		return response.BadRequest(c, err.Error())
	}

	claims := c.Locals("user").(*jwt.CustomClaims)
	log.Printf("[ADMIN_ACTION] Order %s forcefully cancelled by Admin %s", id, claims.Email)

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
	claims := c.Locals("user").(*jwt.CustomClaims)

	orders, err := h.service.GetAvailableOrders(c.Context(), claims.UserID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}

	return response.Success(c, "daftar pesanan tersedia berhasil diambil", orders)
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

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Transfer-Encoding", "chunked")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ctx := context.Background()
		orderIDStr := id.String()

		for {
			state, err := h.service.GetTrackingState(ctx, id)
			if err != nil {
				fmt.Printf("[SSE_ERROR] Failed to get state for %s: %v\n", orderIDStr, err)
				return
			}
			fmt.Printf("[SSE] Sending update for order %s (status: %s)\n", orderIDStr, state.Status)

			// Format JSON payload
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

			time.Sleep(5 * time.Second)
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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)

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

	claims := c.Locals("user").(*jwt.CustomClaims)
	order, err := h.service.RefreshQRIS(c.Context(), id, claims.UserID)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "QRIS berhasil diperbarui", order)
}
