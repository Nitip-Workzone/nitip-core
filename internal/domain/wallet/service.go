package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	notificationDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/notification"
	"github.com/codecoffy/nitip-core/internal/utils"
	"github.com/google/uuid"
	"github.com/midtrans/midtrans-go"
	"github.com/midtrans/midtrans-go/coreapi"
	"github.com/uptrace/bun"
)

var OnPaymentSuccess func(ctx context.Context, reference string) error

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
	FinalizeTopUp(ctx context.Context, reference string, notificationID string) (*WalletTransaction, error)
	GetWithdrawalChannels(ctx context.Context) ([]WithdrawalChannel, error)
	InquiryAccount(ctx context.Context, req InquiryAccountRequest) (*InquiryAccountResponse, error)
	RequestWithdrawal(ctx context.Context, userID uuid.UUID, amount float64, channelID *uuid.UUID, pin string, destMetadata map[string]interface{}) (*WalletTransaction, error)
	FinalizeWithdrawal(ctx context.Context, txID uuid.UUID, status TransactionStatus) error

	// Internal / Automated flow
	HoldEscrow(ctx context.Context, db bun.IDB, userID, orderID uuid.UUID, amount float64) error
	HoldLiability(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64) error
	ReleaseLiability(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64) error
	ReleaseEscrow(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64, platformFee float64) error
	RefundEscrow(ctx context.Context, db bun.IDB, requesterID, orderID uuid.UUID, amount float64) error
	PartialReleaseEscrow(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, refundAmount float64) error
	ReleaseEscrowWithRefund(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, platformFee, refundAmount float64) error
	ReleaseMerchantEscrow(ctx context.Context, db bun.IDB, runnerID, requesterID, merchantOwnerID, orderID uuid.UUID, foodAmount, runnerAmount, platformFee, refundAmount float64) error
	DeductCODPlatformFee(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, platformFee float64) error

	// Admin Actions
	GetPendingWithdrawals(ctx context.Context, limit, offset int) ([]WalletTransaction, error)
	ApproveWithdrawal(ctx context.Context, txID, actorID uuid.UUID) error
	GetTransactionStatus(ctx context.Context, reference string) (*WalletTransaction, error)
	GetSystemBalanceSummary(ctx context.Context) (*SystemBalanceSummary, error)
	GetTransactionByID(ctx context.Context, id uuid.UUID) (*WalletTransaction, error)

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
	fcm       notification.Notifier
	notifSvc  notificationDomain.Service
}

