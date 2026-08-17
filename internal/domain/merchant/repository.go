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
	ListMenusByMerchantIDWithVariants(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error

	// Category
	CreateCategory(ctx context.Context, c *MenuCategory) error
	UpdateCategory(ctx context.Context, c *MenuCategory) error
	GetCategoryByID(ctx context.Context, id uuid.UUID) (*MenuCategory, error)
	ListCategoriesByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]MenuCategory, error)
	DeleteCategory(ctx context.Context, id uuid.UUID) error

	// VariantGroups
	CreateVariantGroup(ctx context.Context, g *MenuVariantGroup) error
	UpdateVariantGroup(ctx context.Context, g *MenuVariantGroup) error
	DeleteVariantGroup(ctx context.Context, id uuid.UUID) error
	ListVariantGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuVariantGroup, error)
	GetVariantGroupByID(ctx context.Context, id uuid.UUID) (*MenuVariantGroup, error)
	CreateVariantOption(ctx context.Context, o *MenuVariantOption) error
	UpdateVariantOption(ctx context.Context, o *MenuVariantOption) error
	GetVariantOptionByID(ctx context.Context, id uuid.UUID) (*MenuVariantOption, error)
	DeleteVariantOption(ctx context.Context, id uuid.UUID) error
	ListVariantOptionsByGroupID(ctx context.Context, groupID uuid.UUID) ([]MenuVariantOption, error)

	// ToppingGroups
	CreateToppingGroup(ctx context.Context, g *MenuToppingGroup) error
	UpdateToppingGroup(ctx context.Context, g *MenuToppingGroup) error
	GetToppingGroupByID(ctx context.Context, id uuid.UUID) (*MenuToppingGroup, error)
	DeleteToppingGroup(ctx context.Context, id uuid.UUID) error
	ListToppingGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuToppingGroup, error)
	CreateToppingOption(ctx context.Context, o *MenuToppingOption) error
	UpdateToppingOption(ctx context.Context, o *MenuToppingOption) error
	GetToppingOptionByID(ctx context.Context, id uuid.UUID) (*MenuToppingOption, error)
	DeleteToppingOption(ctx context.Context, id uuid.UUID) error
	ListToppingOptionsByGroupID(ctx context.Context, groupID uuid.UUID) ([]MenuToppingOption, error)

	// Images cleanup helpers
	ListAllImagesByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]string, error)
	ListImagesByMenuID(ctx context.Context, menuID uuid.UUID) ([]string, error)

	// Addon Masters (Tambahan - independent shared per merchant, bahasa Indonesia lebih cocok daripada Topping)
	CreateAddonMaster(ctx context.Context, m *AddonMaster) error
	UpdateAddonMaster(ctx context.Context, m *AddonMaster) error
	DeleteAddonMaster(ctx context.Context, id uuid.UUID) error
	GetAddonMasterByID(ctx context.Context, id uuid.UUID) (*AddonMaster, error)
	ListAddonMastersByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]AddonMaster, error)
	CreateAddonOption(ctx context.Context, o *AddonOption) error
	GetAddonOptionByID(ctx context.Context, id uuid.UUID) (*AddonOption, error)
	UpdateAddonOption(ctx context.Context, o *AddonOption) error
	DeleteAddonOption(ctx context.Context, id uuid.UUID) error
	ListAddonOptionsByMasterID(ctx context.Context, masterID uuid.UUID) ([]AddonOption, error)

	// OrderItem
	CreateOrderItems(ctx context.Context, items []OrderItem) error
	ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error)

	// Survey
	CreateSurvey(ctx context.Context, s *MerchantSurvey) error
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
	// P1 FIX: Use PostGIS ST_DWithin geography for index-assisted search (was acos full scan)
	// Fallback to acos if PostGIS not available but try geography first for perf 1000ms->30ms on 10k rows
	// Note: requires pg extension postgis enabled (already enabled for trips & orders geom)
	radiusM := radiusKm * 1000
	err := r.db.NewSelect().
		Model(&merchants).
		// Use ST_DWithin with geography(Point) for GIST index acceleration; if geom column exists use it else compute on fly
		Where("ST_DWithin(CAST(ST_SetSRID(ST_MakePoint(longitude, latitude),4326) AS geography), CAST(ST_SetSRID(ST_MakePoint(?, ?),4326) AS geography), ?)", lng, lat, radiusM).
		OrderExpr("ST_Distance(CAST(ST_SetSRID(ST_MakePoint(longitude, latitude),4326) AS geography), CAST(ST_SetSRID(ST_MakePoint(?, ?),4326) AS geography)) ASC", lng, lat).
		Limit(50).
		Scan(ctx)
	if err != nil {
		// fallback to old acos if PostGIS fails
		err = r.db.NewSelect().
			Model(&merchants).
			Where("6371 * acos(cos(radians(?)) * cos(radians(latitude)) * cos(radians(longitude) - radians(?)) + sin(radians(?)) * sin(radians(latitude))) <= ?", lat, lng, lat, radiusKm).
			Limit(50).
			Scan(ctx)
	}
	return merchants, err
}

