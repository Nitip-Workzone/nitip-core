package middleware

import (
	"github.com/gofiber/fiber/v2"
)

// ErrorHandler is the global fiber error handler that returns consistent JSON errors.
func ErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "terjadi kesalahan pada server"

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}

	return c.Status(code).JSON(fiber.Map{
		"success": false,
		"message": message,
	})
}
