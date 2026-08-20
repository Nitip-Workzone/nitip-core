package order_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	systemconfig "github.com/codecoffy/nitip-core/internal/domain/config"
	notifDomain "github.com/codecoffy/nitip-core/internal/domain/notification"
	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/codecoffy/nitip-core/internal/domain/trip"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/domain/wallet"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

func init() {
	config.App = &config.Config{
		UsePaymentGateway:  false,
		StaticQrisTemplate: "00020101021126610014COM.GO-JEK.WWW01189360091439887843340210G9887843340303UMI51440014ID.CO.QRIS.WWW0215ID10265689831950303UMI5204421553033605802ID5925Nihtip, Pengiriman & Anta6007BOLMONG61059576162140703A0111036216304E13B",
	}
}

type mockOrderRepository struct {
	mock.Mock
	order.Repository
}

func (m *mockOrderRepository) FindByID(ctx context.Context, id uuid.UUID) (*order.Order, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*order.Order), args.Error(1)
}

func (m *mockOrderRepository) FindByIDForUpdate(ctx context.Context, db bun.IDB, id uuid.UUID) (*order.Order, error) {
	args := m.Called(ctx, db, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*order.Order), args.Error(1)
}

func (m *mockOrderRepository) Update(ctx context.Context, db bun.IDB, o *order.Order) error {
	args := m.Called(ctx, db, o)
	return args.Error(0)
}

type mockWalletService struct {
	mock.Mock
	wallet.Service
}

func (m *mockWalletService) RefundEscrow(ctx context.Context, db bun.IDB, requesterID, orderID uuid.UUID, amount float64) error {
	args := m.Called(ctx, db, requesterID, orderID, amount)
	return args.Error(0)
}

func (m *mockWalletService) ReleaseLiability(ctx context.Context, db bun.IDB, runnerID, orderID uuid.UUID, amount float64) error {
	args := m.Called(ctx, db, runnerID, orderID, amount)
	return args.Error(0)
}

func (m *mockWalletService) ReleaseEscrowWithRefund(ctx context.Context, db bun.IDB, runnerID, requesterID, orderID uuid.UUID, runnerAmount, platformFee, refundAmount float64) error {
	args := m.Called(ctx, db, runnerID, requesterID, orderID, runnerAmount, platformFee, refundAmount)
	return args.Error(0)
}

type mockTripRepository struct {
	mock.Mock
	trip.Repository
}

func (m *mockTripRepository) RestoreCapacity(ctx context.Context, db bun.IDB, tripID uuid.UUID, weight, volume float64) error {
	args := m.Called(ctx, db, tripID, weight, volume)
	return args.Error(0)
}

type mockUserService struct {
	mock.Mock
	user.Service
}

func (m *mockUserService) GetByID(ctx context.Context, id, requestingUserID uuid.UUID) (*user.User, error) {
	args := m.Called(ctx, id, requestingUserID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*user.User), args.Error(1)
}

type mockAuditService struct {
	mock.Mock
	audit.Service
}

func (m *mockAuditService) Log(ctx context.Context, userID *uuid.UUID, action, resource, resourceID string, oldValues, newValues interface{}, ip, ua string) {
	m.Called(ctx, userID, action, resource, resourceID, oldValues, newValues, ip, ua)
}

func (m *mockAuditService) LogWithDB(ctx context.Context, db bun.IDB, userID *uuid.UUID, action, resource, resourceID string, oldValues, newValues interface{}, ip, ua string) {
	m.Called(ctx, db, userID, action, resource, resourceID, oldValues, newValues, ip, ua)
}

type mockNotificationService struct {
	mock.Mock
	notifDomain.Service
}

func (m *mockNotificationService) CreateNotification(ctx context.Context, req notifDomain.CreateNotificationRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

// ==========================================
// SEC-01: ForceCancelOrder Tests
// ==========================================

func TestForceCancelOrder_Success(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil)
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil)
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Return(nil)

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err := svc.ForceCancelOrder(context.Background(), orderID)
	assert.NoError(t, err)
	assert.Equal(t, order.StatusCancelled, o.Status)
	assert.Equal(t, order.PaymentRefunded, o.PaymentStatus)
}

func TestForceCancelOrder_CalledTwice(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o1, nil).Once()
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil).Once()
	mockRepo.On("Update", mock.Anything, mock.Anything, o1).Return(nil).Once()

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o2, nil).Once()

	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	err = svc.ForceCancelOrder(context.Background(), orderID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")
}

