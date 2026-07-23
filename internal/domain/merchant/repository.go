package merchant

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	// Merchant
	CreateMerchant(ctx context.Context, m *Merchant) error
	UpdateMerchant(ctx context.Context, m *Merchant) error
	GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error)
	GetMerchantByOwnerID(ctx context.Context, ownerID uuid.UUID) (*Merchant, error)
	ListNearbyMerchants(ctx context.Context, lat, lng float64, radiusKm float64) ([]Merchant, error)
	ListAllMerchants(ctx context.Context) ([]Merchant, error)
	DeleteMerchant(ctx context.Context, id uuid.UUID) error

	// Menu
	CreateMenu(ctx context.Context, menu *Menu) error
	UpdateMenu(ctx context.Context, menu *Menu) error
	GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error)
	ListMenusByMerchantID(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error

	// OrderItem
	CreateOrderItems(ctx context.Context, items []OrderItem) error
	ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error)
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

// Merchant Implementation

func (r *repository) CreateMerchant(ctx context.Context, m *Merchant) error {
	_, err := r.db.NewInsert().Model(m).Exec(ctx)
	return err
}

func (r *repository) UpdateMerchant(ctx context.Context, m *Merchant) error {
	_, err := r.db.NewUpdate().Model(m).WherePK().Exec(ctx)
	return err
}

func (r *repository) GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error) {
	m := new(Merchant)
	err := r.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("merchant tidak ditemukan")
		}
		return nil, err
	}
	return m, nil
}

func (r *repository) GetMerchantByOwnerID(ctx context.Context, ownerID uuid.UUID) (*Merchant, error) {
	m := new(Merchant)
	err := r.db.NewSelect().Model(m).Where("owner_id = ?", ownerID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("merchant tidak ditemukan")
		}
		return nil, err
	}
	return m, nil
}

func (r *repository) ListNearbyMerchants(ctx context.Context, lat, lng float64, radiusKm float64) ([]Merchant, error) {
	var merchants []Merchant
	// Haversine formula to compute distance in km
	err := r.db.NewSelect().
		Model(&merchants).
		Where("is_open = ?", true).
		Where("6371 * acos(cos(radians(?)) * cos(radians(latitude)) * cos(radians(longitude) - radians(?)) + sin(radians(?)) * sin(radians(latitude))) <= ?", lat, lng, lat, radiusKm).
		Scan(ctx)
	return merchants, err
}

func (r *repository) ListAllMerchants(ctx context.Context) ([]Merchant, error) {
	var merchants []Merchant
	err := r.db.NewSelect().Model(&merchants).Order("created_at DESC").Scan(ctx)
	return merchants, err
}

func (r *repository) DeleteMerchant(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*Merchant)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// Menu Implementation

func (r *repository) CreateMenu(ctx context.Context, menu *Menu) error {
	_, err := r.db.NewInsert().Model(menu).Exec(ctx)
	return err
}

func (r *repository) UpdateMenu(ctx context.Context, menu *Menu) error {
	_, err := r.db.NewUpdate().Model(menu).WherePK().Exec(ctx)
	return err
}

func (r *repository) GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error) {
	m := new(Menu)
	err := r.db.NewSelect().Model(m).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("menu tidak ditemukan")
		}
		return nil, err
	}
	return m, nil
}

func (r *repository) ListMenusByMerchantID(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error) {
	var menus []Menu
	q := r.db.NewSelect().Model(&menus).Where("merchant_id = ?", merchantID)
	if onlyAvailable {
		q = q.Where("is_available = ?", true)
	}
	err := q.Order("name ASC").Scan(ctx)
	return menus, err
}

func (r *repository) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*Menu)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// OrderItem Implementation

func (r *repository) CreateOrderItems(ctx context.Context, items []OrderItem) error {
	if len(items) == 0 {
		return nil
	}
	_, err := r.db.NewInsert().Model(&items).Exec(ctx)
	return err
}

func (r *repository) ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error) {
	var items []OrderItem
	err := r.db.NewSelect().
		Model(&items).
		ColumnExpr("oi.*").
		ColumnExpr("mn.name AS menu_name, mn.image_url AS menu_image").
		Join("LEFT JOIN menus AS mn ON mn.id = oi.menu_id").
		Where("oi.order_id = ?", orderID).
		Scan(ctx)
	return items, err
}
