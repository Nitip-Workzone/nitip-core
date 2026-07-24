package order

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/codecoffy/nitip-core/pkg/geo"
	"github.com/google/uuid"
	"github.com/midtrans/midtrans-go"
	"github.com/midtrans/midtrans-go/coreapi"
	"github.com/uptrace/bun"
)

type PaymentJob struct {
	OrderID uuid.UUID
	Status  string
	ErrChan chan error
}

const (
	TypeInstant = "instant"
	TypeRegular = "regular"
)

type Matcher interface {
	FindNearestRunners(ctx context.Context, lat, lng float64, radiusKm float64) ([]user.User, error)
	DispatchOrder(ctx context.Context, orderID string, runners []user.User) error
	EnqueueMatching(orderID uuid.UUID)
}

type CreateOrderRequest struct {
	ItemDetails   string  `json:"item_details"   validate:"required"`
	PickupLat     float64 `json:"pickup_lat"     validate:"required"`
	PickupLng     float64 `json:"pickup_lng"     validate:"required"`
	PickupName    string  `json:"pickup_name"`
	PickupAddress string  `json:"pickup_address"`
	DeliveryLat   float64 `json:"delivery_lat"   validate:"required"`
	DeliveryLng   float64 `json:"delivery_lng"   validate:"required"`
	EstimatedCost float64 `json:"estimated_cost" validate:"min=0"`
	PaymentMethod string  `json:"payment_method" validate:"required,oneof=escrow cod"`
	PaymentSource string  `json:"payment_source" validate:"omitempty,oneof=wallet qris"`
	WeightKg      float64 `json:"weight_kg"      validate:"required,min=0"`
	VolumeLiters  float64 `json:"volume_liters"  validate:"required,min=0"` // Frontend maps S/M/L to liters

	// Merchant Fields
	MerchantID *uuid.UUID `json:"merchant_id,omitempty"`
	Items      []struct {
		MenuID   uuid.UUID `json:"menu_id" validate:"required"`
		Quantity int       `json:"quantity" validate:"required,gt=0"`
		Notes    string    `json:"notes,omitempty"`
	} `json:"items,omitempty"`

	// Nitip Kirim Fields
	ServiceCategory string `json:"service_category" validate:"required,oneof=beli kirim"`
	ReceiverName    string `json:"receiver_name"`
	ReceiverPhone   string `json:"receiver_phone"`
	DeliveryName    string `json:"delivery_name"`
	DeliveryAddress string `json:"delivery_address"`
}

type EstimateFeeRequest struct {
	PickupLat    float64 `json:"pickup_lat"     validate:"required"`
	PickupLng    float64 `json:"pickup_lng"     validate:"required"`
	DeliveryLat  float64 `json:"delivery_lat"   validate:"required"`
	DeliveryLng  float64 `json:"delivery_lng"   validate:"required"`
	WeightKg     float64 `json:"weight_kg"      validate:"required,min=0"`
	VolumeLiters float64 `json:"volume_liters"  validate:"required,min=0"`
}

type EstimateFeeResponse struct {
	EstimatedFee float64 `json:"estimated_fee"`
	DistanceKm   float64 `json:"distance_km"`
	OrderType    string  `json:"order_type"`
}

type TrackingState struct {
	Lat      float64 `json:"lat,omitempty"`
	Lng      float64 `json:"lng,omitempty"`
	Distance float64 `json:"distance_km"`
	ETA      int     `json:"eta_minutes"`
	Status   string  `json:"status"` // moving, stopping_by, weak_signal
	Visible  bool    `json:"visible"`
}

type Service interface {
	Create(ctx context.Context, requesterID uuid.UUID, req CreateOrderRequest) (*Order, error)
	GetByID(ctx context.Context, id uuid.UUID, requestingUserID uuid.UUID, role string) (*Order, error)
	GetByRequester(ctx context.Context, requesterID uuid.UUID) ([]Order, error)
	GetByRunner(ctx context.Context, runnerID uuid.UUID) ([]Order, error)
	GetByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Order, error)
	AcceptOrder(ctx context.Context, orderID, runnerID uuid.UUID) error
	PickupOrder(ctx context.Context, orderID, runnerID uuid.UUID) error
	CancelOrder(ctx context.Context, orderID, requesterID uuid.UUID) error
	SubmitPurchaseReceipt(ctx context.Context, orderID, runnerID uuid.UUID, receiptReader io.Reader, receiptFilename string) error
	CompleteOrder(ctx context.Context, orderID, runnerID uuid.UUID, code string, deliveryReader io.Reader, deliveryFilename string) error
	UpdatePaymentStatus(ctx context.Context, orderID uuid.UUID, paymentStatus string) error
	GetAvailableOrders(ctx context.Context, runnerID uuid.UUID) ([]Order, error)
	EstimateFee(ctx context.Context, req EstimateFeeRequest) (*EstimateFeeResponse, error)

	// Merchant specific order actions
	GetMerchantOrders(ctx context.Context, ownerID uuid.UUID) ([]Order, error)
	MerchantAcceptOrder(ctx context.Context, orderID, ownerID uuid.UUID) error
	MerchantReadyOrder(ctx context.Context, orderID, ownerID uuid.UUID) error

	// Admin specific
	GetAllWithFilters(ctx context.Context, status string, offset, limit int) ([]Order, error)
	ForceCancelOrder(ctx context.Context, orderID uuid.UUID) error

	// Phase 2: Disputes
	DisputeOrder(ctx context.Context, orderID, requesterID uuid.UUID, reason, proofURL string) error
	ResolveDispute(ctx context.Context, orderID uuid.UUID, side string) error

	// Price Adjustment
	RequestPriceAdjustment(ctx context.Context, orderID, runnerID uuid.UUID, adjustedCost float64, reason string) error
	ApprovePriceAdjustment(ctx context.Context, orderID, requesterID uuid.UUID) error
	RejectPriceAdjustment(ctx context.Context, orderID, requesterID uuid.UUID, cancelOrder bool) error

	// Tracking
	GetTrackingState(ctx context.Context, orderID uuid.UUID) (*TrackingState, error)

	// Lifecycle
	StartBackgroundCleanup(ctx context.Context)
	StartPaymentWorkerPool(ctx context.Context, numWorkers int)
	RefreshQRIS(ctx context.Context, orderID, requesterID uuid.UUID) (*Order, error)
}

type service struct {
	repo         Repository
	userSvc      user.Service
	tripRepo     trip.Repository
	matchingSvc  Matcher
	walletSvc    wallet.Service
	configSvc    systemconfig.Service
	fcm          notification.Notifier
	notifSvc     notifDomain.Service
	redis        *cache.Redis
	db           *bun.DB
	auditSvc     audit.Service
	storage      storage.Storage
	merchantSvc  merchant.Service
	paymentQueue chan PaymentJob
	paymentOnce  sync.Once
}

func NewService(repo Repository, userSvc user.Service, tripRepo trip.Repository, matchingSvc Matcher, walletSvc wallet.Service, configSvc systemconfig.Service, fcm notification.Notifier, notifSvc notifDomain.Service, redis *cache.Redis, db *bun.DB, auditSvc audit.Service, storage storage.Storage, merchantSvc merchant.Service) Service {
	return &service{
		repo:         repo,
		userSvc:      userSvc,
		tripRepo:     tripRepo,
		matchingSvc:  matchingSvc,
		walletSvc:    walletSvc,
		configSvc:    configSvc,
		fcm:          fcm,
		notifSvc:     notifSvc,
		redis:        redis,
		db:           db,
		auditSvc:     auditSvc,
		storage:      storage,
		merchantSvc:  merchantSvc,
		paymentQueue: make(chan PaymentJob, 500),
	}
}