func TestForceCancelOrder_Concurrent(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil).Once()
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Run(func(args mock.Arguments) {
		o.Status = order.StatusCancelled
		o.PaymentStatus = order.PaymentRefunded
	}).Return(nil).Once()

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()

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
}

func TestForceCancelOrder_AlreadyRefunded(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil)
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Return(nil)

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err := svc.ForceCancelOrder(context.Background(), orderID)
	assert.NoError(t, err)

	mockWallet.AssertNotCalled(t, "RefundEscrow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	assert.Equal(t, order.StatusCancelled, o.Status)
}

func TestForceCancelOrder_NonRefundable(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil)

	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	err := svc.ForceCancelOrder(context.Background(), orderID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tidak dapat membatalkan pesanan yang sudah selesai atau dibatalkan")

	mockWallet.AssertNotCalled(t, "RefundEscrow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything)
}

// ==========================================
// SEC-02: ResolveDispute Tests
// ==========================================

func TestResolveDispute_Refund_Success(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockTrip := new(mockTripRepository)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil)
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil)
	mockTrip.On("RestoreCapacity", mock.Anything, mock.Anything, tripID, 1.5, 2.0).Return(nil)
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Return(nil)
	mockNotif.On("CreateNotification", mock.Anything, mock.Anything).Return(nil)

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err := svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	assert.NoError(t, err)
	assert.Equal(t, order.StatusCancelled, o.Status)
	assert.Equal(t, order.PaymentRefunded, o.PaymentStatus)
}

func TestResolveDispute_Payout_Success(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockTrip := new(mockTripRepository)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil)
	// totalRunnerPayout = 50000 + (10000 - 2000 - 1000) = 57000.0
	mockWallet.On("ReleaseEscrowWithRefund", mock.Anything, mock.Anything, runnerID, requesterID, orderID, 57000.0, 2000.0, 1000.0).Return(nil)
	mockWallet.On("ReleaseLiability", mock.Anything, mock.Anything, runnerID, orderID, 50000.0).Return(nil)
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Return(nil)
	mockNotif.On("CreateNotification", mock.Anything, mock.Anything).Return(nil)

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err := svc.ResolveDispute(context.Background(), orderID, user.RoleRunner)
	assert.NoError(t, err)
	assert.Equal(t, order.StatusCompleted, o.Status)
	assert.Equal(t, order.PaymentReleased, o.PaymentStatus)
}

func TestResolveDispute_AlreadyResolved(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, nil, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

	orderID := uuid.New()
	requesterID := uuid.New()

	o := &order.Order{
		ID:            orderID,
		RequesterID:   requesterID,
		Status:        order.StatusCancelled, // already resolved/cancelled!
		PaymentMethod: order.MethodEscrow,
		PaymentStatus: order.PaymentRefunded,
	}

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil)

	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	err := svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pesanan tidak dalam status sengketa")
}

func TestResolveDispute_CalledTwice(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockTrip := new(mockTripRepository)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o1, nil).Once()
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil).Once()
	mockRepo.On("Update", mock.Anything, mock.Anything, o1).Return(nil).Once()
	mockNotif.On("CreateNotification", mock.Anything, mock.Anything).Return(nil)

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err := svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	assert.NoError(t, err)

	o2 := &order.Order{
		ID:            orderID,
		RequesterID:   requesterID,
		RunnerID:      &runnerID,
		Status:        order.StatusCancelled,
		PaymentMethod: order.MethodEscrow,
		PaymentStatus: order.PaymentRefunded,
	}

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o2, nil).Once()

	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	err = svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pesanan tidak dalam status sengketa")
}

func TestResolveDispute_ConcurrentRefund(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockTrip := new(mockTripRepository)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil).Once()
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Run(func(args mock.Arguments) {
		o.Status = order.StatusCancelled
		o.PaymentStatus = order.PaymentRefunded
	}).Return(nil).Once()
	mockNotif.On("CreateNotification", mock.Anything, mock.Anything).Return(nil)

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()
	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	go func() {
		defer wg.Done()
		err1 = svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	}()

	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		err2 = svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	}()

	wg.Wait()

	assert.True(t, (err1 == nil && err2 != nil) || (err1 != nil && err2 == nil))
}

