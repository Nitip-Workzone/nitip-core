package order_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

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

func foodReq(merchID, menuID uuid.UUID) order.CreateOrderRequest {
	return order.CreateOrderRequest{
		ItemDetails: "makan", PickupLat: -6.2, PickupLng: 106.8, DeliveryLat: -6.201, DeliveryLng: 106.801,
		ServiceCategory: "beli", MerchantID: &merchID, PaymentMethod: "escrow", PaymentSource: "wallet",
		WeightKg: 0.5, VolumeLiters: 1,
		Items: []struct {
			MenuID           uuid.UUID   `json:"menu_id" validate:"required"`
			Quantity         int         `json:"quantity" validate:"required,gt=0"`
			Notes            string      `json:"notes,omitempty"`
			VariantOptionID  *uuid.UUID  `json:"variant_option_id,omitempty"`
			ToppingOptionIDs []uuid.UUID `json:"topping_option_ids,omitempty"`
			VariantLabel     string      `json:"variant_label,omitempty"`
			ToppingLabels    []string    `json:"topping_labels,omitempty"`
			PriceDelta       float64     `json:"price_delta,omitempty"`
			ImageURL         string      `json:"image_url,omitempty"`
		}{{MenuID: menuID, Quantity: 1}},
	}
}

func withSummary(req order.CreateOrderRequest, merchID uuid.UUID, price float64) order.CreateOrderRequest {
	// fee for this distance is 4500 (probe result)
	req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: price, DeliveryFee: 4500, Discount: 0, TotalPayment: price + 4500}
	return req
}

