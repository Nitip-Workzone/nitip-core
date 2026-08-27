package order_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	configMocks "github.com/codecoffy/nitip-core/internal/domain/config/mocks"
	notifMocks "github.com/codecoffy/nitip-core/internal/domain/notification/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	orderMocks "github.com/codecoffy/nitip-core/internal/domain/order/mocks"
	tripMocks "github.com/codecoffy/nitip-core/internal/domain/trip/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	userMocks "github.com/codecoffy/nitip-core/internal/domain/user/mocks"
	walletMocks "github.com/codecoffy/nitip-core/internal/domain/wallet/mocks"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bun"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func init() {
	config.App = &config.Config{
		UsePaymentGateway:  false,
		StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI51440014ID.CO.QRIS.WWW0215ID10265689831950303UMI5204421553033605802ID5925Nihtip, Pengiriman & Anta6007BOLMONG61059576162140703A0111036216304E13B",
	}
}

func TestOrder(t *testing.T) {
	t.Run("ForceCancelOrder", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("order berhasil dibatalkan", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusPending,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ForceCancelOrder(context.Background(), orderID)
				assert.NoError(t, err)
				assert.Equal(t, order.StatusCancelled, o.Status)
				assert.Equal(t, order.PaymentRefunded, o.PaymentStatus)
			})
			t.Run("pemanggilan kedua tetap idempoten", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o1 := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusPending,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o1, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o1).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ForceCancelOrder(context.Background(), orderID)
				assert.NoError(t, err)
				o2 := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusCancelled,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentRefunded,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o2, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err = svc.ForceCancelOrder(context.Background(), orderID)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")
			})
			t.Run("dua request bersamaan tidak memproses dua kali", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusPending,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).DoAndReturn(func(ctx context.Context, db bun.IDB, ord *order.Order) error { ord.Status = order.StatusCancelled; ord.PaymentStatus = order.PaymentRefunded; return nil }).Times(1)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				var wg sync.WaitGroup
				wg.Add(2)
				var err1, err2 error
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				go func() {
					defer wg.Done()
					err1 = svc.ForceCancelOrder(context.Background(), orderID)
				}()
				time.Sleep(20 * time.Millisecond)
				go func() {
					defer wg.Done()
					err2 = svc.ForceCancelOrder(context.Background(), orderID)
				}()
				wg.Wait()
				assert.True(t, (err1 == nil && err2 != nil) || (err1 != nil && err2 == nil))
				if err1 != nil {
					assert.Contains(t, err1.Error(), "tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")
				}
				if err2 != nil {
					assert.Contains(t, err2.Error(), "tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")
				}
			})
			t.Run("order yang sudah direfund tetap dibatalkan tanpa refund ulang", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusPending,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentRefunded,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ForceCancelOrder(context.Background(), orderID)
				assert.NoError(t, err)
				assert.Equal(t, order.StatusCancelled, o.Status)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("status non refundable ditolak", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusCompleted,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentReleased,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.ForceCancelOrder(context.Background(), orderID)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")
			})
		})
	})
	t.Run("ResolveDispute", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("sengketa diselesaikan dengan refund", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				runnerID := uuid.New()
				tripID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					RunnerID:      &runnerID,
					TripID:        &tripID,
					Status:        order.StatusDisputed,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
					WeightKg:      1.5,
					VolumeLiters:  2.0,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockTrip.EXPECT().RestoreCapacity(gomock.Any(), gomock.Any(), tripID, 1.5, 2.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).Return(nil).Times(1)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).MinTimes(1).MaxTimes(2)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ResolveDispute(context.Background(), orderID, "requester")
				assert.NoError(t, err)
				assert.Equal(t, order.StatusCancelled, o.Status)
				assert.Equal(t, order.PaymentRefunded, o.PaymentStatus)
			})
			t.Run("sengketa diselesaikan dengan payout ke runner", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				runnerID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					RunnerID:      &runnerID,
					Status:        order.StatusDisputed,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
					ServiceFee:    2000,
					CheckingFee:   1000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().ReleaseEscrowWithRefund(gomock.Any(), gomock.Any(), runnerID, requesterID, orderID, 57000.0, 2000.0, 1000.0).Return(nil).Times(1)
				mockWallet.EXPECT().ReleaseLiability(gomock.Any(), gomock.Any(), runnerID, orderID, 50000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).Return(nil).Times(1)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).MinTimes(1).MaxTimes(2)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ResolveDispute(context.Background(), orderID, "runner")
				assert.NoError(t, err)
				assert.Equal(t, order.StatusCompleted, o.Status)
				assert.Equal(t, order.PaymentReleased, o.PaymentStatus)
			})
			t.Run("pemanggilan kedua setelah resolved tidak memproses ulang", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				runnerID := uuid.New()
				o1 := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					RunnerID:      &runnerID,
					Status:        order.StatusDisputed,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o1, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o1).Return(nil).Times(1)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).MinTimes(1).MaxTimes(2)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err := svc.ResolveDispute(context.Background(), orderID, "requester")
				assert.NoError(t, err)
				o2 := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					RunnerID:      &runnerID,
					Status:        order.StatusCancelled,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentRefunded,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o2, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err = svc.ResolveDispute(context.Background(), orderID, "requester")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "pesanan tidak dalam status sengketa")
			})
			t.Run("dua refund bersamaan hanya satu yang berhasil", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusDisputed,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).DoAndReturn(func(ctx context.Context, db bun.IDB, ord *order.Order) error { ord.Status = order.StatusCancelled; ord.PaymentStatus = order.PaymentRefunded; return nil }).Times(1)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).MinTimes(1).MaxTimes(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				var wg sync.WaitGroup
				wg.Add(2)
				var err1, err2 error
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				go func() {
					defer wg.Done()
					err1 = svc.ResolveDispute(context.Background(), orderID, "requester")
				}()
				time.Sleep(20 * time.Millisecond)
				go func() {
					defer wg.Done()
					err2 = svc.ResolveDispute(context.Background(), orderID, "requester")
				}()
				wg.Wait()
				assert.True(t, (err1 == nil && err2 != nil) || (err1 != nil && err2 == nil))
			})
			t.Run("dua payout bersamaan hanya satu yang berhasil", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				runnerID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					RunnerID:      &runnerID,
					Status:        order.StatusDisputed,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
					ServiceFee:    2000,
					CheckingFee:   1000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().ReleaseEscrowWithRefund(gomock.Any(), gomock.Any(), runnerID, requesterID, orderID, 57000.0, 2000.0, 1000.0).Return(nil).Times(1)
				mockWallet.EXPECT().ReleaseLiability(gomock.Any(), gomock.Any(), runnerID, orderID, 50000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).DoAndReturn(func(ctx context.Context, db bun.IDB, ord *order.Order) error { ord.Status = order.StatusCompleted; ord.PaymentStatus = order.PaymentReleased; return nil }).Times(1)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).MinTimes(1).MaxTimes(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				var wg sync.WaitGroup
				wg.Add(2)
				var err1, err2 error
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				go func() {
					defer wg.Done()
					err1 = svc.ResolveDispute(context.Background(), orderID, "runner")
				}()
				time.Sleep(20 * time.Millisecond)
				go func() {
					defer wg.Done()
					err2 = svc.ResolveDispute(context.Background(), orderID, "runner")
				}()
				wg.Wait()
				assert.True(t, (err1 == nil && err2 != nil) || (err1 != nil && err2 == nil))
			})
			t.Run("refund dan payout bersamaan hanya satu yang berhasil", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				mockTrip := tripMocks.NewMockRepository(ctrl)
				mockNotif := notifMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				runnerID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					RunnerID:      &runnerID,
					Status:        order.StatusDisputed,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentEscrow,
					EstimatedCost: 50000,
					DeliveryFee:   10000,
					ServiceFee:    2000,
					CheckingFee:   1000,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockWallet.EXPECT().RefundEscrow(gomock.Any(), gomock.Any(), requesterID, orderID, 60000.0).Return(nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), o).DoAndReturn(func(ctx context.Context, db bun.IDB, ord *order.Order) error { ord.Status = order.StatusCancelled; ord.PaymentStatus = order.PaymentRefunded; return nil }).Times(1)
				mockNotif.EXPECT().CreateNotification(gomock.Any(), gomock.Any()).Return(nil).MinTimes(1).MaxTimes(2)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				var wg sync.WaitGroup
				wg.Add(2)
				var err1, err2 error
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				go func() {
					defer wg.Done()
					err1 = svc.ResolveDispute(context.Background(), orderID, "requester")
				}()
				time.Sleep(20 * time.Millisecond)
				go func() {
					defer wg.Done()
					err2 = svc.ResolveDispute(context.Background(), orderID, "runner")
				}()
				wg.Wait()
				assert.NoError(t, err1)
				assert.Error(t, err2)
				assert.Contains(t, err2.Error(), "pesanan tidak dalam status sengketa")
				assert.Equal(t, order.StatusCancelled, o.Status)
				assert.Equal(t, order.PaymentRefunded, o.PaymentStatus)
				mockWallet.EXPECT().ReleaseEscrowWithRefund(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("pesanan yang sudah selesai tidak dapat disengketakan", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockWallet := walletMocks.NewMockService(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, nil, nil, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID:            orderID,
					RequesterID:   requesterID,
					Status:        order.StatusCancelled,
					PaymentMethod: order.MethodEscrow,
					PaymentStatus: order.PaymentRefunded,
				}
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(o, nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectRollback()
				err := svc.ResolveDispute(context.Background(), orderID, "requester")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "pesanan tidak dalam status sengketa")
			})
		})
	})
	t.Run("PaymentCollision", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("dua order dengan total mirip mendapat total pembayaran berbeda", func(t *testing.T) {
				mr, err := miniredis.Run()
				assert.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUserSvc := userMocks.NewMockService(ctrl)
				dummyUser := &user.User{Name: "Test User", Email: "test@example.com"}
				mockUserSvc.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, nil, nil, nil)
				orderIDA := uuid.New()
				requesterIDA := uuid.New()
				oA := &order.Order{
					ID: orderIDA, RequesterID: requesterIDA, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000.0, PGFee: 0,
				}
				orderIDB := uuid.New()
				requesterIDB := uuid.New()
				oB := &order.Order{
					ID: orderIDB, RequesterID: requesterIDB, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 49997.0, PGFee: 0,
				}
				mockConfig.EXPECT().GetValue(gomock.Any(), "qris_pg_fee", "0").Return("0").Times(2)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderIDA).Return(oA, nil).Times(1)
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				resA, err := svc.GetByID(context.Background(), orderIDA, requesterIDA, "requester")
				assert.NoError(t, err)
				assert.True(t, resA.UniqueCode > 0)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderIDB).Return(oB, nil).Times(1)
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				resB, err := svc.GetByID(context.Background(), orderIDB, requesterIDB, "requester")
				assert.NoError(t, err)
				assert.True(t, resB.UniqueCode > 0)
				assert.NotEqual(t, resA.TotalPayment, resB.TotalPayment)
			})
		})
	})
	t.Run("PaymentReservation", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("dua reservasi bersamaan tidak menghasilkan total yang sama", func(t *testing.T) {
				mr, err := miniredis.Run()
				assert.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUserSvc := userMocks.NewMockService(ctrl)
				dummyUser := &user.User{Name: "Test User", Email: "test@example.com"}
				mockUserSvc.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, nil, nil, nil)
				orderIDA := uuid.New()
				requesterIDA := uuid.New()
				oA := &order.Order{
					ID: orderIDA, RequesterID: requesterIDA, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000.0, PGFee: 0,
				}
				orderIDB := uuid.New()
				requesterIDB := uuid.New()
				oB := &order.Order{
					ID: orderIDB, RequesterID: requesterIDB, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 49997.0, PGFee: 0,
				}
				mockConfig.EXPECT().GetValue(gomock.Any(), "qris_pg_fee", "0").Return("0").Times(2)
				var wg sync.WaitGroup
				wg.Add(2)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderIDA).Return(oA, nil).Times(1)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderIDB).Return(oB, nil).Times(1)
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				go func() {
					defer wg.Done()
					_, _ = svc.GetByID(context.Background(), orderIDA, requesterIDA, "requester")
				}()
				go func() {
					defer wg.Done()
					_, _ = svc.GetByID(context.Background(), orderIDB, requesterIDB, "requester")
				}()
				wg.Wait()
				assert.NotEqual(t, oA.TotalPayment, oB.TotalPayment)
			})
			t.Run("reservasi dibersihkan saat order dibatalkan", func(t *testing.T) {
				mr, err := miniredis.Run()
				assert.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUserSvc := userMocks.NewMockService(ctrl)
				dummyUser := &user.User{Name: "Test User", Email: "test@example.com"}
				mockUserSvc.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000.0, PGFee: 0,
				}
				mockConfig.EXPECT().GetValue(gomock.Any(), "qris_pg_fee", "0").Return("0").Times(1)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				res, err := svc.GetByID(context.Background(), orderID, requesterID, "requester")
				assert.NoError(t, err)
				key := fmt.Sprintf("active_total_payment:%.2f", res.TotalPayment)
				assert.True(t, mr.Exists(key))
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderID).Return(res, nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), res).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err = svc.ForceCancelOrder(context.Background(), orderID)
				assert.NoError(t, err)
				assert.False(t, mr.Exists(key))
			})
			t.Run("cleanup terlambat tidak menghapus reservasi order baru", func(t *testing.T) {
				mr, err := miniredis.Run()
				assert.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, mockSql := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				mockConfig := configMocks.NewMockService(ctrl)
				mockUserSvc := userMocks.NewMockService(ctrl)
				dummyUser := &user.User{Name: "Test User", Email: "test@example.com"}
				mockUserSvc.EXPECT().GetByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(dummyUser, nil).AnyTimes()
				svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, nil, nil, nil)
				orderIDA := uuid.New()
				requesterIDA := uuid.New()
				oA := &order.Order{
					ID: orderIDA, RequesterID: requesterIDA, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000.0, PGFee: 0,
				}
				mockConfig.EXPECT().GetValue(gomock.Any(), "qris_pg_fee", "0").Return("0").Times(1)
				mockRepo.EXPECT().FindByID(gomock.Any(), orderIDA).Return(oA, nil).Times(1)
				mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
				resA, err := svc.GetByID(context.Background(), orderIDA, requesterIDA, "requester")
				assert.NoError(t, err)
				key := fmt.Sprintf("active_total_payment:%.2f", resA.TotalPayment)
				assert.True(t, mr.Exists(key))
				val, getErr := mr.Get(key)
				assert.NoError(t, getErr)
				assert.Equal(t, orderIDA.String(), val)
				mr.Del(key)
				assert.False(t, mr.Exists(key))
				orderIDB := uuid.New()
				assert.NoError(t, mr.Set(key, orderIDB.String()))
				assert.True(t, mr.Exists(key))
				val, getErr = mr.Get(key)
				assert.NoError(t, getErr)
				assert.Equal(t, orderIDB.String(), val)
				mockRepo.EXPECT().FindByIDForUpdate(gomock.Any(), gomock.Any(), orderIDA).Return(resA, nil).Times(1)
				mockRepo.EXPECT().Update(gomock.Any(), gomock.Any(), resA).Return(nil).Times(1)
				mockSql.ExpectBegin()
				mockSql.ExpectCommit()
				err = svc.ForceCancelOrder(context.Background(), orderIDA)
				assert.NoError(t, err)
				assert.True(t, mr.Exists(key))
				val, getErr = mr.Get(key)
				assert.NoError(t, getErr)
				assert.Equal(t, orderIDB.String(), val)
			})
		})
	})
	t.Run("GetByID", func(t *testing.T) {
		t.Run("negative", func(t *testing.T) {
			t.Run("akses tanpa izin tidak menghasilkan kode pembayaran", func(t *testing.T) {
				mr, err := miniredis.Run()
				assert.NoError(t, err)
				defer mr.Close()
				rClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())
				db, _ := testutil.NewMockDB(t)
				ctrl := gomock.NewController(t)
				mockRepo := orderMocks.NewMockRepository(ctrl)
				svc := order.NewService(mockRepo, nil, nil, nil, nil, nil, nil, nil, redisCache, db, nil, nil, nil)
				orderID := uuid.New()
				requesterID := uuid.New()
				attackerID := uuid.New()
				o := &order.Order{
					ID: orderID, RequesterID: requesterID, Status: order.StatusPending,
					PaymentMethod: "escrow", PaymentSource: "qris", PaymentStatus: order.PaymentUnpaid,
					TotalPayment: 50000.0, PGFee: 0,
				}
				mockRepo.EXPECT().FindByID(gomock.Any(), orderID).Return(o, nil).Times(1)
				res, err := svc.GetByID(context.Background(), orderID, attackerID, "requester")
				assert.Error(t, err)
				assert.Nil(t, res)
				assert.Contains(t, err.Error(), "akses ditolak")
				keys := mr.Keys()
				for _, k := range keys {
					assert.NotContains(t, k, "active_total_payment")
				}
			})
		})
	})
}