func TestResolveDispute_ConcurrentPayout(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockTrip := new(mockTripRepository)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()
	mockWallet.On("ReleaseEscrowWithRefund", mock.Anything, mock.Anything, runnerID, requesterID, orderID, 57000.0, 2000.0, 1000.0).Return(nil).Once()
	mockWallet.On("ReleaseLiability", mock.Anything, mock.Anything, runnerID, orderID, 50000.0).Return(nil).Once()
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Run(func(args mock.Arguments) {
		o.Status = order.StatusCompleted
		o.PaymentStatus = order.PaymentReleased
	}).Return(nil).Once()
	mockNotif.On("CreateNotification", mock.Anything, mock.Anything).Return(nil)

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()
	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	go func() {
		defer wg.Done()
		err1 = svc.ResolveDispute(context.Background(), orderID, user.RoleRunner)
	}()

	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		err2 = svc.ResolveDispute(context.Background(), orderID, user.RoleRunner)
	}()

	wg.Wait()

	assert.True(t, (err1 == nil && err2 != nil) || (err1 != nil && err2 == nil))
}

func TestResolveDispute_ConcurrentMixedDecision(t *testing.T) {
	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockWallet := new(mockWalletService)
	mockTrip := new(mockTripRepository)
	mockAudit := new(mockAuditService)
	mockNotif := new(mockNotificationService)

	svc := order.NewService(mockRepo, nil, mockTrip, nil, mockWallet, nil, nil, mockNotif, nil, db, mockAudit, nil, nil)

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

	// Goroutine 1 (Refund) gets lock first and succeeds
	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()
	mockWallet.On("RefundEscrow", mock.Anything, mock.Anything, requesterID, orderID, 60000.0).Return(nil).Once()
	mockRepo.On("Update", mock.Anything, mock.Anything, o).Run(func(args mock.Arguments) {
		o.Status = order.StatusCancelled
		o.PaymentStatus = order.PaymentRefunded
	}).Return(nil).Once()
	mockNotif.On("CreateNotification", mock.Anything, mock.Anything).Return(nil)

	// Goroutine 2 (Payout) locks second and fails because status is already Cancelled
	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(o, nil).Once()

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error

	mockSql.ExpectBegin()
	mockSql.ExpectCommit()
	mockSql.ExpectBegin()
	mockSql.ExpectRollback()

	// Request A: Refund
	go func() {
		defer wg.Done()
		err1 = svc.ResolveDispute(context.Background(), orderID, user.RoleRequester)
	}()

	time.Sleep(20 * time.Millisecond)

	// Request B: Payout
	go func() {
		defer wg.Done()
		err2 = svc.ResolveDispute(context.Background(), orderID, user.RoleRunner)
	}()

	wg.Wait()

	// Only one succeeds, and it should be refund (cancelled/refunded)
	assert.NoError(t, err1)
	assert.Error(t, err2)
	assert.Contains(t, err2.Error(), "pesanan tidak dalam status sengketa")

	assert.Equal(t, order.StatusCancelled, o.Status)
	assert.Equal(t, order.PaymentRefunded, o.PaymentStatus)

	// Verify ReleaseEscrowWithRefund was NEVER called
	mockWallet.AssertNotCalled(t, "ReleaseEscrowWithRefund", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

type mockConfigService struct {
	mock.Mock
	systemconfig.Service
}

func (m *mockConfigService) GetValue(ctx context.Context, key, defaultVal string) string {
	args := m.Called(ctx, key, defaultVal)
	return args.String(0)
}

func TestPaymentCollision(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())

	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockConfig := new(mockConfigService)
	mockAudit := new(mockAuditService)
	mockUserSvc := new(mockUserService)
	dummyUser := &user.User{
		Name:  "Test User",
		Email: "test@example.com",
	}
	mockUserSvc.On("GetByID", mock.Anything, mock.Anything, mock.Anything).Return(dummyUser, nil)

	svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, mockAudit, nil, nil)

	// Order A
	orderIDA := uuid.New()
	requesterIDA := uuid.New()
	oA := &order.Order{
		ID:            orderIDA,
		RequesterID:   requesterIDA,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  50000.0,
		PGFee:         0,
	}

	// Order B
	orderIDB := uuid.New()
	requesterIDB := uuid.New()
	oB := &order.Order{
		ID:            orderIDB,
		RequesterID:   requesterIDB,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  49997.0,
		PGFee:         0,
	}

	mockConfig.On("GetValue", mock.Anything, "qris_pg_fee", "0").Return("0")

	// Order A setup in Repo
	mockRepo.On("FindByID", mock.Anything, orderIDA).Return(oA, nil)
	mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))

	// GetByID A -> triggers generateOrderQRIS
	resA, err := svc.GetByID(context.Background(), orderIDA, requesterIDA, user.RoleRequester)
	assert.NoError(t, err)
	assert.True(t, resA.UniqueCode > 0)

	// Now we seed the candidate that B would choose if they collide.
	// For instance, B's base is 49997.
	// We want B's total to match A's total (which is 50000 + A.UniqueCode).
	// B's candidate code would be: A.TotalPayment - B's base = 50000 + A.UniqueCode - 49997 = A.UniqueCode + 3.
	// Let's check: B's candidate code = A.UniqueCode + 3.
	// If A gets unique code 12 (total 50012), B's candidate code 15 would also result in total 50012.
	// Since 50012 is already in miniredis under key "active_total_payment:50012.00" (created by A),
	// B SetNX for 50012.00 will fail!
	// B will then choose another unique code (e.g. 16 -> total 50013).

	mockRepo.On("FindByID", mock.Anything, orderIDB).Return(oB, nil)
	mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))

	resB, err := svc.GetByID(context.Background(), orderIDB, requesterIDB, user.RoleRequester)
	assert.NoError(t, err)
	assert.True(t, resB.UniqueCode > 0)

	// Assert that their final total payments are NOT equal!
	assert.NotEqual(t, resA.TotalPayment, resB.TotalPayment)
}