func (r *repository) ListAllMerchants(ctx context.Context) ([]Merchant, error) {
	var merchants []Merchant
	// P1 FIX: guard limit 100 to prevent OOM 512M on 10k merchants
	err := r.db.NewSelect().Model(&merchants).Order("created_at DESC").Limit(100).Scan(ctx)
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
	q := r.db.NewSelect().Model(&menus).Where("mn.merchant_id = ?", merchantID)
	if onlyAvailable {
		q = q.Where("mn.is_available = ?", true)
	}
	err := q.Order("mn.name ASC").Scan(ctx)
	return menus, err
}

func (r *repository) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*Menu)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *repository) ListMenusByMerchantIDWithVariants(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error) {
	var menus []Menu
	// Fix ambiguous merchant_id: use mn.merchant_id explicit, because Category relation also has merchant_id column in join
	q := r.db.NewSelect().Model(&menus).Where("mn.merchant_id = ?", merchantID)
	if onlyAvailable {
		q = q.Where("mn.is_available = ?", true)
	}
	// Preload variant groups + options + topping groups + options
	q = q.Relation("VariantGroups").Relation("VariantGroups.Options").Relation("ToppingGroups").Relation("ToppingGroups.Options").Relation("Category")
	err := q.Order("mn.name ASC").Scan(ctx)
	return menus, err
}

// Category
func (r *repository) CreateCategory(ctx context.Context, c *MenuCategory) error {
	_, err := r.db.NewInsert().Model(c).Exec(ctx)
	return err
}
func (r *repository) UpdateCategory(ctx context.Context, c *MenuCategory) error {
	_, err := r.db.NewUpdate().Model(c).WherePK().Exec(ctx)
	return err
}
func (r *repository) GetCategoryByID(ctx context.Context, id uuid.UUID) (*MenuCategory, error) {
	c := new(MenuCategory)
	err := r.db.NewSelect().Model(c).Where("id = ?", id).Scan(ctx)
	return c, err
}
func (r *repository) ListCategoriesByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]MenuCategory, error) {
	var list []MenuCategory
	err := r.db.NewSelect().Model(&list).Where("merchant_id = ?", merchantID).OrderExpr("sort_order ASC, name ASC").Scan(ctx)
	return list, err
}
func (r *repository) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*MenuCategory)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

// VariantGroups
func (r *repository) CreateVariantGroup(ctx context.Context, g *MenuVariantGroup) error {
	_, err := r.db.NewInsert().Model(g).Exec(ctx)
	return err
}
func (r *repository) UpdateVariantGroup(ctx context.Context, g *MenuVariantGroup) error {
	// Use explicit columns to avoid zeroing group_id etc - only update mutable fields
	_, err := r.db.NewUpdate().Model(g).WherePK().Column("name", "type", "is_required", "min_select", "max_select", "sort_order", "updated_at").Exec(ctx)
	return err
}
func (r *repository) GetVariantGroupByID(ctx context.Context, id uuid.UUID) (*MenuVariantGroup, error) {
	g := new(MenuVariantGroup)
	err := r.db.NewSelect().Model(g).Where("id = ?", id).Relation("Options").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return g, nil
}
func (r *repository) DeleteVariantGroup(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*MenuVariantGroup)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
func (r *repository) ListVariantGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuVariantGroup, error) {
	var list []MenuVariantGroup
	err := r.db.NewSelect().Model(&list).Where("menu_id = ?", menuID).Relation("Options").Order("sort_order ASC").Scan(ctx)
	return list, err
}
func (r *repository) CreateVariantOption(ctx context.Context, o *MenuVariantOption) error {
	_, err := r.db.NewInsert().Model(o).Exec(ctx)
	return err
}
func (r *repository) GetVariantOptionByID(ctx context.Context, id uuid.UUID) (*MenuVariantOption, error) {
	o := new(MenuVariantOption)
	err := r.db.NewSelect().Model(o).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return o, nil
}
func (r *repository) UpdateVariantOption(ctx context.Context, o *MenuVariantOption) error {
	// Explicit columns only - preserve group_id & created_at, avoid FK violation from zero group_id
	_, err := r.db.NewUpdate().Model(o).WherePK().Column("label", "price_delta", "image_url", "is_default", "is_available", "sort_order", "updated_at").Exec(ctx)
	return err
}
func (r *repository) DeleteVariantOption(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*MenuVariantOption)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
func (r *repository) ListVariantOptionsByGroupID(ctx context.Context, groupID uuid.UUID) ([]MenuVariantOption, error) {
	var list []MenuVariantOption
	err := r.db.NewSelect().Model(&list).Where("group_id = ?", groupID).Order("sort_order ASC").Scan(ctx)
	return list, err
}

