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
	"go.uber.org/mock/gomock"
)

func TestWallet(t *testing.T) {
	t.Run("ReleaseMerchantEscrow", func(t *testing.T) {
		runnerID := uuid.New()
		requesterID := uuid.New()
		merchantOwnerID := uuid.New()
		orderID := uuid.New()

		t.Run("positive", func(t *testing.T) {
			t.Run("escrow dilepas untuk harga makanan tier 1 di bawah 50000", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				db, mockSql := testutil.NewMockDB(t)
				mockRepo := walletMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)

				svc := wallet.NewService(mockRepo, nil, mockConfig, db, nil, nil, nil, nil)

				runnerWallet := &wallet.Wallet{ID: uuid.New(), UserID: runnerID, Balance: 0}
				reqWallet := &wallet.Wallet{ID: uuid.New(), UserID: requesterID, Balance: 0}
				merchWallet := &wallet.Wallet{ID: uuid.New(), UserID: merchantOwnerID, Balance: 0}

				mockRepo.EXPECT().GetOrCreateWallet(gomock.Any(), gomock.Any(), runnerID).Return(runnerWallet, nil).Times(1)
				mockRepo.EXPECT().GetOrCreateWallet(gomock.Any(), gomock.Any(), requesterID).Return(reqWallet, nil).Times(1)
				mockRepo.EXPECT().GetOrCreateWallet(gomock.Any(), gomock.Any(), merchantOwnerID).Return(merchWallet, nil).Times(1)

				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier1_limit", "50000").Return("50000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier2_limit", "100000").Return("100000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier1_amount", "1000").Return("1000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier2_amount", "3000").Return("3000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier3_amount", "5000").Return("5000").Times(1)

				sysWID, _ := uuid.Parse(wallet.SystemWalletID)
				sysRows := sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).
					AddRow(sysWID, wallet.SystemUserID, 5000.0, time.Now(), time.Now())
				mockSql.ExpectQuery(`(?i)SELECT .* FROM "wallets"`).
					WillReturnRows(sysRows)

				foodAmount := 30000.0
				expectedMerchantGets := 29000.0
				expectedCommission := 1000.0
				platformFee := 2000.0

				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), merchWallet.ID, expectedMerchantGets).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), sysWID, expectedCommission).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), runnerWallet.ID, 15000.0).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), sysWID, platformFee).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), reqWallet.ID, 5000.0).Return(nil).Times(1)

				mockRepo.EXPECT().CreateTransaction(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(5)

				err := svc.ReleaseMerchantEscrow(context.Background(), db, runnerID, requesterID, merchantOwnerID, orderID, foodAmount, 15000.0, platformFee, 5000.0)
				assert.NoError(t, err)
			})

			t.Run("escrow dilepas untuk harga makanan tier 3 di atas 100000", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				db, mockSql := testutil.NewMockDB(t)
				mockRepo := walletMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)

				svc := wallet.NewService(mockRepo, nil, mockConfig, db, nil, nil, nil, nil)

				runnerWallet := &wallet.Wallet{ID: uuid.New(), UserID: runnerID, Balance: 0}
				reqWallet := &wallet.Wallet{ID: uuid.New(), UserID: requesterID, Balance: 0}
				merchWallet := &wallet.Wallet{ID: uuid.New(), UserID: merchantOwnerID, Balance: 0}

				mockRepo.EXPECT().GetOrCreateWallet(gomock.Any(), gomock.Any(), runnerID).Return(runnerWallet, nil).Times(1)
				mockRepo.EXPECT().GetOrCreateWallet(gomock.Any(), gomock.Any(), requesterID).Return(reqWallet, nil).Times(1)
				mockRepo.EXPECT().GetOrCreateWallet(gomock.Any(), gomock.Any(), merchantOwnerID).Return(merchWallet, nil).Times(1)

				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier1_limit", "50000").Return("50000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier2_limit", "100000").Return("100000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier1_amount", "1000").Return("1000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier2_amount", "3000").Return("3000").Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), "merchant_fee_tier3_amount", "5000").Return("5000").Times(1)

				sysWID, _ := uuid.Parse(wallet.SystemWalletID)
				sysRows := sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).
					AddRow(sysWID, wallet.SystemUserID, 5000.0, time.Now(), time.Now())
				mockSql.ExpectQuery(`(?i)SELECT .* FROM "wallets"`).
					WillReturnRows(sysRows)

				foodAmount := 150000.0
				expectedMerchantGets := 145000.0
				expectedCommission := 5000.0
				platformFee := 2000.0

				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), merchWallet.ID, expectedMerchantGets).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), sysWID, expectedCommission).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), runnerWallet.ID, 15000.0).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), sysWID, platformFee).Return(nil).Times(1)
				mockRepo.EXPECT().UpdateWalletBalance(gomock.Any(), gomock.Any(), reqWallet.ID, 5000.0).Return(nil).Times(1)

				mockRepo.EXPECT().CreateTransaction(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(5)

				err := svc.ReleaseMerchantEscrow(context.Background(), db, runnerID, requesterID, merchantOwnerID, orderID, foodAmount, 15000.0, platformFee, 5000.0)
				assert.NoError(t, err)
			})
		})
	})
}
