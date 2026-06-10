package app

import (
	"context"
	"os"
	"time"

	_ "github.com/codecoffy/nitip-core/docs" // swagger generated docs
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/gofiber/contrib/fiberzap/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberRecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	fiberSwagger "github.com/gofiber/swagger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type App struct {
	fiber  *fiber.App
	logger *zap.Logger
}

func New(logger *zap.Logger) *App {
	// Tighter body limit for API (2 MB), larger for file uploads handled separately
	f := fiber.New(fiber.Config{
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		BodyLimit:    2 * 1024 * 1024, // 2 MB default
		ErrorHandler: middleware.ErrorHandler,
	})

	// ── Middleware Stack ──────────────────────────────────
	// 1. Request ID
	f.Use(requestid.New())

	// 2. Recover — catch panics
	f.Use(fiberRecover.New(fiberRecover.Config{
		EnableStackTrace: true,
	}))

	// 3. CORS
	allowedOrigins := os.Getenv("CORS_ALLOWED_ORIGINS")
	if allowedOrigins == "" {
		if os.Getenv("APP_ENV") == "production" {
			allowedOrigins = "https://nitip.id,https://admin.nitip.id"
		} else {
			allowedOrigins = "*"
		}
	}
	f.Use(cors.New(cors.Config{
		AllowOrigins: allowedOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Request-ID, X-API-Key, X-Timestamp, X-Signature, X-Grant-Token, X-Platform, X-Location",
		AllowMethods: "GET, POST, PUT, PATCH, DELETE, OPTIONS",
	}))

	// 4. Request / Response Logger
	f.Use(fiberzap.New(fiberzap.Config{
		Logger: logger,
		Fields: []string{"latency", "status", "method", "url", "requestId", "ip"},
		Levels: []zapcore.Level{
			zapcore.DebugLevel,
			zapcore.InfoLevel,
			zapcore.InfoLevel,
			zapcore.DebugLevel,
			zapcore.ErrorLevel,
		},
	}))

	// 5. Security Headers
	isProd := os.Getenv("APP_ENV") == "production"
	f.Use(func(c *fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-XSS-Protection", "1; mode=block")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if isProd {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		return c.Next()
	})

	// 6. Serve uploaded files statically for local development
	f.Static("/uploads", "./uploads")

	return &App{fiber: f, logger: logger}
}

// HealthCheck registers GET /health
// @Summary      Health check
// @Description  Get service health status
// @Tags         System
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /health [get]
func (a *App) HealthCheck() {
	a.fiber.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":    "ok",
			"service":   "nitip-core",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// Suppress favicon 404 noise
	a.fiber.Get("/favicon.ico", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	// Root redirect to docs
	a.fiber.Get("/", func(c *fiber.Ctx) error {
		return c.Redirect("/docs/index.html", fiber.StatusMovedPermanently)
	})
}

// RegisterSwagger mounts the Swagger UI at /docs (only in non-production)
func (a *App) RegisterSwagger() {
	if os.Getenv("APP_ENV") == "production" {
		return // Swagger disabled in production
	}
	a.fiber.Get("/docs/*", fiberSwagger.New(fiberSwagger.Config{
		Title:                "Nitip Core API Docs",
		TagsSorter:           "(a,b) => 0",
		TryItOutEnabled:      true,
		PersistAuthorization: true,
		DocExpansion:         "list",
	}))
}

// RegisterRoutes wires all domain routes under /api/v1
func (a *App) RegisterRoutes(routers ...func(fiber.Router)) {
	api := a.fiber.Group("/api/v1")

	for _, r := range routers {
		r(api)
	}
}

// Listen starts the Fiber HTTP server
func (a *App) Listen(addr string) error {
	return a.fiber.Listen(addr)
}

// Shutdown gracefully shuts down the app
func (a *App) Shutdown() error {
	return a.fiber.Shutdown()
}

// ShutdownWithContext gracefully shuts down the app with context timeout support
func (a *App) ShutdownWithContext(ctx context.Context) error {
	return a.fiber.ShutdownWithContext(ctx)
}
