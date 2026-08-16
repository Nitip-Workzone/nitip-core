package promotion

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

type Service interface {
	CreatePromotion(ctx context.Context, adminID uuid.UUID, req CreatePromotionRequest, ip, ua string) (*Promotion, error)
	UpdatePromotion(ctx context.Context, adminID uuid.UUID, id uuid.UUID, req UpdatePromotionRequest, ip, ua string) (*Promotion, error)
	DeletePromotion(ctx context.Context, adminID uuid.UUID, id uuid.UUID, req DeletePromotionRequest, ip, ua string) error
	List(ctx context.Context, merchantID *uuid.UUID, isActive *bool, search string, firstPurchaseOnly *bool, offset, limit int) ([]Promotion, int, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Promotion, error)
	GetActiveForMerchant(ctx context.Context, merchantID *uuid.UUID) ([]Promotion, error)
	ValidateAndReserveForOrder(ctx context.Context, tx bun.IDB, code string, merchantID *uuid.UUID, userID uuid.UUID, itemTotal, deliveryTotal, total float64) (any, float64, error)
	ApplyUsage(ctx context.Context, tx bun.IDB, promoID, orderID, userID uuid.UUID, merchantID *uuid.UUID, discountAmount, originalAmount float64) error
	ReleaseUsage(ctx context.Context, tx bun.IDB, orderID uuid.UUID) error
	ListUsages(ctx context.Context, promotionID uuid.UUID, offset, limit int) ([]PromotionUsage, int, error)
	CalculatePreview(ctx context.Context, req CalculatePreviewRequest) (*CalculatePreviewResponse, error)
	GetSettlement(ctx context.Context, merchantID *uuid.UUID, from, to *time.Time) (*SettlementResponse, error)
	ValidateForCheckout(ctx context.Context, req ValidatePromotionRequest, userID *uuid.UUID) (*ValidatePromotionResponse, error)
}

type service struct {
	repo         Repository
	userRepo     user.Repository
	merchantRepo merchant.Repository
	auditSvc     audit.Service
	cache        *cache.Redis
	db           *bun.DB
}

func NewService(repo Repository, userRepo user.Repository, merchantRepo merchant.Repository, auditSvc audit.Service, cache *cache.Redis, db *bun.DB) Service {
	return &service{
		repo: repo, userRepo: userRepo, merchantRepo: merchantRepo,
		auditSvc: auditSvc, cache: cache, db: db,
	}
}

func (s *service) verifyAdminSecurity(ctx context.Context, adminID uuid.UUID, adminPassword, totpCode string) (*user.User, error) {
	admin, err := s.userRepo.FindByID(ctx, adminID)
	if err != nil {
		return nil, errors.New("admin tidak ditemukan")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(adminPassword)); err != nil {
		return nil, errors.New("password admin salah")
	}
	if !admin.TotpEnabled {
		return nil, errors.New("tindakan ini memerlukan autentikasi dua faktor (TOTP) diaktifkan pada akun admin Anda")
	}
	if admin.TotpSecret == nil || *admin.TotpSecret == "" {
		return nil, errors.New("TOTP terkonfigurasi tidak valid")
	}
	if !totp.Validate(totpCode, *admin.TotpSecret) {
		return nil, errors.New("kode TOTP tidak valid")
	}
	return admin, nil
}

