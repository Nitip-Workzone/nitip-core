package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/google/uuid"
	"github.com/midtrans/midtrans-go"
	"github.com/midtrans/midtrans-go/coreapi"
	"github.com/uptrace/bun"
)

type InquiryAccountRequest struct {
	ChannelCode string `json:"channel_code" validate:"required"`
	AccountNo   string `json:"account_no" validate:"required"`
}

type InquiryAccountResponse struct {
	AccountName string `json:"account_name"`
	Status      string `json:"status"`
}

type Service interface {
	GetBalance(ctx context.Context, userID uuid.UUID) (*Wallet, error)
	GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]WalletTransaction, error)

	TopUp(ctx context.Context, userID uuid.UUID, amount float64, reference string) (*WalletTransaction, error)
	InitiateTopUp(ctx context.Context, userID uuid.UUID, amount float64) (*WalletTransaction, error)
	FinalizeTopUp(ctx context.Context, reference string) (*WalletTransaction, error)
	GetWithdrawalChannels(ctx context.Context) ([]WithdrawalChannel, error)
	InquiryAccount(ctx context.Context, req InquiryAccountRequest) (*InquiryAccountResponse, error)
	RequestWithdrawal(ctx context.Context, userID uuid.UUID, amount float64, channelID *uuid.UUID, pin string, destMetadata map[string]interface{}) (*WalletTransaction, error)
	FinalizeWithdrawal(ctx context.Context, txID uuid.UUID, status TransactionStatus) error

	// Internal / Automated flow
	HoldEscrow(ctx context.Context, db bun.IDB, userID, orderID uuid.UUID, amount float64) error
	ReleaseEscrow(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64, platformFee float64) error
	RefundEscrow(ctx context.Context, db bun.IDB, requesterID, orderID uuid.UUID, amount float64) error
	PartialReleaseEscrow(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, refundAmount float64) error
	ReleaseEscrowWithRefund(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, platformFee, refundAmount float64) error
	DeductCODPlatformFee(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, platformFee float64) error

	// Admin Actions
	GetPendingWithdrawals(ctx context.Context, limit, offset int) ([]WalletTransaction, error)
	ApproveWithdrawal(ctx context.Context, txID, actorID uuid.UUID) error
	GetTransactionStatus(ctx context.Context, reference string) (*WalletTransaction, error)

	// Recovery
	RecoverPendingWithdrawals(ctx context.Context) error
}

type service struct {
	repo      Repository
	userSvc   user.Service
	configSvc systemconfig.Service
	db        *bun.DB
	redis     *cache.Redis
	auditSvc  audit.Service
}

func NewService(repo Repository, userSvc user.Service, configSvc systemconfig.Service, db *bun.DB, redis *cache.Redis, auditSvc audit.Service) Service {
	return &service{
		repo:      repo,
		userSvc:   userSvc,
		configSvc: configSvc,
		db:        db,
		redis:     redis,
		auditSvc:  auditSvc,
	}
}

func (s *service) GetBalance(ctx context.Context, userID uuid.UUID) (*Wallet, error) {
	return s.repo.GetOrCreateWallet(ctx, s.db, userID)
}

func (s *service) GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]WalletTransaction, error) {
	w, err := s.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetTransactionsByWalletID(ctx, s.db, w.ID, limit, offset)
}

func (s *service) TopUp(ctx context.Context, userID uuid.UUID, amount float64, reference string) (*WalletTransaction, error) {
	if amount <= 0 {
		return nil, errors.New("jumlah harus lebih besar dari nol")
	}

	w, err := s.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}

	var wtx *WalletTransaction
	err = s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// 1. Add funds
		if err := s.repo.UpdateWalletBalance(ctx, tx, w.ID, amount); err != nil {
			return err
		}

		// 2. Record transaction
		wtx = &WalletTransaction{
			ID:        uuid.New(),
			WalletID:  w.ID,
			Type:      TypeTopUp,
			Amount:    amount,
			Reference: reference,
			Status:    StatusCompleted,
		}
		return s.repo.CreateTransaction(ctx, tx, wtx)
	})

	return wtx, err
}

