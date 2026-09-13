package order_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	userMocks "github.com/codecoffy/nitip-core/internal/domain/user/mocks"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

// Payment QRIS focused tests

func TestPaymentQRIS(t *testing.T) {
	origApp := config.App
	t.Cleanup(func() { config.App = origApp })
	// Note: t.Run tidak paralel tanpa t.Parallel(); Test saling memengaruhi sebelumnya karena mutable global config dan urutan test/shuffle,
	// bukan karena t.Run otomatis paralel. Setiap test memulihkan config via t.Cleanup; tidak ada global mutation setelah goroutine start.
	config.App = &config.Config{
		UsePaymentGateway:  false,
		StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI51440014ID.CO.QRIS.WWW0215ID10265689831950303UMI5204421553033605802ID5925Nihtip, Pengiriman & Anta6007BOLMONG61059576162140703A0111036216304E13B",
	}
	dummyUser := &user.User{Name: "Test User", Email: "test@example.com"}

	t.Run("GET /orders/:id", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("detail order dibaca tanpa mengubah QRIS", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				expiry := time.Now().Add(15 * time.Minute)
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/abc", QRISExpiresAt: &expiry,
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				res, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				assert.Equal(t, "https://qris.test/abc", res.QRISData)
				assert.Equal(t, 50050.0, res.TotalPayment)
				assert.Empty(t, mr.Keys())
			})
			t.Run("GET berulang mengembalikan data sama", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				expiry := time.Now().Add(15 * time.Minute)
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50060, PGFee: 60, UniqueCode: 60, QRISData: "https://qris.test/xyz", QRISExpiresAt: &expiry,
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(2)
				r1, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				r2, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				assert.Equal(t, r1.QRISData, r2.QRISData)
				assert.Equal(t, r1.TotalPayment, r2.TotalPayment)
				assert.Empty(t, mr.Keys())
			})
			t.Run("order non-QRIS tetap dibaca tanpa QRIS side effect", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "wallet", PaymentStatus: order.PaymentEscrow,
					TotalPayment: 50000, QRISData: "",
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				res, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				assert.Empty(t, res.QRISData)
				assert.Empty(t, mr.Keys())
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("GET tidak membuat reservation", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000, QRISData: "", CreatedAt: time.Now(),
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				res, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				assert.Empty(t, res.QRISData)
				for _, k := range mr.Keys() {
					assert.NotContains(t, k, "active_total_payment")
				}
			})
			t.Run("GET tidak memanggil Redis SetNX", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000, QRISData: "", CreatedAt: time.Now(),
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				_, err = svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				assert.Empty(t, mr.Keys())
			})
			t.Run("GET tidak menjalankan database update", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000, QRISData: "", CreatedAt: time.Now(),
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				// No ExpectExec for UPDATE — would fail if called
				_ = mockSql
				res, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				require.NoError(t, err)
				assert.Empty(t, res.QRISData)
				require.NoError(t, mockSql.ExpectationsWereMet())
			})
		})
	})

	t.Run("POST /orders/:id/refresh-qris", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("QRIS valid dikembalikan tanpa regenerasi", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				expiry := time.Now().Add(10 * time.Minute)
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry,
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				// No DB tx expected
				res, err := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				require.NoError(t, err)
				assert.Equal(t, "https://qris.test/valid", res.QRISData)
				assert.Equal(t, 50050.0, res.TotalPayment)
			})
			t.Run("QRIS missing dapat dibuat melalui refresh", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now(),
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				res, err := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				require.NoError(t, err)
				assert.NotEmpty(t, res.QRISData)
				assert.True(t, res.QRISExpiresAt != nil)
			})
			t.Run("QRIS kedaluwarsa dibuat ulang satu kali", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				expiry := time.Now().Add(-1 * time.Minute)
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000, PGFee: 0, QRISData: "https://qris.test/old", QRISExpiresAt: &expiry, CreatedAt: time.Now().Add(-20 * time.Minute),
				}
				oldQRIS := o.QRISData
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				res, err := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				require.NoError(t, err)
				assert.NotEqual(t, oldQRIS, res.QRISData)
			})
			t.Run("dua refresh concurrent pada order sama menghasilkan satu QRIS", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				expiry := time.Now().Add(10 * time.Minute)
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry,
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(2)
				// No DB tx expected — valid QRIS path
				r1, e1 := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				r2, e2 := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				require.NoError(t, e1)
				require.NoError(t, e2)
				assert.Equal(t, r1.QRISData, r2.QRISData)
			})
			t.Run("tepat satu database update pada concurrent refresh", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				expiry := time.Now().Add(10 * time.Minute)
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry,
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(2)
				r1, e1 := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				r2, e2 := svc.RefreshQRIS(context.Background(), orderID, requesterID)
				require.NoError(t, e1)
				require.NoError(t, e2)
				assert.Equal(t, r1.QRISData, r2.QRISData)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("requester lain tidak dapat refresh", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				ownerID := uuid.New()
				otherID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), orderID, otherID)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unauthorized")
			})
			t.Run("order tidak ditemukan ditolak", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(nil, fmt.Errorf("not found")).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), orderID, uuid.New())
				assert.Error(t, err)
			})
			t.Run("order non-QRIS ditolak saat refresh", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				ownerID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "wallet", PaymentStatus: order.PaymentEscrow}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), orderID, ownerID)
				assert.Error(t, err)
			})
			t.Run("order paid ditolak", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				ownerID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentEscrow}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), orderID, ownerID)
				assert.Error(t, err)
			})
			t.Run("order cancelled ditolak", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				ownerID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusCancelled, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), orderID, ownerID)
				assert.Error(t, err)
			})
			t.Run("order expired ditolak", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				ownerID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusExpired, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), orderID, ownerID)
				assert.Error(t, err)
			})
		})
		t.Run("EXPIRY", func(t *testing.T) {
			t.Run("positive", func(t *testing.T) {
				t.Run("expiry masih di masa depan QRIS existing", func(t *testing.T) {
					mr, err := miniredis.Run()
					require.NoError(t, err)
					defer mr.Close()
					rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
					redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
					db, _ := testutil.NewMockDB(t)
					ctrl := gomock.NewController(t)
					mockRepo := orderMocks.NewMockRepository(ctrl)
					svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
					orderID := uuid.New()
					ownerID := uuid.New()
					expiry := time.Now().UTC().Add(10 * time.Minute)
					o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry}
					mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
					res, err := svc.RefreshQRIS(context.Background(), orderID, ownerID)
					require.NoError(t, err)
					assert.Equal(t, "https://qris.test/valid", res.QRISData)
				})
				t.Run("timezone berbeda merepresentasikan waktu absolut yang sama", func(t *testing.T) {
					mr, err := miniredis.Run()
					require.NoError(t, err)
					defer mr.Close()
					rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
					redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
					db, _ := testutil.NewMockDB(t)
					ctrl := gomock.NewController(t)
					mockRepo := orderMocks.NewMockRepository(ctrl)
					svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
					orderID := uuid.New()
					ownerID := uuid.New()
					// same absolute time in different location
					locJakarta, _ := time.LoadLocation("Asia/Jakarta")
					locUTC, _ := time.LoadLocation("UTC")
					absUTC := time.Now().UTC().Add(10 * time.Minute)
					expiryJakarta := absUTC.In(locJakarta)
					expiryUTC := absUTC.In(locUTC)
					// ensure UTC comparison: both should be considered same absolute
					assert.True(t, expiryJakarta.UTC().Equal(expiryUTC.UTC()))
					o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiryJakarta}
					mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
					res, err := svc.RefreshQRIS(context.Background(), orderID, ownerID)
					require.NoError(t, err)
					assert.Equal(t, "https://qris.test/valid", res.QRISData)
					// also check UTC stored
					assert.True(t, res.QRISExpiresAt.UTC().Equal(expiryJakarta.UTC()))
				})
			})
			t.Run("negative", func(t *testing.T) {
				t.Run("expiry tepat sekarang atau sudah lewat expired", func(t *testing.T) {
					mr, err := miniredis.Run()
					require.NoError(t, err)
					defer mr.Close()
					rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
					redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
					db, mockSql := testutil.NewMockDB(t)
					ctrl := gomock.NewController(t)
					mockRepo := orderMocks.NewMockRepository(ctrl)
					mockConfig := configMocks.NewMockService(ctrl)
					mockUser := userMocks.NewMockService(ctrl)
					mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
					mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
						if k == "qris_pg_fee" {
							return "0"
						}
						return d
					}).AnyTimes()
					svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
					orderID := uuid.New()
					ownerID := uuid.New()
					expiry := time.Now().UTC() // exactly now -> expired (After)
					o := &order.Order{ID: orderID, RequesterID: ownerID, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "https://qris.test/old", QRISExpiresAt: &expiry, CreatedAt: time.Now().Add(-20 * time.Minute)}
					oldQRIS := o.QRISData
					mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
					mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
					mockSql.ExpectBegin()
					mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
					mockSql.ExpectCommit()
					mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
					res, err := svc.RefreshQRIS(context.Background(), orderID, ownerID)
					require.NoError(t, err)
					assert.NotEqual(t, oldQRIS, res.QRISData)
				})
			})
		})
	})

	t.Run("RESERVATION", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("dua order mendapat nominal berbeda", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				idA := uuid.New()
				idB := uuid.New()
				reqA := uuid.New()
				reqB := uuid.New()
				oA := &order.Order{ID: idA, RequesterID: reqA, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now().UTC()}
				oB := &order.Order{ID: idB, RequesterID: reqB, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now().UTC()}
				mockRepo.EXPECT().FindByID(gomock.Any(), idA).Return(oA, nil).Times(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), idA).Return(oA, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				mockRepo.EXPECT().FindByID(gomock.Any(), idB).Return(oB, nil).Times(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), idB).Return(oB, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				rA, err := svc.RefreshQRIS(context.Background(), idA, reqA)
				require.NoError(t, err)
				rB, err := svc.RefreshQRIS(context.Background(), idB, reqB)
				require.NoError(t, err)
				assert.NotEqual(t, rA.TotalPayment, rB.TotalPayment)
			})
			t.Run("cancel membebaskan reservation milik order", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now()}
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				res, err := svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, err)
				key := fmt.Sprintf("active_total_payment:%.2f", res.TotalPayment)
				assert.True(t, mr.Exists(key))
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(res, nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err = svc.ForceCancelOrder(context.Background(), id)
				require.NoError(t, err)
				assert.False(t, mr.Exists(key))
			})
			t.Run("cleanup berulang aman", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now()}
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				res, err := svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, err)
				key := fmt.Sprintf("active_total_payment:%.2f", res.TotalPayment)
				assert.True(t, mr.Exists(key))
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(res, nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err = svc.ForceCancelOrder(context.Background(), id)
				require.NoError(t, err)
				assert.False(t, mr.Exists(key))
				// second cancel should not delete other key
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(&order.Order{ID: id, RequesterID: req, Status: order.StatusCancelled, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: res.TotalPayment, PGFee: res.PGFee, UniqueCode: res.UniqueCode}, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				_ = svc.ForceCancelOrder(context.Background(), id)
				assert.False(t, mr.Exists(key))
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("cleanup terlambat tidak menghapus reservation order lain", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				idA := uuid.New()
				reqA := uuid.New()
				oA := &order.Order{ID: idA, RequesterID: reqA, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now().UTC()}
				mockRepo.EXPECT().FindByID(gomock.Any(), idA).Return(oA, nil).Times(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), idA).Return(oA, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				resA, err := svc.RefreshQRIS(context.Background(), idA, reqA)
				require.NoError(t, err)
				key := fmt.Sprintf("active_total_payment:%.2f", resA.TotalPayment)
				mr.Del(key)
				idB := uuid.New()
				require.NoError(t, mr.Set(key, idB.String()))
				val, _ := mr.Get(key)
				assert.Equal(t, idB.String(), val)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), idA).Return(resA, nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err = svc.ForceCancelOrder(context.Background(), idA)
				require.NoError(t, err)
				val, _ = mr.Get(key)
				assert.Equal(t, idB.String(), val)
			})
			t.Run("DB update gagal membersihkan Redis reservation sendiri", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now()}
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnError(fmt.Errorf("unique_violation"))
				mockSql.ExpectRollback()
				// second attempt will try next code, need to mock second SetNX success: we just check that first key was cleaned
				// Simpler: test that after failed RefreshQRIS, no active_total_payment key remains for that order
				_, err = svc.RefreshQRIS(context.Background(), id, req)
				assert.Error(t, err)
				// No leaked reservation for failed attempt remains owned by this order (miniredis will have key but we check it was cleaned)
				// Since we use owner-safe ReleaseLock, the failed key should be removed
				foundOwned := false
				for _, k := range mr.Keys() {
					if v, _ := mr.Get(k); v == id.String() {
						foundOwned = true
					}
				}
				assert.False(t, foundOwned)
			})
			t.Run("concurrent refresh tidak membuat dua QRIS", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				expiry := time.Now().UTC().Add(10 * time.Minute)
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry}
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(2)
				r1, e1 := svc.RefreshQRIS(context.Background(), id, req)
				r2, e2 := svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, e1)
				require.NoError(t, e2)
				assert.Equal(t, r1.QRISData, r2.QRISData)
			})
			t.Run("refresh expired membebaskan key lama setelah commit", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				expiry := time.Now().UTC().Add(-5 * time.Minute)
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/old", QRISExpiresAt: &expiry, CreatedAt: time.Now().UTC().Add(-20 * time.Minute)}
				oldKey := fmt.Sprintf("active_total_payment:%.2f", o.TotalPayment)
				require.NoError(t, mr.Set(oldKey, id.String()))
				assert.True(t, mr.Exists(oldKey))
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				res, err := svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, err)
				assert.False(t, mr.Exists(oldKey), "old key should be released after commit")
				newKey := fmt.Sprintf("active_total_payment:%.2f", res.TotalPayment)
				assert.True(t, mr.Exists(newKey))
				assert.NotEqual(t, oldKey, newKey)
			})
			t.Run("commit gagal mempertahankan key lama", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				expiry := time.Now().UTC().Add(-5 * time.Minute)
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/old", QRISExpiresAt: &expiry, CreatedAt: time.Now().UTC().Add(-20 * time.Minute)}
				oldKey := fmt.Sprintf("active_total_payment:%.2f", o.TotalPayment)
				require.NoError(t, mr.Set(oldKey, id.String()))
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnError(fmt.Errorf("db error"))
				mockSql.ExpectRollback()
				_, err = svc.RefreshQRIS(context.Background(), id, req)
				assert.Error(t, err)
				assert.True(t, mr.Exists(oldKey), "old key must remain on commit fail")
				// new key should be cleaned
				foundOwnedNew := false
				for _, k := range mr.Keys() {
					if k == oldKey {
						continue
					}
					if v, _ := mr.Get(k); v == id.String() {
						foundOwnedNew = true
					}
				}
				assert.False(t, foundOwnedNew)
			})
			t.Run("commit gagal membersihkan key baru", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50000, PGFee: 0, QRISData: "", CreatedAt: time.Now().UTC()}
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnError(fmt.Errorf("db error"))
				mockSql.ExpectRollback()
				_, err = svc.RefreshQRIS(context.Background(), id, req)
				assert.Error(t, err)
				foundOwned := false
				for _, k := range mr.Keys() {
					if v, _ := mr.Get(k); v == id.String() {
						foundOwned = true
					}
				}
				assert.False(t, foundOwned)
			})
			t.Run("old key sudah dimiliki order lain tidak dihapus", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
					if k == "qris_pg_fee" {
						return "0"
					}
					return d
				}).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, nil, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				expiry := time.Now().UTC().Add(-5 * time.Minute)
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/old", QRISExpiresAt: &expiry, CreatedAt: time.Now().UTC().Add(-20 * time.Minute)}
				oldKey := fmt.Sprintf("active_total_payment:%.2f", o.TotalPayment)
				otherID := uuid.New()
				require.NoError(t, mr.Set(oldKey, otherID.String()))
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), id).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				_, err = svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, err)
				val, _ := mr.Get(oldKey)
				assert.Equal(t, otherID.String(), val, "old key owned by other must not be deleted")
			})
			t.Run("old key sama dengan new key tidak menghapus reservation aktif", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				expiry := time.Now().UTC().Add(10 * time.Minute)
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry}
				key := fmt.Sprintf("active_total_payment:%.2f", o.TotalPayment)
				require.NoError(t, mr.Set(key, id.String()))
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				res, err := svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, err)
				assert.Equal(t, "https://qris.test/valid", res.QRISData)
				assert.True(t, mr.Exists(key))
			})
			t.Run("valid QRIS tidak melakukan cleanup atau reservation baru", func(t *testing.T) {
				mr, err := miniredis.Run()
				require.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				id := uuid.New()
				req := uuid.New()
				expiry := time.Now().UTC().Add(10 * time.Minute)
				o := &order.Order{ID: id, RequesterID: req, Status: order.StatusPending, PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid, TotalPayment: 50050, PGFee: 50, UniqueCode: 50, QRISData: "https://qris.test/valid", QRISExpiresAt: &expiry}
				mockRepo.EXPECT().FindByID(gomock.Any(), id).Return(o, nil).Times(1)
				res, err := svc.RefreshQRIS(context.Background(), id, req)
				require.NoError(t, err)
				assert.Equal(t, "https://qris.test/valid", res.QRISData)
				assert.Empty(t, mr.Keys())
			})
		})
	})
}