func (s *service) CreatePromotion(ctx context.Context, adminID uuid.UUID, req CreatePromotionRequest, ip, ua string) (*Promotion, error) {
	if _, err := s.verifyAdminSecurity(ctx, adminID, req.AdminPassword, req.TotpCode); err != nil {
		return nil, err
	}

	// Sanitize code
	sanitizedCode, err := SanitizeAndValidateCode(req.Code)
	if err != nil {
		return nil, err
	}

	if sanitizedCode != nil && req.AutoApply {
		return nil, ErrAutoAndCodeConflict
	}
	if sanitizedCode == nil && !req.AutoApply {
		// For MVP, require either code or auto_apply true; but allow global promo without code? Enforce one must be set
		// If both nil/false, error
		return nil, ErrNeedCodeOrAuto
	}

	// Discount validation
	if req.DiscountType == DiscountTypePercent && req.DiscountValue > 90 {
		return nil, errors.New("diskon persen maksimal 90%")
	}
	if req.BudgetTotal < 1000 {
		return nil, ErrBudgetTooSmall
	}
	if req.ValidFrom != nil && req.ValidUntil != nil && req.ValidFrom.After(*req.ValidUntil) {
		return nil, errors.New("valid_from harus sebelum valid_until")
	}

	// Per user limit default
	perUserLimit := req.PerUserLimit
	if perUserLimit <= 0 {
		perUserLimit = 1
	}

	// Scope default
	scope := req.DiscountScope
	if scope == "" {
		scope = ScopeItem
	}

	// Check merchant exists if set
	if req.MerchantID != nil {
		_, err := s.merchantRepo.GetMerchantByID(ctx, *req.MerchantID)
		if err != nil {
			return nil, errors.New("merchant tidak ditemukan")
		}
	}

	// Check code uniqueness case-insensitive
	if sanitizedCode != nil {
		existing, err := s.repo.FindByCodeInsensitive(ctx, *sanitizedCode)
		if err == nil && existing != nil {
			return nil, fmt.Errorf("kode voucher %s sudah digunakan", *sanitizedCode)
		}
	}

	// Avg snapshot
	avg, err := s.repo.GetAvgOrderValue(ctx, req.MerchantID)
	if err != nil {
		avg = 25000
	}
	avgCopy := avg

	promo := &Promotion{
		ID:                    uuid.New(),
		Code:                  sanitizedCode,
		MerchantID:            req.MerchantID,
		Title:                 req.Title,
		Description:           req.Description,
		DiscountType:          req.DiscountType,
		DiscountValue:         req.DiscountValue,
		BudgetTotal:           req.BudgetTotal,
		BudgetUsed:            0,
		MaxUses:               req.MaxUses,
		UsedCount:             0,
		PerUserLimit:          perUserLimit,
		FirstPurchaseOnly:     req.FirstPurchaseOnly,
		DiscountScope:         scope,
		MinOrderAmount:        req.MinOrderAmount,
		AutoApply:             req.AutoApply,
		IsActive:              true,
		ValidFrom:             req.ValidFrom,
		ValidUntil:            req.ValidUntil,
		AvgOrderValueSnapshot: &avgCopy,
		CreatedBy:             &adminID,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	// Tx insert + audit
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := s.repo.Create(ctx, tx, promo); err != nil {
			return err
		}
		if s.auditSvc != nil {
			s.auditSvc.LogWithDB(ctx, tx, &adminID, audit.ActionDiscountCreate, "promotion", promo.ID.String(), nil, map[string]interface{}{
				"title": promo.Title, "code": promo.Code, "discount_type": promo.DiscountType,
				"discount_value": promo.DiscountValue, "budget_total": promo.BudgetTotal,
				"max_uses": promo.MaxUses, "merchant_id": promo.MerchantID,
				"first_purchase_only": promo.FirstPurchaseOnly, "auto_apply": promo.AutoApply,
			}, ip, ua)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Invalidate cache
	if s.cache != nil && promo.MerchantID != nil {
		_ = s.cache.Del(ctx, fmt.Sprintf("promo:active:merchant:%s", promo.MerchantID.String()))
	}
	if s.cache != nil {
		_ = s.cache.Del(ctx, "promo:active:merchant:global")
	}

	promo.ComputeDerived()
	return promo, nil
}

func (s *service) UpdatePromotion(ctx context.Context, adminID uuid.UUID, id uuid.UUID, req UpdatePromotionRequest, ip, ua string) (*Promotion, error) {
	if _, err := s.verifyAdminSecurity(ctx, adminID, req.AdminPassword, req.TotpCode); err != nil {
		return nil, err
	}

	var result *Promotion
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.repo.FindByIDForUpdate(ctx, tx, id)
		if err != nil {
			return ErrPromotionNotFound
		}
		old := *existing

		// Code validation if changing
		if req.Code != nil {
			sanitized, err := SanitizeAndValidateCode(req.Code)
			if err != nil {
				return err
			}
			if sanitized != nil {
				// uniqueness check excluding self
				dup, err := s.repo.FindByCodeInsensitive(ctx, *sanitized)
				if err == nil && dup != nil && dup.ID != id {
					return fmt.Errorf("kode voucher %s sudah digunakan", *sanitized)
				}
			}
			existing.Code = sanitized
		}
		if req.Title != nil {
			existing.Title = *req.Title
		}
		if req.Description != nil {
			existing.Description = *req.Description
		}
		if req.MerchantID != nil {
			_, err := s.merchantRepo.GetMerchantByID(ctx, *req.MerchantID)
			if err != nil {
				return errors.New("merchant tidak ditemukan")
			}
			existing.MerchantID = req.MerchantID
		}
		if req.DiscountType != nil {
			existing.DiscountType = *req.DiscountType
		}
		if req.DiscountValue != nil {
			if existing.DiscountType == DiscountTypePercent && *req.DiscountValue > 90 {
				return errors.New("diskon persen maksimal 90%")
			}
			existing.DiscountValue = *req.DiscountValue
		}
		if req.BudgetTotal != nil {
			if *req.BudgetTotal < existing.BudgetUsed {
				return errors.New("budget_total tidak boleh kurang dari budget_used")
			}
			existing.BudgetTotal = *req.BudgetTotal
		}
		if req.MaxUses != nil {
			if *req.MaxUses < existing.UsedCount {
				return errors.New("max_uses tidak boleh kurang dari used_count")
			}
			existing.MaxUses = *req.MaxUses
		}
		if req.PerUserLimit != nil {
			existing.PerUserLimit = *req.PerUserLimit
		}
		if req.FirstPurchaseOnly != nil {
			existing.FirstPurchaseOnly = *req.FirstPurchaseOnly
		}
		if req.DiscountScope != nil {
			existing.DiscountScope = *req.DiscountScope
		}
		if req.MinOrderAmount != nil {
			existing.MinOrderAmount = *req.MinOrderAmount
		}
		if req.AutoApply != nil {
			if *req.AutoApply && existing.Code != nil {
				return ErrAutoAndCodeConflict
			}
			existing.AutoApply = *req.AutoApply
		}
		if req.IsActive != nil {
			existing.IsActive = *req.IsActive
		}
		if req.ValidFrom != nil {
			existing.ValidFrom = req.ValidFrom
		}
		if req.ValidUntil != nil {
			existing.ValidUntil = req.ValidUntil
		}
		if existing.ValidFrom != nil && existing.ValidUntil != nil && existing.ValidFrom.After(*existing.ValidUntil) {
			return errors.New("valid_from harus sebelum valid_until")
		}

		existing.UpdatedAt = time.Now()
		if err := s.repo.Update(ctx, tx, existing); err != nil {
			return err
		}
		if s.auditSvc != nil {
			s.auditSvc.LogWithDB(ctx, tx, &adminID, audit.ActionDiscountUpdate, "promotion", existing.ID.String(), map[string]interface{}{
				"old_title": old.Title, "old_code": old.Code,
			}, map[string]interface{}{
				"title": existing.Title, "code": existing.Code, "budget_total": existing.BudgetTotal,
				"first_purchase_only": existing.FirstPurchaseOnly,
			}, ip, ua)
		}
		result = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		if result.MerchantID != nil {
			_ = s.cache.Del(ctx, fmt.Sprintf("promo:active:merchant:%s", result.MerchantID.String()))
		}
		_ = s.cache.Del(ctx, "promo:active:merchant:global")
	}
	result.ComputeDerived()
	return result, nil
}

func (s *service) DeletePromotion(ctx context.Context, adminID uuid.UUID, id uuid.UUID, req DeletePromotionRequest, ip, ua string) error {
	if _, err := s.verifyAdminSecurity(ctx, adminID, req.AdminPassword, req.TotpCode); err != nil {
		return err
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		existing, err := s.repo.FindByIDForUpdate(ctx, tx, id)
		if err != nil {
			return ErrPromotionNotFound
		}
		if existing.UsedCount > 0 {
			// soft deactivate instead of hard delete if already used
			existing.IsActive = false
			existing.UpdatedAt = time.Now()
			if err := s.repo.Update(ctx, tx, existing); err != nil {
				return err
			}
			if s.auditSvc != nil {
				s.auditSvc.LogWithDB(ctx, tx, &adminID, audit.ActionDiscountDelete, "promotion", id.String(), existing, map[string]interface{}{"deactivated_due_to_usage": true}, ip, ua)
			}
			return nil
		}
		if err := s.repo.Delete(ctx, tx, id); err != nil {
			return err
		}
		if s.auditSvc != nil {
			s.auditSvc.LogWithDB(ctx, tx, &adminID, audit.ActionDiscountDelete, "promotion", id.String(), existing, nil, ip, ua)
		}
		return nil
	})
}

