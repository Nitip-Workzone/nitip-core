package order_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/merchant"
	merchantMocks "github.com/codecoffy/nitip-core/internal/domain/merchant/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	reviewMocks "github.com/codecoffy/nitip-core/internal/domain/review/mocks"
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

func mustTime() time.Time { return time.Now() }

func TestOrderCreate_ClosedLoop(t *testing.T) {
	orig := config.App
	t.Cleanup(func() { config.App = orig })
	config.App = &config.Config{UsePaymentGateway: false, StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI5204421553033605802ID5925Nihtip", BypassKYCValidation: true}

	t.Run("bypass/negative/Create Food ditolak IDEMPOTENCY_KEY_REQUIRED", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, _ := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		merchID := uuid.New()
		menuID := uuid.New()
		req := foodReq(merchID, menuID)
		req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 4500, Discount: 0, TotalPayment: 14500}
		_, err := svc.Create(context.Background(), uid, req)
		assert.ErrorIs(t, err, order.ErrIdempotencyKeyRequired)
	})
	t.Run("bypass/negative/Food tanpa ExpectedSummary ditolak via CreateWithIdempotency", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, _ := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		merchID := uuid.New()
		menuID := uuid.New()
		req := foodReq(merchID, menuID)
		req.ExpectedSummary = nil
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrExpectedSummaryRequired)
	})
	t.Run("compat/positive/Beli tanpa merchant via Create berhasil", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := order.CreateOrderRequest{
			ItemDetails: "beli tanpa merchant - minimal cost", ServiceCategory: "beli", PaymentMethod: "escrow", PaymentSource: "wallet",
			PickupLat: -6.2, PickupLng: 106.8, DeliveryLat: -6.201, DeliveryLng: 106.801, WeightKg: 0.5, VolumeLiters: 1,
			EstimatedCost: 10000,
		}
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectCommit()
		mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).Times(1)
		mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		_, err := svc.Create(context.Background(), uid, req)
		assert.NoError(t, err)
	})
	t.Run("compat/positive/Kirim via Create berhasil", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := order.CreateOrderRequest{
			ItemDetails: "kirim", ServiceCategory: "kirim", PaymentMethod: "escrow", PaymentSource: "wallet",
			PickupLat: -6.2, PickupLng: 106.8, DeliveryLat: -6.3, DeliveryLng: 106.9, WeightKg: 1, VolumeLiters: 2,
		}
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectCommit()
		mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).Times(1)
		mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		_, err := svc.Create(context.Background(), uid, req)
		assert.NoError(t, err)
	})
	t.Run("menu/negative/merchant lain ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		otherMerchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: otherMerchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrMenuMerchantMismatch)
	})
	t.Run("menu/negative/unavailable ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: false}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrMenuUnavailable)
	})
	t.Run("menu/negative/soft deleted ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		deleted := mustTime()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true, DeletedAt: &deleted}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrMenuUnavailable)
	})
	t.Run("variant/negative/tidak ditemukan", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		vid := uuid.New()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().GetVariantOptionByID(gomock.Any(), vid).Return(nil, errors.New("not found")).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].VariantOptionID = &vid
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrVariantInvalid)
	})
	t.Run("variant/negative/unavailable", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		vid := uuid.New()
		gid := uuid.New()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().GetVariantOptionByID(gomock.Any(), vid).Return(&merchant.MenuVariantOption{ID: vid, GroupID: gid, IsAvailable: false}, nil).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].VariantOptionID = &vid
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrVariantInvalid)
	})
	t.Run("variant/negative/menu lain", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		otherMenu := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		vid := uuid.New()
		gid := uuid.New()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().GetVariantOptionByID(gomock.Any(), vid).Return(&merchant.MenuVariantOption{ID: vid, GroupID: gid, Label: "Pedas", PriceDelta: 0, IsAvailable: true}, nil).Times(1)
		mockMerchant.EXPECT().GetVariantGroupByID(gomock.Any(), gid).Return(&merchant.MenuVariantGroup{ID: gid, MenuID: otherMenu}, nil).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].VariantOptionID = &vid
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrVariantInvalid)
	})
	t.Run("variant/negative/required kosong", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{{ID: uuid.New(), MenuID: menuID, IsRequired: true}}, nil).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrVariantInvalid)
	})
	t.Run("topping/negative/tidak ditemukan", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		tid := uuid.New()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
		mockMerchant.EXPECT().GetToppingOptionByID(gomock.Any(), tid).Return(nil, errors.New("not found")).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].ToppingOptionIDs = []uuid.UUID{tid}
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrToppingInvalid)
	})
	t.Run("topping/negative/unavailable", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		tid := uuid.New()
		gid := uuid.New()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
		mockMerchant.EXPECT().GetToppingOptionByID(gomock.Any(), tid).Return(&merchant.MenuToppingOption{ID: tid, GroupID: gid, IsAvailable: false}, nil).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].ToppingOptionIDs = []uuid.UUID{tid}
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrToppingInvalid)
	})
	t.Run("topping/negative/menu lain", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		otherMenu := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		tid := uuid.New()
		gid := uuid.New()
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
		mockMerchant.EXPECT().GetToppingOptionByID(gomock.Any(), tid).Return(&merchant.MenuToppingOption{ID: tid, GroupID: gid, Label: "Keju", PriceDelta: 0, IsAvailable: true}, nil).Times(1)
		mockMerchant.EXPECT().GetToppingGroupByID(gomock.Any(), gid).Return(&merchant.MenuToppingGroup{ID: gid, MenuID: otherMenu}, nil).Times(1)
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].ToppingOptionIDs = []uuid.UUID{tid}
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.ErrorIs(t, err, order.ErrToppingInvalid)
	})
	t.Run("quantity/negative/0 ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, _ := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, uuid.New()), merchID, 10000)
		req.Items[0].Quantity = 0
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		require.Error(t, err)
	})
	t.Run("quantity/negative/6 ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, _ := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, uuid.New()), merchID, 10000)
		req.Items[0].Quantity = 6
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		require.Error(t, err)
	})
	t.Run("quantity/negative/total quantity 11 ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, _ := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		m1 := uuid.New()
		m2 := uuid.New()
		m3 := uuid.New()
		req := withSummary(foodReq(merchID, m1), merchID, 10000)
		req.Items[0].Quantity = 5
		req.Items = append(req.Items, struct {
			MenuID           uuid.UUID   `json:"menu_id" validate:"required"`
			Quantity         int         `json:"quantity" validate:"required,gt=0"`
			Notes            string      `json:"notes,omitempty"`
			VariantOptionID  *uuid.UUID  `json:"variant_option_id,omitempty"`
			ToppingOptionIDs []uuid.UUID `json:"topping_option_ids,omitempty"`
			VariantLabel     string      `json:"variant_label,omitempty"`
			ToppingLabels    []string    `json:"topping_labels,omitempty"`
			PriceDelta       float64     `json:"price_delta,omitempty"`
			ImageURL         string      `json:"image_url,omitempty"`
		}{MenuID: m2, Quantity: 5}, struct {
			MenuID           uuid.UUID   `json:"menu_id" validate:"required"`
			Quantity         int         `json:"quantity" validate:"required,gt=0"`
			Notes            string      `json:"notes,omitempty"`
			VariantOptionID  *uuid.UUID  `json:"variant_option_id,omitempty"`
			ToppingOptionIDs []uuid.UUID `json:"topping_option_ids,omitempty"`
			VariantLabel     string      `json:"variant_label,omitempty"`
			ToppingLabels    []string    `json:"topping_labels,omitempty"`
			PriceDelta       float64     `json:"price_delta,omitempty"`
			ImageURL         string      `json:"image_url,omitempty"`
		}{MenuID: m3, Quantity: 1})
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		require.Error(t, err)
	})
	t.Run("notes/negative/201 ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, _ := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, uuid.New()), merchID, 10000)
		req.Items[0].Notes = strings.Repeat("a", 201)
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		require.Error(t, err)
	})
	t.Run("notes/negative/order notes 501 ditolak", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), gomock.Any()).Return(&merchant.Menu{ID: uuid.New(), MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		db2, mockSql := testutil.NewMockDB(t)
		svc2 := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db2, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, uuid.New()), merchID, 10000)
		req.ItemDetails = strings.Repeat("b", 501)
		key := uuid.New()
		hash := svc2.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc2.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		require.Error(t, err)
	})
	t.Run("summary/negative/food berubah tidak side effect", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := foodReq(merchID, menuID)
		req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 999, DeliveryFee: 999, Discount: 0, TotalPayment: 1998}
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		mockWallet.EXPECT().GetBalance(gomock.Any(), gomock.Any()).Times(0)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		assert.Error(t, err)
		var sce *order.SummaryChangedError
		assert.True(t, errors.As(err, &sce))
		assert.NotZero(t, sce.FoodSubtotal)
	})
	t.Run("idempotency/positive/user berbeda UUID sama tidak conflict", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid1 := uuid.New()
		uid2 := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(&user.User{ID: uid1, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		key := uuid.New()
		hash := svc.BuildRequestHash(uid1, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid1, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
		mockSql.ExpectCommit()
		mockWallet.EXPECT().GetBalance(gomock.Any(), gomock.Any()).Return(&wallet.Wallet{Balance: 1000000}, nil).Times(1)
		mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid1, req, &key, hash)
		require.NoError(t, err)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid2, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mockSql.ExpectBegin()
		mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
		mockSql.ExpectCommit()
		mockWallet.EXPECT().GetBalance(gomock.Any(), gomock.Any()).Return(&wallet.Wallet{Balance: 1000000}, nil).Times(1)
		mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockUser.EXPECT().GetByID(gomock.Any(), uid2, gomock.Any()).Return(&user.User{ID: uid2, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		hash2 := svc.BuildRequestHash(uid2, req)
		_, _, err2 := svc.CreateWithIdempotency(context.Background(), uid2, req, &key, hash2)
		require.NoError(t, err2)
	})
	t.Run("price/positive/client PriceDelta diabaikan", func(t *testing.T) {
		mr, _ := miniredis.Run()
		defer mr.Close()
		rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
		db, mockSql := testutil.NewMockDB(t)
		ctrl := gomock.NewController(t)
		mockRepo := orderMocks.NewMockRepository(ctrl)
		mockUser := userMocks.NewMockService(ctrl)
		mockMerchant := merchantMocks.NewMockService(ctrl)
		mockConfig := configMocks.NewMockService(ctrl)
		mockWallet := walletMocks.NewMockService(ctrl)
		mockReview := reviewMocks.NewMockRepository(ctrl)
		uid := uuid.New()
		mockUser.EXPECT().GetByID(gomock.Any(), uid, gomock.Any()).Return(&user.User{ID: uid, Role: user.RoleRequester, WhatsappNumber: "628123456789", Name: "Budi"}, nil).AnyTimes()
		merchID := uuid.New()
		menuID := uuid.New()
		merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
		mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
		mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
		mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
		mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string {
			if k == "merchant_discovery_radius_km" {
				return "10"
			}
			return d
		}).AnyTimes()
		mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
		req := withSummary(foodReq(merchID, menuID), merchID, 10000)
		req.Items[0].PriceDelta = 999999
		key := uuid.New()
		hash := svc.BuildRequestHash(uid, req)
		mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
		mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db interface{}, o *order.Order) error {
			assert.Equal(t, 10000.0, o.EstimatedCost)
			return nil
		}).Times(1)
		mockSql.ExpectBegin()
		mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
		mockSql.ExpectCommit()
		mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).Times(1)
		mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).Times(1)
		mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
		_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		if sce, ok := err.(*order.SummaryChangedError); ok {
			req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
			hash = svc.BuildRequestHash(uid, req)
			mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
			mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db interface{}, o *order.Order) error {
				assert.Equal(t, 10000.0, o.EstimatedCost)
				return nil
			}).Times(1)
			mockSql.ExpectBegin()
			mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
			mockSql.ExpectCommit()
			_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
		}
		require.NoError(t, err)
	})
}
