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
	mathrand "math/rand"
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
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/internal/utils"
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
	EscalateMatching(ctx context.Context, orderID uuid.UUID) error
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
		MenuID           uuid.UUID   `json:"menu_id" validate:"required"`
		Quantity         int         `json:"quantity" validate:"required,gt=0"`
		Notes            string      `json:"notes,omitempty"`
		VariantOptionID  *uuid.UUID  `json:"variant_option_id,omitempty"`
		ToppingOptionIDs []uuid.UUID `json:"topping_option_ids,omitempty"`
		VariantLabel     string      `json:"variant_label,omitempty"`
		ToppingLabels    []string    `json:"topping_labels,omitempty"`
		PriceDelta       float64     `json:"price_delta,omitempty"`
		ImageURL         string      `json:"image_url,omitempty"`
	} `json:"items,omitempty"`

	// Nitip Kirim Fields
	ServiceCategory string `json:"service_category" validate:"required,oneof=beli kirim"`
	ReceiverName    string `json:"receiver_name"`
	ReceiverPhone   string `json:"receiver_phone"`
	DeliveryName    string `json:"delivery_name"`
	DeliveryAddress string `json:"delivery_address"`

	// Order Type Selection
	OrderType string `json:"order_type" validate:"omitempty,oneof=instant regular"`

	// Promotion
	PromotionCode *string `json:"promotion_code,omitempty"`
}

type EstimateFeeRequest struct {
	PickupLat     float64    `json:"pickup_lat"     validate:"required"`
	PickupLng     float64    `json:"pickup_lng"     validate:"required"`
	DeliveryLat   float64    `json:"delivery_lat"   validate:"required"`
	DeliveryLng   float64    `json:"delivery_lng"   validate:"required"`
	WeightKg      float64    `json:"weight_kg"      validate:"required,min=0"`
	VolumeLiters  float64    `json:"volume_liters"  validate:"required,min=0"`
	OrderType     string     `json:"order_type"     validate:"omitempty,oneof=instant regular"`
	PromotionCode *string    `json:"promotion_code,omitempty"`
	MerchantID    *uuid.UUID `json:"merchant_id,omitempty"`
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
	GetByUser(ctx context.Context, userID uuid.UUID, limit, offset int, startDate, endDate string) ([]Order, error)
	AcceptOrder(ctx context.Context, orderID, runnerID uuid.UUID) error
	PickupOrder(ctx context.Context, orderID, runnerID uuid.UUID) error
	CancelOrder(ctx context.Context, orderID, userID uuid.UUID, reason string) error
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

	// Runner reassign (Food priority cooking/ready)
	RunnerCancelForReassign(ctx context.Context, orderID, runnerID uuid.UUID, reason string) error
	ReassignOrder(ctx context.Context, orderID, runnerID uuid.UUID, reason string) error

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

	// Realtime pool
	SetPoolBroadcaster(b PoolBroadcaster)
	SetPromotionService(ps PromotionService)
	SetFCMDispatcher(d FCMDispatcher)
	CheckProximity(ctx context.Context, lat, lng float64, merchantID *uuid.UUID, serviceCategory string) (int, error)
}

type PoolBroadcaster interface {
	BroadcastNewOrder(order *Order)
	BroadcastClaimed(orderID string, runnerID string, pickupLat, pickupLng float64)
	BroadcastCancelled(orderID string, reason string, pickupLat, pickupLng float64)
	BroadcastMerchantEvent(merchantID string, eventType string, order *Order)
	BroadcastOrderStatus(orderID string, status string, eventType string)
}

type FCMDispatcher interface {
	Enqueue(ctx context.Context, job notification.Job) error
}

type service struct {
	repo          Repository
	userSvc       user.Service
	tripRepo      trip.Repository
	matchingSvc   Matcher
	walletSvc     wallet.Service
	configSvc     systemconfig.Service
	fcm           notification.Notifier
	fcmDispatcher FCMDispatcher
	notifSvc      notifDomain.Service
	redis         *cache.Redis
	db            *bun.DB
	auditSvc      audit.Service
	storage       storage.Storage
	merchantSvc   merchant.Service
	promotionSvc  PromotionService // optional, nil guard for minimal impact
	paymentQueue  chan PaymentJob
	paymentOnce   sync.Once
	poolHub       PoolBroadcaster
}