func TestPaymentReservation_Concurrent(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())

	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockConfig := new(mockConfigService)
	mockAudit := new(mockAuditService)
	mockUserSvc := new(mockUserService)
	dummyUser := &user.User{
		Name:  "Test User",
		Email: "test@example.com",
	}
	mockUserSvc.On("GetByID", mock.Anything, mock.Anything, mock.Anything).Return(dummyUser, nil)

	svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, mockAudit, nil, nil)

	orderIDA := uuid.New()
	requesterIDA := uuid.New()
	oA := &order.Order{
		ID:            orderIDA,
		RequesterID:   requesterIDA,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  50000.0,
		PGFee:         0,
	}

	orderIDB := uuid.New()
	requesterIDB := uuid.New()
	oB := &order.Order{
		ID:            orderIDB,
		RequesterID:   requesterIDB,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  49997.0,
		PGFee:         0,
	}

	mockConfig.On("GetValue", mock.Anything, "qris_pg_fee", "0").Return("0")

	var wg sync.WaitGroup
	wg.Add(2)

	mockRepo.On("FindByID", mock.Anything, orderIDA).Return(oA, nil)
	mockRepo.On("FindByID", mock.Anything, orderIDB).Return(oB, nil)
	mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))
	mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))

	go func() {
		defer wg.Done()
		_, _ = svc.GetByID(context.Background(), orderIDA, requesterIDA, user.RoleRequester)
	}()

	go func() {
		defer wg.Done()
		_, _ = svc.GetByID(context.Background(), orderIDB, requesterIDB, user.RoleRequester)
	}()

	wg.Wait()

	assert.NotEqual(t, oA.TotalPayment, oB.TotalPayment)
}

func TestPaymentReservation_Cleanup(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())

	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockConfig := new(mockConfigService)
	mockAudit := new(mockAuditService)
	mockUserSvc := new(mockUserService)
	dummyUser := &user.User{
		Name:  "Test User",
		Email: "test@example.com",
	}
	mockUserSvc.On("GetByID", mock.Anything, mock.Anything, mock.Anything).Return(dummyUser, nil)

	svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, mockAudit, nil, nil)

	orderID := uuid.New()
	requesterID := uuid.New()
	o := &order.Order{
		ID:            orderID,
		RequesterID:   requesterID,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  50000.0,
		PGFee:         0,
	}

	mockConfig.On("GetValue", mock.Anything, "qris_pg_fee", "0").Return("0")
	mockRepo.On("FindByID", mock.Anything, orderID).Return(o, nil)
	mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := svc.GetByID(context.Background(), orderID, requesterID, user.RoleRequester)
	assert.NoError(t, err)

	key := fmt.Sprintf("active_total_payment:%.2f", res.TotalPayment)
	assert.True(t, mr.Exists(key))

	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderID).Return(res, nil)
	mockRepo.On("Update", mock.Anything, mock.Anything, res).Return(nil)
	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err = svc.ForceCancelOrder(context.Background(), orderID)
	assert.NoError(t, err)

	assert.False(t, mr.Exists(key))
}