func (s *service) Create(ctx context.Context, requesterID uuid.UUID, req CreateOrderRequest) (*Order, error) {
	// --- Concurrency Guard: Redis Lock for Merchant ---
	if req.MerchantID != nil {
		lockKey := fmt.Sprintf("lock:merchant:order:%s", req.MerchantID.String())
		ok, lockErr := s.redis.AcquireLock(ctx, lockKey, 3*time.Second)
		if lockErr != nil || !ok {
			return nil, errors.New("merchant sedang memproses pesanan lain, silakan coba beberapa saat lagi")
		}
		defer func() { _ = s.redis.ReleaseLock(ctx, lockKey) }()
	}

	u, err := s.userSvc.GetByID(ctx, requesterID, requesterID)
	if err != nil {
		return nil, err
	}
	if u.Role != user.RoleRequester {
		return nil, errors.New("hanya pengguna dengan role requester yang dapat membuat pesanan")
	}

	if u.IsSuspended {
		return nil, errors.New("tidak dapat membuat pesanan: akun Anda sedang ditangguhkan")
	}

	// Load & Validate Merchant info if provided
	var merch *merchant.Merchant
	var orderItems []merchant.OrderItem
	if req.MerchantID != nil {
		merch, err = s.merchantSvc.GetMerchantByID(ctx, *req.MerchantID)
		if err != nil {
			return nil, fmt.Errorf("merchant tidak ditemukan: %w", err)
		}
		if !merch.IsOpen {
			return nil, errors.New("merchant sedang tutup")
		}

		// Batas Antrean Aktif
		activeCount, err := s.db.NewSelect().
			Table("orders").
			Where("merchant_id = ?", merch.ID).
			Where("status = ? OR status = ?", StatusPending, StatusCooking).
			Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("gagal menghitung antrean aktif: %w", err)
		}
		if activeCount >= merch.MaxActiveOrders {
			return nil, errors.New("toko sedang sibuk (antrean penuh), silakan coba beberapa saat lagi")
		}

		// Overwrite pickup details to be merchant's
		req.PickupLat = merch.Latitude
		req.PickupLng = merch.Longitude
		req.PickupName = merch.Name
		req.PickupAddress = merch.Address

		// Validate items
		if len(req.Items) == 0 {
			return nil, errors.New("pesanan merchant harus menyertakan daftar item menu")
		}
		var calculatedCost float64
		for _, it := range req.Items {
			menu, err := s.merchantSvc.GetMenuByID(ctx, it.MenuID)
			if err != nil {
				return nil, fmt.Errorf("menu item tidak ditemukan: %w", err)
			}
			if menu.MerchantID != merch.ID {
				return nil, errors.New("menu item tidak sesuai dengan merchant pilihan")
			}
			if !menu.IsAvailable {
				return nil, fmt.Errorf("menu '%s' sedang tidak tersedia", menu.Name)
			}
			calculatedCost += menu.Price * float64(it.Quantity)

			orderItems = append(orderItems, merchant.OrderItem{
				ID:              uuid.New(),
				MenuID:          it.MenuID,
				Quantity:        it.Quantity,
				Notes:           it.Notes,
				PriceAtPurchase: menu.Price,
			})
		}
		// Enforce maximum 10 items limit
		totalQty := 0
		for _, it := range req.Items {
			totalQty += it.Quantity
		}
		if totalQty > 10 {
			return nil, errors.New("jumlah total item pesanan melebihi batas maksimum 10 item")
		}

		req.EstimatedCost = calculatedCost
	}

	if req.ServiceCategory == CategoryBeli && req.EstimatedCost <= 0 {
		return nil, errors.New("estimasi harga barang (estimated_cost) wajib diisi untuk kategori pembelian")
	}

	// --- Account & COD Restrictions ---
	distance := geo.Haversine(req.PickupLat, req.PickupLng, req.DeliveryLat, req.DeliveryLng)

	if !u.IsVerified && !config.App.BypassKYCValidation {
		// 1. Daily Order Limit
		limitStr := s.configSvc.GetValue(ctx, "kyc_daily_order_limit", "5")
		limit, _ := strconv.Atoi(limitStr)
		count, _ := s.repo.CountTodayOrders(ctx, requesterID)
		if count >= limit {
			return nil, fmt.Errorf("batas harian membuat pesanan untuk akun non-verifikasi adalah %d kali. Silakan selesaikan e-KYC untuk akses tanpa batas", limit)
		}

		// 2. COD Restriction for Non-KYC
		if req.PaymentMethod == "cod" {
			return nil, errors.New("metode pembayaran COD hanya tersedia untuk pengguna yang telah terverifikasi e-KYC")
		}
	}

	// 3. General COD Rules (Distance & Amount)
	if req.PaymentMethod == "cod" {
		maxAmountStr := s.configSvc.GetValue(ctx, "cod_max_amount", "50000")
		maxAmount, _ := strconv.ParseFloat(maxAmountStr, 64)
		if req.EstimatedCost > maxAmount {
			return nil, fmt.Errorf("metode COD hanya tersedia untuk nilai titipan maksimal Rp %.0f", maxAmount)
		}

		maxDistStr := s.configSvc.GetValue(ctx, "cod_max_distance_km", "10")
		maxDist, _ := strconv.ParseFloat(maxDistStr, 64)
		if distance > maxDist {
			return nil, fmt.Errorf("metode COD hanya tersedia untuk jarak pengantaran maksimal %.0f KM", maxDist)
		}
	}

	now := time.Now()

	// Auto-populate receiver_phone from requester's whatsapp_number if not provided
	receiverPhone := req.ReceiverPhone
	if receiverPhone == "" && u.WhatsappNumber != "" {
		receiverPhone = u.WhatsappNumber
	}
	receiverName := req.ReceiverName
	if receiverName == "" {
		receiverName = u.Name
	}

	// 1. Calculate Distance (Already calculated above)

	// 2. Determine Order Type
	orderType := TypeRegular
	if distance <= 5.0 {
		orderType = TypeInstant
	}

	// 3. Calculate Delivery Fee automatically (now includes 10% platform markup + checking fee)
	deliveryFee := s.calculateDeliveryFee(ctx, distance, req.WeightKg, req.VolumeLiters, orderType)

	// Fetch checking fee for storage
	feeStr := s.configSvc.GetValue(ctx, "order_checking_fee", "5000")
	checkingFee, _ := strconv.ParseFloat(feeStr, 64)

	// Extract ServiceFee (Platform markup applies to base fee, excluding checking fee)
	feePercentStr2 := s.configSvc.GetValue(ctx, "platform_fee_percent", "10")
	feePercent2, _ := strconv.ParseFloat(feePercentStr2, 64)
	feeMultiplier2 := 1 + (feePercent2 / 100)
	baseWithMarkup := deliveryFee - checkingFee
	serviceFee := baseWithMarkup - (baseWithMarkup / feeMultiplier2)

	completionCode, err := generateCompletionCode()
	if err != nil {
		return nil, fmt.Errorf("gagal membuat kode konfirmasi: %w", err)
	}

	paymentSource := req.PaymentSource
	if paymentSource == "" {
		paymentSource = "wallet"
	}

	order := &Order{
		ID:              uuid.New(),
		RequesterID:     requesterID,
		ItemDetails:     req.ItemDetails,
		ReceiverName:    receiverName,
		ReceiverPhone:   receiverPhone,
		PickupLat:       req.PickupLat,
		PickupLng:       req.PickupLng,
		PickupName:      req.PickupName,
		PickupAddress:   req.PickupAddress,
		DeliveryLat:     req.DeliveryLat,
		DeliveryLng:     req.DeliveryLng,
		DeliveryName:    req.DeliveryName,
		DeliveryAddress: req.DeliveryAddress,
		EstimatedCost:   req.EstimatedCost,
		DeliveryFee:     deliveryFee,
		PaymentMethod:   req.PaymentMethod,
		PaymentSource:   paymentSource,
		PaymentStatus:   PaymentUnpaid,
		Status:          StatusPending,
		WeightKg:        req.WeightKg,
		VolumeLiters:    req.VolumeLiters,
		ServiceFee:      serviceFee,
		CheckingFee:     checkingFee,
		OrderType:       orderType,
		DistanceKm:      distance,
		ServiceCategory: req.ServiceCategory,
		CompletionCode:  completionCode,
		MerchantID:      req.MerchantID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Calculate Total Payment based on Category
	if req.ServiceCategory == CategoryKirim {
		// In "Kirim", user already owns the item. They only pay for delivery + fees.
		order.TotalPayment = deliveryFee
	} else {
		// In "Beli", user pays for Item + Delivery
		order.TotalPayment = req.EstimatedCost + deliveryFee
	}

	// Add merchant items complexity surcharge (+2000 per additional item)
	if req.MerchantID != nil {
		totalQty := 0
		for _, it := range req.Items {
			totalQty += it.Quantity
		}
		if totalQty > 1 {
			surcharge := float64(totalQty-1) * 2000
			order.DeliveryFee += surcharge
			order.TotalPayment += surcharge
		}
	}

	// Auto confirm for COD order
	if order.PaymentMethod == MethodCOD {
		if merch != nil && merch.AutoConfirm {
			order.Status = StatusCooking
		}
	}

	// --- 4. Transactional Create & Escrow Hold ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// A. Create Order Record FIRST (so Foreign Key exists)
		if err := s.repo.Create(ctx, tx, order); err != nil {
			return err
		}

		// Insert order items if merchant order
		if req.MerchantID != nil && len(orderItems) > 0 {
			for i := range orderItems {
				orderItems[i].OrderID = order.ID
			}
			if _, err := tx.NewInsert().Model(&orderItems).Exec(ctx); err != nil {
				return fmt.Errorf("gagal mencatat item pesanan ke database: %w", err)
			}
		}

		// B. Balance Check & Hold for Escrow (Wallet only)
		if order.PaymentMethod == "escrow" && order.PaymentSource == "wallet" {
			w, err := s.walletSvc.GetBalance(ctx, requesterID)
			if err != nil {
				return fmt.Errorf("gagal mengecek saldo dompet: %v", err)
			}
			if w.Balance < order.TotalPayment {
				return fmt.Errorf("saldo tidak mencukupi. Saldo Anda: Rp %.0f, Total Biaya: Rp %.0f", w.Balance, order.TotalPayment)
			}

			// Hold the balance (references the order we just created)
			if err := s.walletSvc.HoldEscrow(ctx, tx, requesterID, order.ID, order.TotalPayment); err != nil {
				return fmt.Errorf("gagal mengunci saldo: %v", err)
			}

			// Update payment status (we need to update the order we just inserted)
			order.PaymentStatus = PaymentEscrow

			// Auto confirm: if auto_confirm is active, status transitions immediately to cooking
			if merch != nil && merch.AutoConfirm {
				order.Status = StatusCooking
			}

			if err := s.repo.Update(ctx, tx, order); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Generate QRIS URL if unpaid QRIS order
	s.populatePaymentInfo(ctx, order)

	// Audit Log
	s.auditSvc.Log(ctx, &requesterID, audit.ActionOrderCreate, "order", order.ID.String(), nil, order, "", "")

	// Trigger Smart Matching via controlled Worker Pool (only if paid or COD AND auto-confirmed/not merchant)
	if order.MerchantID == nil {
		if order.PaymentStatus == PaymentEscrow || order.PaymentMethod == MethodCOD {
			s.matchingSvc.EnqueueMatching(order.ID)
		}
	} else if order.Status == StatusCooking {
		s.matchingSvc.EnqueueMatching(order.ID)

		// Send FCM notification to Merchant owner that a new auto-confirmed order is placed
		if s.fcm != nil && config.App.FcmEnabled {
			ownerUser, _ := s.userSvc.GetByID(ctx, merch.OwnerID, merch.OwnerID)
			if ownerUser != nil && ownerUser.FcmToken != nil && *ownerUser.FcmToken != "" {
				_ = s.fcm.SendToDevice(ctx, *ownerUser.FcmToken, "Pesanan Baru Masuk (Otomatis)",
					fmt.Sprintf("Pesanan %s diterima otomatis. Silakan mulai masak!", order.ItemDetails),
					map[string]string{"order_id": order.ID.String()})
			}
		}
	} else {
		// Send FCM notification to Merchant owner that a new order is waiting confirmation
		if s.fcm != nil && config.App.FcmEnabled {
			ownerUser, _ := s.userSvc.GetByID(ctx, merch.OwnerID, merch.OwnerID)
			if ownerUser != nil && ownerUser.FcmToken != nil && *ownerUser.FcmToken != "" {
				_ = s.fcm.SendToDevice(ctx, *ownerUser.FcmToken, "Pesanan Baru Masuk",
					fmt.Sprintf("Pesanan %s membutuhkan konfirmasi Anda.", order.ItemDetails),
					map[string]string{"order_id": order.ID.String()})
			}
		}
	}

	return order, nil
}

func generateCompletionCode() (string, error) {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate completion code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (s *service) EstimateFee(ctx context.Context, req EstimateFeeRequest) (*EstimateFeeResponse, error) {
	dist := geo.Haversine(req.PickupLat, req.PickupLng, req.DeliveryLat, req.DeliveryLng)

	orderType := TypeRegular
	if dist <= 5.0 {
		orderType = TypeInstant
	}

	fee := s.calculateDeliveryFee(ctx, dist, req.WeightKg, req.VolumeLiters, orderType)

	return &EstimateFeeResponse{
		EstimatedFee: fee,
		DistanceKm:   dist,
		OrderType:    orderType,
	}, nil
}

func (s *service) GetByID(ctx context.Context, id uuid.UUID, requestingUserID uuid.UUID, role string) (*Order, error) {
	order, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	s.populateRunnerInfo(ctx, order)
	s.populateReviewInfo(ctx, order)
	s.populatePaymentInfo(ctx, order)
	s.signURLs(ctx, order)

	// Authorization Logic:
	// 1. Admin can see anything
	if role == user.RoleAdmin {
		return order, nil
	}

	// 2. Requester or Runner of the order can see it
	if order.RequesterID == requestingUserID || (order.RunnerID != nil && *order.RunnerID == requestingUserID) {
		return order, nil
	}

	// 3. Any Runner can see PENDING orders (to decide whether to take it)
	if role == user.RoleRunner && order.Status == StatusPending {
		return order, nil
	}

	return order, nil
}

func (s *service) GetByRequester(ctx context.Context, requesterID uuid.UUID) ([]Order, error) {
	orders, err := s.repo.FindByRequesterID(ctx, requesterID)
	if err == nil {
		for i := range orders {
			s.populateRunnerInfo(ctx, &orders[i])
			s.populateReviewInfo(ctx, &orders[i])
			s.populatePaymentInfo(ctx, &orders[i])
			s.signURLs(ctx, &orders[i])
		}
	}
	return orders, err
}

func (s *service) GetByRunner(ctx context.Context, runnerID uuid.UUID) ([]Order, error) {
	orders, err := s.repo.FindByRunnerID(ctx, runnerID)
	if err == nil {
		for i := range orders {
			s.populateRunnerInfo(ctx, &orders[i])
			s.populateReviewInfo(ctx, &orders[i])
			s.populatePaymentInfo(ctx, &orders[i])
			s.signURLs(ctx, &orders[i])
		}
	}
	return orders, err
}

func (s *service) GetByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Order, error) {
	orders, err := s.repo.FindByUserID(ctx, userID, limit, offset)
	if err == nil {
		for i := range orders {
			s.populateRunnerInfo(ctx, &orders[i])
			s.populateReviewInfo(ctx, &orders[i])
			s.populatePaymentInfo(ctx, &orders[i])
			s.signURLs(ctx, &orders[i])
		}
	}
	return orders, err
}

func (s *service) AcceptOrder(ctx context.Context, orderID, runnerID uuid.UUID) error {
	// --- Concurrency Guard: Redis Lock ---
	lockKey := fmt.Sprintf("lock:order:accept:%s", orderID.String())
	ok, lockErr := s.redis.AcquireLock(ctx, lockKey, 5*time.Second)
	if lockErr != nil || !ok {
		return errors.New("pesanan ini sedang diproses oleh sistem, silakan coba sesaat lagi")
	}
	defer func() { _ = s.redis.ReleaseLock(ctx, lockKey) }()

	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	var expectedStatus string
	if order.MerchantID != nil {
		if order.Status != StatusCooking && order.Status != StatusReady {
			return errors.New("pesanan merchant belum diterima oleh merchant atau sedang tidak dapat diambil")
		}
		expectedStatus = order.Status
	} else {
		if order.Status != StatusPending {
			return errors.New("pesanan sudah tidak dalam status menunggu")
		}
		expectedStatus = StatusPending
	}
	oldStatus := order.Status

	r, err := s.userSvc.GetByID(ctx, runnerID, runnerID)
	if err != nil {
		return err
	}
	if r.IsSuspended {
		return errors.New("tidak dapat menerima pesanan: akun Anda sedang ditangguhkan")
	}

	if !r.IsVerified && !config.App.BypassKYCValidation {
		limitStr := s.configSvc.GetValue(ctx, "kyc_daily_order_limit", "5")
		limit, _ := strconv.Atoi(limitStr)
		count, err := s.repo.CountTodayAcceptances(ctx, runnerID)
		if err == nil && count >= limit {
			return fmt.Errorf("batas harian menerima pesanan untuk akun non-verifikasi adalah %d kali. Silakan selesaikan e-KYC untuk akses tanpa batas", limit)
		}
	}

	if order.RequesterID == runnerID {
		return errors.New("tidak dapat menerima pesanan Anda sendiri")
	}

	// Capacity Management: Find Runner's current Trip
	trips, err := s.tripRepo.FindByRunnerID(ctx, runnerID)
	var activeTrip *trip.Trip
	if err == nil {
		for _, t := range trips {
			if t.Status == trip.StatusActive || t.Status == trip.StatusStarted {
				activeTrip = &t
				break
			}
		}
	}

	// Logic: If no active trip found AND runner is not in "Accepting Orders" mode, reject
	if activeTrip == nil && !r.IsAcceptingOrders {
		return errors.New("tidak dapat menerima pesanan: Anda harus memiliki perjalanan aktif atau dalam mode 'Online'")
	}

	// Validate Capacity (only if trip exists)
	if activeTrip != nil {
		if activeTrip.AvailableWeightKg < order.WeightKg {
			return errors.New("kapasitas berat pada perjalanan ini tidak mencukupi")
		}
		if activeTrip.AvailableVolumeLiters < order.VolumeLiters {
			return errors.New("kapasitas volume pada perjalanan ini tidak mencukupi")
		}
	}

	// --- Unified Transaction Block ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. Check Escrow (Already held during Create)
		if order.PaymentMethod == MethodEscrow {
			if order.PaymentStatus != PaymentEscrow {
				return errors.New("pembayaran pesanan belum diamankan dalam escrow")
			}
		}

		// 2. Atomic Capacity Update (only if trip exists)
		if activeTrip != nil {
			if err := s.tripRepo.UpdateCapacity(ctx, tx, activeTrip.ID, order.WeightKg, order.VolumeLiters); err != nil {
				return err
			}
		}

		// 3. Finalize Order Acceptance
		order.RunnerID = &runnerID
		if activeTrip != nil {
			order.TripID = &activeTrip.ID
		}
		order.Status = StatusAccepted
		order.UpdatedAt = time.Now()

		ok, err := s.repo.UpdateWithStatusCheck(ctx, tx, order, expectedStatus)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("pesanan sudah diambil oleh runner lain")
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Audit Log
	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderAccept, "order", orderID.String(), map[string]interface{}{"status": oldStatus}, map[string]interface{}{"status": StatusAccepted, "runner_id": runnerID}, "", "")

	// Create In-App Notification for Requester
	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:  order.RequesterID,
		Title:   "Pesanan Diterima",
		Message: fmt.Sprintf("Seorang Runner telah menerima pesanan Anda: %s", order.ItemDetails),
		Type:    "order",
		Metadata: map[string]interface{}{
			"order_id": order.ID,
		},
	})

	// Send Push Notification if token exists
	if s.fcm != nil && config.App.FcmEnabled {
		reqUser, _ := s.userSvc.GetByID(ctx, order.RequesterID, order.RequesterID)
		if reqUser != nil && reqUser.FcmToken != nil && *reqUser.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *reqUser.FcmToken, "Pesanan Diterima",
				fmt.Sprintf("Runner sedang memproses pesanan Anda (%s)", order.ItemDetails),
				map[string]string{"order_id": order.ID.String()})
		}
	}

	return nil
}