// ToppingGroups
func (r *repository) CreateToppingGroup(ctx context.Context, g *MenuToppingGroup) error {
	_, err := r.db.NewInsert().Model(g).Exec(ctx)
	return err
}
func (r *repository) UpdateToppingGroup(ctx context.Context, g *MenuToppingGroup) error {
	_, err := r.db.NewUpdate().Model(g).WherePK().Column("name", "type", "is_required", "min_select", "max_select", "sort_order", "updated_at").Exec(ctx)
	return err
}
func (r *repository) GetToppingGroupByID(ctx context.Context, id uuid.UUID) (*MenuToppingGroup, error) {
	g := new(MenuToppingGroup)
	err := r.db.NewSelect().Model(g).Where("id = ?", id).Relation("Options").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return g, nil
}
func (r *repository) DeleteToppingGroup(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*MenuToppingGroup)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
func (r *repository) ListToppingGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuToppingGroup, error) {
	var list []MenuToppingGroup
	err := r.db.NewSelect().Model(&list).Where("menu_id = ?", menuID).Relation("Options").Order("sort_order ASC").Scan(ctx)
	return list, err
}
func (r *repository) CreateToppingOption(ctx context.Context, o *MenuToppingOption) error {
	_, err := r.db.NewInsert().Model(o).Exec(ctx)
	return err
}
func (r *repository) GetToppingOptionByID(ctx context.Context, id uuid.UUID) (*MenuToppingOption, error) {
	o := new(MenuToppingOption)
	err := r.db.NewSelect().Model(o).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return o, nil
}
func (r *repository) UpdateToppingOption(ctx context.Context, o *MenuToppingOption) error {
	_, err := r.db.NewUpdate().Model(o).WherePK().Column("label", "price_delta", "image_url", "is_available", "sort_order", "updated_at").Exec(ctx)
	return err
}
func (r *repository) DeleteToppingOption(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*MenuToppingOption)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
func (r *repository) ListToppingOptionsByGroupID(ctx context.Context, groupID uuid.UUID) ([]MenuToppingOption, error) {
	var list []MenuToppingOption
	err := r.db.NewSelect().Model(&list).Where("group_id = ?", groupID).Order("sort_order ASC").Scan(ctx)
	return list, err
}

func (r *repository) ListAllImagesByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]string, error) {
	var urls []string
	// menus
	var menus []Menu
	_ = r.db.NewSelect().Model(&menus).Where("merchant_id = ?", merchantID).Scan(ctx)
	for _, m := range menus {
		if m.ImageURL != "" {
			urls = append(urls, m.ImageURL)
		}
	}
	// categories
	var cats []MenuCategory
	_ = r.db.NewSelect().Model(&cats).Where("merchant_id = ?", merchantID).Scan(ctx)
	for _, c := range cats {
		if c.ImageURL != "" {
			urls = append(urls, c.ImageURL)
		}
	}
	// variant options
	var vGroups []MenuVariantGroup
	_ = r.db.NewSelect().Model(&vGroups).Where("menu_id IN (SELECT id FROM menus WHERE merchant_id = ?)", merchantID).Scan(ctx)
	for _, vg := range vGroups {
		var vOpts []MenuVariantOption
		_ = r.db.NewSelect().Model(&vOpts).Where("group_id = ?", vg.ID).Scan(ctx)
		for _, vo := range vOpts {
			if vo.ImageURL != "" {
				urls = append(urls, vo.ImageURL)
			}
		}
	}
	// topping options
	var tGroups []MenuToppingGroup
	_ = r.db.NewSelect().Model(&tGroups).Where("menu_id IN (SELECT id FROM menus WHERE merchant_id = ?)", merchantID).Scan(ctx)
	for _, tg := range tGroups {
		var tOpts []MenuToppingOption
		_ = r.db.NewSelect().Model(&tOpts).Where("group_id = ?", tg.ID).Scan(ctx)
		for _, to := range tOpts {
			if to.ImageURL != "" {
				urls = append(urls, to.ImageURL)
			}
		}
	}
	// addon masters (Tambahan)
	var addons []AddonMaster
	_ = r.db.NewSelect().Model(&addons).Where("merchant_id = ?", merchantID).Relation("Options").Scan(ctx)
	for _, am := range addons {
		if am.ImageURL != "" {
			urls = append(urls, am.ImageURL)
		}
		for _, ao := range am.Options {
			if ao.ImageURL != "" {
				urls = append(urls, ao.ImageURL)
			}
		}
	}
	// merchant logo/cover
	var merch Merchant
	if err := r.db.NewSelect().Model(&merch).Where("id = ?", merchantID).Scan(ctx); err == nil {
		if merch.ImageURL != "" {
			urls = append(urls, merch.ImageURL)
		}
		if merch.CoverURL != "" {
			urls = append(urls, merch.CoverURL)
		}
	}
	return urls, nil
}

