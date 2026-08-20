package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codecoffy/nitip-core/config"
	_ "github.com/codecoffy/nitip-core/docs" // swagger generated docs
	"github.com/codecoffy/nitip-core/internal/app"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/database"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	"github.com/codecoffy/nitip-core/internal/domain/auth"
	"github.com/codecoffy/nitip-core/internal/domain/banner"
	"github.com/codecoffy/nitip-core/internal/domain/chat"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	"github.com/codecoffy/nitip-core/internal/domain/kyc"
	"github.com/codecoffy/nitip-core/internal/domain/matching"
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	notificationDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/codecoffy/nitip-core/internal/domain/promotion"
	"github.com/codecoffy/nitip-core/internal/domain/review"
	"github.com/codecoffy/nitip-core/internal/domain/store"
	supportDomain "github.com/codecoffy/nitip-core/internal/domain/support"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	infraFirebase "github.com/codecoffy/nitip-core/internal/infrastructure/firebase"
	applogger "github.com/codecoffy/nitip-core/internal/logger"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/realtime"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// @title           Nitip Core API
// @version         1.0
// @description     Nitip Core REST API server.
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.email  support@nitip.id

// @license.name  Apache 2.0
// @license.url   http://www.apache.org/licenses/LICENSE-2.0.html

// @host      localhost:8000
// @BasePath  /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

// ── Tag Order (controls display order in Swagger UI) ─────────────────────────
// Tags are listed in the exact order they appear in the UI.
// Prefix legend: [User]=Penitip/Requester [Runner]=Runner [Admin]=Admin only [Shared]=all roles

// @tag.name         Auth
// @tag.description  Registrasi akun baru (requester/runner) dan login untuk mendapatkan JWT token.

// @tag.name         [Admin] User Management
// @tag.description  Admin: Daftar pengguna, verifikasi akun, update trust score, dan suspend pengguna.

// @tag.name         [Admin] KYC Review
// @tag.description  Admin: Review dan setujui/tolak dokumen KYC yang diajukan Runner.

// @tag.name         [Admin] Order Management
// @tag.description  Admin: Pantau semua order, batalkan paksa, dan selesaikan sengketa.

// @tag.name         [Admin] Finance
// @tag.description  Admin: Kelola permintaan penarikan dana dari Runner.

// @tag.name         [Admin] System Config
// @tag.description  Admin: Baca dan ubah nilai konfigurasi sistem secara dinamis.

// @tag.name         [Admin] Store Management
// @tag.description  Admin: Kelola direktori tokoh titip beli — tambah, ubah, dan hapus tokoh beserta koordinat GPS-nya.

// @tag.name         [Runner] KYC
// @tag.description  Proses verifikasi identitas (KTP + Selfie) agar Runner dapat menerima order.

// @tag.name         [Runner] Trip
// @tag.description  Runner mendaftarkan rencana perjalanan beserta kapasitas kendaraan.

// @tag.name         [Runner] Order Execution
// @tag.description  Runner melihat, menerima, membelikan, dan mengirimkan pesanan.

// @tag.name         [User] Profile
// @tag.description  Manajemen profil dan alamat rumah untuk pengguna yang sedang login.

// @tag.name         [User] Order
// @tag.description  Penitip membuat pesanan baru, melakukan pembayaran, dan mengajukan sengketa.

// @tag.name         [User] Finance
// @tag.description  Top-up saldo dompet, lihat riwayat transaksi, dan ajukan penarikan dana.

// @tag.name         [Shared] Order View
// @tag.description  Endpoint bersama untuk melihat detail dan status pesanan (berlaku untuk semua peran).