func (s *service) PickupOrder(ctx context.Context, orderID, runnerID uuid.UUID) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RunnerID == nil || *order.RunnerID != runnerID {
		return errors.New("anda bukan runner untuk pesanan ini")
	}

	switch order.ServiceCategory {
	case CategoryBeli:
		if order.MerchantID != nil {
			if order.Status != StatusReady && order.Status != StatusCooking && order.Status != StatusAccepted {
				return errors.New("pesanan merchant belum siap untuk diambil")
			}
		} else {
			if order.Status != StatusPurchasing {
				return errors.New("kategori pesanan 'beli' harus dibeli (kwitansi diunggah) sebelum dapat diambil")
			}
		}
	case CategoryKirim:
		if order.Status != StatusAccepted {
			return errors.New("kategori pesanan 'kirim' harus diterima sebelum dapat diambil")
		}
	default:
		if order.Status != StatusAccepted && order.Status != StatusPurchasing && order.Status != StatusReady && order.Status != StatusCooking {
			return errors.New("pesanan tidak dalam status yang dapat diambil")
		}
	}

	oldStatus := order.Status
	order.Status = StatusDelivering
	order.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderPickup, "order", orderID.String(),
		map[string]interface{}{"status": oldStatus},
		map[string]interface{}{"status": StatusDelivering}, "", "")

	return nil
}

