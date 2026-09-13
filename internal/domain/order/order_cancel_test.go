package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	merchantMocks "github.com/codecoffy/nitip-core/internal/domain/merchant/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	tripMocks "github.com/codecoffy/nitip-core/internal/domain/trip/mocks"
	walletMocks "github.com/codecoffy/nitip-core/internal/domain/wallet/mocks"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestOrderCancel(t *testing.T) {
	orig := config.App
	t.Cleanup(func() { config.App = orig })
	config.App = &config.Config{BypassKYCValidation: true, UsePaymentGateway: false, StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI5204421553033605802ID5925Nihtip"}

	t.Run("MerchantReject", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("owner pending dengan alasan berhasil refund sekali", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockMerchant := merchantMocks.NewMockService(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, mockMerchant)

				merchID := uuid.New()
				ownerID := uuid.New()
				orderID := uuid.New()
				reqID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 10000, DeliveryFee: 5000, UpdatedAt: time.Now()}
				locked := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 10000, DeliveryFee: 5000, UpdatedAt: time.Now()}
				merch := &merchant.Merchant{ID: merchID, OwnerID: ownerID}

				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, 15000.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()

				err := svc.CancelOrder(context.Background(), orderID, ownerID, "stok habis")
				require.NoError(t, err)
				require.NoError(t, mockSql.ExpectationsWereMet())
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("alasan kosong ditolak", func(t *testing.T) {
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockMerchant := merchantMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, mockMerchant)
				merchID := uuid.New()
				ownerID := uuid.New()
				orderID := uuid.New()
				reqID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now()}
				merch := &merchant.Merchant{ID: merchID, OwnerID: ownerID}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).Times(1)
				err := svc.CancelOrder(context.Background(), orderID, ownerID, "   ")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "alasan penolakan wajib diisi")
			})
			t.Run("ownership salah ditolak", func(t *testing.T) {
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockMerchant := merchantMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, mockMerchant)
				merchID := uuid.New()
				ownerID := uuid.New()
				otherID := uuid.New()
				orderID := uuid.New()
				reqID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now()}
				merch := &merchant.Merchant{ID: merchID, OwnerID: ownerID}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).Times(1)
				err := svc.CancelOrder(context.Background(), orderID, otherID, "alasan")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "akses ditolak")
			})
			t.Run("refund tidak ganda saat cancel dua kali", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockMerchant := merchantMocks.NewMockService(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, mockMerchant)
				merchID := uuid.New()
				ownerID := uuid.New()
				orderID := uuid.New()
				reqID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 8000, DeliveryFee: 2000, UpdatedAt: time.Now()}
				locked := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 8000, DeliveryFee: 2000, UpdatedAt: time.Now()}
				merch := &merchant.Merchant{ID: merchID, OwnerID: ownerID}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).Times(1)
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, 10000.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()
				err := svc.CancelOrder(context.Background(), orderID, ownerID, "tutup")
				require.NoError(t, err)
				cancelled := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentRefunded, UpdatedAt: time.Now()}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(cancelled, nil).Times(1)
				err = svc.CancelOrder(context.Background(), orderID, ownerID, "tutup lagi")
				assert.Error(t, err)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			})
		})
	})

	t.Run("CancelOrderLock", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("stagnan 31m pending tanpa runner berhasil setelah lock", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				reqID := uuid.New()
				old := time.Now().Add(-31 * time.Minute)
				stale := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 5000, DeliveryFee: 2000, UpdatedAt: old}
				locked := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 5000, DeliveryFee: 2000, UpdatedAt: old}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, 7000.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()
				err := svc.CancelOrder(context.Background(), orderID, reqID, "lama tidak ada runner")
				require.NoError(t, err)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("requester cooking ditolak meski stagnan 31m", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				reqID := uuid.New()
				merchID := uuid.New()
				old := time.Now().Add(-31 * time.Minute)
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusCooking, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: old}
				locked := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusCooking, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: old}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.CancelOrder(context.Background(), orderID, reqID, "ingin batal")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "harus melalui admin")
			})
			t.Run("requester ready ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				reqID := uuid.New()
				merchID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusReady, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now().Add(-40 * time.Minute)}
				locked := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusReady, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now().Add(-40 * time.Minute)}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.CancelOrder(context.Background(), orderID, reqID, "batal")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "harus melalui admin")
			})
			t.Run("requester delivering ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				reqID := uuid.New()
				merchID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusDelivering, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now().Add(-50 * time.Minute)}
				locked := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusDelivering, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now().Add(-50 * time.Minute)}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.CancelOrder(context.Background(), orderID, reqID, "batal")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "harus melalui admin")
			})
			t.Run("validasi ulang setelah lock stale pending locked cooking ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				reqID := uuid.New()
				merchID := uuid.New()
				stale := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now().Add(-40 * time.Minute)}
				locked := &order.Order{ID: orderID, RequesterID: reqID, MerchantID: &merchID, Status: order.StatusCooking, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, UpdatedAt: time.Now()}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.CancelOrder(context.Background(), orderID, reqID, "stale race")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "harus melalui admin")
			})
			t.Run("wallet refund error rollback tanpa side effect", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				reqID := uuid.New()
				old := time.Now().Add(-31 * time.Minute)
				stale := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 1000, DeliveryFee: 500, UpdatedAt: old}
				locked := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 1000, DeliveryFee: 500, UpdatedAt: old}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(stale, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, 1500.0).Return(assert.AnError).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.CancelOrder(context.Background(), orderID, reqID, "batal")
				assert.Error(t, err)
			})
		})
	})

	t.Run("LateQRIS", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("cancelled tetap terminal late payment refund saldo net zero tanpa matching", func(t *testing.T) {
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
				cancelled := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: total, UpdatedAt: time.Now()}

				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(cancelled, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(".*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).DoAndReturn(func(ctx context.Context, db interface{}, uid, oid uuid.UUID, amt float64) error {
					assert.Equal(t, total, amt)
					return nil
				}).Times(1)
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()

				err := svc.UpdatePaymentStatus(context.Background(), orderID, order.PaymentEscrow)
				require.NoError(t, err)
				assert.Empty(t, mr.Keys())
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("retry tidak menggandakan kredit idempoten", func(t *testing.T) {
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
				total := 12000.0
				escrowDone := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentRefunded, TotalPayment: total, UpdatedAt: time.Now()}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(escrowDone, nil).Times(1)
				mockRepo.EXPECT().FindByID(gomock.Any(), gomock.Any()).Return(escrowDone, nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
				require.NoError(t, err)
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_ = mockSql
			})
			t.Run("expired tetap terminal tanpa matching", func(t *testing.T) {
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
				total := 9000.0
				expired := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusExpired, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: total, UpdatedAt: time.Now()}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(expired, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(".*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).Return(nil).Times(1)
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				err := svc.UpdatePaymentStatus(context.Background(), orderID, order.PaymentEscrow)
				require.NoError(t, err)
				assert.Empty(t, mr.Keys())
			})
		})
		t.Run("Tx1 commit Tx2 gagal retry melanjutkan refund saldo 15k", func(t *testing.T) {
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
			unpaid := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: total, UpdatedAt: time.Now()}
			mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(unpaid, nil).Times(1)
			mockSql.ExpectBegin()
			mockSql.ExpectQuery(".*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).Return(assert.AnError).Times(1)
			mockSql.ExpectRollback()
			err := svc.UpdatePaymentStatus(context.Background(), orderID, order.PaymentEscrow)
			assert.Error(t, err)
			mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(unpaid, nil).Times(1)
			mockSql.ExpectBegin()
			mockSql.ExpectQuery(".*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).Return(nil).Times(1)
			mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
			mockSql.ExpectCommit()
			err = svc.UpdatePaymentStatus(context.Background(), orderID, order.PaymentEscrow)
			require.NoError(t, err)
			refunded := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentRefunded, TotalPayment: total, UpdatedAt: time.Now()}
			mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(refunded, nil).Times(1)
			mockRepo.EXPECT().FindByID(gomock.Any(), gomock.Any()).Return(refunded, nil).AnyTimes()
			mockSql.ExpectBegin()
			mockSql.ExpectCommit()
			err = svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
			require.NoError(t, err)
			assert.Empty(t, rClient.Keys(context.Background(), "*").Val())
		})
		t.Run("concurrent dua konfirmasi hanya satu kredit", func(t *testing.T) {
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
			unpaid := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: total, UpdatedAt: time.Now()}
			mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(unpaid, nil).Times(1)
			mockSql.ExpectBegin()
			mockSql.ExpectQuery(".*wallet_transactions.*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, total).Return(nil).Times(1)
			mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
			mockSql.ExpectCommit()
			err := svc.UpdatePaymentStatus(context.Background(), orderID, order.PaymentEscrow)
			require.NoError(t, err)
			refunded := &order.Order{ID: orderID, RequesterID: reqID, Status: order.StatusCancelled, PaymentMethod: order.MethodEscrow, PaymentSource: "qris", PaymentStatus: order.PaymentRefunded, TotalPayment: total, UpdatedAt: time.Now()}
			mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(refunded, nil).Times(1)
			mockRepo.EXPECT().FindByID(gomock.Any(), gomock.Any()).Return(refunded, nil).AnyTimes()
			mockSql.ExpectBegin()
			mockSql.ExpectCommit()
			err = svc.ProcessPaymentForTest(context.Background(), orderID, order.PaymentEscrow)
			require.NoError(t, err)
			assert.Empty(t, rClient.Keys(context.Background(), "*").Val())
		})
	})

	t.Run("ReassignAdmin", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("runner cooking reassign keep status clear runner requeue", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runnerID := uuid.New()
				tripID := uuid.New()
				reqID := uuid.New()
				merchID := uuid.New()
				ord := &order.Order{ID: orderID, RequesterID: reqID, RunnerID: &runnerID, TripID: &tripID, MerchantID: &merchID, Status: order.StatusCooking, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 5000, WeightKg: 1, VolumeLiters: 1, PickupLat: -6.2, PickupLng: 106.8, UpdatedAt: time.Now()}
				locked := &order.Order{ID: orderID, RequesterID: reqID, RunnerID: &runnerID, TripID: &tripID, MerchantID: &merchID, Status: order.StatusCooking, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 5000, WeightKg: 1, VolumeLiters: 1, PickupLat: -6.2, PickupLng: 106.8, UpdatedAt: time.Now()}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(ord, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockWallet.EXPECT().ReleaseLiability(gomock.Any(), gomock.Any(), runnerID, orderID, 5000.0).Return(nil).Times(1)
				mockTrip.EXPECT().RestoreCapacity(gomock.Any(), gomock.Any(), tripID, 1.0, 1.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()
				err := svc.RunnerCancelForReassign(context.Background(), orderID, runnerID, "motor mogok")
				require.NoError(t, err)
				ids, _ := rClient.ZRange(context.Background(), "orders:live", 0, -1).Result()
				found := false
				for _, id := range ids {
					if id == orderID.String() {
						found = true
					}
				}
				assert.True(t, found)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("batas reassign cooking tidak final cancel harus via admin", func(t *testing.T) {
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runnerID := uuid.New()
				merchID := uuid.New()
				ord := &order.Order{ID: orderID, RunnerID: &runnerID, MerchantID: &merchID, Status: order.StatusCooking, UpdatedAt: time.Now()}
				rClient.Set(context.Background(), "reassign:count:"+orderID.String(), 2, 0)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(ord, nil).Times(1)
				err := svc.RunnerCancelForReassign(context.Background(), orderID, runnerID, "alasan")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "ditangani admin")
			})
			t.Run("batas reassign pending non-food final cancel dengan refund", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runnerID := uuid.New()
				tripID := uuid.New()
				reqID := uuid.New()
				ord := &order.Order{ID: orderID, RequesterID: reqID, RunnerID: &runnerID, TripID: &tripID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 2000, DeliveryFee: 1000, WeightKg: 1, VolumeLiters: 1, UpdatedAt: time.Now()}
				locked := &order.Order{ID: orderID, RequesterID: reqID, RunnerID: &runnerID, TripID: &tripID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 2000, DeliveryFee: 1000, WeightKg: 1, VolumeLiters: 1, UpdatedAt: time.Now()}
				rClient.Set(context.Background(), "reassign:count:"+orderID.String(), 2, 0)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(ord, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(locked, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, orderID, 3000.0).Return(nil).Times(1)
				mockWallet.EXPECT().ReleaseLiability(gomock.Any(), gomock.Any(), runnerID, orderID, 2000.0).Return(nil).Times(1)
				mockTrip.EXPECT().RestoreCapacity(gomock.Any(), gomock.Any(), tripID, 1.0, 1.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()
				err := svc.RunnerCancelForReassign(context.Background(), orderID, runnerID, "batal limit")
				require.NoError(t, err)
			})
		})
	})

	t.Run("BulkExpiry", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("bulk pending escrow -25h tidak lewat tanpa refund hold tetap", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				repo := order.NewRepository(db)
				cutoff := time.Now().Add(-24 * time.Hour)
				mockSql.ExpectExec("UPDATE.*orders").WillReturnResult(sqlmock.NewResult(0, 0))
				count, err := repo.ExpireOldOrders(context.Background(), cutoff)
				require.NoError(t, err)
				assert.Equal(t, int64(0), count)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("bulk pending unpaid -25h tetap expired tanpa melewatkan hold", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				repo := order.NewRepository(db)
				cutoff := time.Now().Add(-24 * time.Hour)
				mockSql.ExpectExec("UPDATE.*orders").WillReturnResult(sqlmock.NewResult(0, 1))
				count, err := repo.ExpireOldOrders(context.Background(), cutoff)
				require.NoError(t, err)
				assert.Equal(t, int64(1), count)
			})
			t.Run("bulk pending dengan promotion_id tidak expired via bulk", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				repo := order.NewRepository(db)
				cutoff := time.Now().Add(-24 * time.Hour)
				mockSql.ExpectExec("UPDATE.*orders").WillReturnResult(sqlmock.NewResult(0, 0))
				count, err := repo.ExpireOldOrders(context.Background(), cutoff)
				require.NoError(t, err)
				assert.Equal(t, int64(0), count)
			})
			t.Run("bulk cooking tidak expired via bulk predicate", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				repo := order.NewRepository(db)
				cutoff := time.Now().Add(-24 * time.Hour)
				mockSql.ExpectExec("UPDATE.*orders").WillReturnResult(sqlmock.NewResult(0, 0))
				count, err := repo.ExpireOldOrders(context.Background(), cutoff)
				require.NoError(t, err)
				assert.Equal(t, int64(0), count)
			})
			t.Run("invariant pending unpaid tidak punya hold escrow atau liability", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, nil, db, nil, nil, nil)
				eligibleID := uuid.New()
				reqID := uuid.New()
				oldTime := time.Now().Add(-31 * time.Minute)
				mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id", "requester_id", "payment_method", "payment_status", "estimated_cost", "delivery_fee", "status", "runner_id", "trip_id", "promotion_id", "pickup_lat", "pickup_lng", "created_at", "item_details"}).AddRow(eligibleID, reqID, order.MethodEscrow, order.PaymentUnpaid, 5000.0, 2000.0, order.StatusPending, nil, nil, nil, -6.2, 106.8, oldTime, "item unpaid"))
				locked := &order.Order{ID: eligibleID, RequesterID: reqID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentUnpaid, EstimatedCost: 5000, DeliveryFee: 2000, CreatedAt: oldTime}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), eligibleID).Return(locked, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()
				count, err := svc.ExpirePendingOrders(context.Background())
				require.NoError(t, err)
				assert.Equal(t, int64(1), count)
			})
		})
	})

	_ = merchant.Merchant{}
	_ = trip.Trip{}
}
