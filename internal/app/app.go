package app

import (
	"context"
	"log"
	"os"
	"time"

	_ "github.com/codecoffy/nitip-core/docs" // swagger generated docs
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberRecover "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	fiberSwagger "github.com/gofiber/swagger"
	"go.uber.org/zap"
)

type App struct {
	fiber  *fiber.App
	logger *zap.Logger
}

func New(logger *zap.Logger) *App {
	// P0 #8 Fix: Read/Write timeout 5m -> 30s to prevent slow-client DoS holding workers
	// SSE routes (/pool/stream, /track) will override with longer timeout via group if needed.
	f := fiber.New(fiber.Config{
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		BodyLimit:    20 * 1024 * 1024, // 20 MB to allow high-res mobile photos
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

	// 4. Request / Response Logger — P1: sampling only slow >=500ms or error >=400 to avoid GC pressure & log fill
	f.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		elapsed := time.Since(start)
		status := c.Response().StatusCode()

		// Sample: log only slow requests or errors (reduces log volume 90% + avoids PII body dump)
		shouldLog := elapsed >= 500*time.Millisecond || status >= 400
		// In non-prod, log also 2xx but without body dump to keep visibility low cost
		isProd := os.Getenv("APP_ENV") == "production"
		if !isProd && status < 400 && elapsed < 500*time.Millisecond {
			// Skip successful fast requests in dev to reduce noise (optional: log only path+status)
			if elapsed < 100*time.Millisecond {
				return err
			}
			shouldLog = true
		}

		if !shouldLog {
			return err
		}

		method := c.Method()
		path := c.Path()
		// Avoid body dump for PII & GC — only log size & status+latency
		reqSize := len(c.Body())
		respSize := len(c.Response().Body())
		if reqSize > 0 {
			log.Printf("[API] %s %s -> %d (%s) req=%dB resp=%dB", method, path, status, elapsed.Round(time.Millisecond), reqSize, respSize)
		} else {
			log.Printf("[API] %s %s -> %d (%s) resp=%dB", method, path, status, elapsed.Round(time.Millisecond), respSize)
		}
		return err
	})

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

// PoolStatsProvider interface to avoid import cycle (realtime -> app)
type PoolStatsProvider interface {
	ConnectionCount() int64
	TotalEvents() int64
	ClaimConflicts() int64
}

type RedisStatsProvider interface {
	GetAllPoolCounters(ctx context.Context) (map[string]int64, error)
}

func (a *App) HealthCheckWithPool(poolHub PoolStatsProvider, redisProvider RedisStatsProvider) {
	a.fiber.Get("/health", func(c *fiber.Ctx) error {
		resp := fiber.Map{
			"status":    "ok",
			"service":   "nitip-core",
			"timestamp": time.Now().Format(time.RFC3339),
		}
		if poolHub != nil {
			resp["sse_connections"] = poolHub.ConnectionCount()
			resp["sse_total_events"] = poolHub.TotalEvents()
			resp["claim_conflicts"] = poolHub.ClaimConflicts()
		}
		if redisProvider != nil {
			if counters, err := redisProvider.GetAllPoolCounters(c.Context()); err == nil {
				resp["pool_counters"] = counters
			}
			resp["redis_up"] = true
		} else {
			resp["redis_up"] = false
		}
		return c.JSON(resp)
	})

	a.fiber.Get("/favicon.ico", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})
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