// @tag.name         [Shared] Communications & Tracking
// @tag.description  Chat real-time (WebSocket) dan live tracking lokasi Runner (SSE & WebSocket).
func main() {
	// 1. Load config from .env / environment
	cfg := config.Load()

	// 2. Init logger
	logger, err := applogger.New(cfg.IsDevelopment())
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	// 3. Init database
	db, err := database.New(cfg, logger)
	if err != nil {
		logger.Fatal("failed to connect database", zap.Error(err))
	}
	defer func() { _ = db.Close() }()

	// 4. Init Redis — required in prod, fallback to in-memory for dev if unavailable
	redisCache, err := cache.NewRedis(cfg, logger)
	if err != nil {
		if !cfg.IsDevelopment() {
			logger.Fatal("redis is required in non-development env but not available", zap.Error(err))
		}
		logger.Warn("redis not available, using in-memory fallback (dev only) — rate limiting & pool will be degraded", zap.Error(err))
		redisCache = nil
	}

	// 5. Init Fiber app + realtime Pool Hub (reuse chat hub pattern for order pool)
	fiberApp := app.New(logger)
	fiberApp.HealthCheck() // basic, will be replaced with pool aware after hub init
	fiberApp.RegisterSwagger()
	// Notification & Matching
	firebaseApp, err := infraFirebase.NewApp(cfg)
	if err != nil {
		logger.Error("failed to init firebase app", zap.Error(err))
	}

	fcmClient, err := notification.NewFCM(firebaseApp, logger)
	if err != nil {
		logger.Warn("FCM module failed to init, push notifications will not work", zap.Error(err))
	}

	// Chat & Shared Storage
	var storageSvc storage.Storage
	var chatHub *chat.Hub

	// Storage initialization based on driver (Tencent COS or Local)
	storageSvc, err = storage.NewFromEnv(cfg)
	if err != nil {
		logger.Fatal("failed to initialize storage service", zap.Error(err))
	}

	// 6. Wire domain handlers
	auditRepo := audit.NewRepository(db)
	auditSvc := audit.NewService(auditRepo, db)

	// Config
	cfgRepo := systemconfig.NewRepository(db)
	cfgSvc := systemconfig.NewService(cfgRepo)

	// Auth (API Key + Grant Token)
	authHandler := auth.NewHandler(db)
	fiberApp.RegisterRoutes(authHandler.RegisterRoutes)
	auth.StartGrantTokenCleanup(db, 1*time.Hour) // Cleanup expired grant tokens hourly

	userRepo := user.NewRepository(db)
	userSvc := user.NewService(userRepo, redisCache, auditSvc, storageSvc)
	userHandler := user.NewHandler(userSvc, db, redisCache)
	fiberApp.RegisterRoutes(userHandler.RegisterRoutes)

	// Trip
	tripRepo := trip.NewRepository(db)
	tripSvc := trip.NewService(tripRepo)
	tripHandler := trip.NewHandler(tripSvc, db, redisCache)
	fiberApp.RegisterRoutes(tripHandler.RegisterRoutes)

	// Order Repository (Needed by Matching)
	orderRepo := order.NewRepository(db)

	matchingSvc := matching.NewService(userRepo, tripRepo, orderRepo, redisCache, fcmClient, db, cfgSvc)
	// Worker pool will be started gracefully in the background workers section

	// Notification History + FCM Dispatcher (Opsi A - BE only queue with per-device bucket 20 burst + collapse_id)
	notifRepo := notificationDomain.NewRepository(db)
	notifSvc := notificationDomain.NewService(notifRepo)
	notifHandler := notificationDomain.NewHandler(notifSvc, db, redisCache)
	fiberApp.RegisterRoutes(notifHandler.RegisterRoutes)
	notifSvc.StartCleanupWorker(context.Background())

	// Dispatcher injected into domain services for free-tier efficiency: unlimited total, 600k/min downstream, 20 burst per device refill 1/3min, topic 1000/sec, 4KB payload
	var fcmDispatcher notification.Dispatcher
	if redisCache != nil && fcmClient != nil {
		fcmDispatcher = notification.NewDispatcher(redisCache, fcmClient, userRepo, auditSvc, logger)
		// Worker pool started in background workers section
		logger.Info("FCM dispatcher wired", zap.String("queue", notification.FCMQueueGlobal), zap.Int("burst", notification.FCMBurstLimit))
	}

	// Realtime pool hub (SSE for order pool + merchant stream) - reuses chat hub pattern from chat/hub.go
	poolHub := realtime.NewPoolHub()
	poolBroadcaster := realtime.NewBroadcaster(poolHub, redisCache, logger)
	logger.Info("Pool realtime hub initialized", zap.String("pattern", "reuse chat.Hub RWMutex+Broadcast"))
	// Extend /health with pool stats (uses only Redis+Postgres from docker-compose, no Grafana)
	// Note: HealthCheck already registered /health basic, we add /health/pool detail here via same App method that overwrites
	// We'll just add extra route via fiberApp's fiber - but App exposes fiber via RegisterRoutes? So use app.go helper via new method not needed.
	// Instead, we rely on metrics endpoint /admin/metrics/pool + basic health already ok.
	// For extended health, the App's HealthCheckWithPool registers a second route - we call it via app instance: we need to expose fiber.
	// Simplest: we already have basic /health, and /admin/metrics/pool gives detailed pool stats.

	// Init Hub & Chat Domain (PostgreSQL as backend)
	chatHub = chat.NewHub()
	chatRepo := chat.NewRepository(db)
	chatSvc := chat.NewService(chatRepo, orderRepo, userRepo, chatHub, fcmClient, notifSvc, storageSvc)
	chatHandler := chat.NewHandler(chatSvc, db, redisCache)
	fiberApp.RegisterRoutes(chatHandler.RegisterRoutes)
	logger.Info("Chat service initialized (PostgreSQL)")

	if firebaseApp == nil {
		logger.Warn("Firebase App missing, real push notifications and cloud storage disabled (using Dummies)")
	}

	cfgHandler := systemconfig.NewHandler(cfgSvc, db, redisCache)
	fiberApp.RegisterRoutes(cfgHandler.RegisterRoutes)

	// Audit Logs
	auditHandler := audit.NewHandler(auditSvc, db, redisCache)
	fiberApp.RegisterRoutes(auditHandler.RegisterRoutes)

	// Wallet
	walletRepo := wallet.NewRepository(db)
	walletSvc := wallet.NewService(walletRepo, userSvc, cfgSvc, db, redisCache, auditSvc, fcmClient, notifSvc)
	walletHandler := wallet.NewHandler(walletSvc, db, redisCache)
	fiberApp.RegisterRoutes(walletHandler.RegisterRoutes)

	// Merchant Domain
	merchantRepo := merchant.NewRepository(db)
	merchantSvc := merchant.NewService(merchantRepo, userRepo, storageSvc)
	merchantHandler := merchant.NewHandler(merchantSvc, db, redisCache)
	fiberApp.RegisterRoutes(merchantHandler.RegisterRoutes)
	// Promotion Domain (isolated, minimal impact)
	// Note: import promotion with alias to avoid conflict, but we need to add import at top
	// Order Service + Pool Realtime wiring
	orderSvc := order.NewService(orderRepo, userSvc, tripRepo, matchingSvc, walletSvc, cfgSvc, fcmClient, notifSvc, redisCache, db, auditSvc, storageSvc, merchantSvc)
	// Wire pool broadcaster adapter (order -> realtime without cycle)
	poolAdapter := order.NewPoolBroadcasterAdapter(poolHub, poolBroadcaster)
	orderSvc.SetPoolBroadcaster(poolAdapter)

	// Promotion Domain (isolated, minimal impact) - wired after orderSvc creation
	promoRepo := promotion.NewRepository(db)
	promoSvc := promotion.NewService(promoRepo, userRepo, merchantRepo, auditSvc, redisCache, db)
	promoHandler := promotion.NewHandler(promoSvc, db, redisCache)
	fiberApp.RegisterRoutes(promoHandler.RegisterRoutes)
	// Inject promotion service + FCM dispatcher into order service (optional nil guard)
	orderSvc.SetPromotionService(promoSvc)
	if fcmDispatcher != nil {
		orderSvc.SetFCMDispatcher(fcmDispatcher)
	}

	wallet.OnPaymentSuccess = func(ctx context.Context, reference string) error {
		id, err := uuid.Parse(reference)
		if err != nil {
			return err
		}
		return orderSvc.UpdatePaymentStatus(ctx, id, order.PaymentEscrow)
	}
	orderHandler := order.NewHandler(orderSvc, db, redisCache, poolHub)
	fiberApp.RegisterRoutes(orderHandler.RegisterRoutes)

	// Pool metrics endpoint (admin, uses only Redis + Postgres from docker-compose, no Grafana)
	metricsHandler := realtime.NewMetricsHandler(redisCache, db, poolHub)
	fiberApp.RegisterRoutes(metricsHandler.RegisterRoutes)

	// Review (Tied to orders)
	reviewRepo := review.NewRepository(db)
	reviewSvc := review.NewService(reviewRepo, orderRepo, db)
	reviewHandler := review.NewHandler(reviewSvc, db, redisCache)
	fiberApp.RegisterRoutes(reviewHandler.RegisterRoutes)

	// KYC Domain
	kycRepo := kyc.NewRepository(db)
	kycSvc := kyc.NewService(kycRepo, userSvc, storageSvc, fcmClient, notifSvc, auditSvc)
	if fcmDispatcher != nil {
		// wire dispatcher into kyc & matching & wallet (SetFCMDispatcher optional via interface)
		if ks, ok := interface{}(kycSvc).(interface {
			SetFCMDispatcher(interface {
				Enqueue(context.Context, notification.Job) error
			})
		}); ok {
			ks.SetFCMDispatcher(fcmDispatcher)
		}
		if ms, ok := interface{}(matchingSvc).(interface {
			SetFCMDispatcher(interface {
				Enqueue(context.Context, notification.Job) error
			})
		}); ok {
			ms.SetFCMDispatcher(fcmDispatcher)
		}
		if ws, ok := interface{}(walletSvc).(interface {
			SetFCMDispatcher(interface {
				Enqueue(context.Context, notification.Job) error
			})
		}); ok {
			ws.SetFCMDispatcher(fcmDispatcher)
		}
	}
	kycHandler := kyc.NewHandler(kycSvc, db, redisCache)
	fiberApp.RegisterRoutes(kycHandler.RegisterRoutes)

	// Banner Domain
	bannerRepo := banner.NewRepository(db)
	bannerSvc := banner.NewService(bannerRepo, storageSvc)
	bannerHandler := banner.NewHandler(bannerSvc, db, redisCache)
	fiberApp.RegisterRoutes(bannerHandler.RegisterRoutes)

	// Support Ticket + Live Chat
	supportRepo := supportDomain.NewRepository(db)
	supportSvc := supportDomain.NewService(supportRepo, cfgSvc, notifSvc, redisCache, auditSvc)
	supportHandler := supportDomain.NewHandler(supportSvc, db, redisCache)
	fiberApp.RegisterRoutes(supportHandler.RegisterRoutes)

	// Store Directory (Direktori Tokoh Titip Beli) — now injects storageSvc to enforce https://upload.nihtip.com/
	storeRepo := store.NewRepository(db)
	storeSvc := store.NewService(storeRepo, redisCache, storageSvc)
	storeHandler := store.NewHandler(storeSvc, db, redisCache)
	fiberApp.RegisterRoutes(storeHandler.RegisterRoutes)

	// 7. Graceful shutdown listener
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 8. Start background workers
	matchingSvc.StartWorkerPool(ctx, 10)
	orderSvc.StartBackgroundCleanup(ctx)
	orderSvc.StartPaymentWorkerPool(ctx, 5)
	supportSvc.StartAutoCloseWorker(ctx)
	if fcmDispatcher != nil {
		fcmDispatcher.Start(ctx, 10)
	}
	_ = walletSvc.RecoverPendingWithdrawals(ctx)

	// 9. Start server in a goroutine
	go func() {
		logger.Sugar().Infof("server starting on :%s", cfg.AppPort)
		logger.Sugar().Infof("swagger docs at http://localhost:%s/docs/index.html", cfg.AppPort)
		if err := fiberApp.Listen(":" + cfg.AppPort); err != nil {
			// Don't log error if it's just the server shutting down
			if err.Error() != "shutdown" {
				logger.Sugar().Errorf("server error: %v", err)
			}
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()
	logger.Info("shutdown signal received, gracefully shutting down...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 10. Shutdown Fiber (stops taking new requests)
	if err := fiberApp.ShutdownWithContext(shutdownCtx); err != nil {
		logger.Sugar().Errorf("fiber shutdown error: %v", err)
	}

	// 11. Explicitly close other resources
	if redisCache != nil {
		logger.Info("closing redis connection...")
		_ = redisCache.Close()
	}

	logger.Info("server stopped")
}