func (s *service) CancelOrder(ctx context.Context, orderID, requesterID uuid.UUID) error {
	ord, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if ord.RequesterID != requesterID {
		return errors.New("hanya peminta yang dapat membatalkan pesanan ini")
	}

	if ord.Status == StatusCompleted || ord.Status == StatusCancelled || ord.Status == StatusDelivering {
		return errors.New("pesanan tidak dapat dibatalkan pada tahap ini")
	}

	// Logic: Charge checking fee if status is PURCHASING or if there's an adjustment
	shouldChargeFee := ord.Status == StatusPurchasing || ord.AdjustmentStatus != ""

	// --- Unified Cancellation Transaction ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if ord.PaymentMethod == MethodEscrow && ord.PaymentStatus == PaymentEscrow {
			totalEscrow := ord.EstimatedCost + ord.DeliveryFee

			if shouldChargeFee && ord.RunnerID != nil {
				fee := ord.CheckingFee
				refundAmount := totalEscrow - fee
				if refundAmount < 0 {
					refundAmount = 0
					fee = totalEscrow
				}

				if err := s.walletSvc.PartialReleaseEscrow(ctx, tx, *ord.RunnerID, ord.RequesterID, ord.ID, fee, refundAmount); err != nil {
					return errors.New("gagal memproses pengembalian parsial: " + err.Error())
				}
			} else {
				// Refund full amount
				if err := s.walletSvc.RefundEscrow(ctx, tx, ord.RequesterID, ord.ID, totalEscrow); err != nil {
					return errors.New("gagal mengembalikan dana escrow: " + err.Error())
				}
			}
			ord.PaymentStatus = PaymentRefunded
		}

		// Restore Capacity if runner was assigned
		if ord.RunnerID != nil && ord.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters); err != nil {
				return errors.New("gagal memulihkan kapasitas perjalanan")
			}
		}

		ord.Status = StatusCancelled
		ord.UpdatedAt = time.Now()
		_, err := tx.NewUpdate().Model(ord).WherePK().Exec(ctx)
		if err == nil {
			s.auditSvc.LogWithDB(ctx, tx, &requesterID, audit.ActionOrderCancel, "order", orderID.String(), map[string]interface{}{"status": StatusPending}, map[string]interface{}{"status": StatusCancelled}, "", "")
		}
		return err
	})

	return err
}

func (s *service) SubmitPurchaseReceipt(ctx context.Context, orderID, runnerID uuid.UUID, receiptReader io.Reader, receiptFilename string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RunnerID == nil || *order.RunnerID != runnerID {
		return errors.New("anda bukan runner untuk pesanan ini")
	}

	if order.ServiceCategory == CategoryKirim {
		return errors.New("kategori pesanan 'kirim' tidak mendukung fase pembelian")
	}

	// Must be in Accepted state before Purchasing
	if order.Status != StatusAccepted {
		return errors.New("pesanan belum siap untuk fase pembelian")
	}

	if receiptReader == nil {
		return errors.New("file gambar kwitansi wajib diunggah")
	}

	// Compress and resize image (max 1200px, 75% quality)
	compressed, err := fileutil.CompressAndResizeImage(receiptReader, 1200, 75)
	if err != nil {
		return fmt.Errorf("gagal mengompresi gambar kwitansi: %w", err)
	}

	var size int64
	if buf, ok := compressed.(*bytes.Buffer); ok {
		size = int64(buf.Len())
	}

	// Upload using storage driver to orders/{orderID}/receipt_{timestamp}.jpg
	objectKey := fmt.Sprintf("orders/%s/receipt_%d.jpg", orderID.String(), time.Now().Unix())
	path, err := s.storage.Upload(ctx, objectKey, compressed, size, "image/jpeg")
	if err != nil {
		return fmt.Errorf("gagal mengunggah kwitansi ke penyimpanan: %w", err)
	}

	order.Status = StatusPurchasing
	order.ReceiptImageURL = path
	order.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderPurchased, "order", orderID.String(),
		map[string]interface{}{"status": StatusAccepted},
		map[string]interface{}{"status": StatusPurchasing, "receipt_image_url": path}, "", "")

	return nil
}

func (s *service) CompleteOrder(ctx context.Context, orderID, runnerID uuid.UUID, code string, deliveryReader io.Reader, deliveryFilename string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RunnerID == nil || *order.RunnerID != runnerID {
		return errors.New("anda bukan runner untuk pesanan ini")
	}

	if order.Status != StatusDelivering {
		return errors.New("pesanan tidak dapat diselesaikan dari status saat ini (harus dalam fase pengiriman)")
	}

	if order.CompletionCode != code {
		return errors.New("kode konfirmasi salah")
	}

	var path string
	if deliveryReader != nil {
		// Compress and resize image (max 1200px, 75% quality)
		compressed, err := fileutil.CompressAndResizeImage(deliveryReader, 1200, 75)
		if err != nil {
			return fmt.Errorf("gagal mengompresi bukti penyerahan: %w", err)
		}

		var size int64
		if buf, ok := compressed.(*bytes.Buffer); ok {
			size = int64(buf.Len())
		}

		// Upload to orders/{orderID}/delivery_{timestamp}.jpg
		objectKey := fmt.Sprintf("orders/%s/delivery_%d.jpg", orderID.String(), time.Now().Unix())
		path, err = s.storage.Upload(ctx, objectKey, compressed, size, "image/jpeg")
		if err != nil {
			return fmt.Errorf("gagal mengunggah bukti penyerahan ke penyimpanan: %w", err)
		}
	}

	// --- Unified Completion Transaction ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		switch order.PaymentMethod {
		case MethodEscrow:
			platformFee := order.ServiceFee
			refundAmount := order.CheckingFee

			if order.MerchantID != nil {
				merch, err := s.merchantSvc.GetMerchantByID(ctx, *order.MerchantID)
				if err != nil {
					return fmt.Errorf("gagal mengambil data merchant: %w", err)
				}
				foodAmount := order.EstimatedCost
				runnerAmount := order.DeliveryFee - order.ServiceFee - order.CheckingFee

				if err := s.walletSvc.ReleaseMerchantEscrow(ctx, tx, runnerID, order.RequesterID, merch.OwnerID, order.ID, foodAmount, runnerAmount, platformFee, refundAmount); err != nil {
					return errors.New("gagal melepaskan dana escrow merchant: " + err.Error())
				}
			} else {
				totalRunnerPayout := order.EstimatedCost + (order.DeliveryFee - order.ServiceFee - order.CheckingFee)
				if err := s.walletSvc.ReleaseEscrowWithRefund(ctx, tx, runnerID, order.RequesterID, order.ID, totalRunnerPayout, platformFee, refundAmount); err != nil {
					return errors.New("gagal melepaskan dana escrow: " + err.Error())
				}
			}
			order.PaymentStatus = PaymentReleased
		case MethodCOD:
			platformFee := order.ServiceFee
			if err := s.walletSvc.DeductCODPlatformFee(ctx, tx, runnerID, order.ID, platformFee); err != nil {
				return errors.New("gagal memotong biaya platform COD: " + err.Error())
			}
			order.PaymentStatus = PaymentReleased
		}

		if path != "" {
			order.DeliveryImageURL = path
		}
		order.Status = StatusCompleted
		order.UpdatedAt = time.Now()

		if err := s.repo.Update(ctx, tx, order); err != nil {
			return err
		}

		// Audit Log (Transactional)
		s.auditSvc.LogWithDB(ctx, tx, &runnerID, audit.ActionOrderComplete, "order", orderID.String(), nil, map[string]interface{}{"status": StatusCompleted, "delivery_image_url": path}, "", "")

		return nil
	})

	return err
}