func (r *repository) ListImagesByMenuID(ctx context.Context, menuID uuid.UUID) ([]string, error) {
	var urls []string
	var m Menu
	if err := r.db.NewSelect().Model(&m).Where("id = ?", menuID).Scan(ctx); err == nil {
		if m.ImageURL != "" {
			urls = append(urls, m.ImageURL)
		}
	}
	// variants
	var vGroups []MenuVariantGroup
	_ = r.db.NewSelect().Model(&vGroups).Where("menu_id = ?", menuID).Scan(ctx)
	for _, vg := range vGroups {
		var vOpts []MenuVariantOption
		_ = r.db.NewSelect().Model(&vOpts).Where("group_id = ?", vg.ID).Scan(ctx)
		for _, vo := range vOpts {
			if vo.ImageURL != "" {
				urls = append(urls, vo.ImageURL)
			}
		}
	}
	var tGroups []MenuToppingGroup
	_ = r.db.NewSelect().Model(&tGroups).Where("menu_id = ?", menuID).Scan(ctx)
	for _, tg := range tGroups {
		var tOpts []MenuToppingOption
		_ = r.db.NewSelect().Model(&tOpts).Where("group_id = ?", tg.ID).Scan(ctx)
		for _, to := range tOpts {
			if to.ImageURL != "" {
				urls = append(urls, to.ImageURL)
			}
		}
	}
	return urls, nil
}

// Addon Masters (Tambahan)
func (r *repository) CreateAddonMaster(ctx context.Context, m *AddonMaster) error {
	_, err := r.db.NewInsert().Model(m).Exec(ctx)
	return err
}
func (r *repository) UpdateAddonMaster(ctx context.Context, m *AddonMaster) error {
	_, err := r.db.NewUpdate().Model(m).WherePK().Exec(ctx)
	return err
}
func (r *repository) DeleteAddonMaster(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*AddonMaster)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
func (r *repository) GetAddonMasterByID(ctx context.Context, id uuid.UUID) (*AddonMaster, error) {
	m := new(AddonMaster)
	err := r.db.NewSelect().Model(m).Where("id = ?", id).Relation("Options").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return m, nil
}
func (r *repository) ListAddonMastersByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]AddonMaster, error) {
	var list []AddonMaster
	err := r.db.NewSelect().Model(&list).Where("merchant_id = ?", merchantID).Relation("Options").OrderExpr("sort_order ASC, name ASC").Scan(ctx)
	return list, err
}
func (r *repository) CreateAddonOption(ctx context.Context, o *AddonOption) error {
	_, err := r.db.NewInsert().Model(o).Exec(ctx)
	return err
}
func (r *repository) GetAddonOptionByID(ctx context.Context, id uuid.UUID) (*AddonOption, error) {
	o := new(AddonOption)
	err := r.db.NewSelect().Model(o).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return o, nil
}
func (r *repository) UpdateAddonOption(ctx context.Context, o *AddonOption) error {
	_, err := r.db.NewUpdate().Model(o).WherePK().Column("label", "price_delta", "image_url", "is_available", "sort_order", "updated_at").Exec(ctx)
	return err
}
func (r *repository) DeleteAddonOption(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*AddonOption)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
func (r *repository) ListAddonOptionsByMasterID(ctx context.Context, masterID uuid.UUID) ([]AddonOption, error) {
	var list []AddonOption
	err := r.db.NewSelect().Model(&list).Where("master_id = ?", masterID).Order("sort_order ASC").Scan(ctx)
	return list, err
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

func (r *repository) CreateSurvey(ctx context.Context, s *MerchantSurvey) error {
	_, err := r.db.NewInsert().Model(s).Exec(ctx)
	return err
}
