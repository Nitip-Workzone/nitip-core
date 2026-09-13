package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	walletMocks "github.com/codecoffy/nitip-core/internal/domain/wallet/mocks"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestLateQRIS_RealLedger(t *testing.T) {
	orig := config.App
	t.Cleanup(func() { config.App = orig })
	config.App = &config.Config{BypassKYCValidation: true, UsePaymentGateway: false, StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI5204421553033605802ID5925Nihtip"}

	t.Run("saldo awal 0 late qris 15k menjadi 15k tanpa hold status terminal retry tetap 15k", func(t *testing.T) {
		db, mockSql := testutil.NewMockDB(t)
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		wRepo := wallet.NewRepository(db)
		wSvc := wallet.NewService(wRepo, nil, nil, db, redisCache, nil, nil, nil)
		orderID := uuid.New()
		reqID := uuid.New()
		walletID := uuid.New()
		total := 15000.0
		now := time.Now()
		mockSql.ExpectQuery("SELECT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}))
		mockSql.ExpectQuery("INSERT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).AddRow(walletID, reqID, 0.0, now, now))
		mockSql.ExpectExec("UPDATE.*wallets.*").WillReturnResult(sqlmock.NewResult(1, 1))
		mockSql.ExpectQuery("INSERT.*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New().String()))
		err := wSvc.RefundEscrow(context.Background(), db, reqID, orderID, total)
		require.NoError(t, err)
		mockSql.ExpectQuery("SELECT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).AddRow(walletID, reqID, 15000.0, now, now))
		w, err := wSvc.GetBalance(context.Background(), reqID)
		require.NoError(t, err)
		assert.Equal(t, 15000.0, w.Balance)
		mockSql.ExpectQuery("SELECT.*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"id", "wallet_id", "order_id", "type", "amount", "status"}).AddRow(uuid.New(), walletID, orderID, wallet.TypeRefund, 15000.0, wallet.StatusCompleted))
		txs, err := wRepo.GetTransactionsByWalletID(context.Background(), db, walletID, 10, 0)
		require.NoError(t, err)
		require.Len(t, txs, 1)
		assert.Equal(t, wallet.TypeRefund, txs[0].Type)
		assert.Equal(t, 15000.0, txs[0].Amount)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		svc := order.NewService(mockRepo, nil, nil, nil, wSvc, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
		svc.StartPaymentWorkerPool(context.Background(), 2)
		refunded := &order.Order{ID: orderID, RequesterID: reqID, Status: "cancelled", PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentRefunded, TotalPayment: total, CreatedAt: now, UpdatedAt: now}
		mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(refunded, nil).Times(1)
		mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(refunded, nil).AnyTimes()
		mockSql.ExpectBegin()
		mockSql.ExpectCommit()
		err = svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
		require.NoError(t, err)
		mockSql.ExpectQuery("SELECT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).AddRow(walletID, reqID, 15000.0, now, now))
		w, _ = wSvc.GetBalance(context.Background(), reqID)
		assert.Equal(t, 15000.0, w.Balance)
		assert.Empty(t, rClient.Keys(context.Background(), "*").Val())
	})

	t.Run("recovery escrow commit refund retry lanjut", func(t *testing.T) {
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		wRepo := wallet.NewRepository(db)
		wSvc := wallet.NewService(wRepo, nil, nil, db, redisCache, nil, nil, nil)
		svc := order.NewService(mockRepo, nil, nil, nil, wSvc, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
		svc.StartPaymentWorkerPool(context.Background(), 2)
		orderID := uuid.New()
		reqID := uuid.New()
		walletID := uuid.New()
		total := 15000.0
		now := time.Now()
		escrowStuck := &order.Order{ID: orderID, RequesterID: reqID, Status: "cancelled", PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentEscrow, TotalPayment: total, CreatedAt: now, UpdatedAt: now}
		mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(escrowStuck, nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mockSql.ExpectQuery("SELECT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}))
		mockSql.ExpectQuery("INSERT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).AddRow(walletID, reqID, 0.0, now, now))
		mockSql.ExpectExec("UPDATE.*wallets.*").WillReturnResult(sqlmock.NewResult(1, 1))
		mockSql.ExpectQuery("INSERT.*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uuid.New().String()))
		mockSql.ExpectExec("UPDATE.*orders.*").WillReturnResult(sqlmock.NewResult(1, 1))
		mockSql.ExpectCommit()
		err := svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
		require.NoError(t, err)
		mockSql.ExpectQuery("SELECT.*wallets.*").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "balance", "created_at", "updated_at"}).AddRow(walletID, reqID, 15000.0, now, now))
		w, _ := wSvc.GetBalance(context.Background(), reqID)
		assert.Equal(t, 15000.0, w.Balance)
		assert.Empty(t, rClient.Keys(context.Background(), "*").Val())
	})

	t.Run("kegagalan sebelum dan sesudah kredit rollback tidak ganda", func(t *testing.T) {
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
		svc.StartPaymentWorkerPool(context.Background(), 2)
		orderID := uuid.New()
		reqID := uuid.New()
		total := 15000.0
		now := time.Now()
		unpaid := &order.Order{ID: orderID, RequesterID: reqID, Status: "cancelled", PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: "unpaid", TotalPayment: total, CreatedAt: now, UpdatedAt: now}
		balance := 0.0
		mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(unpaid, nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).Return(assert.AnError).Times(1)
		mockSql.ExpectRollback()
		err := svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
		assert.Error(t, err)
		assert.Equal(t, 0.0, balance)
		mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(unpaid, nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).DoAndReturn(func(ctx context.Context, db interface{}, uid, oid uuid.UUID, amt float64) error {
			return assert.AnError
		}).Times(1)
		mockSql.ExpectRollback()
		err = svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
		assert.Error(t, err)
		assert.Equal(t, 0.0, balance)
		mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(unpaid, nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).DoAndReturn(func(ctx context.Context, db interface{}, uid, oid uuid.UUID, amt float64) error {
			balance += amt
			return nil
		}).Times(1)
		mockSql.ExpectExec("UPDATE.*orders.*").WillReturnResult(sqlmock.NewResult(1, 1))
		mockSql.ExpectCommit()
		err = svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
		require.NoError(t, err)
		assert.Equal(t, 15000.0, balance)
	})
}