func (s *service) UpdatePaymentStatus(ctx context.Context, orderID uuid.UUID, paymentStatus string) error {
	job := PaymentJob{
		OrderID: orderID,
		Status:  paymentStatus,
		ErrChan: make(chan error, 1),
	}

	select {
	case s.paymentQueue <- job:
		// Enqueued successfully, wait for the worker to process it
	default:
		return errors.New("antrean proses pembayaran penuh, silakan coba lagi beberapa saat")
	}

	select {
	case err := <-job.ErrChan:
		return err
	case <-time.After(5 * time.Second):
		return errors.New("timeout memproses pembayaran, silakan coba lagi")
	}
}

func (s *service) processPayment(ctx context.Context, orderID uuid.UUID, paymentStatus string) error {
	// If updating to paid (escrow), execute check-and-set to prevent double payments / race conditions
	if paymentStatus == PaymentEscrow {
		var rowsAffected int64
		err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			res, err := tx.NewUpdate().
				Model((*Order)(nil)).
				Set("payment_status = ?", PaymentEscrow).
				Set("updated_at = ?", time.Now()).
				Where("id = ?", orderID).
				Where("payment_status = ?", PaymentUnpaid).
				Exec(ctx)
			if err != nil {
				return err
			}
			rowsAffected, _ = res.RowsAffected()
			return nil
		})
		if err != nil {
			return err
		}
		var orderObj *Order
		if rowsAffected > 0 {
			orderObj, _ = s.repo.FindByID(ctx, orderID)
		}

		if rowsAffected == 0 {
			// Idempotency: check if already paid
			order, err := s.repo.FindByID(ctx, orderID)
			if err == nil && order.PaymentStatus == PaymentEscrow {
				return nil // Already processed, return success to gateway
			}
			return fmt.Errorf("pesanan tidak ditemukan atau tidak berada dalam status belum dibayar")
		}

		// Success update: Trigger matching & audit log
		if orderObj != nil && orderObj.MerchantID != nil {
			merch, err := s.merchantSvc.GetMerchantByID(ctx, *orderObj.MerchantID)
			if err == nil {
				if merch.AutoConfirm {
					orderObj.Status = StatusCooking
					_ = s.repo.Update(ctx, s.db, orderObj)
					s.matchingSvc.EnqueueMatching(orderID)

					// Notify merchant owner of auto-confirmed order
					if s.fcm != nil && config.App.FcmEnabled {
						ownerUser, _ := s.userSvc.GetByID(ctx, merch.OwnerID, merch.OwnerID)
						if ownerUser != nil && ownerUser.FcmToken != nil && *ownerUser.FcmToken != "" {
							_ = s.fcm.SendToDevice(ctx, *ownerUser.FcmToken, "Pesanan Baru Masuk (Otomatis)",
								fmt.Sprintf("Pesanan %s diterima otomatis. Silakan mulai masak!", orderObj.ItemDetails),
								map[string]string{"order_id": orderObj.ID.String()})
						}
					}
				} else {
					// Notify merchant owner of pending manual confirmation
					if s.fcm != nil && config.App.FcmEnabled {
						ownerUser, _ := s.userSvc.GetByID(ctx, merch.OwnerID, merch.OwnerID)
						if ownerUser != nil && ownerUser.FcmToken != nil && *ownerUser.FcmToken != "" {
							_ = s.fcm.SendToDevice(ctx, *ownerUser.FcmToken, "Pesanan Baru Masuk",
								fmt.Sprintf("Pesanan %s membutuhkan konfirmasi Anda.", orderObj.ItemDetails),
								map[string]string{"order_id": orderObj.ID.String()})
						}
					}
				}
			}
		} else {
			s.matchingSvc.EnqueueMatching(orderID)
		}

		runnerID := uuid.Nil // Webhook / system action
		s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderUpdate, "order", orderID.String(),
			map[string]interface{}{"payment_status": PaymentUnpaid},
			map[string]interface{}{"payment_status": PaymentEscrow}, "", "")

		// Record wallet transaction for QRIS payment
		if orderObj != nil && orderObj.PaymentSource == "qris" {
			w, err := s.walletSvc.GetBalance(ctx, orderObj.RequesterID)
			if err == nil && w != nil {
				wtx := &wallet.WalletTransaction{
					ID:        uuid.New(),
					WalletID:  w.ID,
					OrderID:   &orderObj.ID,
					Type:      wallet.TypeEscrowHold,
					Amount:    -orderObj.TotalPayment,
					Reference: fmt.Sprintf("QRIS-PAY-%s", orderObj.ID.String()[:8]),
					Status:    wallet.StatusCompleted,
				}
				_, _ = s.db.NewInsert().Model(wtx).Exec(ctx)
			}
		}

		return nil
	}

	// Fallback for other status updates
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}
	order.PaymentStatus = paymentStatus
	order.UpdatedAt = time.Now()
	return s.repo.Update(ctx, s.db, order)
}

func (s *service) GetAllWithFilters(ctx context.Context, status string, offset, limit int) ([]Order, error) {
	orders, err := s.repo.FindAllWithFilters(ctx, status, offset, limit)
	if err == nil {
		for i := range orders {
			s.signURLs(ctx, &orders[i])
		}
	}
	return orders, err
}

func (s *service) ForceCancelOrder(ctx context.Context, orderID uuid.UUID) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.Status == StatusCompleted || order.Status == StatusCancelled {
		return errors.New("tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")
	}

	// --- Unified Admin Force-Cancel Transaction ---
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. Handle Escrow Refund if applicable
		if order.PaymentMethod == MethodEscrow && order.PaymentStatus == PaymentEscrow {
			totalEscrow := order.EstimatedCost + order.DeliveryFee
			if err := s.walletSvc.RefundEscrow(ctx, tx, order.RequesterID, orderID, totalEscrow); err != nil {
				return errors.New("gagal mengembalikan dana escrow: " + err.Error())
			}
			order.PaymentStatus = PaymentRefunded
		}

		// 2. Restore Capacity
		if order.RunnerID != nil && order.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *order.TripID, order.WeightKg, order.VolumeLiters); err != nil {
				return errors.New("gagal memulihkan kapasitas perjalanan")
			}
		}

		order.Status = StatusCancelled
		order.UpdatedAt = time.Now()

		return s.repo.Update(ctx, tx, order)
	})
}

func (s *service) DisputeOrder(ctx context.Context, orderID, requesterID uuid.UUID, reason, proofURL string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RequesterID != requesterID {
		return errors.New("hanya peminta yang dapat mengajukan sengketa untuk pesanan ini")
	}

	if order.Status != StatusCompleted {
		return errors.New("hanya pesanan selesai yang dapat disengketakan")
	}

	// 24 Hour Limit Enforcement
	if time.Since(order.UpdatedAt) > 24*time.Hour {
		return errors.New("batas waktu pengajuan sengketa (24 jam setelah selesai) telah berakhir")
	}

	if order.PaymentStatus == PaymentRefunded {
		return errors.New("pesanan sudah dikembalikan dananya")
	}

	if proofURL == "" {
		return errors.New("URL gambar bukti wajib diisi untuk mengajukan sengketa")
	}

	order.Status = StatusDisputed
	order.DisputeReason = reason
	order.DisputeProofURL = proofURL
	now := time.Now()
	order.DisputedAt = &now
	order.UpdatedAt = now

	err = s.repo.Update(ctx, s.db, order)
	if err == nil && s.fcm != nil && config.App.FcmEnabled && order.RunnerID != nil {
		runner, _ := s.userSvc.GetByID(ctx, *order.RunnerID, *order.RunnerID)
		if runner != nil && runner.FcmToken != nil && *runner.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *runner.FcmToken, "Pesanan Disengketakan",
				"Penitip membuka sengketa untuk pesanan Anda. Admin akan segera meninjau.", map[string]string{
					"type":     "order_disputed",
					"order_id": order.ID.String(),
				})
		}
	}
	return err
}

func (s *service) ResolveDispute(ctx context.Context, orderID uuid.UUID, side string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.Status != StatusDisputed {
		return errors.New("pesanan tidak dalam status sengketa")
	}

	// --- Unified Resolution Transaction ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if order.PaymentMethod == MethodEscrow {
			switch side {
			case user.RoleRequester:
				totalAmount := order.EstimatedCost + order.DeliveryFee
				if err := s.walletSvc.RefundEscrow(ctx, tx, order.RequesterID, orderID, totalAmount); err != nil {
					return errors.New("gagal mengembalikan dana escrow: " + err.Error())
				}
				order.PaymentStatus = PaymentRefunded
				order.Status = StatusCancelled

				// Restore Capacity
				if order.RunnerID != nil && order.TripID != nil {
					if err := s.tripRepo.RestoreCapacity(ctx, tx, *order.TripID, order.WeightKg, order.VolumeLiters); err != nil {
						return errors.New("gagal memulihkan kapasitas perjalanan")
					}
				}
			case user.RoleRunner:
				if order.RunnerID == nil {
					return errors.New("pesanan tidak memiliki runner")
				}
				platformFee := order.ServiceFee
				refundAmount := order.CheckingFee
				totalRunnerPayout := order.EstimatedCost + (order.DeliveryFee - order.ServiceFee - order.CheckingFee)
				if err := s.walletSvc.ReleaseEscrowWithRefund(ctx, tx, *order.RunnerID, order.RequesterID, orderID, totalRunnerPayout, platformFee, refundAmount); err != nil {
					return errors.New("gagal melepaskan dana escrow: " + err.Error())
				}
				order.PaymentStatus = PaymentReleased
				order.Status = StatusCompleted
			default:
				return errors.New("pihak penyelesaian tidak valid, harus 'requester' atau 'runner'")
			}
		} else {
			order.Status = StatusCompleted
		}

		order.DisputeReason = "RESOLVED: " + order.DisputeReason
		order.UpdatedAt = time.Now()

		return s.repo.Update(ctx, tx, order)
	})
	if err == nil && s.fcm != nil && config.App.FcmEnabled {
		// Notify both parties about resolution
		msg := "Sengketa pesanan telah diselesaikan oleh Admin."
		if side == user.RoleRequester {
			msg += " Dana dikembalikan ke Penitip."
		} else {
			msg += " Dana dilepaskan ke Runner."
		}

		// Notify Requester
		reqUser, _ := s.userSvc.GetByID(ctx, order.RequesterID, order.RequesterID)
		if reqUser != nil && reqUser.FcmToken != nil && *reqUser.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *reqUser.FcmToken, "Sengketa Selesai", msg, map[string]string{"order_id": order.ID.String()})
		}
		// Notify Runner
		if order.RunnerID != nil {
			runUser, _ := s.userSvc.GetByID(ctx, *order.RunnerID, *order.RunnerID)
			if runUser != nil && runUser.FcmToken != nil && *runUser.FcmToken != "" {
				_ = s.fcm.SendToDevice(ctx, *runUser.FcmToken, "Sengketa Selesai", msg, map[string]string{"order_id": order.ID.String()})
			}
		}
	}

	return err
}

