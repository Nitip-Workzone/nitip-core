package order

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"math"
	"strconv"
	"strings"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	"github.com/codecoffy/nitip-core/internal/infrastructure/storage"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/pkg/geo"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

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
	EstimatedCost float64 `json:"estimated_cost" validate:"required,min=0"`
	PaymentMethod string  `json:"payment_method" validate:"required,oneof=escrow cod"`
	WeightKg      float64 `json:"weight_kg"      validate:"required,min=0"`
	VolumeLiters  float64 `json:"volume_liters"  validate:"required,min=0"` // Frontend maps S/M/L to liters

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
	GetByUser(ctx context.Context, userID uuid.UUID) ([]Order, error)
	AcceptOrder(ctx context.Context, orderID, runnerID uuid.UUID) error
	PickupOrder(ctx context.Context, orderID, runnerID uuid.UUID) error
	CancelOrder(ctx context.Context, orderID, requesterID uuid.UUID) error
	SubmitPurchaseReceipt(ctx context.Context, orderID, runnerID uuid.UUID, receiptURL string) error
	CompleteOrder(ctx context.Context, orderID, runnerID uuid.UUID, code string, deliveryImageURL string) error
	UpdatePaymentStatus(ctx context.Context, orderID uuid.UUID, paymentStatus string) error
	GetAvailableOrders(ctx context.Context, runnerID uuid.UUID) ([]Order, error)
	EstimateFee(ctx context.Context, req EstimateFeeRequest) (*EstimateFeeResponse, error)

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
}

type service struct {
	repo        Repository
	userSvc     user.Service
	tripRepo    trip.Repository
	matchingSvc Matcher
	walletSvc   wallet.Service
	configSvc   systemconfig.Service
	fcm         notification.Notifier
	notifSvc    notifDomain.Service
	redis       *cache.Redis
	db          *bun.DB
	auditSvc    audit.Service
	storage     storage.Storage
}

func NewService(repo Repository, userSvc user.Service, tripRepo trip.Repository, matchingSvc Matcher, walletSvc wallet.Service, configSvc systemconfig.Service, fcm notification.Notifier, notifSvc notifDomain.Service, redis *cache.Redis, db *bun.DB, auditSvc audit.Service, storage storage.Storage) Service {
	return &service{
		repo:        repo,
		userSvc:     userSvc,
		tripRepo:    tripRepo,
		matchingSvc: matchingSvc,
		walletSvc:   walletSvc,
		configSvc:   configSvc,
		fcm:         fcm,
		notifSvc:    notifSvc,
		redis:       redis,
		db:          db,
		auditSvc:    auditSvc,
		storage:     storage,
	}
}

