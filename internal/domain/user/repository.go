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
	FindNearbyRunners(ctx context.Context, lat, lng, radiusKm float64) ([]User, error)
	Create(ctx context.Context, user *User) error
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindAll(ctx context.Context) ([]User, error) {
	users := []User{}
	err := r.db.NewSelect().Model(&users).WhereAllWithDeleted().Where("deleted_at IS NULL").Scan(ctx)
	return users, err
}

func (r *repository) FindAllWithFilters(ctx context.Context, role string, isVerified, isSuspended *bool) ([]User, error) {
	users := []User{}
	q := r.db.NewSelect().Model(&users).Order("created_at DESC")

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

func (r *repository) FindNearbyRunners(ctx context.Context, lat, lng, radiusKm float64) ([]User, error) {
	// Bounding Box Calculation (Approximate)
	// 1 degree lat ~ 111km
	latDiff := radiusKm / 111.0
	// 1 degree lng ~ 111km * cos(lat)
	lngDiff := radiusKm / (111.0 * 0.99) // Using 0.99 for cos(low lat) approx

	minLat, maxLat := lat-latDiff, lat+latDiff
	minLng, maxLng := lng-lngDiff, lng+lngDiff

	users := []User{}
	err := r.db.NewSelect().Model(&users).
		Where("role = 'runner'").
		Where("is_suspended = false").
		Where("last_lat BETWEEN ? AND ?", minLat, maxLat).
		Where("last_lng BETWEEN ? AND ?", minLng, maxLng).
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

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