func (s *service) GetTrackingState(ctx context.Context, orderID uuid.UUID) (*TrackingState, error) {
	ord, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if ord.RunnerID == nil {
		return &TrackingState{Status: "waiting_for_runner", Visible: false}, nil
	}

	state := &TrackingState{
		Status:  "moving",
		Visible: false,
	}

	// 1. Get Live Data from Redis
	key := "runner:track:" + ord.RunnerID.String()
	val, err := s.redis.Get(ctx, key)
	if err != nil || val == "" {
		// Fallback to User model if Redis fails or empty
		u, err := s.userSvc.GetByID(ctx, *ord.RunnerID, *ord.RunnerID)
		if err != nil || u.LastLat == nil {
			state.Status = "weak_signal"
			return state, nil
		}
		state.Lat = *u.LastLat
		state.Lng = *u.LastLng
		state.Status = "weak_signal"
	} else {
		// Parse val: "lat,lng,timestamp"
		parts := strings.Split(val, ",")
		if len(parts) == 3 {
			state.Lat, _ = strconv.ParseFloat(parts[0], 64)
			state.Lng, _ = strconv.ParseFloat(parts[1], 64)
			ts, _ := strconv.ParseInt(parts[2], 10, 64)

			// Check for "Weak Signal" (> 2 mins)
			if time.Now().Unix()-ts > 120 {
				state.Status = "weak_signal"
			} else {
				// Check for "Stopping By" (> 30s)
				// Note: Real "Stopping By" detection needs history,
				// for MVP we can check the time since last move if we store it.
				// Since we only have current TS, let's assume if TS is > 30s old, it's stopping.
				if time.Now().Unix()-ts > 30 {
					state.Status = "stopping_by"
				}
			}
		}
	}

	// 2. Calculate Distance & Visibility
	dist := geo.Haversine(state.Lat, state.Lng, ord.DeliveryLat, ord.DeliveryLng)
	state.Distance = dist
	state.ETA = geo.CalculateETA(dist, 30) // Assuming 30km/h avg

	if dist < 10.0 {
		state.Visible = true
	} else {
		// Hide coordinates but keep Status & ETA
		state.Lat = 0
		state.Lng = 0
		state.Visible = false
	}

	return state, nil
}

func (s *service) GetAvailableOrders(ctx context.Context, runnerID uuid.UUID) ([]Order, error) {
	// Fetch expiration duration from config (default 24h)
	expiryStr := s.configSvc.GetValue(ctx, "order_expiration_hours", "24")
	expiryHours, err := strconv.Atoi(expiryStr)
	if err != nil {
		expiryHours = 24
	}

	cutoff := time.Now().Add(-time.Duration(expiryHours) * time.Hour)

	// Fetch Runner's current status and location
	u, err := s.userSvc.GetByID(ctx, runnerID, runnerID)
	if err != nil {
		return []Order{}, err
	}

	params := FindAvailableParams{
		Cutoff:            cutoff,
		Limit:             100,
		Offset:            0,
		RunnerLat:         0,
		RunnerLng:         0,
		IsAcceptingOrders: u.IsAcceptingOrders,
	}

	if u.LastLat != nil {
		params.RunnerLat = *u.LastLat
		params.RunnerLng = *u.LastLng
	}

	trips, err := s.tripRepo.FindByRunnerID(ctx, runnerID)
	if err == nil {
		for _, t := range trips {
			if t.Status == trip.StatusStarted {
				params.HasActiveTrip = true
				params.AllowedTypes = append(params.AllowedTypes, t.AllowedServiceTypes...)
				params.OriginLat = t.OriginLat
				params.OriginLng = t.OriginLng
				params.DestLat = t.DestinationLat
				params.DestLng = t.DestinationLng
				params.IsRoundTrip = t.IsRoundTrip
				params.RadiusKm = 10.0
				for _, st := range t.AllowedServiceTypes {
					if st == TypeInstant && len(t.AllowedServiceTypes) == 1 {
						params.RadiusKm = 2.0
					}
				}
				break // Use the first active trip
			}
		}
	}

	// Logic: If no trip and not accepting orders, return empty
	if !params.HasActiveTrip && !params.IsAcceptingOrders {
		return []Order{}, nil
	}

	fmt.Printf("[Matching] Runner %s searching orders. LastLoc: %f,%f. HasTrip: %v. AcceptingOrders: %v\n",
		runnerID, params.RunnerLat, params.RunnerLng, params.HasActiveTrip, params.IsAcceptingOrders)

	orders, err := s.repo.FindAvailable(ctx, params)
	if err == nil {
		fmt.Printf("[Matching] Found %d matching orders in DB. Details:\n", len(orders))
		for _, o := range orders {
			fmt.Printf("  - Order %s: Status=%s, Payment=%s, Type=%s\n", o.ID, o.Status, o.PaymentStatus, o.OrderType)
		}
	}

	return orders, err
}

func (s *service) StartBackgroundCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.expireOldOrders(context.Background())
			}
		}
	}()
}

func (s *service) expireOldOrders(ctx context.Context) {
	expiryStr := s.configSvc.GetValue(ctx, "order_expiration_hours", "24")
	expiryHours, err := strconv.Atoi(expiryStr)
	if err != nil {
		expiryHours = 24
	}

	cutoff := time.Now().Add(-time.Duration(expiryHours) * time.Hour)
	count, err := s.repo.ExpireOldOrders(ctx, cutoff)
	if err != nil {
		// Log error if logger was available here,
		// but since we are in a domain service we just let it be or use a simple log
		return
	}

	if count > 0 {
		return
	}
}

func (s *service) RequestPriceAdjustment(ctx context.Context, orderID, runnerID uuid.UUID, adjustedCost float64, reason string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RunnerID == nil || *order.RunnerID != runnerID {
		return errors.New("anda bukan runner untuk pesanan ini")
	}

	if order.Status != StatusAccepted && order.Status != StatusPurchasing {
		return errors.New("tidak dapat menyesuaikan harga pada status pesanan saat ini")
	}

	if order.AdjustmentStatus != "" {
		return errors.New("pengajuan penyesuaian harga sudah dilakukan untuk pesanan ini (batas 1x)")
	}

	if adjustedCost <= order.EstimatedCost {
		return errors.New("biaya yang disesuaikan harus lebih tinggi dari estimasi saat ini")
	}

	order.AdjustedCost = adjustedCost
	order.AdjustmentReason = reason
	order.AdjustmentStatus = AdjustmentPending
	order.UpdatedAt = time.Now()

	err = s.repo.Update(ctx, s.db, order)
	if err == nil && s.fcm != nil && config.App.FcmEnabled {
		// Notify Requester
		requester, errReq := s.userSvc.GetByID(ctx, order.RequesterID, order.RequesterID)
		if errReq == nil && requester.FcmToken != nil && *requester.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *requester.FcmToken, "Penyesuaian Harga", "Runner meminta penyesuaian harga untuk pesanan Anda.", map[string]string{
				"type":     "price_adjustment",
				"order_id": order.ID.String(),
			})
		}
	}
	return err
}