// PromotionService is an interface to avoid import cycle with promotion domain
type PromotionService interface {
	ValidateAndReserveForOrder(ctx context.Context, tx bun.IDB, code string, merchantID *uuid.UUID, userID uuid.UUID, itemTotal, deliveryTotal, total float64) (any, float64, error)
	ApplyUsage(ctx context.Context, tx bun.IDB, promoID, orderID, userID uuid.UUID, merchantID *uuid.UUID, discountAmount, originalAmount float64) error
	ReleaseUsage(ctx context.Context, tx bun.IDB, orderID uuid.UUID) error
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

func (s *service) SetPoolBroadcaster(b PoolBroadcaster) {
	s.poolHub = b
}

func (s *service) SetPromotionService(ps PromotionService) {
	s.promotionSvc = ps
}

func (s *service) SetFCMDispatcher(d FCMDispatcher) {
	s.fcmDispatcher = d
}

func (s *service) enqueueFCM(ctx context.Context, userID uuid.UUID, title, body, notifType string, data map[string]string, collapseID string, high bool) {
	// Always inbox first (BE only minimal impact)
	_ = s.notifSvc.CreateNotification(ctx, notifDomain.CreateNotificationRequest{
		UserID:   userID,
		Title:    title,
		Message:  body,
		Type:     notifType,
		Metadata: map[string]interface{}{"data": data, "collapse_id": collapseID},
	})

	job := notification.Job{
		UserID:     userID,
		Title:      title,
		Body:       body,
		Type:       notifType,
		Data:       data,
		CollapseID: collapseID,
		Priority:   notification.PriorityNormal,
	}
	if high {
		job.Priority = notification.PriorityHigh
	}

	if s.fcmDispatcher != nil {
		_ = s.fcmDispatcher.Enqueue(ctx, job)
		return
	}
	// Fallback direct FCM with collapse
	if s.fcm != nil && config.App.FcmEnabled {
		go func() {
			bgCtx := context.Background()
			u, _ := s.userSvc.GetByID(bgCtx, userID, userID)
			if u != nil && u.FcmToken != nil && *u.FcmToken != "" {
				if collapseID != "" {
					if fc, ok := s.fcm.(interface {
						SendToDeviceWithCollapse(ctx context.Context, token, title, body string, data map[string]string, collapseID string) error
					}); ok {
						_ = fc.SendToDeviceWithCollapse(bgCtx, *u.FcmToken, title, body, data, collapseID)
						return
					}
				}
				_ = s.fcm.SendToDevice(bgCtx, *u.FcmToken, title, body, data)
			}
		}()
	}
}

func (s *service) Create(ctx context.Context, requesterID uuid.UUID, req CreateOrderRequest) (*Order, error) {
	// --- Concurrency Guard: Redis Lock for Merchant ---
	if req.MerchantID != nil {
		lockKey := fmt.Sprintf("lock:merchant:order:%s", req.MerchantID.String())
		lockToken, lockErr := s.redis.AcquireLock(ctx, lockKey, 3*time.Second)
		if lockErr != nil || lockToken == "" {
			return nil, errors.New("merchant sedang memproses pesanan lain, silakan coba beberapa saat lagi")
		}
		defer func() { _ = s.redis.ReleaseLock(ctx, lockKey, lockToken) }()
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

		// Validate items + varian ± & topping + image
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
			// Base price
			unitPrice := menu.Price
			// Price delta dari varian ± (boleh minus)
			unitPrice += it.PriceDelta
			// Topping labels already included in PriceDelta from FE? FE calc base+variant+toppings, sends PriceDelta = variantDelta + sum(toppings). We trust FE delta but clamp >=0
			if unitPrice < 0 {
				unitPrice = 0
			}
			calculatedCost += unitPrice * float64(it.Quantity)

			// Build options snapshot with image info
			options := map[string]interface{}{
				"variant_option_id":  it.VariantOptionID,
				"variant_label":      it.VariantLabel,
				"topping_option_ids": it.ToppingOptionIDs,
				"topping_labels":     it.ToppingLabels,
				"price_delta":        it.PriceDelta,
				"image_url":          it.ImageURL,
			}

			orderItems = append(orderItems, merchant.OrderItem{
				ID:               uuid.New(),
				MenuID:           it.MenuID,
				Quantity:         it.Quantity,
				Notes:            it.Notes,
				PriceAtPurchase:  unitPrice,
				Options:          options,
				VariantOptionID:  it.VariantOptionID,
				ToppingOptionIDs: it.ToppingOptionIDs,
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

	// === Merchant Fee Audit (Opsi A: merchant bayar, bukan buyer) ===
	var merchantFeeAudit float64
	var merchantFeeTierAudit int
	var foodOriginalAudit float64
	if req.MerchantID != nil {
		t1Limit, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier1_limit", "50000"), 64)
		t2Limit, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier2_limit", "100000"), 64)
		t1Amount, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier1_amount", "1000"), 64)
		t2Amount, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier2_amount", "3000"), 64)
		t3Amount, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier3_amount", "5000"), 64)
		foodOriginalAudit = req.EstimatedCost
		if foodOriginalAudit < t1Limit {
			merchantFeeAudit = t1Amount
			merchantFeeTierAudit = 1
		} else if foodOriginalAudit <= t2Limit {
			merchantFeeAudit = t2Amount
			merchantFeeTierAudit = 2
		} else {
			merchantFeeAudit = t3Amount
			merchantFeeTierAudit = 3
		}
		if merchantFeeAudit > foodOriginalAudit {
			merchantFeeAudit = foodOriginalAudit
		}
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

		// 2. COD Restriction for Non-KYC (except regular shipping/delivery)
		isRegular := (req.OrderType == "regular" || req.OrderType == "") && req.MerchantID == nil
		if req.PaymentMethod == "cod" && !isRegular {
			return nil, errors.New("metode pembayaran COD hanya tersedia untuk pengguna yang telah terverifikasi e-KYC")
		}
	}

	// 3. General COD Rules (Enabled flag + Distance & Amount)
	if req.PaymentMethod == "cod" {
		enabledStr := s.configSvc.GetValue(ctx, "cod_enabled", "true")
		enabledStr = strings.ToLower(strings.TrimSpace(enabledStr))
		if enabledStr == "false" || enabledStr == "0" || enabledStr == "off" || enabledStr == "disabled" {
			return nil, errors.New("metode pembayaran COD sedang dinonaktifkan oleh admin")
		}

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

	// 2. Determine Order Type
	orderType := TypeRegular
	if req.MerchantID != nil {
		// Nitip-Food otomatis menggunakan mode instant
		orderType = TypeInstant
	} else {
		switch req.OrderType {
		case "instant":
			orderType = TypeInstant
		case "regular":
			orderType = TypeRegular
		default:
			// Fallback: Jika tidak dikirim FE, gunakan logika deteksi jarak default
			if distance <= 5.0 {
				orderType = TypeInstant
			}
		}
	}

	// 3. Calculate Delivery Fee automatically (now includes 10% platform markup + checking fee)
	// Untuk nitip-food (merchant_id ada): hanya ongkos kirim, tidak ada biaya pengecekan — sesuai request user
	// Food = instant, no checking fee, delivery fee konsisten dengan pengecekan awal 10k
	isFood := req.MerchantID != nil
	deliveryFee := s.calculateDeliveryFee(ctx, distance, req.WeightKg, req.VolumeLiters, orderType)
	if isFood {
		// Food: hitung ulang tanpa checking fee agar konsisten dengan keranjang 10k
		// calculateDeliveryFee includes checking, so subtract it for food display consistency
		feeStrTmp := s.configSvc.GetValue(ctx, "order_checking_fee", "5000")
		chkTmp, _ := strconv.ParseFloat(feeStrTmp, 64)
		deliveryFee = deliveryFee - chkTmp
		if deliveryFee < 3000 {
			deliveryFee = 3000
		}
		// Pembulatan 500
		deliveryFee = math.Ceil(deliveryFee/500) * 500
	}

	// Fetch checking fee for storage — untuk food 0
	feeStr := s.configSvc.GetValue(ctx, "order_checking_fee", "5000")
	checkingFee, _ := strconv.ParseFloat(feeStr, 64)
	if isFood {
		checkingFee = 0 // food tidak ada biaya pengecekan
	}

	// Extract ServiceFee (Platform markup applies to base fee, excluding checking fee)
	feePercentStr2 := s.configSvc.GetValue(ctx, "platform_fee_percent", "10")
	feePercent2, _ := strconv.ParseFloat(feePercentStr2, 64)
	feeMultiplier2 := 1 + (feePercent2 / 100)
	baseWithMarkup := deliveryFee - checkingFee
	serviceFee := baseWithMarkup - (baseWithMarkup / feeMultiplier2)
	// Untuk food, serviceFee ditanggung merchant — tetap disimpan untuk settlement, tapi tidak tampil ke requester

	completionCode, err := generateCompletionCode()
	if err != nil {
		return nil, fmt.Errorf("gagal membuat kode konfirmasi: %w", err)
	}

	paymentSource := req.PaymentSource
	if paymentSource == "" {
		paymentSource = "wallet"
	}

	order := &Order{
		ID:                 uuid.New(),
		RequesterID:        requesterID,
		ItemDetails:        req.ItemDetails,
		ReceiverName:       receiverName,
		ReceiverPhone:      receiverPhone,
		PickupLat:          req.PickupLat,
		PickupLng:          req.PickupLng,
		PickupName:         req.PickupName,
		PickupAddress:      req.PickupAddress,
		DeliveryLat:        req.DeliveryLat,
		DeliveryLng:        req.DeliveryLng,
		DeliveryName:       req.DeliveryName,
		DeliveryAddress:    req.DeliveryAddress,
		EstimatedCost:      req.EstimatedCost,
		DeliveryFee:        deliveryFee,
		PaymentMethod:      req.PaymentMethod,
		PaymentSource:      paymentSource,
		PaymentStatus:      PaymentUnpaid,
		Status:             StatusPending,
		WeightKg:           req.WeightKg,
		VolumeLiters:       req.VolumeLiters,
		ServiceFee:         serviceFee,
		CheckingFee:        checkingFee,
		OrderType:          orderType,
		DistanceKm:         distance,
		ServiceCategory:    req.ServiceCategory,
		CompletionCode:     completionCode,
		MerchantID:         req.MerchantID,
		MerchantFee:        merchantFeeAudit,
		MerchantFeeTier:    merchantFeeTierAudit,
		FoodAmountOriginal: foodOriginalAudit,
		CreatedAt:          now,
		UpdatedAt:          now,
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
		if false {
			order.Status = StatusCooking
		}
	}

	// Keep original total for discount audit
	originalTotalForAudit := order.TotalPayment

	// --- 4. Transactional Create & Escrow Hold + Promotion Reserve (Food only + wallet/qris only) ---
	// Prioritas voucher hanya untuk Nitip-Food (terafiliasi merchant). Nitip-Beli (non-merchant) tidak bisa.
	// Dan voucher hanya bisa wallet/qris, jika COD maka peringatan + dikosongkan (tidak bisa digunakan)
	if req.PromotionCode != nil && *req.PromotionCode != "" {
		if req.MerchantID == nil {
			// Tidak terafiliasi merchant -> Nitip-Beli Regular tidak bisa pakai voucher (prioritas Food only)
			return nil, errors.New("voucher hanya berlaku untuk Nitip Food (terafiliasi merchant), tidak bisa untuk Nitip Beli/Kirim")
		}
		if req.PaymentMethod == MethodCOD {
			// COD tidak bisa pakai voucher - harus warning dan dikosongkan
			return nil, errors.New("voucher tidak dapat digunakan dengan metode COD. Silakan pilih Wallet atau QRIS dan voucher akan dikosongkan")
		}
	}

	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// A. Apply Promotion Discount if any (inside same Tx for FOR UPDATE safety) - Food only
		if s.promotionSvc != nil {
			promoCode := ""
			if req.PromotionCode != nil {
				promoCode = *req.PromotionCode
			}
			// Prioritas Food Only: hanya jika merchant_id ada (terafiliasi merchant)
			// Jika tidak ada merchant_id (Nitip Beli/Kirim) -> skip promo, tidak bisa voucher
			isFoodOrder := req.MerchantID != nil
			if !isFoodOrder && promoCode != "" {
				return errors.New("voucher hanya untuk Nitip Food")
			}
			// Only attempt if food order (merchant affiliated) and (code provided or auto)
			if isFoodOrder && (promoCode != "" || req.MerchantID != nil) {
				itemTotal := req.EstimatedCost
				deliveryTotal := order.DeliveryFee
				total := order.TotalPayment

				promoObj, discountAmt, err := s.promotionSvc.ValidateAndReserveForOrder(ctx, tx, promoCode, req.MerchantID, requesterID, itemTotal, deliveryTotal, total)
				if err != nil {
					// If auto promo not found, graceful no discount (don't fail)
					if promoCode == "" && (err.Error() == "tidak ada promo aktif" || err.Error() == "tidak ada promo auto aktif") {
						// no auto promo, proceed without discount
					} else {
						return err
					}
				} else if promoObj != nil && discountAmt > 0 {
					// Apply discount to order
					order.DiscountAmount = discountAmt
					originalTotalForAudit = order.TotalPayment
					order.OriginalTotal = &originalTotalForAudit
					order.TotalPayment = math.Max(0, order.TotalPayment-discountAmt)

					if promoID := extractPromoID(promoObj); promoID != uuid.Nil {
						order.PromotionID = &promoID
						if dt := extractPromoDiscountType(promoObj); dt != "" {
							order.DiscountType = &dt
						}
						if pc := extractPromoCode(promoObj); pc != "" {
							order.PromotionCode = pc
						}
					}
				}
			}
		}

		// B. Create Order Record (after discount calc so TotalPayment already reduced)
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

		// C. Insert promotion usage if discount applied
		if s.promotionSvc != nil && order.PromotionID != nil && order.DiscountAmount > 0 {
			if err := s.promotionSvc.ApplyUsage(ctx, tx, *order.PromotionID, order.ID, requesterID, req.MerchantID, order.DiscountAmount, originalTotalForAudit); err != nil {
				return fmt.Errorf("gagal mencatat penggunaan promo: %w", err)
			}
		}

		// D. Balance Check & Hold for Escrow (Wallet only) - now with discounted TotalPayment
		if order.PaymentMethod == "escrow" && order.PaymentSource == "wallet" {
			w, err := s.walletSvc.GetBalance(ctx, requesterID)
			if err != nil {
				return fmt.Errorf("gagal mengecek saldo dompet: %v", err)
			}
			if w.Balance < order.TotalPayment {
				return fmt.Errorf("saldo tidak mencukupi. Saldo Anda: Rp %.0f, Total Biaya: Rp %.0f (diskon Rp %.0f)", w.Balance, order.TotalPayment, order.DiscountAmount)
			}

			if err := s.walletSvc.HoldEscrow(ctx, tx, requesterID, order.ID, order.TotalPayment); err != nil {
				return fmt.Errorf("gagal mengunci saldo: %v", err)
			}

			order.PaymentStatus = PaymentEscrow

			if false {
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

	// Audit Log + Merchant Fee Audit Level
	auditPayload := order
	if order.MerchantID != nil {
		// Include fee audit details in log for transparency
		s.auditSvc.Log(ctx, &requesterID, audit.ActionOrderCreate, "order", order.ID.String(), nil, map[string]interface{}{
			"order_id":             order.ID.String(),
			"merchant_id":          order.MerchantID.String(),
			"food_amount_original": order.FoodAmountOriginal,
			"merchant_fee":         order.MerchantFee,
			"merchant_fee_tier":    order.MerchantFeeTier,
			"estimated_cost":       order.EstimatedCost,
			"total_payment":        order.TotalPayment,
			"note":                 "Opsi A: merchant bayar fee, buyer bayar murni, audit level",
		}, "", "")
	}
	s.auditSvc.Log(ctx, &requesterID, audit.ActionOrderCreate, "order", order.ID.String(), nil, auditPayload, "", "")

	// Trigger Smart Matching & Merchant Notifications only if order is PAID (escrow) or COD
	if order.PaymentStatus == PaymentEscrow || order.PaymentMethod == MethodCOD {
		if s.redis != nil {
			_ = s.redis.GeoAddOrder(ctx, order.ID.String(), order.PickupLat, order.PickupLng)
		}
		if order.MerchantID == nil {
			s.matchingSvc.EnqueueMatching(order.ID)
		} else if order.Status == StatusCooking {
			s.matchingSvc.EnqueueMatching(order.ID)

			// Unified notification via enqueueFCM (inbox + push)
			s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Baru Masuk (Otomatis)",
				fmt.Sprintf("Pesanan %s diterima otomatis. Silakan mulai masak!", order.ItemDetails),
				"order", map[string]string{"order_id": order.ID.String(), "type": "merchant_order"},
				fmt.Sprintf("order_%s", order.ID.String()), false)
		} else {
			s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Baru Masuk",
				fmt.Sprintf("Pesanan %s membutuhkan konfirmasi Anda.", order.ItemDetails),
				"order", map[string]string{"order_id": order.ID.String(), "type": "merchant_order"},
				fmt.Sprintf("order_%s", order.ID.String()), false)
		}

		// --- Realtime Pool Broadcast (new) ---
		if s.poolHub != nil {
			// async non-blocking
			go s.poolHub.BroadcastNewOrder(order)
			if order.MerchantID != nil {
				go s.poolHub.BroadcastMerchantEvent(order.MerchantID.String(), "order_created", order)
			}
		}
		if s.redis != nil {
			_, _ = s.redis.IncrCounter(context.Background(), "orders:created")
			_, _ = s.redis.IncrCounter(context.Background(), "events:total")
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

// Promotion helpers via reflection to avoid import cycle
func extractPromoID(obj interface{}) uuid.UUID {
	if obj == nil {
		return uuid.Nil
	}
	// Try interface method
	if m, ok := obj.(interface{ GetID() uuid.UUID }); ok {
		return m.GetID()
	}
	return extractFieldUUID(obj, "ID")
}

func extractPromoDiscountType(obj interface{}) string {
	return extractFieldString(obj, "DiscountType")
}

func extractPromoCode(obj interface{}) string {
	s := extractFieldStringPtr(obj, "Code")
	if s != nil {
		return *s
	}
	return ""
}

func extractFieldUUID(obj interface{}, field string) uuid.UUID {
	return extractUUIDReflect(obj, field)
}

func extractFieldString(obj interface{}, field string) string {
	return extractStringReflect(obj, field)
}

func extractFieldStringPtr(obj interface{}, field string) *string {
	return extractStringPtrReflect(obj, field)
}

func (s *service) EstimateFee(ctx context.Context, req EstimateFeeRequest) (*EstimateFeeResponse, error) {
	dist := geo.Haversine(req.PickupLat, req.PickupLng, req.DeliveryLat, req.DeliveryLng)

	orderType := TypeRegular
	switch req.OrderType {
	case "instant":
		orderType = TypeInstant
	case "regular":
		orderType = TypeRegular
	default:
		// Fallback: Jika tidak dikirim FE, gunakan logika jarak default
		if dist <= 5.0 {
			orderType = TypeInstant
		}
	}

	isFood := req.MerchantID != nil
	fee := s.calculateDeliveryFee(ctx, dist, req.WeightKg, req.VolumeLiters, orderType)
	if isFood {
		// Food: hitung ulang tanpa checking fee agar konsisten dengan keranjang 10k
		feeStrTmp := s.configSvc.GetValue(ctx, "order_checking_fee", "5000")
		chkTmp, _ := strconv.ParseFloat(feeStrTmp, 64)
		fee = fee - chkTmp
		if fee < 3000 {
			fee = 3000
		}
		// Pembulatan 500
		fee = math.Ceil(fee/500) * 500
	}

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
	if err != nil {
		return orders, err
	}
	s.bulkPopulateOrders(ctx, orders)
	return orders, nil
}

func (s *service) GetByRunner(ctx context.Context, runnerID uuid.UUID) ([]Order, error) {
	orders, err := s.repo.FindByRunnerID(ctx, runnerID)
	if err != nil {
		return orders, err
	}
	s.bulkPopulateOrders(ctx, orders)
	return orders, nil
}

func (s *service) GetByUser(ctx context.Context, userID uuid.UUID, limit, offset int, startDate, endDate string) ([]Order, error) {
	orders, err := s.repo.FindByUserID(ctx, userID, limit, offset, startDate, endDate)
	if err != nil {
		return orders, err
	}
	s.bulkPopulateOrders(ctx, orders)
	return orders, nil
}

// bulkPopulateOrders P1 FIX: N+1 -> bulk fetch runners + reviews + signed URLs cached per request
// Before: N=15 => 30 queries + 15 crypto presign. After: 3 queries + 15 cached presign with map
func (s *service) bulkPopulateOrders(ctx context.Context, orders []Order) {
	if len(orders) == 0 {
		return
	}
	// Collect runner IDs unique
	runnerIDSet := make(map[uuid.UUID]bool)
	var runnerIDs []uuid.UUID
	for _, o := range orders {
		if o.RunnerID != nil && !runnerIDSet[*o.RunnerID] {
			runnerIDSet[*o.RunnerID] = true
			runnerIDs = append(runnerIDs, *o.RunnerID)
		}
	}
	runnerMap := make(map[uuid.UUID]*user.User)
	if len(runnerIDs) > 0 {
		// use userSvc bulk FindByIDs
		if users, err := s.userSvc.GetByIDs(ctx, runnerIDs); err == nil {
			for i := range users {
				runnerMap[users[i].ID] = &users[i]
			}
		}
	}
	// Bulk reviews WHERE order_id IN (...)
	type reviewRow struct {
		OrderID uuid.UUID `bun:"order_id"`
		Rating  int       `bun:"runner_rating"`
		Comment string    `bun:"runner_comment"`
	}
	var revRows []reviewRow
	orderIDs := make([]uuid.UUID, len(orders))
	for i, o := range orders {
		orderIDs[i] = o.ID
	}
	_ = s.db.NewSelect().
		Table("reviews").
		Column("order_id", "runner_rating", "runner_comment").
		Where("order_id IN (?)", bun.List(orderIDs)).
		Where("runner_rating IS NOT NULL").
		Scan(ctx, &revRows)
	reviewMap := make(map[uuid.UUID]reviewRow)
	for _, r := range revRows {
		reviewMap[r.OrderID] = r
	}
	// Cache signed URLs per key per request to avoid duplicate HMAC
	signedCache := make(map[string]string)

	for i := range orders {
		// runner info from bulk map
		if orders[i].RunnerID != nil {
			if ru, ok := runnerMap[*orders[i].RunnerID]; ok && ru != nil {
				orders[i].RunnerName = ru.Name
				orders[i].RunnerPhone = ru.WhatsappNumber
				orders[i].RunnerLastLat = ru.LastLat
				orders[i].RunnerLastLng = ru.LastLng
				// try redis track override still per order (1 Redis GET) — low cost vs DB
				if s.redis != nil {
					if val, err := s.redis.Get(ctx, "runner:track:"+ru.ID.String()); err == nil && val != "" {
						var lat, lng float64
						var ts int64
						if _, scanErr := fmt.Sscanf(val, "%f,%f,%d", &lat, &lng, &ts); scanErr == nil {
							orders[i].RunnerLastLat = &lat
							orders[i].RunnerLastLng = &lng
						}
					}
				}
			}
		}
		// review from bulk map
		if rv, ok := reviewMap[orders[i].ID]; ok {
			rating := rv.Rating
			orders[i].FeedbackRating = &rating
			orders[i].FeedbackComment = rv.Comment
		}
		// payment info (keep existing logic but avoid DB hit, uses cache)
		s.populatePaymentInfo(ctx, &orders[i])
		// sign URLs with per-request cache
		s.signURLsCached(ctx, &orders[i], signedCache)
	}
}

// signURLsCached version with cache map to avoid duplicate HMAC presign per request — ensures https://upload.nihtip.com/
func (s *service) signURLsCached(ctx context.Context, o *Order, cache map[string]string) {
	if o == nil {
		return
	}
	sign := func(key string) string {
		if key == "" {
			return ""
		}
		if v, ok := cache[key]; ok {
			return v
		}
		// Already final CDN? keep
		if strings.HasPrefix(key, "https://upload.nihtip.com/") {
			cache[key] = key
			return key
		}
		sanitized := sanitizeStorageKey(key)
		if sanitized == "" {
			return key
		}
		if signed, err := s.storage.SignedURL(ctx, sanitized, 1*time.Hour); err == nil {
			cache[key] = signed
			return signed
		}
		return key
	}
	if o.ReceiptImageURL != "" {
		o.ReceiptImageURL = sign(o.ReceiptImageURL)
	}
	if o.DeliveryImageURL != "" {
		o.DeliveryImageURL = sign(o.DeliveryImageURL)
	}
	if o.DisputeProofURL != "" {
		o.DisputeProofURL = sign(o.DisputeProofURL)
	}
	// Order items image_url — ensure https://upload.nihtip.com/ too
	if o.Items != nil {
		for i := range o.Items {
			if o.Items[i].ImageURL != "" {
				o.Items[i].ImageURL = sign(o.Items[i].ImageURL)
			}
		}
	}
}

func (s *service) AcceptOrder(ctx context.Context, orderID, runnerID uuid.UUID) error {
	// --- Concurrency Guard: Redis Lock ---
	lockKey := fmt.Sprintf("lock:order:accept:%s", orderID.String())
	lockToken, lockErr := s.redis.AcquireLock(ctx, lockKey, 5*time.Second)
	if lockErr != nil || lockToken == "" {
		return errors.New("pesanan ini sedang diproses oleh sistem, silakan coba sesaat lagi")
	}
	defer func() { _ = s.redis.ReleaseLock(ctx, lockKey, lockToken) }()

	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	var expectedStatus string
	var newStatus string
	if order.MerchantID != nil {
		if order.Status != StatusMerchantAccepted && order.Status != StatusCooking && order.Status != StatusReady {
			return errors.New("pesanan merchant belum disetujui merchant atau sedang tidak dapat diambil")
		}
		expectedStatus = order.Status
		if order.Status == StatusMerchantAccepted {
			newStatus = StatusCooking
		} else {
			newStatus = order.Status
		}
	} else {
		if order.Status != StatusPending {
			return errors.New("pesanan sudah tidak dalam status menunggu")
		}
		expectedStatus = StatusPending
		newStatus = StatusAccepted
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

	// Check minimum balance requirement based on runner verification status
	var minBalanceRequired float64
	if r.IsVerified {
		minBalanceStr := s.configSvc.GetValue(ctx, "runner_min_balance_verified", "0")
		minBalanceRequired, _ = strconv.ParseFloat(minBalanceStr, 64)
	} else {
		minBalanceStr := s.configSvc.GetValue(ctx, "runner_min_balance_unverified", "10000")
		minBalanceRequired, _ = strconv.ParseFloat(minBalanceStr, 64)
	}

	w, err := s.walletSvc.GetBalance(ctx, runnerID)
	if err != nil {
		return fmt.Errorf("gagal mengecek saldo dompet: %v", err)
	}

	if w.Balance < minBalanceRequired {
		return fmt.Errorf("saldo dompet Anda (Rp %.0f) kurang dari batas minimal untuk mengambil pesanan (Rp %.0f)", w.Balance, minBalanceRequired)
	}

	if order.RequesterID == runnerID {
		return errors.New("tidak dapat menerima pesanan Anda sendiri")
	}

	// Check if runner already has another active order
	activeCount, err := s.db.NewSelect().
		Model((*Order)(nil)).
		Where("runner_id = ?", runnerID).
		Where("status NOT IN (?, ?, ?, ?)", StatusCompleted, StatusCancelled, StatusExpired, StatusDisputed).
		Count(ctx)
	if err == nil && activeCount > 0 {
		return errors.New("tidak dapat menerima pesanan baru: Anda masih memiliki pesanan aktif yang belum diselesaikan")
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

		// 1b. Hold Runner Liability (Deposit)
		// Only hold liability for unverified runners! Verified runners do not hold balance.
		if order.EstimatedCost > 0 && !r.IsVerified {
			if err := s.walletSvc.HoldLiability(ctx, tx, runnerID, order.ID, order.EstimatedCost); err != nil {
				return err
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
		order.Status = newStatus
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
		// Track claim conflict for metrics if race detected
		if strings.Contains(err.Error(), "diambil oleh runner lain") {
			if s.redis != nil {
				ctxT, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, _ = s.redis.IncrCounter(ctxT, "claim:conflict")
				cancel()
			}
			// also hub internal counter if broadcaster supports it
			if bc, ok := s.poolHub.(interface{ IncrConflict() }); ok {
				bc.IncrConflict()
			}
		}
		return err
	}

	// --- Realtime: broadcast claimed to remove from pool + order status push ---
	if s.poolHub != nil {
		go s.poolHub.BroadcastClaimed(orderID.String(), runnerID.String(), order.PickupLat, order.PickupLng)
		go s.poolHub.BroadcastOrderStatus(orderID.String(), newStatus, "order_status")
	}
	if s.redis != nil {
		_ = s.redis.GeoRemoveOrder(ctx, orderID.String())
		ctxT, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = s.redis.IncrCounter(ctxT, "claim:success")
		_, _ = s.redis.IncrCounter(ctxT, "events:total")
		cancel()
	}

	// Audit Log
	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderAccept, "order", orderID.String(), map[string]interface{}{"status": oldStatus}, map[string]interface{}{"status": newStatus, "runner_id": runnerID}, "", "")

	s.enqueueFCM(ctx, order.RequesterID, "Pesanan Diterima",
		fmt.Sprintf("Runner sedang memproses pesanan Anda (%s) - %s", order.ItemDetails, order.ID.String()),
		"order", map[string]string{"order_id": order.ID.String()},
		fmt.Sprintf("order_%s", order.ID.String()), true)

	// Notify Merchant Owner if runner accepts confirmed order
	if order.MerchantID != nil {
		merch, err := s.merchantSvc.GetMerchantByID(ctx, *order.MerchantID)
		if err == nil && merch != nil {
			s.enqueueFCM(ctx, merch.OwnerID, "Runner Menuju Toko",
				fmt.Sprintf("Runner %s telah menerima pesanan: %s. Silakan mulai menyiapkan makanan!", r.Name, order.ItemDetails),
				"order", map[string]string{"order_id": order.ID.String(), "type": "merchant_order"},
				fmt.Sprintf("order_%s", order.ID.String()), false)
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

	// Realtime push delivering
	if s.poolHub != nil {
		go s.poolHub.BroadcastOrderStatus(orderID.String(), StatusDelivering, "order_status")
	}

	s.enqueueFCM(ctx, order.RequesterID, "Pesanan Dalam Perjalanan",
		fmt.Sprintf("Runner sedang menuju lokasi Anda untuk pesanan %s", order.ItemDetails),
		"order", map[string]string{"order_id": order.ID.String(), "type": "order_delivering"},
		fmt.Sprintf("order_%s", order.ID.String()), true)

	return nil
}

func (s *service) CancelOrder(ctx context.Context, orderID, userID uuid.UUID, reason string) error {
	ord, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if ord.Status == StatusCompleted || ord.Status == StatusCancelled {
		return errors.New("pesanan sudah selesai atau dibatalkan")
	}

	// 1. Determine caller role
	isRequester := ord.RequesterID == userID
	isRunner := ord.RunnerID != nil && *ord.RunnerID == userID
	isMerchantOwner := false
	if ord.MerchantID != nil {
		merch, err := s.merchantSvc.GetMerchantByID(ctx, *ord.MerchantID)
		if err == nil && merch != nil && merch.OwnerID == userID {
			isMerchantOwner = true
		}
	}

	if !isRequester && !isRunner && !isMerchantOwner {
		return errors.New("akses ditolak: hanya pihak terkait pesanan yang dapat membatalkan")
	}

	// 2. Check if normal cancellation by requester
	isNormalCancel := false
	if isRequester {
		if ord.MerchantID != nil { // Nitip Food
			if ord.Status == StatusPending || ord.Status == StatusMerchantAccepted {
				isNormalCancel = true
			}
		} else if ord.ServiceCategory == CategoryKirim { // Titip Kirim
			if ord.Status == StatusPending || ord.Status == StatusAccepted {
				isNormalCancel = true
			}
		} else { // Titip Beli
			if ord.Status == StatusPending || ord.Status == StatusAccepted {
				isNormalCancel = true
			}
		}
	}

	// Detect runner pre-pickup reassign scenario (Food priority: cooking/ready/merchant_accepted/accepted/pending before delivering/purchasing)
	isRunnerPrePickupReassign := false
	if isRunner {
		switch ord.Status {
		case StatusPending, StatusMerchantAccepted, StatusCooking, StatusReady, StatusAccepted:
			// Food: cooking/ready must be reassign not cancel (bahaya dapur sudah masak)
			// Beli/Kirim: pending/accepted before pickup also reassign
			isRunnerPrePickupReassign = true
		}
	}

	// 3. Conditional cancel with strict guard for runner/merchant (prod fraud protection)
	// For reassign case, we relax 30m guard? We allow immediate reassign with reason, but still require reason
	// Keep 30m guard only for final cancel, not for reassign. For reassign we just need reason.
	if !isNormalCancel {
		if isRunner && isRunnerPrePickupReassign {
			// Allow immediate reassign if reason provided, no 30m wait, but reason mandatory
			if strings.TrimSpace(reason) == "" {
				return errors.New("alasan pembatalan/pengalihan wajib diisi")
			}
			// Continue to reassign flow below (skip 30m check)
		} else {
			// Runner/Merchant only allowed to cancel before goods purchased / delivering
			if isRunner || isMerchantOwner {
				if ord.Status == StatusPurchasing || ord.Status == StatusDelivering || ord.Status == StatusCompleted || ord.Status == StatusCancelled {
					return errors.New("pesanan dalam tahap pembelian/pengiriman tidak dapat dibatalkan oleh runner/merchant, hubungi admin")
				}
			}
			if time.Since(ord.UpdatedAt) <= 30*time.Minute {
				return errors.New("pembatalan tidak diizinkan kecuali status pesanan stagnan (tidak berubah) lebih dari 30 menit")
			}
			if strings.TrimSpace(reason) == "" {
				return errors.New("alasan pembatalan (reason) wajib diisi")
			}
		}
	}

	// If runner pre-pickup -> do reassign instead of cancel
	if isRunnerPrePickupReassign {
		return s.runnerCancelForReassign(ctx, ord, userID, reason)
	}

	// Logic: Charge checking fee if status is PURCHASING or if there's an adjustment
	shouldChargeFee := ord.Status == StatusPurchasing || ord.AdjustmentStatus != ""

	// --- Unified Cancellation Transaction with FOR UPDATE to prevent double refund race ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Re-fetch with lock inside TX to prevent concurrent cancel/complete race
		lockedOrd, err := s.repo.FindByIDForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if lockedOrd.Status == StatusCompleted || lockedOrd.Status == StatusCancelled || lockedOrd.Status == StatusExpired {
			return errors.New("pesanan sudah selesai atau dibatalkan (race)")
		}
		// Use locked version for status checks
		ord = lockedOrd
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

		// Release Runner Liability Hold
		if ord.RunnerID != nil && ord.EstimatedCost > 0 {
			if err := s.walletSvc.ReleaseLiability(ctx, tx, *ord.RunnerID, ord.ID, ord.EstimatedCost); err != nil {
				return err
			}
		}

		// Restore Capacity if runner was assigned
		if ord.RunnerID != nil && ord.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters); err != nil {
				return errors.New("gagal memulihkan kapasitas perjalanan")
			}
		}

		// Release promotion usage if any
		if s.promotionSvc != nil && ord.PromotionID != nil {
			_ = s.promotionSvc.ReleaseUsage(ctx, tx, ord.ID)
		}

		oldStatus := ord.Status
		ord.Status = StatusCancelled
		if reason != "" {
			ord.DisputeReason = reason
		}
		ord.UpdatedAt = time.Now()
		_, updErr := tx.NewUpdate().Model(ord).WherePK().Exec(ctx)
		if updErr == nil {
			s.auditSvc.LogWithDB(ctx, tx, &userID, audit.ActionOrderCancel, "order", orderID.String(), map[string]interface{}{"status": oldStatus}, map[string]interface{}{"status": StatusCancelled, "reason": reason}, "", "")
		}
		return updErr
	})

	if err == nil {
		if ord != nil && ord.UniqueCode > 0 && s.redis != nil {
			baseAmt := ord.TotalPayment - ord.PGFee
			cacheKey := fmt.Sprintf("active_uniq:%.2f:%d", baseAmt, ord.UniqueCode)
			_ = s.redis.Del(ctx, cacheKey)
		}
		// Notify other parties about cancellation (unified via enqueueFCM)
		if isRequester {
			if ord.RunnerID != nil {
				s.enqueueFCM(ctx, *ord.RunnerID, "Pesanan Dibatalkan",
					fmt.Sprintf("Pesanan %s dibatalkan oleh penitip. Alasan: %s", ord.ItemDetails, reason),
					"order", map[string]string{"order_id": ord.ID.String(), "type": "order_cancelled"},
					fmt.Sprintf("order_%s", ord.ID.String()), true)
			}
			if ord.MerchantID != nil {
				merch, _ := s.merchantSvc.GetMerchantByID(ctx, *ord.MerchantID)
				if merch != nil {
					s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Dibatalkan",
						fmt.Sprintf("Pesanan %s dibatalkan oleh pelanggan. Alasan: %s", ord.ItemDetails, reason),
						"order", map[string]string{"order_id": ord.ID.String(), "type": "merchant_order"},
						fmt.Sprintf("order_%s", ord.ID.String()), true)
				}
			}
		}
		if isRunner {
			s.enqueueFCM(ctx, ord.RequesterID, "Pesanan Dibatalkan Runner",
				fmt.Sprintf("Runner membatalkan pesanan %s. Alasan: %s", ord.ItemDetails, reason),
				"order", map[string]string{"order_id": ord.ID.String(), "type": "order_cancelled"},
				fmt.Sprintf("order_%s", ord.ID.String()), true)
			if ord.MerchantID != nil {
				merch, _ := s.merchantSvc.GetMerchantByID(ctx, *ord.MerchantID)
				if merch != nil {
					s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Dibatalkan Runner",
						fmt.Sprintf("Runner membatalkan pesanan %s. Alasan: %s", ord.ItemDetails, reason),
						"order", map[string]string{"order_id": ord.ID.String(), "type": "merchant_order"},
						fmt.Sprintf("order_%s", ord.ID.String()), true)
				}
			}
		}
		if isMerchantOwner {
			s.enqueueFCM(ctx, ord.RequesterID, "Pesanan Dibatalkan Merchant",
				fmt.Sprintf("Merchant membatalkan pesanan %s. Alasan: %s", ord.ItemDetails, reason),
				"order", map[string]string{"order_id": ord.ID.String(), "type": "order_cancelled"},
				fmt.Sprintf("order_%s", ord.ID.String()), true)
			if ord.RunnerID != nil {
				s.enqueueFCM(ctx, *ord.RunnerID, "Pesanan Merchant Dibatalkan",
					fmt.Sprintf("Merchant membatalkan pesanan %s: %s", ord.ItemDetails, reason),
					"order", map[string]string{"order_id": ord.ID.String(), "type": "order_cancelled"},
					fmt.Sprintf("order_%s", ord.ID.String()), true)
			}
		}
		// Realtime: remove cancelled order from pool
		if s.poolHub != nil {
			go s.poolHub.BroadcastCancelled(ord.ID.String(), reason, ord.PickupLat, ord.PickupLng)
			if ord.MerchantID != nil {
				go s.poolHub.BroadcastMerchantEvent(ord.MerchantID.String(), "order_cancelled", ord)
			}
		}
		if s.redis != nil {
			_ = s.redis.GeoRemoveOrder(ctx, ord.ID.String())
		}
	}

	return err
}

// runnerCancelForReassign: Runner cancel pre-pickup -> reassign to nearest online runner instead of final cancelled
// Food priority: cooking/ready/merchant_accepted must keep same status to avoid merchant rework, clear runner, re-queue
func (s *service) runnerCancelForReassign(ctx context.Context, ord *Order, runnerID uuid.UUID, reason string) error {
	// Check reassign limit via Redis counter
	reassignKey := fmt.Sprintf("reassign:count:%s", ord.ID.String())
	var reassignCount int
	if s.redis != nil {
		if val, err := s.redis.Client().Get(ctx, reassignKey).Int(); err == nil {
			reassignCount = val
		}
		// If already >=2, fallback to final cancel
		if reassignCount >= 2 {
			// Final cancel after 2 reassigns
			return s.finalCancelAfterReassignLimit(ctx, ord, runnerID, reason)
		}
	}

	oldStatus := ord.Status
	oldRunnerID := ord.RunnerID

	// Tx: clear runner, release liability, restore capacity, keep status
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked, err := s.repo.FindByIDForUpdate(ctx, tx, ord.ID)
		if err != nil {
			return err
		}
		if locked.Status == StatusCompleted || locked.Status == StatusCancelled || locked.Status == StatusExpired {
			return errors.New("pesanan sudah selesai atau dibatalkan (race)")
		}
		// Ensure still same runner
		if locked.RunnerID == nil || *locked.RunnerID != runnerID {
			return errors.New("anda bukan runner untuk pesanan ini (race)")
		}
		ord = locked
		oldStatus = ord.Status

		// Release Runner Liability Hold
		if ord.RunnerID != nil && ord.EstimatedCost > 0 {
			if err := s.walletSvc.ReleaseLiability(ctx, tx, *ord.RunnerID, ord.ID, ord.EstimatedCost); err != nil {
				return err
			}
		}
		// Restore Capacity
		if ord.RunnerID != nil && ord.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters); err != nil {
				return errors.New("gagal memulihkan kapasitas perjalanan")
			}
		}

		// Clear runner fields, keep status same (cooking/ready/merchant_accepted/accepted/pending)
		// Do NOT refund escrow, DO NOT release promotion
		ord.RunnerID = nil
		ord.TripID = nil
		ord.UpdatedAt = time.Now()
		// For non-food pending/accepted keep same, food cooking/ready keep same to preserve merchant work
		// Do not set DisputeReason as cancel, use separate field for reassign reason? Use DisputeReason for audit temporary
		if reason != "" {
			ord.DisputeReason = fmt.Sprintf("Runner cancel reassign [%s]: %s", oldRunnerID, reason)
		}

		_, updErr := tx.NewUpdate().Model(ord).WherePK().Exec(ctx)
		if updErr == nil {
			s.auditSvc.LogWithDB(ctx, tx, &runnerID, audit.ActionOrderReassign, "order", ord.ID.String(),
				map[string]interface{}{"status": oldStatus, "runner_id": oldRunnerID, "reason": reason},
				map[string]interface{}{"status": ord.Status, "runner_id": nil, "reassign_count": reassignCount + 1, "reason": reason}, "", "")
		}
		return updErr
	})

	if err != nil {
		return err
	}

	// Incr reassign counter
	if s.redis != nil {
		_ = s.redis.Client().Incr(ctx, reassignKey).Err()
		_ = s.redis.Client().Expire(ctx, reassignKey, 24*time.Hour).Err()
		_, _ = s.redis.IncrCounter(ctx, "reassign:count")
		_, _ = s.redis.IncrCounter(ctx, "events:total")
	}

	// Re-queue: GeoAdd + EnqueueMatching + BroadcastNewOrder
	if s.redis != nil {
		_ = s.redis.GeoAddOrder(ctx, ord.ID.String(), ord.PickupLat, ord.PickupLng)
	}
	if s.matchingSvc != nil {
		s.matchingSvc.EnqueueMatching(ord.ID)
	}
	if s.poolHub != nil {
		go s.poolHub.BroadcastNewOrder(ord)
		go s.poolHub.BroadcastOrderStatus(ord.ID.String(), oldStatus, "reassigned")
		if ord.MerchantID != nil {
			go s.poolHub.BroadcastMerchantEvent(ord.MerchantID.String(), "order_requeued", ord)
		}
	}

	// Unified via enqueueFCM
	s.enqueueFCM(ctx, ord.RequesterID, "Mencari Runner Pengganti",
		fmt.Sprintf("Runner membatalkan pesanan %s, kami cari pengganti terdekat.", ord.ItemDetails),
		"order", map[string]string{"order_id": ord.ID.String(), "type": "order_reassigned"},
		fmt.Sprintf("order_%s", ord.ID.String()), true)

	if ord.MerchantID != nil {
		merch, _ := s.merchantSvc.GetMerchantByID(ctx, *ord.MerchantID)
		if merch != nil {
			s.enqueueFCM(ctx, merch.OwnerID, "Runner Batal - Tetap Masak",
				fmt.Sprintf("Runner batal pesanan %s, tetap lanjut masak, kami cari pengganti.", ord.ItemDetails),
				"order", map[string]string{"order_id": ord.ID.String(), "type": "merchant_order_requeued"},
				fmt.Sprintf("order_%s", ord.ID.String()), true)
		}
	}

	s.enqueueFCM(ctx, runnerID, "Pesanan Dialihkan ke Runner Lain",
		fmt.Sprintf("Pembatalan pesanan %s diterima, pesanan akan dialihkan ke runner terdekat yang online. Alasan: %s", ord.ItemDetails, reason),
		"order", map[string]string{"order_id": ord.ID.String(), "type": "order_reassigned"},
		fmt.Sprintf("order_%s", ord.ID.String()), false)

	return nil
}

func (s *service) finalCancelAfterReassignLimit(ctx context.Context, ord *Order, runnerID uuid.UUID, reason string) error {
	// Final cancel after 2 reassigns limit reached
	// Do full refund + release promo + etc similar to CancelOrder final
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked, err := s.repo.FindByIDForUpdate(ctx, tx, ord.ID)
		if err != nil {
			return err
		}
		if locked.Status == StatusCompleted || locked.Status == StatusCancelled {
			return errors.New("pesanan sudah selesai atau dibatalkan")
		}
		ord = locked
		if ord.PaymentMethod == MethodEscrow && ord.PaymentStatus == PaymentEscrow {
			totalEscrow := ord.EstimatedCost + ord.DeliveryFee
			if err := s.walletSvc.RefundEscrow(ctx, tx, ord.RequesterID, ord.ID, totalEscrow); err != nil {
				return err
			}
			ord.PaymentStatus = PaymentRefunded
		}
		if ord.RunnerID != nil && ord.EstimatedCost > 0 {
			_ = s.walletSvc.ReleaseLiability(ctx, tx, *ord.RunnerID, ord.ID, ord.EstimatedCost)
		}
		if ord.RunnerID != nil && ord.TripID != nil {
			_ = s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters)
		}
		if s.promotionSvc != nil && ord.PromotionID != nil {
			_ = s.promotionSvc.ReleaseUsage(ctx, tx, ord.ID)
		}
		oldStatus := ord.Status
		ord.Status = StatusCancelled
		ord.DisputeReason = fmt.Sprintf("Reassign limit exceeded (2x) final cancel by %s: %s", runnerID.String(), reason)
		ord.UpdatedAt = time.Now()
		_, err = tx.NewUpdate().Model(ord).WherePK().Exec(ctx)
		if err == nil {
			s.auditSvc.LogWithDB(ctx, tx, &runnerID, audit.ActionOrderCancel, "order", ord.ID.String(),
				map[string]interface{}{"status": oldStatus, "reassign_limit": 2},
				map[string]interface{}{"status": StatusCancelled, "reason": ord.DisputeReason}, "", "")
		}
		return err
	})
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

	// Compress <1MB with bounded concurrency (Lighthouse 2C4G app 512MB) + anti-penumpukan delete old
	oldReceipt := order.ReceiptImageURL
	compressed, compSize, compErr := fileutil.CompressToLimit(receiptReader, 1200, fileutil.DefaultMaxUpload)
	if compErr != nil {
		return fmt.Errorf("gagal mengompresi gambar kwitansi: %w", compErr)
	}
	if compSize > fileutil.DefaultMaxUpload {
		return fmt.Errorf("kwitansi masih >1MB (%dKB), coba foto lebih kecil", compSize/1024)
	}

	// Cache-busting: unik per upload agar CDN https://upload.nihtip.com/ tidak serve file lama saat re-upload
	objectKey := fmt.Sprintf("orders/%s/receipt_%s_%d.jpg", orderID.String(), uuid.New().String()[:8], time.Now().UnixNano())
	path, err := s.storage.Upload(ctx, objectKey, compressed, compSize, "image/jpeg")
	if err != nil {
		return fmt.Errorf("gagal mengunggah kwitansi ke penyimpanan: %w", err)
	}

	order.Status = StatusPurchasing
	order.ReceiptImageURL = path
	order.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, s.db, order); err != nil {
		// Cleanup new file if DB update fails
		_ = s.storage.Delete(ctx, objectKey)
		return err
	}

	// Anti-penumpukan: hapus old receipt jika re-upload dengan nama baru unik
	if oldReceipt != "" && oldReceipt != path {
		// Use sanitize from storageutil if available, else fileutil fallback
		// For order, we have own sanitizeStorageKey func in same file — use it
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldReceipt))
	}

	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderPurchased, "order", orderID.String(),
		map[string]interface{}{"status": StatusAccepted},
		map[string]interface{}{"status": StatusPurchasing, "receipt_image_url": path}, "", "")

	// Missing push: notify requester that runner submitted receipt (beli)
	s.enqueueFCM(ctx, order.RequesterID, "Pembelian Selesai - Menunggu Pickup",
		fmt.Sprintf("Runner telah membeli pesanan %s. Kwitansi diunggah.", order.ItemDetails),
		"order", map[string]string{"order_id": order.ID.String(), "type": "order_purchased"},
		fmt.Sprintf("order_%s", order.ID.String()), true)

	if s.poolHub != nil {
		go s.poolHub.BroadcastOrderStatus(orderID.String(), StatusPurchasing, "purchased")
	}

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

	isForceComplete := time.Since(order.UpdatedAt) > 30*time.Minute
	if !isForceComplete {
		if order.CompletionCode != code {
			return errors.New("kode konfirmasi salah")
		}
	} else {
		// Prod hardening: force complete (>30m) must still have delivery proof + still check code if provided
		// If code provided, it must be correct; if not provided, require delivery proof and mark as force
		if code != "" && order.CompletionCode != code {
			return errors.New("kode konfirmasi salah (mode force)")
		}
		if deliveryReader == nil {
			return errors.New("foto bukti penyerahan wajib diunggah untuk menyelesaikan pesanan tanpa kode PIN/QR (mode force)")
		}
		// Force flag will be audited in transaction
	}

	var path string
	var oldDelivery string
	if deliveryReader != nil {
		oldDelivery = order.DeliveryImageURL
		// Compress <1MB bounded concurrency + anti-penumpukan
		compressed, compSize, compErr := fileutil.CompressToLimit(deliveryReader, 1200, fileutil.DefaultMaxUpload)
		if compErr != nil {
			return fmt.Errorf("gagal mengompresi bukti penyerahan: %w", compErr)
		}
		if compSize > fileutil.DefaultMaxUpload {
			return fmt.Errorf("bukti penyerahan masih >1MB (%dKB), coba foto lebih kecil", compSize/1024)
		}

		objectKey := fmt.Sprintf("orders/%s/delivery_%s_%d.jpg", orderID.String(), uuid.New().String()[:8], time.Now().UnixNano())
		path, err = s.storage.Upload(ctx, objectKey, compressed, compSize, "image/jpeg")
		if err != nil {
			return fmt.Errorf("gagal mengunggah bukti penyerahan ke penyimpanan: %w", err)
		}
		// Anti-penumpukan: hapus old delivery jika re-upload (best-effort after DB tx success, so save for later)
		_ = oldDelivery
	}

	// --- Unified Completion Transaction with FOR UPDATE anti double release ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked, err := s.repo.FindByIDForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if locked.Status != StatusDelivering {
			return errors.New("pesanan tidak dapat diselesaikan dari status saat ini (race), harus delivering")
		}
		if locked.RunnerID == nil || *locked.RunnerID != runnerID {
			return errors.New("anda bukan runner untuk pesanan ini (race)")
		}
		order = locked
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

		// 3. Release Runner Liability (Deposit)
		if order.RunnerID != nil && order.EstimatedCost > 0 {
			if err := s.walletSvc.ReleaseLiability(ctx, tx, *order.RunnerID, order.ID, order.EstimatedCost); err != nil {
				return err
			}
		}

		if path != "" {
			order.DeliveryImageURL = path
		}
		order.Status = StatusCompleted
		order.UpdatedAt = time.Now()

		if err := s.repo.Update(ctx, tx, order); err != nil {
			return err
		}

		// Audit Log (Transactional) with force flag for prod fraud detection
		s.auditSvc.LogWithDB(ctx, tx, &runnerID, audit.ActionOrderComplete, "order", orderID.String(), nil, map[string]interface{}{"status": StatusCompleted, "delivery_image_url": path, "is_force": isForceComplete, "has_code": code != ""}, "", "")

		return nil
	})

	if err == nil {
		// Anti-penumpukan: hapus old delivery jika re-upload dengan nama baru unik cache-busting
		if oldDelivery != "" && path != "" && oldDelivery != path {
			_ = s.storage.Delete(ctx, sanitizeStorageKey(oldDelivery))
		}
		if s.poolHub != nil {
			go s.poolHub.BroadcastOrderStatus(orderID.String(), StatusCompleted, "completed")
		}
		s.enqueueFCM(ctx, order.RequesterID, "Pesanan Selesai",
			fmt.Sprintf("Pesanan %s selesai! Beri ulasan sekarang.", order.ItemDetails),
			"order", map[string]string{"order_id": order.ID.String(), "type": "order_completed"},
			fmt.Sprintf("order_%s", order.ID.String()), true)
		if order.MerchantID != nil {
			merch, err := s.merchantSvc.GetMerchantByID(ctx, *order.MerchantID)
			if err == nil && merch != nil {
				s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Selesai",
					fmt.Sprintf("Pesanan %s telah selesai dan dana telah masuk ke saldo Anda.", order.ItemDetails),
					"order", map[string]string{"order_id": order.ID.String(), "type": "order_completed"},
					fmt.Sprintf("order_%s", order.ID.String()), true)
			}
		}
	}

	return err
}

