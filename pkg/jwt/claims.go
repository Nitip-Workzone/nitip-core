package jwt

import (
	"github.com/gofiber/fiber/v2"
)

// GetClaims safely extracts CustomClaims from Fiber locals.
// Returns nil if not present or invalid type — avoids panic from unsafe type assert.
func GetClaims(c *fiber.Ctx) *CustomClaims {
	v := c.Locals("user")
	if v == nil {
		return nil
	}
	claims, ok := v.(*CustomClaims)
	if !ok || claims == nil {
		return nil
	}
	return claims
}

// MustGetClaims returns claims or nil if invalid; helper for legacy handlers that need explicit ok.
func MustGetClaims(c *fiber.Ctx) (*CustomClaims, bool) {
	v := c.Locals("user")
	if v == nil {
		return nil, false
	}
	claims, ok := v.(*CustomClaims)
	if !ok || claims == nil {
		return nil, false
	}
	return claims, true
}