func (s *service) List(ctx context.Context, merchantID *uuid.UUID, isActive *bool, search string, firstPurchaseOnly *bool, offset, limit int) ([]Promotion, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	list, total, err := s.repo.List(ctx, merchantID, isActive, search, firstPurchaseOnly, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	list, _ = s.repo.UpdateMerchantNameBatch(ctx, list)
	for i := range list {
		list[i].ComputeDerived()
	}
	return list, total, nil
}

func (s *service) GetByID(ctx context.Context, id uuid.UUID) (*Promotion, error) {
	p, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrPromotionNotFound
	}
	p.ComputeDerived()
	enriched, _ := s.repo.UpdateMerchantNameBatch(ctx, []Promotion{*p})
	if len(enriched) > 0 {
		p = &enriched[0]
		p.ComputeDerived()
	}
	return p, nil
}

func (s *service) GetActiveForMerchant(ctx context.Context, merchantID *uuid.UUID) ([]Promotion, error) {
	var cacheKey string
	if merchantID != nil {
		cacheKey = fmt.Sprintf("promo:active:merchant:%s", merchantID.String())
	} else {
		cacheKey = "promo:active:merchant:global"
	}
	if s.cache != nil {
		// try to get from cache? For simplicity, we cache serialized list? Skip for MVP - just use redis as existence check
		// We implement simple get: if cached, return cached? We don't have serialization, so always query DB and set expiry later
		// For minimal impact, we skip caching reads, only invalidate on writes (as done)
		_ = cacheKey
	}

	list, err := s.repo.FindActiveByMerchant(ctx, merchantID)
	if err != nil {
		return nil, err
	}
	_, _ = s.repo.UpdateMerchantNameBatch(ctx, list)
	for i := range list {
		list[i].ComputeDerived()
	}
	return list, nil
}