func (s *service) Create(ctx context.Context, requesterID uuid.UUID, req CreateOrderRequest) (*Order, error) {
	u, err := s.userSvc.GetByID(ctx, requesterID, requesterID)
	if err != nil {
		return nil, err
	}
	if u.Role != user.RoleRequester {
		return nil, errors.New("unauthorized: only users with requester role can create orders")
	}

	if u.IsSuspended {
		return nil, errors.New("cannot create order: your account is suspended")
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

	// Extract ServiceFee (Markup 10% only applies to base fee, excluding checking fee)
	// Example: (Total - CheckingFee) = Base * 1.1
	baseWithMarkup := deliveryFee - checkingFee
	serviceFee := baseWithMarkup - (baseWithMarkup / 1.1)

	completionCode, err := generateCompletionCode()
	if err != nil {
		return nil, fmt.Errorf("gagal membuat kode konfirmasi: %w", err)
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

	// --- 4. Transactional Create & Escrow Hold ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// A. Create Order Record FIRST (so Foreign Key exists)
		if err := s.repo.Create(ctx, tx, order); err != nil {
			return err
		}

		// B. Balance Check & Hold for Escrow
		if order.PaymentMethod == "escrow" {
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
			if err := s.repo.Update(ctx, tx, order); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Audit Log
	s.auditSvc.Log(ctx, &requesterID, audit.ActionOrderCreate, "order", order.ID.String(), nil, order, "", "")

	// Trigger Smart Matching via controlled Worker Pool
	s.matchingSvc.EnqueueMatching(order.ID)

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

	// 4. Sign URLs for privacy
	s.signURLs(ctx, order)
	return order, nil
}

func (s *service) GetByRequester(ctx context.Context, requesterID uuid.UUID) ([]Order, error) {
	orders, err := s.repo.FindByRequesterID(ctx, requesterID)
	if err == nil {
		for i := range orders {
			s.populateRunnerInfo(ctx, &orders[i])
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
			s.signURLs(ctx, &orders[i])
		}
	}
	return orders, err
}

func (s *service) GetByUser(ctx context.Context, userID uuid.UUID) ([]Order, error) {
	orders, err := s.repo.FindByUserID(ctx, userID)
	if err == nil {
		for i := range orders {
			s.populateRunnerInfo(ctx, &orders[i])
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

	if order.Status != StatusPending {
		return errors.New("order is no longer pending")
	}

	r, err := s.userSvc.GetByID(ctx, runnerID, runnerID)
	if err != nil {
		return err
	}
	if r.IsSuspended {
		return errors.New("cannot accept order: your account is suspended")
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
		return errors.New("cannot accept your own order")
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
		return errors.New("cannot accept order: you must have an active trip or be in 'Online' mode")
	}

	// Validate Capacity (only if trip exists)
	if activeTrip != nil {
		if activeTrip.AvailableWeightKg < order.WeightKg {
			return errors.New("insufficient weight capacity on this trip")
		}
		if activeTrip.AvailableVolumeLiters < order.VolumeLiters {
			return errors.New("insufficient volume capacity on this trip")
		}
	}

	// --- Unified Transaction Block ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. Check Escrow (Already held during Create)
		if order.PaymentMethod == MethodEscrow {
			if order.PaymentStatus != PaymentEscrow {
				return errors.New("order payment is not secured in escrow")
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

		ok, err := s.repo.UpdateWithStatusCheck(ctx, tx, order, StatusPending)
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
	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderAccept, "order", orderID.String(), map[string]interface{}{"status": StatusPending}, map[string]interface{}{"status": StatusPurchasing, "runner_id": runnerID}, "", "")

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
		return errors.New("unauthorized: you are not the runner for this order")
	}

	switch order.ServiceCategory {
	case CategoryBeli:
		if order.Status != StatusPurchasing {
			return errors.New("order category 'beli' must be purchased (receipt uploaded) before it can be picked up")
		}
	case CategoryKirim:
		if order.Status != StatusAccepted {
			return errors.New("order category 'kirim' must be accepted before it can be picked up")
		}
	default:
		if order.Status != StatusAccepted && order.Status != StatusPurchasing {
			return errors.New("order is not in a state that can be picked up")
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
		return errors.New("unauthorized: only the requester can cancel this order")
	}

	if ord.Status == StatusCompleted || ord.Status == StatusCancelled || ord.Status == StatusDelivering {
		return errors.New("order cannot be cancelled at this stage")
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
					return errors.New("failed to process partial refund: " + err.Error())
				}
			} else {
				// Refund full amount
				if err := s.walletSvc.RefundEscrow(ctx, tx, ord.RequesterID, ord.ID, totalEscrow); err != nil {
					return errors.New("failed to refund escrow: " + err.Error())
				}
			}
			ord.PaymentStatus = PaymentRefunded
		}

		// Restore Capacity if runner was assigned
		if ord.RunnerID != nil && ord.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters); err != nil {
				return errors.New("failed to restore trip capacity")
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

func (s *service) SubmitPurchaseReceipt(ctx context.Context, orderID, runnerID uuid.UUID, receiptURL string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RunnerID == nil || *order.RunnerID != runnerID {
		return errors.New("unauthorized: you are not the runner for this order")
	}

	if order.ServiceCategory == CategoryKirim {
		return errors.New("order category 'kirim' does not support the purchasing phase")
	}

	// Must be in Accepted state before Purchasing
	if order.Status != StatusAccepted {
		return errors.New("order is not ready for purchasing phase")
	}

	if receiptURL == "" {
		return errors.New("receipt image URL is required")
	}

	order.Status = StatusPurchasing
	order.ReceiptImageURL = receiptURL
	order.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, s.db, order); err != nil {
		return err
	}

	s.auditSvc.Log(ctx, &runnerID, audit.ActionOrderPurchased, "order", orderID.String(),
		map[string]interface{}{"status": StatusAccepted},
		map[string]interface{}{"status": StatusPurchasing, "receipt_image_url": receiptURL}, "", "")

	return nil
}

func (s *service) CompleteOrder(ctx context.Context, orderID, runnerID uuid.UUID, code string, deliveryImageURL string) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return err
	}

	if order.RunnerID == nil || *order.RunnerID != runnerID {
		return errors.New("unauthorized: you are not the runner for this order")
	}

	if order.Status != StatusDelivering {
		return errors.New("order cannot be completed from current status (must be in delivering phase)")
	}

	if order.CompletionCode != code {
		return errors.New("kode konfirmasi salah")
	}

	// --- Unified Completion Transaction ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		switch order.PaymentMethod {
		case MethodEscrow:
			platformFee := order.ServiceFee
			refundAmount := order.CheckingFee
			totalRunnerPayout := order.EstimatedCost + (order.DeliveryFee - order.ServiceFee - order.CheckingFee)

			if err := s.walletSvc.ReleaseEscrowWithRefund(ctx, tx, runnerID, order.RequesterID, order.ID, totalRunnerPayout, platformFee, refundAmount); err != nil {
				return errors.New("failed to release escrow: " + err.Error())
			}
			order.PaymentStatus = PaymentReleased
		case MethodCOD:
			platformFee := order.ServiceFee
			if err := s.walletSvc.DeductCODPlatformFee(ctx, tx, runnerID, order.ID, platformFee); err != nil {
				return errors.New("failed to deduct COD platform fee: " + err.Error())
			}
			order.PaymentStatus = PaymentReleased
		}

		if deliveryImageURL != "" {
			order.DeliveryImageURL = deliveryImageURL
		}
		order.Status = StatusCompleted
		order.UpdatedAt = time.Now()

		if err := s.repo.Update(ctx, tx, order); err != nil {
			return err
		}

		// Audit Log (Transactional)
		s.auditSvc.LogWithDB(ctx, tx, &runnerID, audit.ActionOrderComplete, "order", orderID.String(), nil, map[string]interface{}{"status": StatusCompleted, "delivery_image_url": deliveryImageURL}, "", "")

		return nil
	})

	return err
}

