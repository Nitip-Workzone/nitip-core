package response

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Meta holds pagination information for list responses.
// swagger:model
type Meta struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// ValidationError represents a single field validation error.
// swagger:model
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// envelope is the unified API response wrapper.
// swagger:model
type envelope struct {
	Success bool              `json:"success"`
	Message string            `json:"message"`
	Data    any               `json:"data,omitempty"`
	Meta    *Meta             `json:"meta,omitempty"`
	Errors  []ValidationError `json:"errors,omitempty"`
}

// ── Success responses ────────────────────────────────────

func Success(c *fiber.Ctx, message string, data any) error {
	return c.Status(fiber.StatusOK).JSON(envelope{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func Created(c *fiber.Ctx, message string, data any) error {
	return c.Status(fiber.StatusCreated).JSON(envelope{
		Success: true,
		Message: message,
		Data:    data,
	})
}

func Paginated(c *fiber.Ctx, message string, data any, meta Meta) error {
	return c.Status(fiber.StatusOK).JSON(envelope{
		Success: true,
		Message: message,
		Data:    data,
		Meta:    &meta,
	})
}

func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// ── Error responses ──────────────────────────────────────

func ValidationFailed(c *fiber.Ctx, errs []ValidationError) error {
	return c.Status(fiber.StatusUnprocessableEntity).JSON(envelope{
		Success: false,
		Message: "validation failed",
		Errors:  errs,
	})
}

func BadRequest(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(envelope{
		Success: false,
		Message: message,
	})
}

func Unauthorized(c *fiber.Ctx, message string) error {
	if message == "" {
		message = "unauthorized"
	}
	return c.Status(fiber.StatusUnauthorized).JSON(envelope{
		Success: false,
		Message: message,
	})
}

func Forbidden(c *fiber.Ctx, message string) error {
	if message == "" {
		message = "forbidden"
	}
	return c.Status(fiber.StatusForbidden).JSON(envelope{
		Success: false,
		Message: message,
	})
}

func NotFound(c *fiber.Ctx, message string) error {
	if message == "" {
		message = "resource not found"
	}
	return c.Status(fiber.StatusNotFound).JSON(envelope{
		Success: false,
		Message: message,
	})
}

func InternalError(c *fiber.Ctx, message string) error {
	if message == "" {
		message = "internal server error"
	}

	// Security: Mask database errors in production/response
	lowMsg := strings.ToLower(message)
	if strings.Contains(lowMsg, "sql") ||
		strings.Contains(lowMsg, "unique constraint") ||
		strings.Contains(lowMsg, "foreign key") ||
		strings.Contains(lowMsg, "table") ||
		strings.Contains(lowMsg, "column") ||
		strings.Contains(lowMsg, "relation") {
		message = "terjadi kesalahan internal pada server"
	}

	return c.Status(fiber.StatusInternalServerError).JSON(envelope{
		Success: false,
		Message: message,
	})
}

func Custom(c *fiber.Ctx, code int, message string, data any) error {
	return c.Status(code).JSON(envelope{
		Success: code >= 200 && code < 300,
		Message: message,
		Data:    data,
	})
}
