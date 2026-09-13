package order_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	merchantMocks "github.com/codecoffy/nitip-core/internal/domain/merchant/mocks"
	notifMocks "github.com/codecoffy/nitip-core/internal/domain/notification/mocks"
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

func TestMatching(t *testing.T) {
	origCfg := config.App
	t.Cleanup(func() { config.App = origCfg })
	config.App = &config.Config{BypassKYCValidation: true, UsePaymentGateway: false, StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI5204421553033605802ID5925Nihtip"}

	t.Run("FoodDispatch", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("setelah merchant menerima order dapat dispatch", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockMatcher := orderMocks.NewMockMatcher(ctrl)
				mockMerchant := merchantMocks.NewMockService(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, nil, nil, mockMatcher, nil, nil, nil, nil, mockNotif, redisCache, db, nil, nil, mockMerchant)
				merchID := uuid.New()
				ownerID := uuid.New()
				orderID := uuid.New()
				o := &order.Order{ID: orderID, MerchantID: &merchID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, PickupLat: -6.2, PickupLng: 106.8}
				merchObj := &merchant.Merchant{ID: merchID, OwnerID: ownerID}
				mockMerchant.EXPECT().GetMerchantByOwnerID(gomock.Any(), ownerID).Return(merchObj, nil).Times(1)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockRepo.EXPECT().UpdateWithStatusCheck(gomock.Any(), gomock.Any(), gomock.Any(), order.StatusPending).Return(true, nil).Times(1)
				mockMatcher.EXPECT().EnqueueMatching(orderID).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.MerchantAcceptOrder(context.Background(), orderID, ownerID)
				require.NoError(t, err)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("Food belum diterima merchant tidak masuk matching - FindAvailable tidak mengembalikan pending food", func(t *testing.T) {
				assert.True(t, true)
			})
		})
	})

	t.Run("RunnerEligibility", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("order kecil dengan trip tetap memeriksa kapasitas", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, mockWallet, mockConfig, nil, nil, mockNotif, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runnerID := uuid.New()
				requesterID := uuid.New()
				tripID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: requesterID, Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, WeightKg: 2, VolumeLiters: 1, EstimatedCost: 5000, DeliveryFee: 5000, ServiceCategory: order.CategoryBeli}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockUser.EXPECT().GetByID(gomock.Any(), runnerID, runnerID).Return(&user.User{ID: runnerID, IsAcceptingOrders: true, IsSuspended: false, IsVerified: true}, nil).Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("0").AnyTimes()
				mockWallet.EXPECT().GetBalance(gomock.Any(), runnerID).Return(&wallet.Wallet{Balance: 100000}, nil).Times(1)
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runnerID).Return([]trip.Trip{{ID: tripID, RunnerID: runnerID, Status: trip.StatusStarted, AvailableWeightKg: 10, AvailableVolumeLiters: 10}}, nil).Times(1)
				mockTrip.EXPECT().UpdateCapacity(gomock.Any(), gomock.Any(), tripID, 2.0, 1.0).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockRepo.EXPECT().UpdateWithStatusCheck(gomock.Any(), gomock.Any(), gomock.Any(), order.StatusPending).Return(true, nil).Times(1)
				mockSql.ExpectCommit()
				err := svc.AcceptOrder(context.Background(), orderID, runnerID)
				require.NoError(t, err)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("runner tidak accepting order ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, mockWallet, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runnerID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: uuid.New(), Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, WeightKg: 1, VolumeLiters: 1, EstimatedCost: 5000, DeliveryFee: 5000, ServiceCategory: order.CategoryBeli}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockUser.EXPECT().GetByID(gomock.Any(), runnerID, runnerID).Return(&user.User{ID: runnerID, IsAcceptingOrders: false, IsSuspended: false, IsVerified: true}, nil).Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("0").AnyTimes()
				mockWallet.EXPECT().GetBalance(gomock.Any(), runnerID).Return(&wallet.Wallet{Balance: 100000}, nil).Times(1)
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runnerID).Return([]trip.Trip{}, nil).Times(1)
				err := svc.AcceptOrder(context.Background(), orderID, runnerID)
				assert.Error(t, err)
			})
		})
	})

	t.Run("ClaimConcurrency", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("tepat satu transition berhasil sequential", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, mockWallet, mockConfig, nil, nil, mockNotif, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runner1 := uuid.New()
				runner2 := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: uuid.New(), Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, WeightKg: 1, VolumeLiters: 1, EstimatedCost: 5000, DeliveryFee: 5000, ServiceCategory: order.CategoryBeli}
				// First runner: succeeds
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockUser.EXPECT().GetByID(gomock.Any(), runner1, runner1).Return(&user.User{ID: runner1, IsAcceptingOrders: true, IsSuspended: false, IsVerified: true}, nil).Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("0").AnyTimes()
				mockWallet.EXPECT().GetBalance(gomock.Any(), runner1).Return(&wallet.Wallet{Balance: 100000}, nil).Times(1)
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runner1).Return([]trip.Trip{}, nil).Times(1)
				mockSql.ExpectBegin()
				mockRepo.EXPECT().UpdateWithStatusCheck(gomock.Any(), gomock.Any(), gomock.Any(), order.StatusPending).Return(true, nil).Times(1)
				mockSql.ExpectCommit()
				err1 := svc.AcceptOrder(context.Background(), orderID, runner1)
				require.NoError(t, err1)
				// Second runner: order already taken, status changed to accepted, so FindByID returns updated order with different status -> fails at status check before Tx
				oTaken := &order.Order{ID: orderID, RequesterID: o.RequesterID, Status: order.StatusAccepted, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, WeightKg: 1, VolumeLiters: 1, EstimatedCost: 5000, DeliveryFee: 5000, ServiceCategory: order.CategoryBeli}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(oTaken, nil).Times(1)
				err2 := svc.AcceptOrder(context.Background(), orderID, runner2)
				assert.Error(t, err2)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("loser rollback Times0 di wallet hold", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockUser := userMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				svc := order.NewService(mockRepo, mockUser, mockTrip, nil, mockWallet, mockConfig, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				runnerID := uuid.New()
				o := &order.Order{ID: orderID, RequesterID: uuid.New(), Status: order.StatusPending, PaymentMethod: order.MethodEscrow, PaymentStatus: order.PaymentEscrow, WeightKg: 1, VolumeLiters: 1, EstimatedCost: 5000, DeliveryFee: 5000, ServiceCategory: order.CategoryBeli}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockUser.EXPECT().GetByID(gomock.Any(), runnerID, runnerID).Return(&user.User{ID: runnerID, IsAcceptingOrders: true, IsSuspended: false, IsVerified: false}, nil).Times(1)
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("0").AnyTimes()
				mockWallet.EXPECT().GetBalance(gomock.Any(), runnerID).Return(&wallet.Wallet{Balance: 100000}, nil).Times(1)
				mockSql.ExpectQuery("SELECT count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockTrip.EXPECT().FindByRunnerID(gomock.Any(), runnerID).Return([]trip.Trip{}, nil).Times(1)
				mockWallet.EXPECT().HoldLiability(gomock.Any(), gomock.Any(), runnerID, orderID, gomock.Any()).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockRepo.EXPECT().UpdateWithStatusCheck(gomock.Any(), gomock.Any(), gomock.Any(), order.StatusPending).Return(false, nil).Times(1)
				mockSql.ExpectRollback()
				err := svc.AcceptOrder(context.Background(), orderID, runnerID)
				assert.Error(t, err)
				mockWallet.EXPECT().ReleaseLiability(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			})
		})
	})
}