func (s *service) InitiateTopUp(ctx context.Context, userID uuid.UUID, amount float64) (*WalletTransaction, error) {
	// 1. Validate against minimum top-up configuration
	minStr := s.configSvc.GetValue(ctx, "wallet_min_topup", "10000")
	minAmount, _ := strconv.ParseFloat(minStr, 64)

	if amount < minAmount {
		return nil, fmt.Errorf("nominal top-up minimal adalah Rp %.0f", minAmount)
	}

	w, err := s.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}

	var reference string
	var qrString string
	var deeplinkString string

	if config.App.MidtransServerKey != "" && !config.App.UseMockPayment {
		reference = "TOPUP-" + uuid.New().String()[:8]

		// Get user info for Midtrans payload
		userObj, err := s.userSvc.GetByID(ctx, userID, userID)
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
			PaymentType: coreapi.PaymentTypeGopay,
			TransactionDetails: midtrans.TransactionDetails{
				OrderID:  reference,
				GrossAmt: int64(amount),
			},
			CustomerDetails: &midtrans.CustomerDetails{
				FName: userName,
				Email: userEmail,
			},
			Gopay: &coreapi.GopayDetails{
				EnableCallback: true,
				CallbackUrl:    "nitip://payment-callback",
			},
		}

		chargeResp, midtransErr := client.ChargeTransaction(req)
		if !isMidtransErrorNil(midtransErr) {
			log.Printf("[MIDTRANS-CHARGE-ERROR] StatusCode: %d, Message: %s", midtransErr.StatusCode, midtransErr.Message)
			if midtransErr.StatusCode == 402 {
				return nil, errors.New("saluran pembayaran GoPay belum diaktifkan pada akun Midtrans Sandbox Anda, silakan aktifkan terlebih dahulu di dashboard Midtrans")
			}
			return nil, errors.New("gagal membuat kode pembayaran GoPay/QRIS, silakan coba lagi beberapa saat lagi")
		}

		for _, action := range chargeResp.Actions {
			switch action.Name {
			case "generate-qr-code":
				qrString = action.URL
			case "deeplink-redirect":
				deeplinkString = action.URL
			}
		}
		if qrString == "" && deeplinkString == "" && len(chargeResp.Actions) > 0 {
			qrString = chargeResp.Actions[0].URL
		}
	} else {
		// FALLBACK to mock-qris
		reference = "MOCK-" + uuid.New().String()[:8]

		payload := map[string]interface{}{
			"reference_id": reference,
			"amount":       int64(amount),
		}
		body, _ := json.Marshal(payload)

		pgUrl := os.Getenv("PAYMENT_GATEWAY_URL")
		if pgUrl == "" {
			pgUrl = "http://localhost:4000"
		}

		resp, err := http.Post(fmt.Sprintf("%s/api/qris/generate", pgUrl), "application/json", bytes.NewBuffer(body))
		if err != nil {
			return nil, fmt.Errorf("gagal menghubungi payment gateway: %v", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		var qrisResp struct {
			Status     string `json:"status"`
			TrxID      string `json:"trx_id"`
			QrisString string `json:"qris_string"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&qrisResp); err != nil {
			return nil, fmt.Errorf("gagal membaca respon payment gateway")
		}

		reference = qrisResp.TrxID
		qrString = qrisResp.QrisString
	}

	wtx := &WalletTransaction{
		ID:          uuid.New(),
		WalletID:    w.ID,
		Type:        TypeTopUp,
		Amount:      amount,
		Reference:   reference,
		Status:      StatusPending,
		QrisString:  qrString,
		DeeplinkURL: deeplinkString,
	}

	if err := s.repo.CreateTransaction(ctx, s.db, wtx); err != nil {
		return nil, err
	}

	return wtx, nil
}

func (s *service) FinalizeTopUp(ctx context.Context, reference string) (*WalletTransaction, error) {
	var wtx *WalletTransaction
	err := s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		wtx, err = s.repo.GetTransactionByReference(ctx, tx, reference)
		if err != nil {
			if strings.Contains(err.Error(), "no rows") {
				return errors.New("transaksi top-up tidak ditemukan")
			}
			return err
		}

		if wtx.Status != StatusPending || wtx.Type != TypeTopUp {
			return errors.New("transaksi tidak valid untuk finalisasi atau sudah diproses")
		}

		// 1. Update status
		if err := s.repo.UpdateTransactionStatus(ctx, tx, wtx.ID, StatusCompleted); err != nil {
			return err
		}

		// 2. Update balance
		if err := s.repo.UpdateWalletBalance(ctx, tx, wtx.WalletID, wtx.Amount); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errors.New("transaksi top-up tidak ditemukan")
		}
		return nil, err
	}

	return s.repo.GetTransactionByReference(ctx, s.db, reference)
}

func (s *service) GetWithdrawalChannels(ctx context.Context) ([]WithdrawalChannel, error) {
	return s.repo.GetActiveWithdrawalChannels(ctx, s.db)
}

func (s *service) InquiryAccount(ctx context.Context, req InquiryAccountRequest) (*InquiryAccountResponse, error) {
	payload := map[string]interface{}{
		"bank_code":  req.ChannelCode,
		"account_no": req.AccountNo,
	}
	body, _ := json.Marshal(payload)

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pgUrl := os.Getenv("PAYMENT_GATEWAY_URL")
	if pgUrl == "" {
		pgUrl = "http://localhost:4000"
	}

	reqHttp, err := http.NewRequestWithContext(ctxTimeout, "POST", fmt.Sprintf("%s/api/disbursement/inquiry", pgUrl), bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	reqHttp.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(reqHttp)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi payment gateway (timeout): %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("gagal melakukan verifikasi rekening")
	}

	var res InquiryAccountResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, errors.New("gagal membaca respon verifikasi")
	}

	return &res, nil
}

func (s *service) RequestWithdrawal(ctx context.Context, userID uuid.UUID, amount float64, channelID *uuid.UUID, pin string, destMetadata map[string]interface{}) (*WalletTransaction, error) {
	// --- Concurrency Guard: Redis Lock ---
	lockKey := fmt.Sprintf("lock:withdraw:%s", userID.String())
	ok, err := s.redis.AcquireLock(ctx, lockKey, 10*time.Second)
	if err != nil || !ok {
		return nil, errors.New("permintaan penarikan sedang diproses, silakan tunggu sejenak")
	}
	defer func() { _ = s.redis.ReleaseLock(ctx, lockKey) }()

	u, err := s.userSvc.GetByID(ctx, userID, userID)
	if err != nil {
		return nil, err
	}
	if u.IsSuspended {
		return nil, errors.New("tidak dapat mengajukan penarikan: akun Anda sedang ditangguhkan")
	}

	// 0. Verify PIN
	if err := s.userSvc.VerifyPin(ctx, userID, pin); err != nil {
		return nil, err
	}

	if amount <= 0 {
		return nil, errors.New("jumlah harus lebih besar dari nol")
	}

	// 1. Ambil info channel jika ada
	var channel *WithdrawalChannel
	var adminFee float64
	if channelID != nil {
		channel, err = s.repo.GetWithdrawalChannelByID(ctx, s.db, *channelID)
		if err != nil {
			return nil, errors.New("saluran penarikan tidak ditemukan")
		}
		if !channel.IsActive {
			return nil, errors.New("saluran penarikan yang dipilih sedang tidak aktif")
		}
		if amount < channel.MinAmount {
			return nil, fmt.Errorf("minimum withdrawal for this channel is Rp %.0f", channel.MinAmount)
		}

		// Hitung biaya admin (Flat + Percent)
		adminFee = channel.AdminFeeFlat + (amount * channel.AdminFeePercent / 100)
	}

	totalDeduction := amount + adminFee

	w, err := s.GetBalance(ctx, userID)
	if err != nil {
		return nil, err
	}

	// --- KYC Withdrawal Limit ---
	if !u.IsVerified && !config.App.BypassKYCValidation {
		limitStr := s.configSvc.GetValue(ctx, "kyc_daily_withdrawal_limit", "100000")
		limit, _ := strconv.ParseFloat(limitStr, 64)

		todaySum, err := s.repo.SumTodayWithdrawals(ctx, s.db, w.ID)
		if err == nil && todaySum+amount > limit {
			return nil, fmt.Errorf("batas harian penarikan dana untuk akun non-verifikasi adalah Rp %.0f. Akumulasi hari ini: Rp %.0f", limit, todaySum)
		}
	}

	var wtx *WalletTransaction
	err = s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// 1. Refresh balance within TX to ensure strict correctness
		wTxState, err := s.repo.GetWalletByUserID(ctx, tx, userID)
		if err != nil {
			return err
		}

		if wTxState.Balance < totalDeduction {
			return errors.New("saldo tidak mencukupi (termasuk biaya admin)")
		}

		// 2. Deduct funds immediately (Amount + Fee)
		if err := s.repo.UpdateWalletBalance(ctx, tx, w.ID, -totalDeduction); err != nil {
			return err
		}

		// 3. Record as Pending
		wtx = &WalletTransaction{
			ID:                  uuid.New(),
			WalletID:            w.ID,
			Type:                TypeWithdrawal,
			Amount:              -amount, // The requested net amount
			Status:              StatusPending,
			ChannelID:           channelID,
			DestinationMetadata: destMetadata,
		}

		// Jika ada biaya admin, buat transaksi terpisah untuk mencatatnya
		if adminFee > 0 {
			feeTx := &WalletTransaction{
				ID:        uuid.New(),
				WalletID:  w.ID,
				Type:      TypePlatformFee,
				Amount:    -adminFee,
				Reference: fmt.Sprintf("FEE-WITHDRAW-%s", wtx.ID.String()[:8]),
				Status:    StatusCompleted,
			}
			if err := s.repo.CreateTransaction(ctx, tx, feeTx); err != nil {
				return err
			}
		}

		if err := s.repo.CreateTransaction(ctx, tx, wtx); err != nil {
			return err
		}

		// 4. Audit Log (Transactional)
		s.auditSvc.LogWithDB(ctx, tx, &userID, audit.ActionWalletWithdrawal, "wallet", wtx.ID.String(), nil, wtx, "", "")

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 5. Trigger PG Disbursement (Async for this simulation)
	if channel != nil && channel.Type != "MANUAL" {
		go s.triggerPgDisbursement(wtx, channel)
	}

	return wtx, nil
}

func (s *service) HoldEscrow(ctx context.Context, db bun.IDB, userID, orderID uuid.UUID, amount float64) error {
	w, err := s.repo.GetWalletByUserID(ctx, db, userID)
	if err != nil {
		return err
	}

	if w.Balance < amount {
		return errors.New("saldo tidak mencukupi untuk escrow")
	}

	if err := s.repo.UpdateWalletBalance(ctx, db, w.ID, -amount); err != nil {
		return err
	}

	wtx := &WalletTransaction{
		ID:       uuid.New(),
		WalletID: w.ID,
		OrderID:  &orderID,
		Type:     TypeEscrowHold,
		Amount:   -amount,
		Status:   StatusCompleted,
	}
	return s.repo.CreateTransaction(ctx, db, wtx)
}

func (s *service) ReleaseEscrow(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64, platformFee float64) error {
	w, err := s.GetBalance(ctx, runnerID)
	if err != nil {
		return err
	}

	runnerGets := amount - platformFee

	// 1. Give Runner their cut
	if err := s.repo.UpdateWalletBalance(ctx, db, w.ID, runnerGets); err != nil {
		return err
	}

	wtx := &WalletTransaction{
		ID:       uuid.New(),
		WalletID: w.ID,
		OrderID:  &orderID,
		Type:     TypeEscrowRelease,
		Amount:   runnerGets,
		Status:   StatusCompleted,
	}
	if err := s.repo.CreateTransaction(ctx, db, wtx); err != nil {
		return err
	}

	// 2. Transfer Platform Fee to System Wallet
	if platformFee > 0 {
		sysWID, _ := uuid.Parse(SystemWalletID)
		if err := s.repo.UpdateWalletBalance(ctx, db, sysWID, platformFee); err != nil {
			return err
		}

		feeTx := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: sysWID,
			OrderID:  &orderID,
			Type:     TypePlatformFee,
			Amount:   platformFee,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, feeTx); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) RefundEscrow(ctx context.Context, db bun.IDB, requesterID, orderID uuid.UUID, amount float64) error {
	w, err := s.GetBalance(ctx, requesterID)
	if err != nil {
		return err
	}

	if err := s.repo.UpdateWalletBalance(ctx, db, w.ID, amount); err != nil {
		return err
	}

	wtx := &WalletTransaction{
		ID:       uuid.New(),
		WalletID: w.ID,
		OrderID:  &orderID,
		Type:     TypeRefund,
		Amount:   amount,
		Status:   StatusCompleted,
	}
	return s.repo.CreateTransaction(ctx, db, wtx)
}

func (s *service) PartialReleaseEscrow(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, refundAmount float64) error {
	wRunner, err := s.GetBalance(ctx, runnerID)
	if err != nil {
		return err
	}
	wReq, err := s.GetBalance(ctx, requesterID)
	if err != nil {
		return err
	}

	// 1. Give Runner their portion
	if runnerAmount > 0 {
		if err := s.repo.UpdateWalletBalance(ctx, db, wRunner.ID, runnerAmount); err != nil {
			return err
		}
		wtxRunner := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: wRunner.ID,
			OrderID:  &orderID,
			Type:     TypeEscrowRelease,
			Amount:   runnerAmount,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, wtxRunner); err != nil {
			return err
		}
	}

	// 2. Refund remainder to Requester
	if refundAmount > 0 {
		if err := s.repo.UpdateWalletBalance(ctx, db, wReq.ID, refundAmount); err != nil {
			return err
		}
		wtxReq := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: wReq.ID,
			OrderID:  &orderID,
			Type:     TypeRefund,
			Amount:   refundAmount,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, wtxReq); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) ReleaseEscrowWithRefund(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, platformFee, refundAmount float64) error {
	wRunner, err := s.GetBalance(ctx, runnerID)
	if err != nil {
		return err
	}
	wReq, err := s.GetBalance(ctx, requesterID)
	if err != nil {
		return err
	}
	sysWID, _ := uuid.Parse(SystemWalletID)

	// 1. Give Runner their portion
	if runnerAmount > 0 {
		if err := s.repo.UpdateWalletBalance(ctx, db, wRunner.ID, runnerAmount); err != nil {
			return err
		}
		wtxRunner := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: wRunner.ID,
			OrderID:  &orderID,
			Type:     TypeEscrowRelease,
			Amount:   runnerAmount,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, wtxRunner); err != nil {
			return err
		}
	}

	// 2. Transfer Platform Fee to System
	if platformFee > 0 {
		if err := s.repo.UpdateWalletBalance(ctx, db, sysWID, platformFee); err != nil {
			return err
		}
		feeTx := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: sysWID,
			OrderID:  &orderID,
			Type:     TypePlatformFee,
			Amount:   platformFee,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, feeTx); err != nil {
			return err
		}
	}

	// 3. Refund Deposit to Requester
	if refundAmount > 0 {
		if err := s.repo.UpdateWalletBalance(ctx, db, wReq.ID, refundAmount); err != nil {
			return err
		}
		wtxReq := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: wReq.ID,
			OrderID:  &orderID,
			Type:     TypeRefund,
			Amount:   refundAmount,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, wtxReq); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) DeductCODPlatformFee(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, platformFee float64) error {
	if platformFee <= 0 {
		return nil
	}

	w, err := s.GetBalance(ctx, runnerID)
	if err != nil {
		return err
	}

	// 1. Debit from Runner's wallet (platformFee is positive, so we pass -platformFee)
	if err := s.repo.UpdateWalletBalance(ctx, db, w.ID, -platformFee); err != nil {
		return err
	}

	wtx := &WalletTransaction{
		ID:       uuid.New(),
		WalletID: w.ID,
		OrderID:  &orderID,
		Type:     TypePlatformFee,
		Amount:   -platformFee,
		Status:   StatusCompleted,
	}
	if err := s.repo.CreateTransaction(ctx, db, wtx); err != nil {
		return err
	}

	// 2. Transfer Platform Fee to System Wallet
	sysWID, _ := uuid.Parse(SystemWalletID)
	if err := s.repo.UpdateWalletBalance(ctx, db, sysWID, platformFee); err != nil {
		return err
	}

	feeTx := &WalletTransaction{
		ID:       uuid.New(),
		WalletID: sysWID,
		OrderID:  &orderID,
		Type:     TypePlatformFee,
		Amount:   platformFee,
		Status:   StatusCompleted,
	}
	if err := s.repo.CreateTransaction(ctx, db, feeTx); err != nil {
		return err
	}

	return nil
}

func (s *service) GetPendingWithdrawals(ctx context.Context, limit, offset int) ([]WalletTransaction, error) {
	return s.repo.GetPendingWithdrawals(ctx, s.db, limit, offset)
}

func (s *service) ApproveWithdrawal(ctx context.Context, txID, actorID uuid.UUID) error {
	return s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		wtx, err := s.repo.GetTransactionByID(ctx, tx, txID)
		if err != nil {
			if strings.Contains(err.Error(), "no rows") {
				return errors.New("transaksi penarikan tidak ditemukan")
			}
			return err
		}

		if wtx.Status != StatusPending || wtx.Type != TypeWithdrawal {
			return errors.New("tidak dapat menyetujui penarikan yang tidak dalam status menunggu")
		}

		if err := s.repo.UpdateTransactionStatus(ctx, tx, txID, StatusCompleted); err != nil {
			return err
		}

		// Audit Log (Transactional)
		s.auditSvc.LogWithDB(ctx, tx, &actorID, audit.ActionWithdrawalApprove, "wallet", wtx.ID.String(),
			map[string]interface{}{"status": StatusPending},
			map[string]interface{}{"status": StatusCompleted}, "", "")

		return nil
	})
}

func (s *service) GetTransactionStatus(ctx context.Context, reference string) (*WalletTransaction, error) {
	wtx, err := s.repo.GetTransactionByReference(ctx, s.db, reference)
	if err != nil {
		return nil, errors.New("transaksi tidak ditemukan")
	}
	return wtx, nil
}

func (s *service) FinalizeWithdrawal(ctx context.Context, txID uuid.UUID, status TransactionStatus) error {
	tx, err := s.repo.GetTransactionByID(ctx, s.db, txID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return errors.New("transaksi penarikan tidak ditemukan")
		}
		return err
	}

	if tx.Type != TypeWithdrawal {
		return errors.New("transaksi bukan merupakan penarikan")
	}

	if tx.Status != StatusPending {
		return nil // Already processed
	}

	return s.repo.RunInTx(ctx, func(ctx context.Context, btx bun.Tx) error {
		// Update status
		if err := s.repo.UpdateTransactionStatus(ctx, btx, txID, status); err != nil {
			return err
		}

		// Jika ditolak, kembalikan saldo (termasuk fee jika ada)
		if status == StatusRejected || status == StatusFailed {
			// Refund nominal withdrawal
			if err := s.repo.UpdateWalletBalance(ctx, btx, tx.WalletID, -tx.Amount); err != nil { // tx.Amount is negative
				return err
			}

			// Mencari platform fee terkait penarikan ini
			feeRef := fmt.Sprintf("FEE-WITHDRAW-%s", tx.ID.String()[:8])
			feeTx, err := s.repo.GetTransactionByReference(ctx, btx, feeRef)
			if err == nil && feeTx != nil {
				if err := s.repo.UpdateWalletBalance(ctx, btx, tx.WalletID, -feeTx.Amount); err != nil {
					return err
				}
				_ = s.repo.UpdateTransactionStatus(ctx, btx, feeTx.ID, StatusFailed)
			}
		}

		return nil
	})
}

func (s *service) RecoverPendingWithdrawals(ctx context.Context) error {
	// 1. Get all pending withdrawals
	txs, err := s.repo.GetPendingWithdrawals(ctx, s.db, 100, 0)
	if err != nil {
		return err
	}

	for _, tx := range txs {
		// Only recover if older than 5 minutes
		if time.Since(tx.CreatedAt) < 5*time.Minute {
			continue
		}

		fmt.Printf("[WALLET] Attempting to recover pending withdrawal: %s\n", tx.ID)

		// In a real PG, we would hit their GET /status API.
		// For this mock, we just re-trigger the disbursement or check with mock.
		// For now, let's just re-trigger the mock-qris call if it's "stuck"
		channel, err := s.repo.GetWithdrawalChannelByID(ctx, s.db, *tx.ChannelID)
		if err == nil && channel.Type != "MANUAL" {
			go s.triggerPgDisbursement(&tx, channel)
		}
	}

	return nil
}

func (s *service) triggerPgDisbursement(wtx *WalletTransaction, channel *WithdrawalChannel) {
	// For simulation, we call the mock-qris disbursement API
	payload := map[string]interface{}{
		"trx_id":     wtx.ID.String(),
		"amount":     -wtx.Amount, // Amount was negative in DB
		"bank_code":  channel.Code,
		"account_no": wtx.DestinationMetadata["account_no"],
	}

	pgUrl := os.Getenv("PAYMENT_GATEWAY_URL")
	if pgUrl == "" {
		pgUrl = "http://localhost:4000"
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(fmt.Sprintf("%s/api/disbursement/transfer", pgUrl), "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("[WALLET] Error triggering PG disbursement: %v\n", err)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	fmt.Printf("[WALLET] Disbursement request sent to PG for Trx: %s, Status: %d\n", wtx.ID, resp.StatusCode)
}

func isMidtransErrorNil(err *midtrans.Error) bool {
	return err == nil
}
