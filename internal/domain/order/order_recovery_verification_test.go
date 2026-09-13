package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	tripMocks "github.com/codecoffy/nitip-core/internal/domain/trip/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	userMocks "github.com/codecoffy/nitip-core/internal/domain/user/mocks"
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

func TestRecoveryVerification(t *testing.T) {
	origCfg := config.App
	t.Cleanup(func() { config.App = origCfg })
	config.App = &config.Config{BypassKYCValidation: true}

	t.Run("RedisError", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("Redis error tetap temukan order eligible sebelum 30m expiry", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				mr.Close() // simulate Redis down
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, mockWallet, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				runnerID := uuid.New()
				mockUser.EXPECT().GetByID(gomock.Any(), runnerID, runnerID).Return(&user.User{ID: runnerID, IsAcceptingOrders: true, IsSuspended: false, IsVerified: true, LastLat: func() *float64 { v := -6.2; return &v }(), LastLng: func() *float64 { v := 106.8; return &v }()}, nil).Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("0").AnyTimes()
				mockWallet.EXPECT().GetBalance(gomock.Any(), gomock.Any()).Return(&wallet.Wallet{Balance: 100000}, nil).AnyTimes()
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runnerID).Return([]trip.Trip{}, nil).Times(1)
				// Fallback DB must be hit without IDs filter
				mockRepo.EXPECT().FindAvailable(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, p order.FindAvailableParams) ([]order.Order, error) {
					assert.Empty(t, p.IDs, "Redis error fallback must not filter by IDs")
					assert.True(t, p.IsAcceptingOrders)
					return []order.Order{{ID: uuid.New(), Status: order.StatusMerchantAccepted, PaymentStatus: order.PaymentEscrow}}, nil
				}).Times(1)
				ords, err := svc.GetAvailableOrders(context.Background(), runnerID)
				require.NoError(t, err)
				assert.Len(t, ords, 1)
			})
		})
	})

	t.Run("GeoSearchEmpty", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("GeoSearch sukses kosong tetap temukan via DB ST_DWithin", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				runnerID := uuid.New()
				mockUser.EXPECT().GetByID(gomock.Any(), runnerID, runnerID).Return(&user.User{ID: runnerID, IsAcceptingOrders: true, IsSuspended: false, IsVerified: true, LastLat: func() *float64 { v := -6.2; return &v }(), LastLng: func() *float64 { v := 106.8; return &v }()}, nil).Times(1)
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runnerID).Return([]trip.Trip{}, nil).Times(1)
				// Do not expect GetBalance when no eligible check needed, but service calls it in Accept only
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				// Redis empty, but DB fallback must still be called and apply ST_DWithin
				mockRepo.EXPECT().FindAvailable(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, p order.FindAvailableParams) ([]order.Order, error) {
					assert.Empty(t, p.IDs)
					assert.NotZero(t, p.RunnerLat)
					return []order.Order{{ID: uuid.New(), Status: order.StatusReady, PaymentStatus: order.PaymentEscrow}}, nil
				}).Times(1)
				ords, err := svc.GetAvailableOrders(context.Background(), runnerID)
				require.NoError(t, err)
				assert.Len(t, ords, 1)
			})
		})
	})

	t.Run("PartialMiss", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("Geo berisi order lain tapi eligible hilang tetap ditemukan via DB radius", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				// Pre-populate Redis with one unrelated order GEO
				_ = rClient.GeoAdd(context.Background(), "orders:live", &redis.GeoLocation{Name: uuid.New().String(), Longitude: 106.8, Latitude: -6.2}).Err()
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				runnerID := uuid.New()
				mockUser.EXPECT().GetByID(gomock.Any(), runnerID, runnerID).Return(&user.User{ID: runnerID, IsAcceptingOrders: true, IsSuspended: false, IsVerified: true, LastLat: func() *float64 { v := -6.2; return &v }(), LastLng: func() *float64 { v := 106.8; return &v }()}, nil).Times(1)
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runnerID).Return([]trip.Trip{}, nil).Times(1)
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				// Even though Redis has 1 entry, DB must not be limited to it
				mockRepo.EXPECT().FindAvailable(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, p order.FindAvailableParams) ([]order.Order, error) {
					assert.Empty(t, p.IDs, "partial miss must not limit to Redis IDs")
					return []order.Order{{ID: uuid.New(), Status: order.StatusMerchantAccepted}, {ID: uuid.New(), Status: order.StatusReady}}, nil
				}).Times(1)
				ords, err := svc.GetAvailableOrders(context.Background(), runnerID)
				require.NoError(t, err)
				assert.Len(t, ords, 2)
			})
		})
	})

	t.Run("RadiusBounding", func(t *testing.T) {
		t.Run("negative", func(t *testing.T) {
			t.Run("order di luar radius tidak muncul - DB ST_DWithin memfilter", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id", "requester_id", "runner_id", "item_details", "pickup_lat", "pickup_lng", "delivery_lat", "delivery_lng", "estimated_cost", "delivery_fee", "status", "payment_status", "payment_method", "payment_source", "cod_handling_fee", "created_at", "updated_at", "receipt_image_url", "delivery_image_url", "dispute_reason", "dispute_proof_url", "disputed_at", "adjusted_cost", "adjustment_reason", "adjustment_status", "weight_kg", "volume_liters", "service_fee", "trip_id", "merchant_id", "total_payment", "order_type", "checking_fee", "pg_fee", "unique_code", "pickup_name", "pickup_address", "distance_km", "escalated_at", "service_category", "receiver_name", "receiver_phone", "delivery_name", "delivery_address", "completion_code", "promotion_id", "discount_amount", "original_total", "discount_type", "merchant_fee", "merchant_fee_tier", "food_amount_original", "qris_data", "qris_expires_at", "idempotency_key", "idempotency_request_hash"}))
				repo := order.NewRepository(db)
				params := order.FindAvailableParams{
					Cutoff:        time.Now().Add(-24 * time.Hour),
					HasActiveTrip: true,
					RadiusKm:      0.5,
					OriginLat:     -6.2, OriginLng: 106.8,
					DestLat: -6.2, DestLng: 106.8,
					IsAcceptingOrders: true,
					Limit:             100,
				}
				ords, err := repo.FindAvailable(context.Background(), params)
				require.NoError(t, err)
				assert.Empty(t, ords)
			})
		})
	})

	t.Run("ExpireBoundary", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("31 menit eligible expired dengan fixture stabil", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				eligibleID := uuid.New()
				reqID := uuid.New()
				stableOld := time.Now().Add(-31 * time.Minute)
				mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id", "requester_id", "payment_method", "payment_status", "estimated_cost", "delivery_fee", "status", "runner_id", "trip_id", "promotion_id", "pickup_lat", "pickup_lng", "created_at", "item_details"}).AddRow(eligibleID, reqID, order.MethodEscrow, order.PaymentEscrow, 10000.0, 5000.0, order.StatusPending, nil, nil, nil, -6.2, 106.8, stableOld, "item"))
				locked := &order.Order{ID: eligibleID, RequesterID: reqID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, EstimatedCost: 10000, DeliveryFee: 5000, CreatedAt: stableOld}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), eligibleID).Return(locked, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), reqID, eligibleID, 15000.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
				mockSql.ExpectCommit()
				count, err := svc.ExpirePendingOrders(context.Background())
				require.NoError(t, err)
				assert.Equal(t, int64(1), count)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("29 menit belum expired fixture stabil", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil, db, nil, nil, nil)
				// 29m ago is not <= cutoff (30m), so initial SELECT returns empty (WHERE created_at <= now-30m)
				mockSql.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id", "requester_id", "payment_method", "payment_status", "estimated_cost", "delivery_fee", "status", "runner_id", "trip_id", "promotion_id", "pickup_lat", "pickup_lng", "created_at", "item_details"}))
				count, err := svc.ExpirePendingOrders(context.Background())
				require.NoError(t, err)
				assert.Equal(t, int64(0), count)
			})
		})
	})
}
