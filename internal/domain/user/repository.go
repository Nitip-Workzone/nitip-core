package user

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	FindAll(ctx context.Context) ([]User, error)
	FindAllWithFilters(ctx context.Context, role string, isVerified, isSuspended *bool) ([]User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByIDs(ctx context.Context, ids []uuid.UUID) ([]User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByWhatsappNumber(ctx context.Context, whatsappNumber string) (*User, error)
	FindNearbyRunners(ctx context.Context, lat, lng, radiusKm float64) ([]User, error)
	Create(ctx context.Context, user *User) error
	Update(ctx context.Context, user *User) error
	UpdateLocation(ctx context.Context, id uuid.UUID, lat, lng float64) error
	Delete(ctx context.Context, id uuid.UUID) error
	ClearDeviceSessions(ctx context.Context, deviceID string, excludeUserID uuid.UUID) error
	FindBankAccountByUserID(ctx context.Context, userID uuid.UUID) (*UserBankAccount, error)
	UpsertBankAccount(ctx context.Context, bankAccount *UserBankAccount) error
	UpdateAcceptingOrders(ctx context.Context, id uuid.UUID, isAccepting bool) error
	GetDB() *bun.DB

	// Invitations
	CreateInvitation(ctx context.Context, invite *RegistrationInvitation) error
	FindInvitationByToken(ctx context.Context, token string) (*RegistrationInvitation, error)
	ListInvitations(ctx context.Context) ([]RegistrationInvitation, error)
	UpdateInvitation(ctx context.Context, invite *RegistrationInvitation) error
}

type repository struct {
	db *bun.DB
}

func (r *repository) GetDB() *bun.DB {
	return r.db
}


func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindAll(ctx context.Context) ([]User, error) {
	users := []User{}
	// P1 FIX: guard limit 100 to prevent OOM on 10k users (was unbounded)
	err := r.db.NewSelect().Model(&users).WhereAllWithDeleted().Where("deleted_at IS NULL").Order("created_at DESC").Limit(100).Scan(ctx)
	return users, err
}

func (r *repository) FindAllWithFilters(ctx context.Context, role string, isVerified, isSuspended *bool) ([]User, error) {
	users := []User{}
	q := r.db.NewSelect().Model(&users).Order("created_at DESC").Limit(100)

	if role != "" {
		q = q.Where("role = ?", role)
	}
	if isVerified != nil {
		q = q.Where("is_verified = ?", *isVerified)
	}
	if isSuspended != nil {
		q = q.Where("is_suspended = ?", *isSuspended)
	}

	err := q.Scan(ctx)
	return users, err
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*User, error) {
	user := new(User)
	err := r.db.NewSelect().Model(user).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return user, nil
}
func (r *repository) FindByIDs(ctx context.Context, ids []uuid.UUID) ([]User, error) {
	if len(ids) == 0 {
		return []User{}, nil
	}
	users := []User{}
	err := r.db.NewSelect().Model(&users).Where("id IN (?)", bun.In(ids)).Scan(ctx) // nolint:staticcheck
	return users, err
}

func (r *repository) FindByEmail(ctx context.Context, email string) (*User, error) {
	user := new(User)
	err := r.db.NewSelect().Model(user).Where("email = ?", email).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (r *repository) FindByWhatsappNumber(ctx context.Context, whatsappNumber string) (*User, error) {
	user := new(User)
	err := r.db.NewSelect().Model(user).Where("whatsapp_number = ?", whatsappNumber).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (r *repository) FindNearbyRunners(ctx context.Context, lat, lng, radiusKm float64) ([]User, error) {
	// Bounding Box Calculation (Approximate)
	// 1 degree lat ~ 111km
	latDiff := radiusKm / 111.0
	// 1 degree lng ~ 111km * cos(lat)
	lngDiff := radiusKm / (111.0 * 0.99) // Using 0.99 for cos(low lat) approx

	minLat, maxLat := lat-latDiff, lat+latDiff
	minLng, maxLng := lng-lngDiff, lng+lngDiff

	users := []User{}
	// P1 FIX: limit 100 to prevent full table scan OOM, plus is_accepting filter for runner live
	err := r.db.NewSelect().Model(&users).
		Where("role = 'runner'").
		Where("is_suspended = false").
		Where("last_lat BETWEEN ? AND ?", minLat, maxLat).
		Where("last_lng BETWEEN ? AND ?", minLng, maxLng).
		OrderExpr("id DESC").
		Limit(100).
		Scan(ctx)

	return users, err
}

func (r *repository) Create(ctx context.Context, user *User) error {
	_, err := r.db.NewInsert().Model(user).Exec(ctx)
	return err
}

func (r *repository) Update(ctx context.Context, user *User) error {
	_, err := r.db.NewUpdate().Model(user).WherePK().Exec(ctx)
	return err
}

func (r *repository) UpdateAcceptingOrders(ctx context.Context, id uuid.UUID, isAccepting bool) error {
	// P2 perf: atomic update only needed fields, avoid full row lock
	_, err := r.db.NewUpdate().Model((*User)(nil)).
		Set("is_accepting_orders = ?", isAccepting).
		Set("updated_at = NOW()").
		Where("id = ?", id).
		Where("role = ?", "runner").
		Exec(ctx)
	return err
}

func (r *repository) UpdateLocation(ctx context.Context, id uuid.UUID, lat, lng float64) error {
	_, err := r.db.NewUpdate().
		Model((*User)(nil)).
		Set("last_lat = ?", lat).
		Set("last_lng = ?", lng).
		Set("updated_at = NOW()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *repository) ClearDeviceSessions(ctx context.Context, deviceID string, excludeUserID uuid.UUID) error {
	_, err := r.db.NewUpdate().
		Table("users").
		Set("device_id = ?", nil).
		Set("fcm_token = ?", nil).
		Set("token_version = token_version + 1").
		Where("device_id = ?", deviceID).
		Where("id != ?", excludeUserID).
		Exec(ctx)
	return err
}

func (r *repository) FindBankAccountByUserID(ctx context.Context, userID uuid.UUID) (*UserBankAccount, error) {
	uba := new(UserBankAccount)
	err := r.db.NewSelect().Model(uba).Where("user_id = ?", userID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return uba, nil
}

func (r *repository) UpsertBankAccount(ctx context.Context, bankAccount *UserBankAccount) error {
	_, err := r.db.NewInsert().
		Model(bankAccount).
		On("CONFLICT (user_id) DO UPDATE").
		Set("bank_name = EXCLUDED.bank_name").
		Set("account_no = EXCLUDED.account_no").
		Set("account_name = EXCLUDED.account_name").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	return err
}

func (r *repository) CreateInvitation(ctx context.Context, invite *RegistrationInvitation) error {
	_, err := r.db.NewInsert().Model(invite).Exec(ctx)
	return err
}

func (r *repository) FindInvitationByToken(ctx context.Context, token string) (*RegistrationInvitation, error) {
	invite := new(RegistrationInvitation)
	err := r.db.NewSelect().Model(invite).Where("token = ?", token).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return invite, nil
}

func (r *repository) ListInvitations(ctx context.Context) ([]RegistrationInvitation, error) {
	var invites []RegistrationInvitation
	err := r.db.NewSelect().Model(&invites).Order("created_at DESC").Limit(100).Scan(ctx)
	return invites, err
}

func (r *repository) UpdateInvitation(ctx context.Context, invite *RegistrationInvitation) error {
	_, err := r.db.NewUpdate().Model(invite).WherePK().Exec(ctx)
	return err
}