func NewService(repo Repository, userSvc user.Service, configSvc systemconfig.Service, db *bun.DB, redis *cache.Redis, auditSvc audit.Service, fcm notification.Notifier, notifSvc notificationDomain.Service) Service {
	return &service{
		repo:      repo,
		userSvc:   userSvc,
		configSvc: configSvc,
		db:        db,
		redis:     redis,
		auditSvc:  auditSvc,
		fcm:       fcm,
		notifSvc:  notifSvc,
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

	pgFeeStr := s.configSvc.GetValue(ctx, "qris_pg_fee", "0")
	configuredPGFee, _ := strconv.ParseFloat(pgFeeStr, 64)
	if configuredPGFee < 0 {
		configuredPGFee = 0
	}
	var uniqueCodeVal int
	if s.redis != nil {
		for i := 1; i <= 99; i++ {
			key := fmt.Sprintf("active_uniq:%.2f:%d", amount, i)
			ok, err := s.redis.Client().SetNX(ctx, key, "active", 15*time.Minute).Result()
			if err == nil && ok {
				uniqueCodeVal = i
				break
			}
		}
	}
	if uniqueCodeVal == 0 {
		uniqueCodeVal = rand.Intn(99) + 1
	}
	uniqueCode := float64(uniqueCodeVal)
	pgFee := configuredPGFee + uniqueCode
	grossAmt := amount + pgFee

	if config.App.UsePaymentGateway {
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
			log.Printf("[MIDTRANS-CHARGE-REQUEST] Payload: %s", string(reqJSON))
			chargeResp, midtransErr := client.ChargeTransaction(req)
			if chargeResp != nil {
				respJSON, _ := json.Marshal(chargeResp)
				log.Printf("[MIDTRANS-CHARGE-RESPONSE] Body: %s", string(respJSON))
			}
			if !isMidtransErrorNil(midtransErr) {
				log.Printf("[MIDTRANS-CHARGE-ERROR] StatusCode: %d, Message: %s", midtransErr.StatusCode, midtransErr.Message)
				if midtransErr.StatusCode == 402 {
					return nil, errors.New("saluran pembayaran GoPay belum diaktifkan pada akun Midtrans Sandbox Anda, silakan aktifkan terlebih dahulu di dashboard Midtrans")
				}
				return nil, errors.New("gagal membuat kode pembayaran GoPay/QRIS, silakan coba lagi beberapa saat lagi")
			}

			qrString = chargeResp.QRString
			for _, action := range chargeResp.Actions {
				switch action.Name {
				case "generate-qr-code":
					if qrString == "" {
						qrString = action.URL
					}
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
				"amount":       int64(grossAmt),
			}
			body, _ := json.Marshal(payload)

			pgUrl := os.Getenv("PAYMENT_GATEWAY_URL")
			if pgUrl == "" {
				pgUrl = "http://localhost:4000"
			}

			log.Printf("[MOCK-QRIS-CHARGE-REQUEST] URL: %s/api/qris/generate, Payload: %s", pgUrl, string(body))
			httpClient := &http.Client{Timeout: 10 * time.Second}
			resp, err := httpClient.Post(fmt.Sprintf("%s/api/qris/generate", pgUrl), "application/json", bytes.NewBuffer(body))
			if err != nil {
				log.Printf("[MOCK-QRIS-CHARGE-ERROR] Connection error: %v", err)
				return nil, fmt.Errorf("gagal menghubungi payment gateway: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// P0 FIX: LimitReader 1MB to prevent OOM if PG returns huge body
			respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			log.Printf("[MOCK-QRIS-CHARGE-RESPONSE] Status: %s, Body: %s", resp.Status, string(respBytes))

			var qrisResp struct {
				Status     string `json:"status"`
				TrxID      string `json:"trx_id"`
				QrisString string `json:"qris_string"`
			}
			if err := json.Unmarshal(respBytes, &qrisResp); err != nil {
				log.Printf("[MOCK-QRIS-CHARGE-ERROR] Parse error: %v", err)
				return nil, fmt.Errorf("gagal membaca respon payment gateway")
			}

			reference = qrisResp.TrxID
			qrString = qrisResp.QrisString
		}
	} else {
		// Generate dynamic QRIS locally from static template
		reference = "TOPUP-QRIS-" + uuid.New().String()[:8]
		var err error
		qrString, err = utils.ConvertStaticToDynamicQRIS(config.App.StaticQrisTemplate, grossAmt)
		if err != nil {
			log.Printf("[LOCAL-QRIS-TOPUP-ERROR] Failed to convert static QRIS: %v", err)
			return nil, fmt.Errorf("gagal membuat kode pembayaran QRIS secara mandiri: %v", err)
		}
		log.Printf("[LOCAL-QRIS-TOPUP] Generated dynamic QRIS locally for wallet top-up, Ref: %s, GrossAmt: %f", reference, grossAmt)
	}

	wtx := &WalletTransaction{
		ID:          uuid.New(),
		WalletID:    w.ID,
		Type:        TypeTopUp,
		Amount:      amount,
		PGFee:       pgFee,
		UniqueCode:  uniqueCodeVal,
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

func (s *service) FinalizeTopUp(ctx context.Context, reference string, notificationID string) (*WalletTransaction, error) {
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

		// 1. Update status using check-and-set to prevent race conditions (double credit)
		res, err := tx.NewUpdate().Model((*WalletTransaction)(nil)).
			Set("status = ?", StatusCompleted).
			Set("amount = amount + unique_code").
			Set("pg_fee = pg_fee - unique_code").
			Where("id = ?", wtx.ID).
			Where("status = ?", StatusPending).
			Exec(ctx)
		if err != nil {
			return err
		}
		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAffected == 0 {
			return errors.New("transaksi top-up sudah diproses atau tidak valid")
		}

		// 2. Update balance
		creditAmt := wtx.Amount + float64(wtx.UniqueCode)
		if err := s.repo.UpdateWalletBalance(ctx, tx, wtx.WalletID, creditAmt); err != nil {
			return err
		}

		// 3. Register notification_id in Redis if provided to prevent future listener duplication
		if notificationID != "" && s.redis != nil {
			cacheKey := fmt.Sprintf("payment_listener:processed:%s", notificationID)
			_ = s.redis.Set(ctx, cacheKey, "processed", 24*time.Hour)
		}

		// 4. Release unique code
		if wtx != nil && wtx.UniqueCode > 0 && s.redis != nil {
			cacheKey := fmt.Sprintf("active_uniq:%.2f:%d", wtx.Amount, wtx.UniqueCode)
			_ = s.redis.Del(ctx, cacheKey)
		}

		return nil
	})

	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, errors.New("transaksi top-up tidak ditemukan")
		}
		return nil, err
	}

	// Send Push and in-app Notification asynchronously to prevent blocking the webhook response
	go func() {
		bgCtx := context.Background()
		var wallet Wallet
		if err := s.db.NewSelect().Model(&wallet).Where("id = ?", wtx.WalletID).Scan(bgCtx); err != nil {
			log.Printf("[FCM-TOPUP] Gagal mendapatkan data wallet: %v", err)
			return
		}

		userObj, err := s.userSvc.GetByID(bgCtx, wallet.UserID, uuid.Nil)
		if err != nil {
			log.Printf("[FCM-TOPUP] Gagal mendapatkan data user: %v", err)
			return
		}

		title := "Top Up Berhasil"
		msg := fmt.Sprintf("Top up sebesar Rp%s berhasil ditambahkan ke saldo Anda.", strconv.FormatFloat(wtx.Amount, 'f', 0, 64))

		// Write to database in-app notification
		_ = s.notifSvc.CreateNotification(bgCtx, notificationDomain.CreateNotificationRequest{
			UserID:  wallet.UserID,
			Title:   title,
			Message: msg,
			Type:    "wallet",
			Metadata: map[string]interface{}{
				"amount":       wtx.Amount,
				"reference_id": wtx.Reference,
			},
		})

		// Send push notification via FCM
		if userObj.FcmToken != nil && *userObj.FcmToken != "" {
			_ = s.fcm.SendToDevice(bgCtx, *userObj.FcmToken, title, msg, map[string]string{
				"type":   "wallet_update",
				"amount": strconv.FormatFloat(wtx.Amount, 'f', 0, 64),
			})
		}
	}()

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

	log.Printf("[DISBURSEMENT-INQUIRY-REQUEST] URL: %s/api/disbursement/inquiry, Payload: %s", pgUrl, string(body))
	resp, err := http.DefaultClient.Do(reqHttp)
	if err != nil {
		log.Printf("[DISBURSEMENT-INQUIRY-ERROR] Connection error: %v", err)
		return nil, fmt.Errorf("gagal menghubungi payment gateway (timeout): %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBytes, _ := io.ReadAll(resp.Body)
	log.Printf("[DISBURSEMENT-INQUIRY-RESPONSE] Status: %s, Body: %s", resp.Status, string(respBytes))

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("gagal melakukan verifikasi rekening")
	}

	var res InquiryAccountResponse
	if err := json.Unmarshal(respBytes, &res); err != nil {
		log.Printf("[DISBURSEMENT-INQUIRY-ERROR] Parse error: %v", err)
		return nil, errors.New("gagal membaca respon verifikasi")
	}

	return &res, nil
}

func (s *service) RequestWithdrawal(ctx context.Context, userID uuid.UUID, amount float64, channelID *uuid.UUID, pin string, destMetadata map[string]interface{}) (*WalletTransaction, error) {
	// --- Concurrency Guard: Redis Lock ---
	lockKey := fmt.Sprintf("lock:withdraw:%s", userID.String())
	lockToken, err := s.redis.AcquireLock(ctx, lockKey, 10*time.Second)
	if err != nil || lockToken == "" {
		return nil, errors.New("permintaan penarikan sedang diproses, silakan tunggu sejenak")
	}
	defer func() { _ = s.redis.ReleaseLock(ctx, lockKey, lockToken) }()

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

	// Verify user has registered bank account
	registeredBank, err := s.userSvc.GetBankAccount(ctx, userID)
	if err != nil || registeredBank == nil {
		return nil, errors.New("rekening penarikan belum didaftarkan oleh admin, silakan hubungi admin")
	}

	// Override destMetadata with the registered bank details
	destMetadata = map[string]interface{}{
		"bank_name":    registeredBank.BankName,
		"account_no":   registeredBank.AccountNo,
		"account_name": registeredBank.AccountName,
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

	// Create in-app notification (inbox message)
	_ = s.notifSvc.CreateNotification(ctx, notificationDomain.CreateNotificationRequest{
		UserID:  userID,
		Title:   "Permintaan Penarikan Diajukan",
		Message: fmt.Sprintf("Permintaan penarikan dana sebesar Rp%.0f telah diajukan dan sedang menunggu persetujuan admin.", amount),
		Type:    "wallet",
		Metadata: map[string]interface{}{
			"amount":       amount,
			"reference_id": wtx.ID.String(),
			"status":       "pending",
		},
	})

	// 5. Trigger PG Disbursement (Async for this simulation)
	if channel != nil && channel.Type != "MANUAL" {
		go s.triggerPgDisbursement(wtx, channel)
	}

	return wtx, nil
}

func (s *service) HoldLiability(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64) error {
	w, err := s.repo.GetWalletByUserID(ctx, db, runnerID)
	if err != nil {
		return err
	}

	if w.Balance < amount {
		return errors.New("saldo jaminan tidak mencukupi untuk pesanan ini")
	}

	if err := s.repo.UpdateWalletBalance(ctx, db, w.ID, -amount); err != nil {
		return err
	}

	wtx := &WalletTransaction{
		ID:       uuid.New(),
		WalletID: w.ID,
		OrderID:  &orderID,
		Type:     TypeLiabilityHold,
		Amount:   -amount,
		Status:   StatusCompleted,
	}
	return s.repo.CreateTransaction(ctx, db, wtx)
}

func (s *service) ReleaseLiability(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64) error {
	w, err := s.repo.GetOrCreateWallet(ctx, db, runnerID)
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
		Type:     TypeLiabilityRelease,
		Amount:   amount,
		Status:   StatusCompleted,
	}
	return s.repo.CreateTransaction(ctx, db, wtx)
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
	w, err := s.repo.GetOrCreateWallet(ctx, db, runnerID)
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
		sysWID, err := s.getSystemWalletID(ctx, db)
		if err != nil {
			return err
		}
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
	w, err := s.repo.GetOrCreateWallet(ctx, db, requesterID)
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
	wRunner, err := s.repo.GetOrCreateWallet(ctx, db, runnerID)
	if err != nil {
		return err
	}
	wReq, err := s.repo.GetOrCreateWallet(ctx, db, requesterID)
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
	wRunner, err := s.repo.GetOrCreateWallet(ctx, db, runnerID)
	if err != nil {
		return err
	}
	wReq, err := s.repo.GetOrCreateWallet(ctx, db, requesterID)
	if err != nil {
		return err
	}
	sysWID, err := s.getSystemWalletID(ctx, db)
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

func (s *service) ReleaseMerchantEscrow(ctx context.Context, db bun.IDB, runnerID, requesterID, merchantOwnerID, orderID uuid.UUID, foodAmount, runnerAmount, platformFee, refundAmount float64) error {
	wRunner, err := s.repo.GetOrCreateWallet(ctx, db, runnerID)
	if err != nil {
		return err
	}
	wReq, err := s.repo.GetOrCreateWallet(ctx, db, requesterID)
	if err != nil {
		return err
	}
	wMerchant, err := s.repo.GetOrCreateWallet(ctx, db, merchantOwnerID)
	if err != nil {
		return err
	}
	sysWID, err := s.getSystemWalletID(ctx, db)
	if err != nil {
		return err
	}

	// Calculate tiered merchant commission
	t1Limit, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier1_limit", "50000"), 64)
	t2Limit, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier2_limit", "100000"), 64)
	t1Amount, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier1_amount", "1000"), 64)
	t2Amount, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier2_amount", "3000"), 64)
	t3Amount, _ := strconv.ParseFloat(s.configSvc.GetValue(ctx, "merchant_fee_tier3_amount", "5000"), 64)

	var merchantCommission float64
	if foodAmount < t1Limit {
		merchantCommission = t1Amount
	} else if foodAmount <= t2Limit {
		merchantCommission = t2Amount
	} else {
		merchantCommission = t3Amount
	}

	if merchantCommission > foodAmount {
		merchantCommission = foodAmount
	}

	merchantGets := foodAmount - merchantCommission

	// 1. Give Merchant their portion (Food price minus commission)
	if foodAmount > 0 {
		if err := s.repo.UpdateWalletBalance(ctx, db, wMerchant.ID, merchantGets); err != nil {
			return err
		}
		wtxMerchant := &WalletTransaction{
			ID:       uuid.New(),
			WalletID: wMerchant.ID,
			OrderID:  &orderID,
			Type:     TypeEscrowRelease,
			Amount:   merchantGets,
			Status:   StatusCompleted,
		}
		if err := s.repo.CreateTransaction(ctx, db, wtxMerchant); err != nil {
			return err
		}

		// Record the merchant commission as a separate transaction in System Wallet
		if merchantCommission > 0 {
			if err := s.repo.UpdateWalletBalance(ctx, db, sysWID, merchantCommission); err != nil {
				return err
			}
			commissionTx := &WalletTransaction{
				ID:        uuid.New(),
				WalletID:  sysWID,
				OrderID:   &orderID,
				Type:      TypePlatformFee,
				Amount:    merchantCommission,
				Reference: fmt.Sprintf("MERCH-FEE-%s", orderID.String()[:8]),
				Status:    StatusCompleted,
			}
			if err := s.repo.CreateTransaction(ctx, db, commissionTx); err != nil {
				return err
			}
		}
	}

	// 2. Give Runner their portion
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

	// 3. Transfer Platform Fee to System
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

	// 4. Refund Deposit to Requester
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

	w, err := s.repo.GetOrCreateWallet(ctx, db, runnerID)
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

func (s *service) GetTransactionByID(ctx context.Context, id uuid.UUID) (*WalletTransaction, error) {
	return s.repo.GetTransactionByID(ctx, s.db, id)
}

func (s *service) ApproveWithdrawal(ctx context.Context, txID, actorID uuid.UUID) error {
	err := s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
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
	if err != nil {
		return err
	}

	// Fetch transaction to get User ID and amount
	wtx, err := s.repo.GetTransactionByID(ctx, s.db, txID)
	if err == nil && wtx != nil {
		var walletObj Wallet
		if err := s.db.NewSelect().Model(&walletObj).Where("id = ?", wtx.WalletID).Scan(ctx); err == nil {
			_ = s.notifSvc.CreateNotification(ctx, notificationDomain.CreateNotificationRequest{
				UserID:  walletObj.UserID,
				Title:   "Penarikan Dana Berhasil",
				Message: fmt.Sprintf("Penarikan dana sebesar Rp%.0f telah disetujui dan berhasil dikirim.", math.Abs(wtx.Amount)),
				Type:    "wallet",
				Metadata: map[string]interface{}{
					"amount":       math.Abs(wtx.Amount),
					"reference_id": wtx.ID.String(),
					"status":       "completed",
				},
			})
		}
	}

	return nil
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

	err = s.repo.RunInTx(ctx, func(ctx context.Context, btx bun.Tx) error {
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
	if err != nil {
		return err
	}

	// Send notification if finalized successfully
	var walletObj Wallet
	if err := s.db.NewSelect().Model(&walletObj).Where("id = ?", tx.WalletID).Scan(ctx); err == nil {
		var title, message string
		switch status {
		case StatusCompleted:
			title = "Penarikan Dana Berhasil"
			message = fmt.Sprintf("Penarikan dana sebesar Rp%.0f telah disetujui dan berhasil dikirim.", math.Abs(tx.Amount))
		case StatusRejected:
			title = "Penarikan Dana Dibatalkan"
			message = fmt.Sprintf("Penarikan dana sebesar Rp%.0f telah dibatalkan dan saldo dikembalikan.", math.Abs(tx.Amount))
		case StatusFailed:
			title = "Penarikan Dana Gagal"
			message = fmt.Sprintf("Penarikan dana sebesar Rp%.0f gagal diproses dan saldo dikembalikan.", math.Abs(tx.Amount))
		}

		if title != "" {
			_ = s.notifSvc.CreateNotification(ctx, notificationDomain.CreateNotificationRequest{
				UserID:  walletObj.UserID,
				Title:   title,
				Message: message,
				Type:    "wallet",
				Metadata: map[string]interface{}{
					"amount":       math.Abs(tx.Amount),
					"reference_id": tx.ID.String(),
					"status":       string(status),
				},
			})
		}
	}

	return nil
}

func (s *service) GetSystemBalanceSummary(ctx context.Context) (*SystemBalanceSummary, error) {
	return s.repo.GetSystemBalanceSummary(ctx)
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

		log.Printf("[WALLET] Attempting to recover pending withdrawal: %s", tx.ID)

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
	log.Printf("[DISBURSEMENT-TRANSFER-REQUEST] URL: %s/api/disbursement/transfer, Payload: %s", pgUrl, string(body))
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Post(fmt.Sprintf("%s/api/disbursement/transfer", pgUrl), "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[WALLET] Error triggering PG disbursement: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	log.Printf("[DISBURSEMENT-TRANSFER-RESPONSE] Status: %d, Body: %s", resp.StatusCode, string(respBytes))
}

func isMidtransErrorNil(err *midtrans.Error) bool {
	return err == nil
}

func (s *service) getSystemWalletID(ctx context.Context, db bun.IDB) (uuid.UUID, error) {
	sysWID, _ := uuid.Parse(SystemWalletID)

	// Check if wallet exists
	var w Wallet
	err := db.NewSelect().
		Model(&w).
		Where("id = ?", sysWID).
		Scan(ctx)
	if err == nil {
		return sysWID, nil
	}

	// It doesn't exist! Let's ensure system user exists in users table first
	sysUserID, _ := uuid.Parse(SystemUserID)

	// Check if system user exists
	var u user.User
	userErr := s.db.NewSelect().
		Model(&u).
		Where("id = ?", sysUserID).
		Scan(ctx)
	if userErr != nil {
		// Insert system user raw
		_, insertUserErr := db.NewInsert().
			Table("users").
			Value("id", "?", sysUserID).
			Value("name", "?", "System Revenue").
			Value("email", "?", "system@nitip.internal").
			Value("password", "?", "$2a$10$UnUsedPasswordHashForSystemUser").
			Value("role", "?", "admin").
			Value("is_verified", "?", true).
			Exec(ctx)
		if insertUserErr != nil {
			return sysWID, insertUserErr
		}
	}

	// Now insert the system wallet raw
	_, insertWalletErr := db.NewInsert().
		Table("wallets").
		Value("id", "?", sysWID).
		Value("user_id", "?", sysUserID).
		Value("balance", "?", 0).
		Exec(ctx)
	if insertWalletErr != nil {
		return sysWID, insertWalletErr
	}

	return sysWID, nil
}
