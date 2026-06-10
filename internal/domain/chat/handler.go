package chat

import (
	"strconv"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/codecoffy/nitip-core/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
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
	// Root group is usually /api/v1 (passed from app.go)
	chat := router.Group("/orders/:id", middleware.Protected(h.db, h.redis))

	chat.Get("/messages", h.GetMessages)
	chat.Post("/messages", h.SendMessage)
	chat.Post("/messages/image", h.SendImage)

	// WebSocket path - Disabled for MVP v2 in favor of WhatsApp redirection
	// router.Get("/orders/:id/ws", middleware.Protected(h.db, h.redis), websocket.New(h.WebSocket))
}

// GetMessages godoc
// @Summary      Get chat history for an order
// @Description  Retrieve last messages for a specific order. Only for participants.
// @Tags         [Shared] Communications & Tracking
// @Produce      json
// @Security     BearerAuth
// @Param        id     path    string  true  "Order UUID" Format(uuid)
// @Param        limit  query   int     false "Limit messages"
// @Success      200    {object} response.envelope{data=[]ChatMessage}
// @Router       /orders/{id}/messages [get]
func (h *Handler) GetMessages(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid order id")
	}

	userClaims := c.Locals("user").(*jwt.CustomClaims)
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	messages, err := h.service.GetHistory(c.Context(), orderID, userClaims.UserID, limit)
	if err != nil {
		if errorsIs(err, ErrUnauthorized) {
			return response.Forbidden(c, err.Error())
		}
		return response.BadRequest(c, err.Error())
	}

	return response.Success(c, "chat history retrieved", messages)
}

// SendMessage godoc
// @Summary      Send a chat message
// @Description  Send a text message to the other participant of the order.
// @Tags         [Shared] Communications & Tracking
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string              true  "Order UUID" Format(uuid)
// @Param        body  body      SendMessageRequest  true  "Message payload"
// @Success      201   {object}  response.envelope{data=ChatMessage}
// @Router       /orders/{id}/messages [post]
func (h *Handler) SendMessage(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid order id")
	}

	userClaims := c.Locals("user").(*jwt.CustomClaims)

	var req SendMessageRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	if errs := validator.Validate(req); errs != nil {
		return response.ValidationFailed(c, errs)
	}

	msg, err := h.service.SendMessage(c.Context(), orderID, userClaims.UserID, req.Content, "text")
	if err != nil {
		if errorsIs(err, ErrUnauthorized) {
			return response.Forbidden(c, err.Error())
		}
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "message sent", msg)
}

// SendImage godoc
// @Summary      Send an image in chat
// @Description  Upload an image and send it as a chat message.
// @Tags         [Shared] Communications & Tracking
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string  true  "Order UUID" Format(uuid)
// @Param        image formData  file    true  "Image file"
// @Success      201   {object}  response.envelope{data=ChatMessage}
// @Router       /orders/{id}/messages/image [post]
func (h *Handler) SendImage(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid order id")
	}

	userClaims := c.Locals("user").(*jwt.CustomClaims)

	file, err := c.FormFile("image")
	if err != nil {
		return response.BadRequest(c, "image file is required")
	}
	if file.Size > 5*1024*1024 {
		return response.BadRequest(c, "image too large (max 5MB)")
	}

	if !fileutil.IsImage(file) {
		return response.BadRequest(c, "file must be an image (jpg, jpeg, png)")
	}

	f, err := file.Open()
	if err != nil {
		return response.InternalError(c, "failed to open file")
	}
	defer func() { _ = f.Close() }()

	imageUrl, err := h.service.UploadImage(c.Context(), orderID, userClaims.UserID, file.Filename, f)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	msg, err := h.service.SendMessage(c.Context(), orderID, userClaims.UserID, imageUrl, "image")
	if err != nil {
		return response.BadRequest(c, err.Error())
	}

	return response.Created(c, "image sent", msg)
}

// WebSocket godoc
// @Summary      [DISABLED] Real-time chat (WebSocket)
// @Description  [DISABLED FOR MVP V2] WebSocket endpoint for real-time messaging between participants of an order. Use WhatsApp redirection instead.
// @Tags         [Shared] Communications & Tracking
// @Security     BearerAuth
// @Param        id   path  string  true  "Order UUID" Format(uuid)
// @Router       /orders/{id}/ws [get]
func (h *Handler) WebSocket(c *websocket.Conn) {
	orderID := c.Params("id")
	userClaims := c.Locals("user").(*jwt.CustomClaims)

	// Register client to Hub via Service
	client := &Client{
		UserID: userClaims.UserID,
		Conn:   c,
	}
	h.service.RegisterClient(orderID, client)

	defer func() {
		h.service.UnregisterClient(orderID, userClaims.UserID)
		_ = c.Close()
	}()

	// 2. Keep connection alive with Heartbeat (Ping/Pong)
	c.SetReadLimit(4096)

	// Send pings periodically to keep the connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}()

	for {
		_, _, err := c.ReadMessage()
		if err != nil {
			break
		}
	}
}

// Helper for errors.Is because I can't import "errors" in a clean way without checking it's in the same package
func errorsIs(err, target error) bool {
	return err.Error() == target.Error()
}
