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
	"github.com/codecoffy/nitip-core/internal/domain/review"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	infraFirebase "github.com/codecoffy/nitip-core/internal/infrastructure/firebase"
	"github.com/codecoffy/nitip-core/internal/storage"
	applogger "github.com/codecoffy/nitip-core/internal/logger"
	"github.com/codecoffy/nitip-core/internal/notification"
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

	// 4. Init Redis (optional — comment out if not needed)
	redisCache, err := cache.NewRedis(cfg, logger)
	if err != nil {
		logger.Warn("redis not available, skipping", zap.Error(err))
	}

	// 5. Init Fiber app
	fiberApp := app.New(logger)
	fiberApp.HealthCheck()
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

	matchingSvc := matching.NewService(userRepo, tripRepo, orderRepo, redisCache, fcmClient)
	matchingSvc.StartWorkerPool(context.Background(), 10) // Start background matching workers

	// Notification History
	notifRepo := notificationDomain.NewRepository(db)
	notifSvc := notificationDomain.NewService(notifRepo)
	notifHandler := notificationDomain.NewHandler(notifSvc, db, redisCache)
	fiberApp.RegisterRoutes(notifHandler.RegisterRoutes)

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

	// Config
	cfgRepo := systemconfig.NewRepository(db)
	cfgSvc := systemconfig.NewService(cfgRepo)
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

	// Order Service
	orderSvc := order.NewService(orderRepo, userSvc, tripRepo, matchingSvc, walletSvc, cfgSvc, fcmClient, notifSvc, redisCache, db, auditSvc, storageSvc, merchantSvc)
	wallet.OnPaymentSuccess = func(ctx context.Context, reference string) error {
		id, err := uuid.Parse(reference)
		if err != nil {
			return err
		}
		return orderSvc.UpdatePaymentStatus(ctx, id, order.PaymentEscrow)
	}
	orderHandler := order.NewHandler(orderSvc, db, redisCache)
	fiberApp.RegisterRoutes(orderHandler.RegisterRoutes)

	// Review (Tied to orders)
	reviewRepo := review.NewRepository(db)
	reviewSvc := review.NewService(reviewRepo, orderRepo, db)
	reviewHandler := review.NewHandler(reviewSvc, db, redisCache)
	fiberApp.RegisterRoutes(reviewHandler.RegisterRoutes)

	// KYC Domain
	kycRepo := kyc.NewRepository(db)
	kycSvc := kyc.NewService(kycRepo, userSvc, storageSvc, fcmClient, notifSvc, auditSvc)
	kycHandler := kyc.NewHandler(kycSvc, db, redisCache)
	fiberApp.RegisterRoutes(kycHandler.RegisterRoutes)

	// Banner Domain
	bannerRepo := banner.NewRepository(db)
	bannerSvc := banner.NewService(bannerRepo, storageSvc)
	bannerHandler := banner.NewHandler(bannerSvc, db, redisCache)
	fiberApp.RegisterRoutes(bannerHandler.RegisterRoutes)

	// 7. Graceful shutdown listener
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 8. Start background workers
	matchingSvc.StartWorkerPool(ctx, 10)
	orderSvc.StartBackgroundCleanup(ctx)
	orderSvc.StartPaymentWorkerPool(ctx, 5)
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