func (s *service) RunnerCancelForReassign(ctx context.Context, orderID, runnerID uuid.UUID, reason string) error {
	// Public wrapper calls internal runnerCancelForReassign after fetching order
	ord, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}
	if ord.RunnerID == nil || *ord.RunnerID != runnerID {
		return errors.New("anda bukan runner untuk pesanan ini")
	}
	return s.runnerCancelForReassign(ctx, ord, runnerID, reason)
}

func (s *service) ReassignOrder(ctx context.Context, orderID, runnerID uuid.UUID, reason string) error {
	return s.RunnerCancelForReassign(ctx, orderID, runnerID, reason)
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
			if orderObj != nil && orderObj.UniqueCode > 0 && s.redis != nil {
				baseAmt := orderObj.TotalPayment - orderObj.PGFee
				cacheKey := fmt.Sprintf("active_uniq:%.2f:%d", baseAmt, orderObj.UniqueCode)
				_ = s.redis.Del(ctx, cacheKey)
			}
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
		if s.redis != nil && orderObj != nil {
			_ = s.redis.GeoAddOrder(ctx, orderID.String(), orderObj.PickupLat, orderObj.PickupLng)
		}
		if orderObj != nil && orderObj.MerchantID != nil {
			merch, err := s.merchantSvc.GetMerchantByID(ctx, *orderObj.MerchantID)
			if err == nil {
				if false {
					orderObj.Status = StatusCooking
					_ = s.repo.Update(ctx, s.db, orderObj)
					s.matchingSvc.EnqueueMatching(orderID)
					if s.poolHub != nil {
						go s.poolHub.BroadcastNewOrder(orderObj)
					}
					s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Baru Masuk (Otomatis)",
						fmt.Sprintf("Pesanan %s diterima otomatis. Silakan mulai masak!", orderObj.ItemDetails),
						"order", map[string]string{"order_id": orderObj.ID.String(), "type": "merchant_order"},
						fmt.Sprintf("order_%s", orderObj.ID.String()), false)
				} else {
					s.enqueueFCM(ctx, merch.OwnerID, "Pesanan Baru Masuk",
						fmt.Sprintf("Pesanan %s membutuhkan konfirmasi Anda.", orderObj.ItemDetails),
						"order", map[string]string{"order_id": orderObj.ID.String(), "type": "merchant_order"},
						fmt.Sprintf("order_%s", orderObj.ID.String()), false)
				}
			}
		} else {
			s.matchingSvc.EnqueueMatching(orderID)
			if s.poolHub != nil {
				go s.poolHub.BroadcastNewOrder(orderObj)
			}
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
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. Handle Escrow Refund if applicable
		if order.PaymentMethod == MethodEscrow && order.PaymentStatus == PaymentEscrow {
			totalEscrow := order.EstimatedCost + order.DeliveryFee
			if err := s.walletSvc.RefundEscrow(ctx, tx, order.RequesterID, orderID, totalEscrow); err != nil {
				return errors.New("gagal mengembalikan dana escrow: " + err.Error())
			}
			order.PaymentStatus = PaymentRefunded
		}

		// 1b. Release Runner Liability
		if order.RunnerID != nil && order.EstimatedCost > 0 {
			if err := s.walletSvc.ReleaseLiability(ctx, tx, *order.RunnerID, orderID, order.EstimatedCost); err != nil {
				return err
			}
		}

		// 2. Restore Capacity
		if order.RunnerID != nil && order.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *order.TripID, order.WeightKg, order.VolumeLiters); err != nil {
				return errors.New("gagal memulihkan kapasitas perjalanan")
			}
		}

		// Release promotion usage
		if s.promotionSvc != nil && order.PromotionID != nil {
			_ = s.promotionSvc.ReleaseUsage(ctx, tx, orderID)
		}

		order.Status = StatusCancelled
		order.UpdatedAt = time.Now()

		return s.repo.Update(ctx, tx, order)
	})
	if err == nil && s.redis != nil {
		_ = s.redis.GeoRemoveOrder(ctx, orderID.String())
		if order != nil && order.UniqueCode > 0 {
			baseAmt := order.TotalPayment - order.PGFee
			cacheKey := fmt.Sprintf("active_uniq:%.2f:%d", baseAmt, order.UniqueCode)
			_ = s.redis.Del(ctx, cacheKey)
		}
	}
	return err
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
	if err == nil && order.RunnerID != nil {
		s.enqueueFCM(ctx, *order.RunnerID, "Pesanan Disengketakan",
			fmt.Sprintf("Penitip membuka sengketa untuk pesanan %s. Alasan: %s", order.ItemDetails, reason),
			"order", map[string]string{"order_id": order.ID.String(), "type": "order_disputed"},
			fmt.Sprintf("order_%s", order.ID.String()), true)
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

				// Release Runner Liability
				if order.EstimatedCost > 0 {
					if err := s.walletSvc.ReleaseLiability(ctx, tx, *order.RunnerID, orderID, order.EstimatedCost); err != nil {
						return err
					}
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
	if err == nil {
		msg := "Sengketa pesanan telah diselesaikan oleh Admin."
		if side == user.RoleRequester {
			msg += " Dana dikembalikan ke Penitip."
		} else {
			msg += " Dana dilepaskan ke Runner."
		}
		s.enqueueFCM(ctx, order.RequesterID, "Sengketa Selesai",
			msg+fmt.Sprintf(" Pesanan: %s", order.ItemDetails),
			"order", map[string]string{"order_id": order.ID.String(), "type": "dispute_resolved"},
			fmt.Sprintf("order_%s", order.ID.String()), true)
		if order.RunnerID != nil {
			s.enqueueFCM(ctx, *order.RunnerID, "Sengketa Selesai",
				msg+fmt.Sprintf(" Pesanan: %s", order.ItemDetails),
				"order", map[string]string{"order_id": order.ID.String(), "type": "dispute_resolved"},
				fmt.Sprintf("order_%s", order.ID.String()), true)
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

	hasTrip := params.HasActiveTrip && params.RadiusKm > 0
	hasProximity := params.IsAcceptingOrders && params.RunnerLat != 0 && params.RunnerLng != 0
	hasOnlineNoGeo := params.IsAcceptingOrders && !hasProximity && !hasTrip

	if !hasTrip && !hasProximity && !hasOnlineNoGeo {
		// Offline & no trip -> empty to save DB
		return []Order{}, nil
	}

	if (hasTrip || hasProximity) && s.redis != nil {
		var nearbyIDs []uuid.UUID
		if hasProximity {
			if ids, err := s.redis.GeoSearchOrders(ctx, params.RunnerLat, params.RunnerLng, 15.0); err == nil {
				for _, idStr := range ids {
					if id, parseErr := uuid.Parse(idStr); parseErr == nil {
						nearbyIDs = append(nearbyIDs, id)
					}
				}
			}
		}
		if hasTrip {
			if ids, err := s.redis.GeoSearchOrders(ctx, params.OriginLat, params.OriginLng, params.RadiusKm); err == nil {
				for _, idStr := range ids {
					if id, parseErr := uuid.Parse(idStr); parseErr == nil {
						nearbyIDs = append(nearbyIDs, id)
					}
				}
			}
			if ids, err := s.redis.GeoSearchOrders(ctx, params.DestLat, params.DestLng, params.RadiusKm); err == nil {
				for _, idStr := range ids {
					if id, parseErr := uuid.Parse(idStr); parseErr == nil {
						nearbyIDs = append(nearbyIDs, id)
					}
				}
			}
		}

		if len(nearbyIDs) == 0 {
			// No nearby orders in Redis GEO -> avoid hitting Postgres altogether!
			return []Order{}, nil
		}

		// Deduplicate IDs
		seen := make(map[uuid.UUID]bool)
		var uniqueIDs []uuid.UUID
		for _, id := range nearbyIDs {
			if !seen[id] {
				seen[id] = true
				uniqueIDs = append(uniqueIDs, id)
			}
		}
		params.IDs = uniqueIDs
	}

	// P1: debug prints removed to avoid docker json-file 10m*3 fill — use logger if needed with sampling

	orders, err := s.repo.FindAvailable(ctx, params)
	return orders, err
}

func (s *service) StartBackgroundCleanup(ctx context.Context) {
	expiryTicker := time.NewTicker(5 * time.Hour)
	geoTicker := time.NewTicker(1 * time.Hour)
	escalationTicker := time.NewTicker(1 * time.Minute)

	// Sync once on startup asynchronously
	go s.syncRedisGeoOrderPool(context.Background())

	go func() {
		for {
			select {
			case <-ctx.Done():
				expiryTicker.Stop()
				geoTicker.Stop()
				escalationTicker.Stop()
				return
			case <-expiryTicker.C:
				s.expireOldOrders(context.Background())
			case <-geoTicker.C:
				s.syncRedisGeoOrderPool(context.Background())
			case <-escalationTicker.C:
				s.escalateAndCancelUnassignedOrders(context.Background())
			}
		}
	}()
}

func (s *service) escalateAndCancelUnassignedOrders(ctx context.Context) {
	// 1. Eskalasi Pesanan Regular yang Belum Diambil > 30 Detik
	var regularOrders []Order
	thirtySecondsAgo := time.Now().Add(-30 * time.Second)
	err := s.db.NewSelect().
		Model(&regularOrders).
		Where("order_type = ?", TypeRegular).
		Where("status IN (?)", bun.List([]string{StatusPending, StatusCooking})).
		Where("runner_id IS NULL").
		Where("escalated_at IS NULL").
		Where("created_at < ?", thirtySecondsAgo).
		Scan(ctx)
	if err == nil {
		for _, o := range regularOrders {
			log.Printf("[matching] Escalating unassigned regular order: %s", o.ID)
			if err := s.matchingSvc.EscalateMatching(ctx, o.ID); err != nil {
				log.Printf("[matching] Failed to escalate regular order %s: %v", o.ID, err)
			}
		}
	} else {
		log.Printf("[matching-escalation] Error fetching regular orders for escalation: %v", err)
	}

	// 2. Batalkan Otomatis (Auto-Cancel) & Refund Pesanan > 15 Menit
	cancelMinutesStr := s.configSvc.GetValue(ctx, "order_auto_cancel_minutes", "15")
	cancelMinutes, err := strconv.Atoi(cancelMinutesStr)
	if err != nil || cancelMinutes <= 0 {
		cancelMinutes = 15
	}
	cutoffTime := time.Now().Add(-time.Duration(cancelMinutes) * time.Minute)

	var staleOrders []Order
	err = s.db.NewSelect().
		Model(&staleOrders).
		Where("status IN (?)", bun.List([]string{StatusPending, StatusMerchantAccepted, StatusCooking, StatusReady})).
		Where("runner_id IS NULL").
		Where("created_at < ?", cutoffTime).
		Scan(ctx)
	if err == nil {
		for _, o := range staleOrders {
			log.Printf("[order-autocancel] Auto-cancelling unassigned order: %s", o.ID)
			if err := s.ForceCancelOrder(ctx, o.ID); err != nil {
				log.Printf("[order-autocancel] Failed to auto-cancel order %s: %v", o.ID, err)
			} else {
				s.enqueueFCM(ctx, o.RequesterID, "Pesanan Dibatalkan Otomatis",
					fmt.Sprintf("Pesanan %s dibatalkan otomatis karena tidak ada Runner yang mengambil setelah %d menit.", o.ItemDetails, cancelMinutes),
					"order", map[string]string{"order_id": o.ID.String(), "type": "order_cancelled_expired"},
					fmt.Sprintf("order_%s", o.ID.String()), true)
			}
		}
	} else {
		log.Printf("[order-autocancel] Error fetching orders for auto-cancel: %v", err)
	}
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

func (s *service) syncRedisGeoOrderPool(ctx context.Context) {
	if s.redis == nil {
		return
	}

	limit := 500
	offset := 0
	activeMap := make(map[string]Order)
	totalActive := 0

	for {
		var activeOrders []Order
		err := s.db.NewSelect().
			Model(&activeOrders).
			Column("id", "pickup_lat", "pickup_lng").
			Where("status IN (?)", bun.List([]string{StatusPending, StatusMerchantAccepted, StatusAccepted, StatusCooking, StatusReady})).
			WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
				return q.Where("payment_status = ?", PaymentEscrow).
					WhereOr("payment_method = ?", MethodCOD)
			}).
			Limit(limit).
			Offset(offset).
			Scan(ctx)

		if err != nil {
			log.Printf("[GEO-SWEEPER] Gagal mendapatkan daftar order aktif dari DB: %v", err)
			return
		}

		if len(activeOrders) == 0 {
			break
		}

		for _, o := range activeOrders {
			activeMap[o.ID.String()] = o
			totalActive++
		}

		if len(activeOrders) < limit {
			break
		}
		offset += limit
	}

	redisIDs, err := s.redis.Client().ZRange(ctx, "orders:live", 0, -1).Result()
	if err != nil {
		log.Printf("[GEO-SWEEPER] Gagal membaca orders:live dari Redis: %v", err)
		return
	}

	for _, idStr := range redisIDs {
		if _, exists := activeMap[idStr]; !exists {
			_ = s.redis.GeoRemoveOrder(ctx, idStr)
			log.Printf("[GEO-SWEEPER] Menghapus order stale %s dari Redis GEO", idStr)
		}
	}

	for idStr, o := range activeMap {
		// FIX P0 #3 Geo swap bug: signature is (lat,lng), previous code passed (lng,lat) causing matching to break
		_ = s.redis.GeoAddOrder(ctx, idStr, o.PickupLat, o.PickupLng)
	}

	log.Printf("[GEO-SWEEPER] Sinkronisasi Redis GEO selesai. Jumlah order aktif: %d", totalActive)
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
	if err == nil {
		s.enqueueFCM(ctx, order.RequesterID, "Penyesuaian Harga",
			fmt.Sprintf("Runner meminta penyesuaian harga dari Rp %.0f menjadi Rp %.0f. Alasan: %s", order.EstimatedCost, adjustedCost, reason),
			"order", map[string]string{"order_id": order.ID.String(), "type": "price_adjustment"},
			fmt.Sprintf("order_%s", order.ID.String()), true)
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
	if ord.RunnerID != nil {
		s.enqueueFCM(ctx, *ord.RunnerID, "Penyesuaian Disetujui",
			fmt.Sprintf("Penitip menyetujui penyesuaian harga pesanan %s menjadi Rp %.0f", ord.ItemDetails, ord.EstimatedCost),
			"order", map[string]string{"order_id": ord.ID.String(), "type": "price_adjustment_approved"},
			fmt.Sprintf("order_%s", ord.ID.String()), true)
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

	if err == nil && ord.RunnerID != nil {
		title := "Penyesuaian Ditolak"
		msg := fmt.Sprintf("Penitip menolak penyesuaian harga pesanan %s", ord.ItemDetails)
		if cancelOrder {
			title = "Pesanan Dibatalkan Setelah Penyesuaian Ditolak"
			msg = fmt.Sprintf("Penitip menolak penyesuaian harga dan membatalkan pesanan %s", ord.ItemDetails)
		}
		s.enqueueFCM(ctx, *ord.RunnerID, title, msg,
			"order", map[string]string{"order_id": ord.ID.String(), "type": "price_adjustment_rejected"},
			fmt.Sprintf("order_%s", ord.ID.String()), true)
	}
	return err
}

func (s *service) calculateDeliveryFee(ctx context.Context, distance, weight, volume float64, orderType string) float64 {
	var totalFee float64

	if orderType == TypeInstant {
		// MODE INSTANT
		feeBase, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_short_base", "3000"), 64)
		feePerKM, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_short_per_km", "300"), 64)
		feePerKG, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "fee_short_per_kg", "2000"), 64)

		routeDistance := distance * 1.3
		totalFee = feeBase + (routeDistance * feePerKM) + (weight * feePerKG)
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

			// Strip query parameters (e.g. ?q-sign-algorithm=...)
			if qIdx := strings.Index(path, "?"); qIdx != -1 {
				path = path[:qIdx]
			}
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

		// Attempt to get live tracking coordinate from Redis first (to avoid hitting Postgres repeatedly)
		if s.redis != nil {
			val, redisErr := s.redis.Get(ctx, "runner:track:"+o.RunnerID.String())
			if redisErr == nil && val != "" {
				var lat, lng float64
				var ts int64
				if _, scanErr := fmt.Sscanf(val, "%f,%f,%d", &lat, &lng, &ts); scanErr == nil {
					o.RunnerLastLat = &lat
					o.RunnerLastLng = &lng
					return
				}
			}
		}

		// Fallback to database last known coordinates if not found in Redis
		o.RunnerLastLat = r.LastLat
		o.RunnerLastLng = r.LastLng
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
				Column("qris_data", "pg_fee", "unique_code", "total_payment").
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

	baseAmt := order.TotalPayment - order.PGFee

	if order.UniqueCode > 0 && s.redis != nil {
		oldKey := fmt.Sprintf("active_uniq:%.2f:%d", baseAmt, order.UniqueCode)
		_ = s.redis.Del(ctx, oldKey)
	}

	pgFeeStr := s.configSvc.GetValue(ctx, "qris_pg_fee", "0")
	configuredPGFee, _ := strconv.ParseFloat(pgFeeStr, 64)
	if configuredPGFee < 0 {
		configuredPGFee = 0
	}

	var uniqueCodeVal int
	if s.redis != nil {
		for i := 1; i <= 99; i++ {
			key := fmt.Sprintf("active_uniq:%.2f:%d", baseAmt, i)
			//nolint:staticcheck // s.redis.Client().SetNX is deprecated
			ok, err := s.redis.Client().SetNX(ctx, key, "active", 15*time.Minute).Result()
			if err == nil && ok {
				uniqueCodeVal = i
				break
			}
		}
	}
	if uniqueCodeVal == 0 {
		uniqueCodeVal = mathrand.Intn(99) + 1
	}
	uniqueCode := float64(uniqueCodeVal)
	pgFee := configuredPGFee + uniqueCode
	grossAmt := baseAmt + pgFee
	order.PGFee = pgFee
	order.UniqueCode = uniqueCodeVal
	order.TotalPayment = grossAmt

	if config.App.UsePaymentGateway {
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
					GrossAmt: int64(grossAmt),
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
				"amount":       int64(grossAmt),
			}
			body, _ := json.Marshal(payload)

			pgUrl := os.Getenv("PAYMENT_GATEWAY_URL")
			if pgUrl == "" {
				pgUrl = "http://localhost:4000"
			}

			log.Printf("[MOCK-QRIS-ORDER] Order: %s, GrossAmt: %d", order.ID.String(), int64(grossAmt))
			// P0 #10: http client timeout to prevent hang holding Fiber worker 5m
			httpClient := &http.Client{Timeout: 10 * time.Second}
			resp, err := httpClient.Post(fmt.Sprintf("%s/api/qris/generate", pgUrl), "application/json", bytes.NewBuffer(body))
			if err != nil {
				log.Printf("[MOCK-QRIS-ORDER-ERROR] Connection error: %v", err)
				return "", fmt.Errorf("gagal menghubungi payment gateway: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			respBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("[MOCK-QRIS-ORDER-ERROR] Read error: %v", err)
				return "", fmt.Errorf("gagal membaca respon payment gateway")
			}
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
	} else {
		// Generate dynamic QRIS locally from static template
		var err error
		qrString, err = utils.ConvertStaticToDynamicQRIS(config.App.StaticQrisTemplate, grossAmt)
		if err != nil {
			log.Printf("[LOCAL-QRIS-ORDER-ERROR] Failed to convert static QRIS: %v", err)
			return "", fmt.Errorf("gagal membuat kode pembayaran QRIS secara mandiri: %v", err)
		}
		log.Printf("[LOCAL-QRIS-ORDER] Generated dynamic QRIS locally for order: %s, GrossAmt: %f", order.ID.String(), grossAmt)
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
			// P0 #6 FIX: non-blocking send to prevent goroutine leak when request timeout receiver gone
			select {
			case job.ErrChan <- err:
			default:
				log.Printf("[PAYMENT-WORKER] ErrChan full/blocked for order %s, dropping err: %v", job.OrderID, err)
			}
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
		Where("status IN (?)", bun.List([]string{
			StatusPending,
			StatusMerchantAccepted,
			StatusCooking,
			StatusReady,
			StatusAccepted,
			StatusPurchasing,
			StatusDelivering,
			StatusCompleted,
		})).
		Where("payment_status IN (?) OR payment_method = ?", bun.List([]string{
			PaymentEscrow,
			PaymentReleased,
			PaymentRefunded,
			PaymentUnpaid,
		}), MethodCOD).
		Order("created_at DESC").
		Limit(100).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	// Enrich with order_items detail (variant & tambahan) for merchant view
	for i := range orders {
		items, err := s.merchantSvc.ListOrderItemsByOrderID(ctx, orders[i].ID)
		if err == nil && len(items) > 0 {
			var dtos []OrderItemDTO
			for _, it := range items {
				dto := OrderItemDTO{
					ID:              it.ID.String(),
					MenuID:          it.MenuID.String(),
					MenuName:        it.MenuName,
					Quantity:        it.Quantity,
					Notes:           it.Notes,
					PriceAtPurchase: it.PriceAtPurchase,
					Options:         it.Options,
				}
				if it.VariantOptionID != nil {
					s := it.VariantOptionID.String()
					dto.VariantOptionID = &s
				}
				if len(it.ToppingOptionIDs) > 0 {
					for _, tid := range it.ToppingOptionIDs {
						dto.ToppingOptionIDs = append(dto.ToppingOptionIDs, tid.String())
					}
				}
				if it.Options != nil {
					if v, ok := it.Options["variant_label"].(string); ok {
						dto.VariantLabel = v
					}
					if tl, ok := it.Options["topping_labels"].([]interface{}); ok {
						for _, t := range tl {
							if str, ok := t.(string); ok {
								dto.ToppingLabels = append(dto.ToppingLabels, str)
							}
						}
					} else if tl2, ok := it.Options["topping_labels"].([]string); ok {
						dto.ToppingLabels = tl2
					}
					if pd, ok := it.Options["price_delta"].(float64); ok {
						dto.PriceDelta = pd
					}
					if img, ok := it.Options["image_url"].(string); ok {
						dto.ImageURL = img
					}
				}
				dtos = append(dtos, dto)
			}
			orders[i].Items = dtos
		}
	}
	return orders, nil
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

	order.Status = StatusMerchantAccepted
	order.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	if s.poolHub != nil {
		go s.poolHub.BroadcastOrderStatus(orderID.String(), StatusMerchantAccepted, "order_status")
		go s.poolHub.BroadcastNewOrder(order)
	}

	s.matchingSvc.EnqueueMatching(orderID)
	s.enqueueFCM(ctx, order.RequesterID, "Pesanan Diterima Merchant",
		fmt.Sprintf("Merchant menyetujui pesanan Anda: %s. Menunggu runner menjemput.", order.ItemDetails),
		"order", map[string]string{"order_id": order.ID.String()},
		fmt.Sprintf("order_%s", order.ID.String()), true)
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
	if order.Status != StatusCooking {
		return errors.New("pesanan tidak berada dalam proses memasak (menunggu runner menerima pesanan)")
	}

	order.Status = StatusReady
	order.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	if s.poolHub != nil {
		go s.poolHub.BroadcastOrderStatus(orderID.String(), StatusReady, "order_status")
	}
	s.enqueueFCM(ctx, order.RequesterID, "Makanan Siap Diambil",
		fmt.Sprintf("Pesanan Anda di %s sudah selesai disiapkan!", merch.Name),
		"order", map[string]string{"order_id": order.ID.String()},
		fmt.Sprintf("order_%s", order.ID.String()), true)
	if order.RunnerID != nil {
		s.enqueueFCM(ctx, *order.RunnerID, "Pesanan Siap Diambil",
			fmt.Sprintf("Silakan ambil pesanan %s di %s.", order.ItemDetails, merch.Name),
			"order", map[string]string{"order_id": order.ID.String()},
			fmt.Sprintf("order_%s", order.ID.String()), true)
	}
	return nil
}

func (s *service) CheckProximity(ctx context.Context, lat, lng float64, merchantID *uuid.UUID, serviceCategory string) (int, error) {
	var radius float64
	var radiusStr string
	if merchantID != nil {
		radiusStr = s.configSvc.GetValue(ctx, "matching_radius_food", "5")
	} else if serviceCategory == CategoryBeli {
		radiusStr = s.configSvc.GetValue(ctx, "matching_radius_beli", "8")
	} else {
		radiusStr = s.configSvc.GetValue(ctx, "matching_radius_kirim", "8")
	}

	var err error
	radius, err = strconv.ParseFloat(radiusStr, 64)
	if err != nil {
		radius = 8.0
	}

	runners, err := s.matchingSvc.FindNearestRunners(ctx, lat, lng, radius)
	if err != nil {
		return 0, err
	}
	return len(runners), nil
}