func (s *service) UpdatePaymentStatus(ctx context.Context, orderID uuid.UUID, paymentStatus string) error {
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
		return errors.New("cannot cancel an already completed or cancelled order")
	}

	// --- Unified Admin Force-Cancel Transaction ---
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. Handle Escrow Refund if applicable
		if order.PaymentMethod == MethodEscrow && order.PaymentStatus == PaymentEscrow {
			totalEscrow := order.EstimatedCost + order.DeliveryFee
			if err := s.walletSvc.RefundEscrow(ctx, tx, order.RequesterID, orderID, totalEscrow); err != nil {
				return errors.New("failed to refund escrow: " + err.Error())
			}
			order.PaymentStatus = PaymentRefunded
		}

		// 2. Restore Capacity
		if order.RunnerID != nil && order.TripID != nil {
			if err := s.tripRepo.RestoreCapacity(ctx, tx, *order.TripID, order.WeightKg, order.VolumeLiters); err != nil {
				return errors.New("failed to restore trip capacity")
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
		return errors.New("only the requester can dispute this order")
	}

	if order.Status != StatusCompleted {
		return errors.New("only completed orders can be disputed")
	}

	// 24 Hour Limit Enforcement
	if time.Since(order.UpdatedAt) > 24*time.Hour {
		return errors.New("dispute period (24 hours after completion) has expired")
	}

	if order.PaymentStatus == PaymentRefunded {
		return errors.New("order is already refunded")
	}

	if proofURL == "" {
		return errors.New("proof image URL is required to open a dispute")
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
		return errors.New("order is not under dispute")
	}

	// --- Unified Resolution Transaction ---
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if order.PaymentMethod == MethodEscrow {
			switch side {
			case user.RoleRequester:
				totalAmount := order.EstimatedCost + order.DeliveryFee
				if err := s.walletSvc.RefundEscrow(ctx, tx, order.RequesterID, orderID, totalAmount); err != nil {
					return errors.New("escrow refund failed: " + err.Error())
				}
				order.PaymentStatus = PaymentRefunded
				order.Status = StatusCancelled

				// Restore Capacity
				if order.RunnerID != nil && order.TripID != nil {
					if err := s.tripRepo.RestoreCapacity(ctx, tx, *order.TripID, order.WeightKg, order.VolumeLiters); err != nil {
						return errors.New("failed to restore trip capacity")
					}
				}
			case user.RoleRunner:
				if order.RunnerID == nil {
					return errors.New("order has no runner")
				}
				platformFee := order.ServiceFee
				refundAmount := order.CheckingFee
				totalRunnerPayout := order.EstimatedCost + (order.DeliveryFee - order.ServiceFee - order.CheckingFee)
				if err := s.walletSvc.ReleaseEscrowWithRefund(ctx, tx, *order.RunnerID, order.RequesterID, orderID, totalRunnerPayout, platformFee, refundAmount); err != nil {
					return errors.New("escrow release failed: " + err.Error())
				}
				order.PaymentStatus = PaymentReleased
				order.Status = StatusCompleted
			default:
				return errors.New("invalid resolution side, must be 'requester' or 'runner'")
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
		return errors.New("unauthorized: you are not the runner for this order")
	}

	if order.Status != StatusAccepted && order.Status != StatusPurchasing {
		return errors.New("cannot adjust price in current order status")
	}

	if order.AdjustmentStatus != "" {
		return errors.New("price adjustment has already been requested for this order (limit 1x)")
	}

	if adjustedCost <= order.EstimatedCost {
		return errors.New("adjusted cost must be higher than current estimated cost")
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
		return errors.New("unauthorized: only the requester can approve penyesuaian harga")
	}

	if ord.AdjustmentStatus != AdjustmentPending {
		return errors.New("no pending price adjustment found")
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
				return errors.New("failed to hold additional escrow funds: " + err.Error())
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
		return errors.New("unauthorized")
	}

	if ord.AdjustmentStatus != AdjustmentPending {
		return errors.New("no pending price adjustment found")
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
						return errors.New("failed to process partial refund: " + err.Error())
					}
				} else {
					if err := s.walletSvc.RefundEscrow(ctx, tx, ord.RequesterID, ord.ID, totalEscrow); err != nil {
						return errors.New("failed to refund escrow: " + err.Error())
					}
				}
				ord.PaymentStatus = PaymentRefunded
			}

			// Restore Capacity
			if ord.RunnerID != nil && ord.TripID != nil {
				if err := s.tripRepo.RestoreCapacity(ctx, tx, *ord.TripID, ord.WeightKg, ord.VolumeLiters); err != nil {
					return errors.New("failed to restore trip capacity")
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

	// Add 10% Platform Markup
	totalWithMarkup := totalFee * 1.1

	// Add Checking Fee (Deposit)
	checkingFeeStr := s.configSvc.GetValue(ctx, "order_checking_fee", "5000")
	checkingFee, _ := strconv.ParseFloat(checkingFeeStr, 64)
	totalWithMarkup += checkingFee

	// Pembulatan ke kelipatan 500 terdekat ke atas
	return math.Ceil(totalWithMarkup/500) * 500
}

func (s *service) signURLs(ctx context.Context, o *Order) {
	if o == nil {
		return
	}
	if o.ReceiptImageURL != "" {
		if signed, err := s.storage.GetSignedURL(ctx, o.ReceiptImageURL, 1*time.Hour); err == nil {
			o.ReceiptImageURL = signed
		}
	}
	if o.DeliveryImageURL != "" {
		if signed, err := s.storage.GetSignedURL(ctx, o.DeliveryImageURL, 1*time.Hour); err == nil {
			o.DeliveryImageURL = signed
		}
	}
	if o.DisputeProofURL != "" {
		if signed, err := s.storage.GetSignedURL(ctx, o.DisputeProofURL, 1*time.Hour); err == nil {
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