func (s *service) CalculatePreview(ctx context.Context, req CalculatePreviewRequest) (*CalculatePreviewResponse, error) {
	avg, err := s.repo.GetAvgOrderValue(ctx, req.MerchantID)
	if err != nil {
		avg = 25000
	}
	flat := ComputeFlatPerOrder(req.BudgetTotal, req.MaxUses)
	var percent float64
	var msg string
	if req.DiscountType == DiscountTypePercent {
		if req.DiscountValue > 0 {
			flat = avg * req.DiscountValue / 100
			percent = req.DiscountValue
			msg = fmt.Sprintf("Persen %.0f%% dari avg Rp%.0f => flat Rp%.0f/order, budget %.0f untuk %d order", percent, avg, flat, req.BudgetTotal, req.MaxUses)
		} else {
			percent = ComputePercentFromFlat(flat, avg)
			msg = fmt.Sprintf("Flat Rp%.0f/order dari budget %.0f/%d, estimasi %.1f%% dari avg Rp%.0f", flat, req.BudgetTotal, req.MaxUses, percent, avg)
		}
	} else {
		if req.DiscountValue > 0 {
			flat = req.DiscountValue
			percent = ComputePercentFromFlat(flat, avg)
			msg = fmt.Sprintf("Flat Rp%.0f/order => %.1f%% dari avg Rp%.0f, budget %.0f untuk %d order", flat, percent, avg, req.BudgetTotal, req.MaxUses)
		} else {
			percent = ComputePercentFromFlat(flat, avg)
			msg = fmt.Sprintf("Budget Rp%.0f dibagi %d order => Rp%.0f/order (~%.1f%% dari avg Rp%.0f)", req.BudgetTotal, req.MaxUses, flat, percent, avg)
		}
	}
	return &CalculatePreviewResponse{
		FlatPerOrder:  flat,
		AvgOrderValue: avg,
		PercentEst:    percent,
		Message:       msg,
	}, nil
}