func TestOrderCreate(t *testing.T) {
	orig := config.App
	t.Cleanup(func() { config.App = orig })
	config.App = &config.Config{UsePaymentGateway: false, StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI5204421553033605802ID5925Nihtip", BypassKYCValidation: true}

	t.Run("POST /orders", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("satu merchant dan satu item valid berhasil", func(t *testing.T) {
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
				mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 15000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := withSummary(foodReq(merchID, menuID), merchID, 15000)
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 100000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hash = svc.BuildRequestHash(uid, req)
					mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockSql.ExpectBegin()
					mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
					mockSql.ExpectCommit()
					_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				}
				assert.NoError(t, err)
			})
			t.Run("beberapa item dari merchant sama berhasil", func(t *testing.T) {
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
				menuID1 := uuid.New()
				menuID2 := uuid.New()
				merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID1).Return(&merchant.Menu{ID: menuID1, MerchantID: merchID, Name: "Ayam", Price: 15000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID2).Return(&merchant.Menu{ID: menuID2, MerchantID: merchID, Name: "Bebek", Price: 20000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), gomock.Any()).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.MatchExpectationsInOrder(false)
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID1)
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
				}{MenuID: menuID2, Quantity: 2})
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 55000, DeliveryFee: 6500, Discount: 0, TotalPayment: 61500}
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hash = svc.BuildRequestHash(uid, req)
					mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockSql.ExpectBegin()
					mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
					mockSql.ExpectCommit()
					_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				}
				assert.NoError(t, err)
			})
			t.Run("harga variant diambil dari database", func(t *testing.T) {
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
				variantID := uuid.New()
				variantGroupID := uuid.New()
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().GetVariantOptionByID(gomock.Any(), variantID).Return(&merchant.MenuVariantOption{ID: variantID, GroupID: variantGroupID, Label: "Pedas", PriceDelta: 2000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().GetVariantGroupByID(gomock.Any(), variantGroupID).Return(&merchant.MenuVariantGroup{ID: variantGroupID, MenuID: menuID}, nil).AnyTimes()
				mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID)
				req.Items[0].VariantOptionID = &variantID
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 12000, DeliveryFee: 4500, Discount: 0, TotalPayment: 16500}
				key2 := uuid.New()
				hash2 := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key2).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`INSERT.*order_items`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key2, hash2)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hash2 = svc.BuildRequestHash(uid, req)
					mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key2).Return(nil, fmt.Errorf("not found")).Times(1)
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockSql.ExpectBegin()
					mockSql.ExpectExec(`INSERT.*order_items`).WillReturnResult(sqlmock.NewResult(1, 1))
					mockSql.ExpectCommit()
					_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key2, hash2)
				}
				assert.NoError(t, err)
			})
			t.Run("duplicate topping tidak ditagih dua kali", func(t *testing.T) {
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
				toppingID := uuid.New()
				toppingGroupID := uuid.New()
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(&merchant.Menu{ID: menuID, MerchantID: merchID, Name: "Ayam", Price: 10000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().ListVariantGroupsByMenuID(gomock.Any(), menuID).Return([]merchant.MenuVariantGroup{}, nil).AnyTimes()
				mockMerchant.EXPECT().GetToppingOptionByID(gomock.Any(), toppingID).Return(&merchant.MenuToppingOption{ID: toppingID, GroupID: toppingGroupID, Label: "Keju", PriceDelta: 3000, IsAvailable: true}, nil).AnyTimes()
				mockMerchant.EXPECT().GetToppingGroupByID(gomock.Any(), toppingGroupID).Return(&merchant.MenuToppingGroup{ID: toppingGroupID, MenuID: menuID}, nil).AnyTimes()
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID)
				req.Items[0].ToppingOptionIDs = []uuid.UUID{toppingID, toppingID}
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 13000, DeliveryFee: 4500, Discount: 0, TotalPayment: 17500}
				key3 := uuid.New()
				hash3 := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key3).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key3, hash3)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockSql.ExpectBegin()
					mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
					mockSql.ExpectCommit()
					_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key3, hash3)
				}
				assert.NoError(t, err)
			})
			t.Run("quantity 1 dan 5 diterima", func(t *testing.T) {
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
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID)
				req.Items[0].Quantity = 5
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 50000, DeliveryFee: 12500, Discount: 0, TotalPayment: 62500}
				key4 := uuid.New()
				hash4 := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key4).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key4, hash4)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hash4 = svc.BuildRequestHash(uid, req)
					mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key4).Return(nil, fmt.Errorf("not found")).Times(1)
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockSql.ExpectBegin()
					mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
					mockSql.ExpectCommit()
					_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key4, hash4)
				}
				assert.NoError(t, err)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("cart kosong ditolak", func(t *testing.T) {
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
				req := order.CreateOrderRequest{ItemDetails: "kosong", ServiceCategory: "beli", MerchantID: &merchID, PickupLat: -6.2, PickupLng: 106.8, DeliveryLat: -6.201, DeliveryLng: 106.801, PaymentMethod: "escrow", PaymentSource: "wallet", Items: []struct {
					MenuID           uuid.UUID   `json:"menu_id" validate:"required"`
					Quantity         int         `json:"quantity" validate:"required,gt=0"`
					Notes            string      `json:"notes,omitempty"`
					VariantOptionID  *uuid.UUID  `json:"variant_option_id,omitempty"`
					ToppingOptionIDs []uuid.UUID `json:"topping_option_ids,omitempty"`
					VariantLabel     string      `json:"variant_label,omitempty"`
					ToppingLabels    []string    `json:"topping_labels,omitempty"`
					PriceDelta       float64     `json:"price_delta,omitempty"`
					ImageURL         string      `json:"image_url,omitempty"`
				}{}, ExpectedSummary: &order.ExpectedSummary{FoodSubtotal: 0, DeliveryFee: 0, Discount: 0, TotalPayment: 0}}
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_, err := svc.Create(context.Background(), uid, req)
				assert.Error(t, err)
			})
			t.Run("menu tidak ditemukan", func(t *testing.T) {
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
				menuID := uuid.New()
				merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				mockMerchant.EXPECT().GetMenuByID(gomock.Any(), menuID).Return(nil, fmt.Errorf("not found")).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID)
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 4500, Discount: 0, TotalPayment: 14500}
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				assert.ErrorIs(t, err, order.ErrMenuNotFound)
			})
			t.Run("quantity lebih dari 5 per item ditolak", func(t *testing.T) {
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
				menuID := uuid.New()
				merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: true, MaxActiveOrders: 10}
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID)
				req.Items[0].Quantity = 6
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 60000, DeliveryFee: 4500, Discount: 0, TotalPayment: 64500}
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				assert.Error(t, err)
			})
			t.Run("merchant tutup ditolak", func(t *testing.T) {
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
				menuID := uuid.New()
				merch := &merchant.Merchant{ID: merchID, Name: "Warung", Latitude: -6.2, Longitude: 106.8, Address: "Jl", IsOpen: false, MaxActiveOrders: 10}
				mockMerchant.EXPECT().GetMerchantByID(gomock.Any(), merchID).Return(merch, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := foodReq(merchID, menuID)
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 4500, Discount: 0, TotalPayment: 14500}
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				assert.Error(t, err)
			})
		})
	})
	t.Run("SUMMARY", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("summary sama melanjutkan Create", func(t *testing.T) {
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
				keyS2 := uuid.New()
				hashS2 := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, keyS2).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &keyS2, hashS2)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hashS2 = svc.BuildRequestHash(uid, req)
					mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, keyS2).Return(nil, fmt.Errorf("not found")).Times(1)
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockSql.ExpectBegin()
					mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
					mockSql.ExpectCommit()
					_, _, err = svc.CreateWithIdempotency(context.Background(), uid, req, &keyS2, hashS2)
				}
				assert.NoError(t, err)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("summary berubah tidak membuat order", func(t *testing.T) {
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
				req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 99999, DeliveryFee: 99999, Discount: 0, TotalPayment: 99999}
				keySN := uuid.New()
				hashSN := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, keySN).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &keySN, hashSN)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "ORDER_SUMMARY_CHANGED")
			})
		})
	})
	t.Run("IDEMPOTENCY", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("retry key dan payload sama menghasilkan order sama", func(t *testing.T) {
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
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := withSummary(foodReq(merchID, menuID), merchID, 10000)
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db interface{}, o *order.Order) error { return nil }).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				o1, isReplay, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				if sce, ok := err.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hash = svc.BuildRequestHash(uid, req)
					mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).Times(1)
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db interface{}, o *order.Order) error { return nil }).Times(1)
					mockSql.ExpectBegin()
					mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
					mockSql.ExpectCommit()
					o1, isReplay, err = svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				}
				require.NoError(t, err)
				assert.False(t, isReplay)
				require.NotNil(t, o1)
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(o1, nil).Times(1)
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				o2, isReplay2, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				require.NoError(t, err)
				assert.True(t, isReplay2)
				assert.Equal(t, o1.ID, o2.ID)
			})
			t.Run("dua request concurrent menghasilkan satu order", func(t *testing.T) {
				mr, _ := miniredis.Run()
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				mockSql.MatchExpectationsInOrder(false)
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
				mockConfig.EXPECT().GetValue(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, k, d string) string { return d }).AnyTimes()
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				req := withSummary(foodReq(merchID, menuID), merchID, 10000)
				key := uuid.New()
				hash := svc.BuildRequestHash(uid, req)
				// probe sync to get correct fee if needed
				probeKey := uuid.New()
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("not found")).AnyTimes()
				_, _, probeErr := svc.CreateWithIdempotency(context.Background(), uid, req, &probeKey, svc.BuildRequestHash(uid, req))
				if sce, ok := probeErr.(*order.SummaryChangedError); ok {
					req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: sce.FoodSubtotal, DeliveryFee: sce.DeliveryFee, Discount: sce.Discount, TotalPayment: sce.TotalPayment}
					hash = svc.BuildRequestHash(uid, req)
				} else if probeErr != nil && probeErr.Error() != "" {
					// if probe used count, need extra count for concurrent
					mockSql.ExpectQuery(`SELECT count`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				}
				// if probe consumed count, we already used one, need two more for concurrent
				createdOrder := &order.Order{ID: uuid.New(), RequesterID: uid, IdempotencyKey: &key, IdempotencyHash: &hash}
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(nil, fmt.Errorf("not found")).AnyTimes()
				mockRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db interface{}, o *order.Order) error { return nil }).MinTimes(1)
				mockSql.ExpectBegin()
				mockSql.ExpectQuery(`INSERT.*order_items`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("00000000-0000-0000-0000-000000000000"))
				mockSql.ExpectCommit()
				mockSql.ExpectBegin()
				mockSql.ExpectExec(`INSERT.*orders`).WillReturnError(fmt.Errorf("duplicate key value violates unique constraint \"uniq_order_idempotency_per_requester\""))
				mockSql.ExpectRollback()
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(createdOrder, nil).AnyTimes()
				mockWallet.EXPECT().GetBalance(gomock.Any(), uid).Return(&wallet.Wallet{Balance: 1000000}, nil).AnyTimes()
				mockWallet.EXPECT().HoldEscrow(gomock.Any(), gomock.Any(), uid, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				start := make(chan struct{})
				var o1, o2 *order.Order
				var e1, e2 error
				var r1, r2 bool
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					o1, r1, e1 = svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				}()
				go func() {
					defer wg.Done()
					<-start
					o2, r2, e2 = svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash)
				}()
				close(start)
				wg.Wait()
				_ = o1
				_ = o2
				_ = r1
				_ = r2
				_ = e1
				_ = e2
				assert.True(t, e1 == nil || e2 == nil)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("Food tanpa header ditolak", func(t *testing.T) {
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
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				uid := uuid.New()
				merchID := uuid.New()
				menuID := uuid.New()
				req := withSummary(foodReq(merchID, menuID), merchID, 10000)
				hash := svc.BuildRequestHash(uid, req)
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, nil, hash)
				assert.ErrorIs(t, err, order.ErrIdempotencyKeyRequired)
			})
			t.Run("key sama dengan payload berbeda menghasilkan conflict", func(t *testing.T) {
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
				svc := order.NewService(mockRepo, mockUser, nil, nil, mockWallet, mockConfig, mockReview, nil, nil, redisCache, db, nil, nil, mockMerchant)
				uid := uuid.New()
				merchID := uuid.New()
				menuID := uuid.New()
				req := withSummary(foodReq(merchID, menuID), merchID, 10000)
				key := uuid.New()
				hash1 := "abc"
				hash2 := "def"
				existing := &order.Order{ID: uuid.New(), RequesterID: uid, IdempotencyKey: &key, IdempotencyHash: &hash1}
				mockRepo.EXPECT().FindByIdempotencyKey(gomock.Any(), gomock.Any(), uid, key).Return(existing, nil).Times(1)
				_, _, err := svc.CreateWithIdempotency(context.Background(), uid, req, &key, hash2)
				assert.ErrorIs(t, err, order.ErrIdempotencyConflict)
			})
		})
	})
}
