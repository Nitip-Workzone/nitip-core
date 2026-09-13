package order_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/internal/cache"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	merchantMocks "github.com/codecoffy/nitip-core/internal/domain/merchant/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	reviewMocks "github.com/codecoffy/nitip-core/internal/domain/review/mocks"
	userMocks "github.com/codecoffy/nitip-core/internal/domain/user/mocks"
	walletMocks "github.com/codecoffy/nitip-core/internal/domain/wallet/mocks"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func buildHashSvc(t *testing.T) (order.Service, *miniredis.Miniredis) {
	mr, _ := miniredis.Run()
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
	return svc, mr
}

func TestOrderCreate_HashCanonical(t *testing.T) {
	t.Run("positive/HASH", func(t *testing.T) {
		t.Run("payload sama menghasilkan hash sama", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req := foodReq(merchID, menuID)
			req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			h1 := svc.BuildRequestHash(uid, req)
			h2 := svc.BuildRequestHash(uid, req)
			assert.Equal(t, h1, h2)
		})
		t.Run("urutan topping berbeda menghasilkan hash sama", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			t1 := uuid.New()
			t2 := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.Items[0].ToppingOptionIDs = []uuid.UUID{t1, t2}
			req2 := foodReq(merchID, menuID)
			req2.Items[0].ToppingOptionIDs = []uuid.UUID{t2, t1}
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			h1 := svc.BuildRequestHash(uid, req1)
			h2 := svc.BuildRequestHash(uid, req2)
			assert.Equal(t, h1, h2)
		})
		t.Run("whitespace notes sama", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.Items[0].Notes = "  pedas  "
			req2 := foodReq(merchID, menuID)
			req2.Items[0].Notes = "pedas"
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.Equal(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("notes berbeda menghasilkan hash berbeda", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.Items[0].Notes = "pedas"
			req2 := foodReq(merchID, menuID)
			req2.Items[0].Notes = "tidak pedas"
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("delimiter collision tidak terjadi", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.Items[0].Notes = "a|b:c\nd"
			req2 := foodReq(merchID, menuID)
			req2.Items[0].Notes = "a"
			extra := req2.Items[0]
			extra.Notes = "|b:c\nd"
			req2.Items = append(req2.Items, extra)
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("destination berbeda hash berbeda", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.DeliveryLat = -6.201
			req2 := foodReq(merchID, menuID)
			req2.DeliveryLat = -6.202
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("payment source berbeda hash berbeda", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.PaymentSource = "wallet"
			req2 := foodReq(merchID, menuID)
			req2.PaymentSource = "qris"
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("expected summary berbeda hash berbeda", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2 := foodReq(merchID, menuID)
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 11000, DeliveryFee: 5000, Discount: 0, TotalPayment: 16000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("unicode notes tidak collision", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req1 := foodReq(merchID, menuID)
			req1.Items[0].Notes = "🍜 pedas"
			req2 := foodReq(merchID, menuID)
			req2.Items[0].Notes = "🍜 tidak pedas"
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
			req3 := foodReq(merchID, menuID)
			req3.Items[0].Notes = "🍜 pedas"
			req3.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.Equal(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req3))
		})
		t.Run("urutan item dipertahankan", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID1 := uuid.New()
			menuID2 := uuid.New()
			req1 := foodReq(merchID, menuID1)
			req1.Items = append(req1.Items, foodReq(merchID, menuID2).Items[0])
			req2 := foodReq(merchID, menuID2)
			req2.Items = append(req2.Items, foodReq(merchID, menuID1).Items[0])
			req1.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			req2.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			assert.NotEqual(t, svc.BuildRequestHash(uid, req1), svc.BuildRequestHash(uid, req2))
		})
		t.Run("map tidak dipakai di canonical struct", func(t *testing.T) {
			svc, mr := buildHashSvc(t)
			defer mr.Close()
			uid := uuid.New()
			merchID := uuid.New()
			menuID := uuid.New()
			req := foodReq(merchID, menuID)
			req.ExpectedSummary = &order.ExpectedSummary{FoodSubtotal: 10000, DeliveryFee: 5000, Discount: 0, TotalPayment: 15000}
			h1 := svc.BuildRequestHash(uid, req)
			h2 := svc.BuildRequestHash(uid, req)
			assert.Equal(t, h1, h2)
		})
	})
}