func (s *service) ValidateAndReserveForOrder(ctx context.Context, tx bun.IDB, code string, merchantID *uuid.UUID, userID uuid.UUID, itemTotal, deliveryTotal, total float64) (any, float64, error) {
	var promo *Promotion
	var err error

	if code != "" {
		promo, err = s.repo.FindByCodeForUpdate(ctx, tx, code)
		if err != nil {
			return nil, 0, errors.New("kode voucher tidak valid: " + code)
		}
	} else {
		// auto apply
		promo, err = s.repo.FindAutoActiveForMerchantForUpdate(ctx, tx, merchantID)
		if err != nil {
			return nil, 0, ErrNoPromotionFound
		}
	}

	// Basic checks
	if !promo.IsActive {
		return nil, 0, ErrPromotionInactive
	}
	now := time.Now()
	if promo.ValidFrom != nil && now.Before(*promo.ValidFrom) {
		return nil, 0, ErrPromotionExpired
	}
	if promo.ValidUntil != nil && now.After(*promo.ValidUntil) {
		return nil, 0, ErrPromotionExpired
	}
	if promo.UsedCount >= promo.MaxUses {
		return nil, 0, ErrPromotionExhausted
	}
	if promo.BudgetUsed >= promo.BudgetTotal {
		return nil, 0, ErrBudgetExhausted
	}

	// Per user limit
	cnt, err := s.repo.CountUserUsage(ctx, promo.ID, userID)
	if err == nil && cnt >= promo.PerUserLimit {
		return nil, 0, ErrPerUserLimit
	}

	// First purchase only check
	if promo.FirstPurchaseOnly {
		completedCnt, err := s.repo.CountUserCompletedOrders(ctx, userID)
		if err == nil && completedCnt > 0 {
			return nil, 0, ErrFirstPurchaseOnly
		}
	}

	// Min order
	if promo.MinOrderAmount > 0 && total < promo.MinOrderAmount {
		return nil, 0, ErrMinOrderNotMet
	}

	// Merchant scoping: if promo has merchant_id, it must match order merchant if order has merchant
	if promo.MerchantID != nil && merchantID != nil && *promo.MerchantID != *merchantID {
		return nil, 0, errors.New("voucher tidak berlaku untuk merchant ini")
	}

	// Compute discount amount based on scope
	var scopedCap float64
	switch promo.DiscountScope {
	case ScopeItem:
		scopedCap = itemTotal
	case ScopeDelivery:
		scopedCap = deliveryTotal
	case ScopeTotal:
		scopedCap = total
	default:
		scopedCap = itemTotal
	}

	var discount float64
	if promo.DiscountType == DiscountTypeFlat {
		discount = promo.DiscountValue
	} else {
		discount = scopedCap * promo.DiscountValue / 100
	}

	// Cap by scopedCap and remaining budget
	if discount > scopedCap {
		discount = scopedCap
	}
	remainingBudget := promo.BudgetTotal - promo.BudgetUsed
	if discount > remainingBudget {
		discount = remainingBudget
	}
	// Ensure not negative
	if discount < 0 {
		discount = 0
	}
	discount = math.Round(discount)

	if discount <= 0 {
		return promo, 0, nil
	}

	return promo, discount, nil
}

func (s *service) ApplyUsage(ctx context.Context, tx bun.IDB, promoID, orderID, userID uuid.UUID, merchantID *uuid.UUID, discountAmount, originalAmount float64) error {
	usage := &PromotionUsage{
		ID:             uuid.New(),
		PromotionID:    promoID,
		OrderID:        orderID,
		UserID:         userID,
		MerchantID:     merchantID,
		DiscountAmount: discountAmount,
		OriginalAmount: &originalAmount,
		CreatedAt:      time.Now(),
	}
	if err := s.repo.InsertUsage(ctx, tx, usage); err != nil {
		return err
	}
	// Update promo budget_used + used_count atomically
	_, err := tx.NewUpdate().Model((*Promotion)(nil)).Where("id = ?", promoID).
		Set("budget_used = budget_used + ?", discountAmount).
		Set("used_count = used_count + 1").
		Set("updated_at = ?", time.Now()).
		Exec(ctx)
	return err
}

func (s *service) ReleaseUsage(ctx context.Context, tx bun.IDB, orderID uuid.UUID) error {
	usage, err := s.repo.DeleteUsageByOrderID(ctx, tx, orderID)
	if err != nil {
		// if not found, idempotent
		return nil
	}
	_, err = tx.NewUpdate().Model((*Promotion)(nil)).Where("id = ?", usage.PromotionID).
		Set("budget_used = GREATEST(0, budget_used - ?)", usage.DiscountAmount).
		Set("used_count = GREATEST(0, used_count - 1)").
		Set("updated_at = ?", time.Now()).
		Exec(ctx)
	return err
}

func (s *service) ListUsages(ctx context.Context, promotionID uuid.UUID, offset, limit int) ([]PromotionUsage, int, error) {
	return s.repo.ListUsages(ctx, promotionID, offset, limit)
}