func (s *service) ApprovePriceAdjustment(ctx context.Context, orderID, requesterID uuid.UUID) error {
	ord, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if ord.RequesterID != requesterID {
		return errors.New("hanya peminta yang dapat menyetujui penyesuaian harga")
	}

	if ord.AdjustmentStatus != AdjustmentPending {
		return errors.New("tidak ada pengajuan penyesuaian harga yang tertunda")
	}

	diff := ord.AdjustedCost - ord.EstimatedCost

	// Logic: Verified User + COD = No immediate hold. Others = Hold!
	requiresHold := true
	if ord.PaymentMethod == MethodCOD {
		u, err := s.userSvc.GetByID(ctx, requesterID, requesterID)
		if err == nil && u.IsVerified {
			requiresHold = false
		}
	}

	// --- Transactional Price Adjustment ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if requiresHold && ord.PaymentMethod == MethodEscrow {
			if err := s.walletSvc.HoldEscrow(ctx, tx, requesterID, orderID, diff); err != nil {
				return errors.New("gagal menahan dana escrow tambahan: " + err.Error())
			}
		}

		ord.EstimatedCost = ord.AdjustedCost
		ord.TotalPayment = ord.AdjustedCost + ord.DeliveryFee
		ord.AdjustmentStatus = AdjustmentAccepted
		ord.UpdatedAt = time.Now()

		return s.repo.Update(ctx, tx, ord)
	})

	if err != nil {
		return err
	}
	if s.fcm != nil && config.App.FcmEnabled && ord.RunnerID != nil {
		// Notify Runner
		runner, errRun := s.userSvc.GetByID(ctx, *ord.RunnerID, *ord.RunnerID)
		if errRun == nil && runner.FcmToken != nil && *runner.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *runner.FcmToken, "Penyesuaian Disetujui", "Penitip telah menyetujui penyesuaian harga Anda.", map[string]string{
				"type":     "price_adjustment_approved",
				"order_id": orderID.String(),
			})
		}
	}

	return err
}

func (s *service) RejectPriceAdjustment(ctx context.Context, orderID, requesterID uuid.UUID, cancelOrder bool) error {
	ord, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if ord.RequesterID != requesterID {
		return errors.New("tidak memiliki akses")
	}

	if ord.AdjustmentStatus != AdjustmentPending {
		return errors.New("tidak ada pengajuan penyesuaian harga yang tertunda")
	}

	// --- Unified Rejection Transaction ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ord.AdjustmentStatus = AdjustmentRejected
		ord.UpdatedAt = time.Now()

		if cancelOrder {
			ord.Status = StatusCancelled
			if ord.PaymentMethod == MethodEscrow && ord.PaymentStatus == PaymentEscrow {
				totalEscrow := ord.EstimatedCost + ord.DeliveryFee

				if ord.RunnerID != nil {
					fee := ord.CheckingFee
					refundAmount := totalEscrow - fee
					if refundAmount < 0 {
						refundAmount = 0
						fee = totalEscrow
					}

					if err := s.walletSvc.PartialReleaseEscrow(ctx, tx, *ord.RunnerID, ord.RequesterID, ord.ID, fee, refundAmount); err != nil {
						return errors.New("gagal memproses pengembalian parsial: " + err.Error())
					}
				} else {
					if err := s.walletSvc.RefundEscrow(ctx, tx, ord.RequesterID, ord.ID, totalEscrow); err != nil {
						return errors.New("gagal mengembalikan dana escrow: " + err.Error())
					}
				}
				ord.PaymentStatus = PaymentRefunded
			}

			// Restore Capacity
			if ord.RunnerID != nil && ord.TripID != nil {
				if err := s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters); err != nil {
					return errors.New("gagal memulihkan kapasitas perjalanan")
				}
			}
		}

		return s.repo.Update(ctx, tx, ord)
	})

	return err
}

func (s *service) calculateDeliveryFee(ctx context.Context, distance, weight, volume float64, orderType string) float64 {
	var totalFee float64

	if orderType == TypeInstant {
		// MODE INSTANT (≤ 5km)
		feeBase, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_short_base", "3000"), 64)
		feePerKG, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_short_per_kg", "2000"), 64)

		totalFee = feeBase + (weight * feePerKG)
	} else {
		// MODE REGULAR (> 5km)
		feeBase, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_base", "3000"), 64)
		feePerKM, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_per_km", "100"), 64)
		feePerKG, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_per_kg", "4000"), 64)
		feePerL, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_per_liter", "500"), 64)

		routeDistance := distance * 1.3
		totalFee = feeBase + (routeDistance * feePerKM) + (weight * feePerKG) + (volume * feePerL)
	}

	// Add Platform Markup from config (default 10%)
	feePercentStr := s.configSvc.GetValue(ctx, "platform_fee_percent", "10")
	feePercent, _ := strconv.ParseFloat(feePercentStr, 64)
	feeMultiplier := 1 + (feePercent / 100)
	totalWithMarkup := totalFee * feeMultiplier

	// Add Checking Fee (Deposit)
	checkingFeeStr := s.configSvc.GetValue(ctx, "order_checking_fee", "5000")
	checkingFee, _ := strconv.ParseFloat(checkingFeeStr, 64)
	totalWithMarkup += checkingFee

	// Pembulatan ke kelipatan 500 terdekat ke atas
	return math.Ceil(totalWithMarkup/500) * 500
}

func sanitizeStorageKey(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	// Jika key berupa URL absolut (misal dari storage.nitip.id atau localhost), bersihkan domainnya agar menjadi relative key
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		temp := urlStr
		if strings.HasPrefix(temp, "https://") {
			temp = strings.TrimPrefix(temp, "https://")
		} else {
			temp = strings.TrimPrefix(temp, "http://")
		}
		
		slashIdx := strings.Index(temp, "/")
		if slashIdx != -1 {
			path := temp[slashIdx+1:]
			path = strings.TrimPrefix(path, "uploads/")
			return path
		}
	}
	return urlStr
}

func (s *service) signURLs(ctx context.Context, o *Order) {
	if o == nil {
		return
	}
	if o.ReceiptImageURL != "" {
		key := sanitizeStorageKey(o.ReceiptImageURL)
		if signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour); err == nil {
			o.ReceiptImageURL = signed
		}
	}
	if o.DeliveryImageURL != "" {
		key := sanitizeStorageKey(o.DeliveryImageURL)
		if signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour); err == nil {
			o.DeliveryImageURL = signed
		}
	}
	if o.DisputeProofURL != "" {
		key := sanitizeStorageKey(o.DisputeProofURL)
		if signed, err := s.storage.SignedURL(ctx, key, 1*time.Hour); err == nil {
			o.DisputeProofURL = signed
		}
	}
}

func (s *service) populateRunnerInfo(ctx context.Context, o *Order) {
	if o == nil || o.RunnerID == nil {
		return
	}
	r, err := s.userSvc.GetByID(ctx, *o.RunnerID, *o.RunnerID)
	if err == nil && r != nil {
		o.RunnerName = r.Name
		o.RunnerPhone = r.WhatsappNumber
	}
}

func (s *service) populateReviewInfo(ctx context.Context, o *Order) {
	if o == nil {
		return
	}
	type dbReview struct {
		Rating  int    `bun:"runner_rating"`
		Comment string `bun:"runner_comment"`
	}
	var rv dbReview
	err := s.db.NewSelect().
		Table("reviews").
		Column("runner_rating", "runner_comment").
		Where("order_id = ?", o.ID).
		Where("runner_rating IS NOT NULL").
		Scan(ctx, &rv)
	if err == nil {
		o.FeedbackRating = &rv.Rating
		o.FeedbackComment = rv.Comment
	}
}

func (s *service) populatePaymentInfo(ctx context.Context, o *Order) {
	if o == nil {
		return
	}
	if o.PaymentMethod == "escrow" && o.PaymentSource == "qris" && o.PaymentStatus == PaymentUnpaid && o.Status != "cancelled" {
		// If already generated and not expired (15 minutes), keep using it
		if o.QRISData != "" && time.Since(o.CreatedAt) < 15*time.Minute {
			return
		}

		cacheKey := fmt.Sprintf("order:qris:%s", o.ID.String())
		qrisStr, err := s.redis.Get(ctx, cacheKey)
		if err == nil && qrisStr != "" && time.Since(o.CreatedAt) < 15*time.Minute {
			o.QRISData = qrisStr
			return
		}

		qrString, err := s.generateOrderQRIS(ctx, o)
		if err == nil && qrString != "" {
			// If Midtrans/Mock QRIS is a raw QRIS string (not a URL), wrap it so the frontend can render it!
			if !strings.HasPrefix(qrString, "http://") && !strings.HasPrefix(qrString, "https://") {
				qrString = fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=%s", url.QueryEscape(qrString))
			}
			o.QRISData = qrString
			_ = s.redis.Set(ctx, cacheKey, qrString, 15*time.Minute)

			// Save the generated QRIS back to orders table in database so it persists!
			_, dbErr := s.db.NewUpdate().
				Model(o).
				Column("qris_data").
				WherePK().
				Exec(ctx)
			if dbErr != nil {
				log.Printf("[QRIS-SAVE-ERROR] Failed to save QRIS to DB: %v", dbErr)
			}
		}
	}
}