func TestGetByID_Unauthorized_NoQRISGeneration(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())

	db, _ := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockConfig := new(mockConfigService)
	mockAudit := new(mockAuditService)
	mockUserSvc := new(mockUserService)

	svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, mockAudit, nil, nil)

	orderID := uuid.New()
	requesterID := uuid.New()
	attackerID := uuid.New() // Unauthorized user

	o := &order.Order{
		ID:            orderID,
		RequesterID:   requesterID,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  50000.0,
		PGFee:         0,
	}

	mockRepo.On("FindByID", mock.Anything, orderID).Return(o, nil)

	// Attacker calls GetByID -> should fail with 403 / Access Denied
	res, err := svc.GetByID(context.Background(), orderID, attackerID, user.RoleRequester)
	assert.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "akses ditolak")

	// Verify NO Redis keys were generated (since populatePaymentInfo was not called)
	keys := mr.Keys()
	for _, k := range keys {
		assert.NotContains(t, k, "active_total_payment")
	}
}

func TestPaymentReservation_LateCleanupRace(t *testing.T) {
	mr, err := miniredis.Run()
	assert.NoError(t, err)
	defer mr.Close()

	rClient := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	redisCache := cache.NewRedisFromClient(rClient, zap.NewNop())

	db, mockSql := testutil.NewMockDB(t)
	mockRepo := new(mockOrderRepository)
	mockConfig := new(mockConfigService)
	mockAudit := new(mockAuditService)
	mockUserSvc := new(mockUserService)
	dummyUser := &user.User{
		Name:  "Test User",
		Email: "test@example.com",
	}
	mockUserSvc.On("GetByID", mock.Anything, mock.Anything, mock.Anything).Return(dummyUser, nil)

	svc := order.NewService(mockRepo, mockUserSvc, nil, nil, nil, mockConfig, nil, nil, redisCache, db, mockAudit, nil, nil)

	// Order A
	orderIDA := uuid.New()
	requesterIDA := uuid.New()
	oA := &order.Order{
		ID:            orderIDA,
		RequesterID:   requesterIDA,
		Status:        order.StatusPending,
		PaymentMethod: "escrow",
		PaymentSource: "qris",
		PaymentStatus: order.PaymentUnpaid,
		TotalPayment:  50000.0,
		PGFee:         0,
	}

	mockConfig.On("GetValue", mock.Anything, "qris_pg_fee", "0").Return("0")
	mockRepo.On("FindByID", mock.Anything, orderIDA).Return(oA, nil)
	mockSql.ExpectExec(`UPDATE.*orders`).WillReturnResult(sqlmock.NewResult(1, 1))

	// 1. Order A generates QRIS and reserves key
	resA, err := svc.GetByID(context.Background(), orderIDA, requesterIDA, user.RoleRequester)
	assert.NoError(t, err)

	key := fmt.Sprintf("active_total_payment:%.2f", resA.TotalPayment)
	assert.True(t, mr.Exists(key))
	val, getErr := mr.Get(key)
	assert.NoError(t, getErr)
	assert.Equal(t, orderIDA.String(), val)

	// 2. Simulate Order A's key expires (TTL ends)
	mr.Del(key)
	assert.False(t, mr.Exists(key))

	// 3. Order B is created and reclaims the same nominal key
	orderIDB := uuid.New()
	assert.NoError(t, mr.Set(key, orderIDB.String()))
	assert.True(t, mr.Exists(key))
	
	val, getErr = mr.Get(key)
	assert.NoError(t, getErr)
	assert.Equal(t, orderIDB.String(), val)

	// 4. Order A's late cancel flow triggers and tries to cancel Order A
	mockRepo.On("FindByIDForUpdate", mock.Anything, mock.Anything, orderIDA).Return(resA, nil)
	mockRepo.On("Update", mock.Anything, mock.Anything, resA).Return(nil)
	mockSql.ExpectBegin()
	mockSql.ExpectCommit()

	err = svc.ForceCancelOrder(context.Background(), orderIDA)
	assert.NoError(t, err)

	// 5. Verify that Order B's key was NOT deleted by Order A's late cleanup!
	assert.True(t, mr.Exists(key))
	
	val, getErr = mr.Get(key)
	assert.NoError(t, getErr)
	assert.Equal(t, orderIDB.String(), val)
}