func (s *service) GetSettlement(ctx context.Context, merchantID *uuid.UUID, from, to *time.Time) (*SettlementResponse, error) {
	items, totalLiab, totalCnt, err := s.repo.GetSettlement(ctx, merchantID, from, to)
	if err != nil {
		return nil, err
	}
	return &SettlementResponse{
		TotalLiability: totalLiab,
		TotalOrders:    totalCnt,
		Items:          items,
	}, nil
}

func (s *service) ValidateForCheckout(ctx context.Context, req ValidatePromotionRequest, userID *uuid.UUID) (*ValidatePromotionResponse, error) {
	// For checkout preview without locking (read only)
	var promo *Promotion
	var err error
	if req.Code != "" {
		promo, err = s.repo.FindByCodeInsensitive(ctx, req.Code)
		if err != nil {
			return &ValidatePromotionResponse{Valid: false, Message: "kode voucher tidak valid: " + req.Code}, nil
		}
	} else {
		// find auto active
		list, err := s.repo.FindActiveByMerchant(ctx, req.MerchantID)
		if err != nil || len(list) == 0 {
			return &ValidatePromotionResponse{Valid: false, Message: "tidak ada promo aktif"}, nil
		}
		// pick first auto
		for _, p := range list {
			if p.AutoApply {
				pCopy := p
				promo = &pCopy
				break
			}
		}
		if promo == nil {
			return &ValidatePromotionResponse{Valid: false, Message: "tidak ada promo auto aktif"}, nil
		}
	}

	now := time.Now()
	if !promo.IsActive {
		return &ValidatePromotionResponse{Valid: false, Message: "promosi tidak aktif"}, nil
	}
	if promo.ValidFrom != nil && now.Before(*promo.ValidFrom) {
		return &ValidatePromotionResponse{Valid: false, Message: "promo belum mulai"}, nil
	}
	if promo.ValidUntil != nil && now.After(*promo.ValidUntil) {
		return &ValidatePromotionResponse{Valid: false, Message: "promo sudah berakhir"}, nil
	}
	if promo.UsedCount >= promo.MaxUses {
		return &ValidatePromotionResponse{Valid: false, Message: "kuota promo habis"}, nil
	}
	if promo.BudgetUsed >= promo.BudgetTotal {
		return &ValidatePromotionResponse{Valid: false, Message: "budget promo habis"}, nil
	}

	if userID != nil {
		cnt, _ := s.repo.CountUserUsage(ctx, promo.ID, *userID)
		if cnt >= promo.PerUserLimit {
			return &ValidatePromotionResponse{Valid: false, Message: "batas penggunaan per user tercapai"}, nil
		}
		if promo.FirstPurchaseOnly {
			completedCnt, _ := s.repo.CountUserCompletedOrders(ctx, *userID)
			if completedCnt > 0 {
				return &ValidatePromotionResponse{Valid: false, Message: "voucher hanya untuk pembelian pertama"}, nil
			}
		}
	}

	if promo.MinOrderAmount > 0 && req.Total < promo.MinOrderAmount {
		return &ValidatePromotionResponse{Valid: false, Message: fmt.Sprintf("minimal order Rp%.0f", promo.MinOrderAmount)}, nil
	}

	if promo.MerchantID != nil && req.MerchantID != nil && *promo.MerchantID != *req.MerchantID {
		return &ValidatePromotionResponse{Valid: false, Message: "voucher tidak berlaku untuk merchant ini"}, nil
	}

	var scopedCap float64
	switch promo.DiscountScope {
	case ScopeItem:
		scopedCap = req.ItemTotal
	case ScopeDelivery:
		scopedCap = req.DeliveryTotal
	case ScopeTotal:
		scopedCap = req.Total
	default:
		scopedCap = req.ItemTotal
	}

	var discount float64
	if promo.DiscountType == DiscountTypeFlat {
		discount = promo.DiscountValue
	} else {
		discount = scopedCap * promo.DiscountValue / 100
	}
	if discount > scopedCap {
		discount = scopedCap
	}
	remaining := promo.BudgetTotal - promo.BudgetUsed
	if discount > remaining {
		discount = remaining
	}
	discount = math.Round(discount)

	promo.ComputeDerived()

	return &ValidatePromotionResponse{
		Valid:          true,
		Promotion:      promo,
		DiscountAmount: discount,
		Message:        "voucher valid",
	}, nil
}