func (s *service) generateOrderQRIS(ctx context.Context, order *Order) (string, error) {
	var qrString string
	var reference = order.ID.String()

	if config.App.MidtransServerKey != "" && !config.App.UseMockPayment {
		userObj, err := s.userSvc.GetByID(ctx, order.RequesterID, order.RequesterID)
		var userEmail string
		var userName string
		if err == nil && userObj != nil {
			userEmail = userObj.Email
			userName = userObj.Name
		}

		midtransEnv := midtrans.Sandbox
		if config.App.MidtransIsProduction {
			midtransEnv = midtrans.Production
		}

		var client coreapi.Client
		client.New(config.App.MidtransServerKey, midtransEnv)

		req := &coreapi.ChargeReq{
			PaymentType: coreapi.PaymentTypeQris,
			TransactionDetails: midtrans.TransactionDetails{
				OrderID:  reference,
				GrossAmt: int64(order.TotalPayment),
			},
			CustomerDetails: &midtrans.CustomerDetails{
				FName: userName,
				Email: userEmail,
			},
			Qris: &coreapi.QrisDetails{
				Acquirer: "gopay",
			},
		}

		reqJSON, _ := json.Marshal(req)
		log.Printf("[MIDTRANS-ORDER-CHARGE] Order: %s | Payload: %s", order.ID.String(), string(reqJSON))
		chargeResp, midtransErr := client.ChargeTransaction(req)
		if chargeResp != nil {
			log.Printf("[MIDTRANS-ORDER-RESPONSE] Order: %s, Status: %s", order.ID.String(), chargeResp.TransactionStatus)
		}
		if midtransErr != nil {
			log.Printf("[MIDTRANS-ORDER-ERROR] Order: %s, StatusCode: %d, Message: %s", order.ID.String(), midtransErr.StatusCode, midtransErr.Message)
			return "", errors.New("gagal membuat kode pembayaran GoPay/QRIS dari Midtrans")
		}

		qrString = chargeResp.QRString
		for _, action := range chargeResp.Actions {
			switch action.Name {
			case "generate-qr-code":
				if qrString == "" {
					qrString = action.URL
				}
			}
		}
		if qrString == "" && len(chargeResp.Actions) > 0 {
			qrString = chargeResp.Actions[0].URL
		}
	} else {
		// Fallback to mock-qris
		payload := map[string]interface{}{
			"reference_id": reference,
			"amount":       int64(order.TotalPayment),
		}
		body, _ := json.Marshal(payload)

		pgUrl := os.Getenv("PAYMENT_GATEWAY_URL")
		if pgUrl == "" {
			pgUrl = "http://localhost:4000"
		}

		log.Printf("[MOCK-QRIS-ORDER] Order: %s, GrossAmt: %d", order.ID.String(), int64(order.TotalPayment))
		resp, err := http.Post(fmt.Sprintf("%s/api/qris/generate", pgUrl), "application/json", bytes.NewBuffer(body))
		if err != nil {
			log.Printf("[MOCK-QRIS-ORDER-ERROR] Connection error: %v", err)
			return "", fmt.Errorf("gagal menghubungi payment gateway: %v", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		respBytes, _ := io.ReadAll(resp.Body)
		log.Printf("[MOCK-QRIS-ORDER-RESPONSE] Order: %s, Status: %s", order.ID.String(), resp.Status)

		var qrisResp struct {
			Status     string `json:"status"`
			TrxID      string `json:"trx_id"`
			QrisString string `json:"qris_string"`
		}
		if err := json.Unmarshal(respBytes, &qrisResp); err != nil {
			log.Printf("[MOCK-QRIS-ORDER-ERROR] Parse error: %v", err)
			return "", fmt.Errorf("gagal membaca respon payment gateway")
		}

		qrString = qrisResp.QrisString
	}

	return qrString, nil
}

func (s *service) StartPaymentWorkerPool(ctx context.Context, numWorkers int) {
	s.paymentOnce.Do(func() {
		for i := 0; i < numWorkers; i++ {
			go s.paymentWorker(ctx, i)
		}
		log.Printf("Started %d payment workers", numWorkers)
	})
}

func (s *service) paymentWorker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.paymentQueue:
			err := s.processPayment(ctx, job.OrderID, job.Status)
			job.ErrChan <- err
		}
	}
}

func (s *service) RefreshQRIS(ctx context.Context, orderID, requesterID uuid.UUID) (*Order, error) {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		log.Printf("[QRIS-REFRESH] Order %s not found: %v", orderID, err)
		return nil, errors.New("order tidak ditemukan")
	}

	if order.RequesterID != requesterID {
		log.Printf("[QRIS-REFRESH] Unauthorized refresh attempt for Order %s by User %s", orderID, requesterID)
		return nil, errors.New("unauthorized")
	}

	if order.Status == "cancelled" {
		log.Printf("[QRIS-REFRESH] Order %s already cancelled, refresh aborted", orderID)
		return nil, errors.New("pesanan sudah dibatalkan, tidak dapat memperbarui QRIS")
	}

	if order.PaymentStatus != PaymentUnpaid || order.PaymentMethod != "escrow" || order.PaymentSource != "qris" {
		log.Printf("[QRIS-REFRESH] Order %s is not an unpaid QRIS escrow order", orderID)
		return nil, errors.New("pesanan tidak memerlukan pembayaran QRIS")
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("order:qris:%s", order.ID.String())
	_ = s.redis.Del(ctx, cacheKey)

	// Update created_at to now
	order.CreatedAt = time.Now()
	order.UpdatedAt = time.Now()

	err = s.repo.Update(ctx, s.db, order)
	if err != nil {
		log.Printf("[QRIS-REFRESH] Failed to update Order %s in database: %v", orderID, err)
		return nil, err
	}

	// Populate QRIS Data (forces fresh call)
	s.populatePaymentInfo(ctx, order)

	return order, nil
}

func (s *service) GetMerchantOrders(ctx context.Context, ownerID uuid.UUID) ([]Order, error) {
	merch, err := s.merchantSvc.GetMerchantByOwnerID(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var orders []Order
	err = s.db.NewSelect().
		Model(&orders).
		Where("merchant_id = ?", merch.ID).
		Order("created_at DESC").
		Scan(ctx)
	return orders, err
}

func (s *service) MerchantAcceptOrder(ctx context.Context, orderID, ownerID uuid.UUID) error {
	merch, err := s.merchantSvc.GetMerchantByOwnerID(ctx, ownerID)
	if err != nil {
		return err
	}
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order.MerchantID == nil || *order.MerchantID != merch.ID {
		return errors.New("pesanan ini bukan milik merchant Anda")
	}
	if order.Status != StatusPending {
		return errors.New("pesanan tidak berada dalam status menunggu konfirmasi")
	}
	if order.PaymentStatus != PaymentEscrow && order.PaymentMethod != MethodCOD {
		return errors.New("pembayaran pesanan belum diselesaikan")
	}

	order.Status = StatusCooking
	order.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	// Trigger runner matching now that merchant accepted
	s.matchingSvc.EnqueueMatching(orderID)

	// Send notification to Penitip
	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:  order.RequesterID,
		Title:   "Pesanan Sedang Dimasak",
		Message: fmt.Sprintf("Merchant telah menerima pesanan Anda dan sedang menyiapkan makanan: %s", order.ItemDetails),
		Type:    "order",
		Metadata: map[string]interface{}{"order_id": order.ID},
	})
	if s.fcm != nil && config.App.FcmEnabled {
		reqUser, _ := s.userSvc.GetByID(ctx, order.RequesterID, order.RequesterID)
		if reqUser != nil && reqUser.FcmToken != nil && *reqUser.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *reqUser.FcmToken, "Pesanan Mulai Dimasak",
				fmt.Sprintf("Merchant sedang menyiapkan pesanan Anda: %s", order.ItemDetails),
				map[string]string{"order_id": order.ID.String()})
		}
	}

	return nil
}

func (s *service) MerchantReadyOrder(ctx context.Context, orderID, ownerID uuid.UUID) error {
	merch, err := s.merchantSvc.GetMerchantByOwnerID(ctx, ownerID)
	if err != nil {
		return err
	}
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order.MerchantID == nil || *order.MerchantID != merch.ID {
		return errors.New("pesanan ini bukan milik merchant Anda")
	}
	if order.Status != StatusCooking && order.Status != StatusAccepted {
		return errors.New("pesanan tidak berada dalam proses memasak atau belum diterima runner")
	}

	order.Status = StatusReady
	order.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	// Notify Penitip
	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:  order.RequesterID,
		Title:   "Makanan Siap Diambil",
		Message: fmt.Sprintf("Pesanan Anda di %s sudah selesai disiapkan!", merch.Name),
		Type:    "order",
		Metadata: map[string]interface{}{"order_id": order.ID},
	})
	if s.fcm != nil && config.App.FcmEnabled {
		reqUser, _ := s.userSvc.GetByID(ctx, order.RequesterID, order.RequesterID)
		if reqUser != nil && reqUser.FcmToken != nil && *reqUser.FcmToken != "" {
			_ = s.fcm.SendToDevice(ctx, *reqUser.FcmToken, "Makanan Siap Diambil",
				fmt.Sprintf("Pesanan Anda di %s sudah selesai disiapkan!", merch.Name),
				map[string]string{"order_id": order.ID.String()})
		}
	}

	// Notify Runner if assigned
	if order.RunnerID != nil {
		_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
			UserID:  *order.RunnerID,
			Title:   "Pesanan Siap Diambil",
			Message: fmt.Sprintf("Makanan untuk pesanan %s siap diambil di %s.", order.ItemDetails, merch.Name),
			Type:    "order",
			Metadata: map[string]interface{}{"order_id": order.ID},
		})
		if s.fcm != nil && config.App.FcmEnabled {
			runUser, _ := s.userSvc.GetByID(ctx, *order.RunnerID, *order.RunnerID)
			if runUser != nil && runUser.FcmToken != nil && *runUser.FcmToken != "" {
				_ = s.fcm.SendToDevice(ctx, *runUser.FcmToken, "Pesanan Siap Diambil",
					fmt.Sprintf("Silakan ambil pesanan %s di %s.", order.ItemDetails, merch.Name),
					map[string]string{"order_id": order.ID.String()})
			}
		}
	}

	return nil
}
