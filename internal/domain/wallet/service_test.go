package wallet_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	walletMocks "github.com/codecoffy/nitip-core/internal/domain/wallet/mocks"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestReleaseMerchantEscrow(t *testing.T) {
	runnerID := uuid.New()
	requesterID := uuid.New()
	merchantOwnerID := uuid.New()
	orderID := uuid.New()

	t.Run("Successfully release escrow for Tier 1 food price (< 50000)", func(t *testing.T) {
		db, mockSql := testutil.NewMockDB(t)
		mockRepo := new(walletMocks.Repository)
		mockConfig := new(configMocks.Service)

		svc := wallet.NewService(mockRepo, nil, mockConfig, db, nil, nil, nil, nil)

		// 1. Mock Repository GetOrCreateWallet
		runnerWallet := &wallet.Wallet{ID: uuid.New(), UserID: runnerID, Balance: 0}
		reqWallet := &wallet.Wallet{ID: uuid.New(), UserID: requesterID, Balance: 0}
		merchWallet := &wallet.Wallet{ID: uuid.New(), UserID: merchantOwnerID, Balance: 0}

		mockRepo.On("GetOrCreateWallet", mock.Anything, mock.Anything, runnerID).Return(runnerWallet, nil)
		mockRepo.On("GetOrCreateWallet", mock.Anything, mock.Anything, requesterID).Return(reqWallet, nil)
		mockRepo.On("GetOrCreateWallet", mock.Anything, mock.Anything, merchantOwnerID).Return(merchWallet, nil)

		// 2. Mock Config Service for Tiered Fees
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier1_limit", "50000").Return("50000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier2_limit", "100000").Return("100000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier1_amount", "1000").Return("1000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier2_amount", "3000").Return("3000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier3_amount", "5000").Return("5000")

		// 3. Mock Database Query for System Wallet Lookup
		sysWID, _ := uuid.Parse(wallet.SystemWalletID)
		sysRows := sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).
			AddRow(sysWID, wallet.SystemUserID, 5000.0, time.Now(), time.Now())
		mockSql.ExpectQuery(`(?i)SELECT .* FROM "wallets"`).
			WillReturnRows(sysRows)

		// Food Price: Rp30.000 -> Tier 1 (Potongan Rp1.000)
		foodAmount := 30000.0
		expectedMerchantGets := 29000.0
		expectedCommission := 1000.0
		platformFee := 2000.0

		// 4. Mock Repository UpdateWalletBalance
		// Merchant gets clean food price
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, merchWallet.ID, expectedMerchantGets).Return(nil)
		// Runner gets runnerAmount
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, runnerWallet.ID, 15000.0).Return(nil)
		// System wallet gets merchantCommission and platformFee separately
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, sysWID, expectedCommission).Return(nil)
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, sysWID, platformFee).Return(nil)
		// Requester gets refundAmount (deposit)
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, reqWallet.ID, 5000.0).Return(nil)

		// 5. Mock Repository CreateTransaction
		mockRepo.On("CreateTransaction", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// 6. Execute
		err := svc.ReleaseMerchantEscrow(context.Background(), db, runnerID, requesterID, merchantOwnerID, orderID, foodAmount, 15000.0, platformFee, 5000.0)
		assert.NoError(t, err)

		mockRepo.AssertExpectations(t)
		mockConfig.AssertExpectations(t)
	})

	t.Run("Successfully release escrow for Tier 3 food price (> 100000)", func(t *testing.T) {
		db, mockSql := testutil.NewMockDB(t)
		mockRepo := new(walletMocks.Repository)
		mockConfig := new(configMocks.Service)

		svc := wallet.NewService(mockRepo, nil, mockConfig, db, nil, nil, nil, nil)

		// 1. Mock Repository GetOrCreateWallet
		runnerWallet := &wallet.Wallet{ID: uuid.New(), UserID: runnerID, Balance: 0}
		reqWallet := &wallet.Wallet{ID: uuid.New(), UserID: requesterID, Balance: 0}
		merchWallet := &wallet.Wallet{ID: uuid.New(), UserID: merchantOwnerID, Balance: 0}

		mockRepo.On("GetOrCreateWallet", mock.Anything, mock.Anything, runnerID).Return(runnerWallet, nil)
		mockRepo.On("GetOrCreateWallet", mock.Anything, mock.Anything, requesterID).Return(reqWallet, nil)
		mockRepo.On("GetOrCreateWallet", mock.Anything, mock.Anything, merchantOwnerID).Return(merchWallet, nil)

		// 2. Mock Config Service for Tiered Fees
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier1_limit", "50000").Return("50000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier2_limit", "100000").Return("100000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier1_amount", "1000").Return("1000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier2_amount", "3000").Return("3000")
		mockConfig.On("GetValue", mock.Anything, "merchant_fee_tier3_amount", "5000").Return("5000")

		// 3. Mock Database Query for System Wallet Lookup
		sysWID, _ := uuid.Parse(wallet.SystemWalletID)
		sysRows := sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).
			AddRow(sysWID, wallet.SystemUserID, 5000.0, time.Now(), time.Now())
		mockSql.ExpectQuery(`(?i)SELECT .* FROM "wallets"`).
			WillReturnRows(sysRows)

		// Food Price: Rp150.000 -> Tier 3 (Potongan Rp5.000)
		foodAmount := 150000.0
		expectedMerchantGets := 145000.0
		expectedCommission := 5000.0
		platformFee := 2000.0

		// 4. Mock Repository UpdateWalletBalance
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, merchWallet.ID, expectedMerchantGets).Return(nil)
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, runnerWallet.ID, 15000.0).Return(nil)
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, sysWID, expectedCommission).Return(nil)
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, sysWID, platformFee).Return(nil)
		mockRepo.On("UpdateWalletBalance", mock.Anything, mock.Anything, reqWallet.ID, 5000.0).Return(nil)

		// 5. Mock Repository CreateTransaction
		mockRepo.On("CreateTransaction", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// 6. Execute
		err := svc.ReleaseMerchantEscrow(context.Background(), db, runnerID, requesterID, merchantOwnerID, orderID, foodAmount, 15000.0, platformFee, 5000.0)
		assert.NoError(t, err)

		mockRepo.AssertExpectations(t)
		mockConfig.AssertExpectations(t)
	})
}
