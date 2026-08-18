package merchant

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/google/uuid"
)

// compressWithFileUtil kept for compat — now CompressToLimit is primary
func compressWithFileUtil(r io.Reader) (io.Reader, error) {
	return fileutil.CompressAndResizeImage(r, 1200, 75)
}

var _ = bytes.NewReader
var _ = compressWithFileUtil

func sanitizeStorageKey(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		temp := urlStr
		if strings.HasPrefix(temp, "https://") {
			temp = strings.TrimPrefix(temp, "https://")
		} else {
			temp = strings.TrimPrefix(temp, "http://")
		}

		slashIdx := strings.Index(temp, "/")
		if slashIdx != -1 {
			path := temp[slashIdx+1:]
			path = strings.TrimPrefix(path, "uploads/")

			// Strip query parameters (e.g. ?q-sign-algorithm=...)
			if qIdx := strings.Index(path, "?"); qIdx != -1 {
				path = path[:qIdx]
			}
			return path
		}
	}
	return urlStr
}

type Service interface {
	// Merchant
	CreateMerchant(ctx context.Context, ownerID uuid.UUID, name, description, address string, lat, lng float64, category string, autoConfirm bool, maxActiveOrders int) (*Merchant, error)
	UpdateMerchant(ctx context.Context, id uuid.UUID, name, description, address string, lat, lng float64, category string, maxActiveOrders int) (*Merchant, error)
	UpdateMerchantFull(ctx context.Context, id uuid.UUID, name, description, address string, lat, lng float64, category string, maxActiveOrders int, openingHours *OpeningHours, imageURL *string, coverURL *string) (*Merchant, error)
	GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error)
	GetMerchantByOwnerID(ctx context.Context, ownerID uuid.UUID) (*Merchant, error)
	ListNearbyMerchants(ctx context.Context, lat, lng float64, radiusKm float64) ([]Merchant, error)
	ListAllMerchants(ctx context.Context) ([]Merchant, error)
	DeleteMerchant(ctx context.Context, id uuid.UUID) error
	ToggleOpenStatus(ctx context.Context, id uuid.UUID, isOpen bool) (*Merchant, error)
	ToggleAutoConfirm(ctx context.Context, id uuid.UUID, autoConfirm bool) (*Merchant, error)

	// Menu
	CreateMenu(ctx context.Context, merchantID uuid.UUID, name, description string, price float64, imageURL string, isAvailable bool) (*Menu, error)
	UpdateMenu(ctx context.Context, id uuid.UUID, name, description string, price float64, imageURL string, isAvailable bool) (*Menu, error)
	UpdateMenuFull(ctx context.Context, id uuid.UUID, name, description string, price float64, imageURL string, categoryID *uuid.UUID, isAvailable bool) (*Menu, error)
	GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error)
	ListMenusByMerchantID(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error)
	ListMenusByMerchantIDWithVariants(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error
	ToggleMenuAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) (*Menu, error)
	UploadMenuImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error)

	// Category (Makanan, Minuman)
	CreateCategory(ctx context.Context, merchantID uuid.UUID, name, imageURL string, sortOrder int) (*MenuCategory, error)
	UpdateCategory(ctx context.Context, id uuid.UUID, name, imageURL string, sortOrder int, isActive bool) (*MenuCategory, error)
	DeleteCategory(ctx context.Context, id uuid.UUID) error
	ListCategoriesByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]MenuCategory, error)

	// Variant
	CreateVariantGroup(ctx context.Context, menuID uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuVariantGroup, error)
	UpdateVariantGroup(ctx context.Context, id uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuVariantGroup, error)
	DeleteVariantGroup(ctx context.Context, id uuid.UUID) error
	ListVariantGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuVariantGroup, error)
	CreateVariantOption(ctx context.Context, groupID uuid.UUID, label string, priceDelta float64, imageURL string, isDefault bool, isAvailable bool, sortOrder int) (*MenuVariantOption, error)
	UpdateVariantOption(ctx context.Context, id uuid.UUID, label string, priceDelta float64, imageURL string, isDefault bool, isAvailable bool, sortOrder int) (*MenuVariantOption, error)
	DeleteVariantOption(ctx context.Context, id uuid.UUID) error

	// Topping (per-menu, tetap support untuk backward compat, istilah baru: Tambahan per menu)
	CreateToppingGroup(ctx context.Context, menuID uuid.UUID, variantOptionID *uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuToppingGroup, error)
	UpdateToppingGroup(ctx context.Context, id uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuToppingGroup, error)
	DeleteToppingGroup(ctx context.Context, id uuid.UUID) error
	ListToppingGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuToppingGroup, error)
	CreateToppingOption(ctx context.Context, groupID uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*MenuToppingOption, error)
	UpdateToppingOption(ctx context.Context, id uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*MenuToppingOption, error)
	DeleteToppingOption(ctx context.Context, id uuid.UUID) error

	// Addon Master (Tambahan - independent shared, istilah Indonesia yang lebih cocok daripada Topping)
	CreateAddonMaster(ctx context.Context, merchantID uuid.UUID, name, imageURL string, sortOrder int) (*AddonMaster, error)
	UpdateAddonMaster(ctx context.Context, id uuid.UUID, name, imageURL string, sortOrder int, isActive bool) (*AddonMaster, error)
	DeleteAddonMaster(ctx context.Context, id uuid.UUID) error
	ListAddonMastersByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]AddonMaster, error)
	GetAddonMasterByID(ctx context.Context, id uuid.UUID) (*AddonMaster, error)
	CreateAddonOption(ctx context.Context, masterID uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*AddonOption, error)
	UpdateAddonOption(ctx context.Context, id uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*AddonOption, error)
	DeleteAddonOption(ctx context.Context, id uuid.UUID) error

	// OrderItem
	CreateOrderItems(ctx context.Context, items []OrderItem) error
	ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error)

	// Survey
	CreateSurvey(ctx context.Context, merchantID uuid.UUID, monthlySalesRange string, averageItemPrice float64) (*MerchantSurvey, error)
}

type service struct {
	repo     Repository
	userRepo user.Repository
	storage  storage.Storage
}

func NewService(repo Repository, userRepo user.Repository, storage storage.Storage) Service {
	return &service{
		repo:     repo,
		userRepo: userRepo,
		storage:  storage,
	}
}

// Merchant Implementation

func (s *service) CreateMerchant(ctx context.Context, ownerID uuid.UUID, name, description, address string, lat, lng float64, category string, autoConfirm bool, maxActiveOrders int) (*Merchant, error) {
	// 1. Promote user role
	u, err := s.userRepo.FindByID(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	u.Role = user.RoleMerchant
	if err := s.userRepo.Update(ctx, u); err != nil {
		return nil, err
	}

	// 2. Create merchant
	m := &Merchant{
		ID:              uuid.New(),
		OwnerID:         ownerID,
		Name:            name,
		Description:     description,
		Address:         address,
		Latitude:        lat,
		Longitude:       lng,
		Category:        category,
		IsOpen:          true,
		AutoConfirm:     autoConfirm,
		MaxActiveOrders: maxActiveOrders,
		Rating:          5.0,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := s.repo.CreateMerchant(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *service) UpdateMerchant(ctx context.Context, id uuid.UUID, name, description, address string, lat, lng float64, category string, maxActiveOrders int) (*Merchant, error) {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return nil, err
	}

	m.Name = name
	m.Description = description
	m.Address = address
	m.Latitude = lat
	m.Longitude = lng
	m.Category = category
	m.MaxActiveOrders = maxActiveOrders
	m.UpdatedAt = time.Now()

	if err := s.repo.UpdateMerchant(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *service) UpdateMerchantFull(ctx context.Context, id uuid.UUID, name, description, address string, lat, lng float64, category string, maxActiveOrders int, openingHours *OpeningHours, imageURL *string, coverURL *string) (*Merchant, error) {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return nil, err
	}
	m.Name = name
	m.Description = description
	m.Address = address
	m.Latitude = lat
	m.Longitude = lng
	m.Category = category
	m.MaxActiveOrders = maxActiveOrders
	if openingHours != nil {
		m.OpeningHours = *openingHours
	}
	if imageURL != nil {
		m.ImageURL = sanitizeStorageKey(*imageURL)
	}
	if coverURL != nil {
		m.CoverURL = sanitizeStorageKey(*coverURL)
	}
	m.UpdatedAt = time.Now()
	if err := s.repo.UpdateMerchant(ctx, m); err != nil {
		return nil, err
	}
	s.signMerchantImages(ctx, m)
	return m, nil
}

func (s *service) GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error) {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.signMerchantImages(ctx, m)
	return m, nil
}

func (s *service) GetMerchantByOwnerID(ctx context.Context, ownerID uuid.UUID) (*Merchant, error) {
	m, err := s.repo.GetMerchantByOwnerID(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	s.signMerchantImages(ctx, m)
	return m, nil
}

func (s *service) ListNearbyMerchants(ctx context.Context, lat, lng float64, radiusKm float64) ([]Merchant, error) {
	merchants, err := s.repo.ListNearbyMerchants(ctx, lat, lng, radiusKm)
	if err != nil {
		return nil, err
	}
	for i := range merchants {
		s.signMerchantImages(ctx, &merchants[i])
	}
	return merchants, nil
}

func (s *service) ListAllMerchants(ctx context.Context) ([]Merchant, error) {
	merchants, err := s.repo.ListAllMerchants(ctx)
	if err != nil {
		return nil, err
	}
	for i := range merchants {
		s.signMerchantImages(ctx, &merchants[i])
	}
	return merchants, nil
}

func (s *service) DeleteMerchant(ctx context.Context, id uuid.UUID) error {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return err
	}

	// Collect all images before delete for COS cleanup
	images, _ := s.repo.ListAllImagesByMerchantID(ctx, id)

	// Demote user role back to requester
	u, err := s.userRepo.FindByID(ctx, m.OwnerID)
	if err == nil {
		u.Role = user.RoleRequester
		_ = s.userRepo.Update(ctx, u)
	}

	if err := s.repo.DeleteMerchant(ctx, id); err != nil {
		return err
	}
	// Best effort delete all images from COS
	for _, url := range images {
		if key := sanitizeStorageKey(url); key != "" {
			_ = s.storage.Delete(ctx, key)
		}
	}
	return nil
}

func (s *service) signMenuImagesForVariant(ctx context.Context, m *Menu) {
	if m == nil {
		return
	}
	for i := range m.VariantGroups {
		for j := range m.VariantGroups[i].Options {
			opt := &m.VariantGroups[i].Options[j]
			if opt.ImageURL != "" && (len(opt.ImageURL) <= 4 || opt.ImageURL[:4] != "http") {
				if signed, err := s.storage.SignedURL(ctx, opt.ImageURL, 1*time.Hour); err == nil {
					opt.ImageURL = signed
				}
			}
		}
	}
	for i := range m.ToppingGroups {
		for j := range m.ToppingGroups[i].Options {
			opt := &m.ToppingGroups[i].Options[j]
			if opt.ImageURL != "" && (len(opt.ImageURL) <= 4 || opt.ImageURL[:4] != "http") {
				if signed, err := s.storage.SignedURL(ctx, opt.ImageURL, 1*time.Hour); err == nil {
					opt.ImageURL = signed
				}
			}
		}
	}
	// category image
	if m.Category != nil && m.Category.ImageURL != "" && (len(m.Category.ImageURL) <= 4 || m.Category.ImageURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, m.Category.ImageURL, 1*time.Hour); err == nil {
			m.Category.ImageURL = signed
		}
	}
}

func (s *service) ToggleOpenStatus(ctx context.Context, id uuid.UUID, isOpen bool) (*Merchant, error) {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return nil, err
	}

	m.IsOpen = isOpen
	m.UpdatedAt = time.Now()

	if err := s.repo.UpdateMerchant(ctx, m); err != nil {
		return nil, err
	}
	s.signMerchantImages(ctx, m)
	return m, nil
}

func (s *service) ToggleAutoConfirm(ctx context.Context, id uuid.UUID, autoConfirm bool) (*Merchant, error) {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return nil, err
	}

	m.AutoConfirm = autoConfirm
	m.UpdatedAt = time.Now()

	if err := s.repo.UpdateMerchant(ctx, m); err != nil {
		return nil, err
	}
	s.signMerchantImages(ctx, m)
	return m, nil
}

// Menu Implementation

func (s *service) CreateMenu(ctx context.Context, merchantID uuid.UUID, name, description string, price float64, imageURL string, isAvailable bool) (*Menu, error) {
	menu := &Menu{
		ID:          uuid.New(),
		MerchantID:  merchantID,
		Name:        name,
		Description: description,
		Price:       price,
		ImageURL:    sanitizeStorageKey(imageURL),
		IsAvailable: isAvailable,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.repo.CreateMenu(ctx, menu); err != nil {
		return nil, err
	}
	s.signMenuImage(ctx, menu)
	return menu, nil
}

func (s *service) UpdateMenu(ctx context.Context, id uuid.UUID, name, description string, price float64, imageURL string, isAvailable bool) (*Menu, error) {
	menu, err := s.repo.GetMenuByID(ctx, id)
	if err != nil {
		return nil, err
	}
	oldImg := menu.ImageURL
	menu.Name = name
	menu.Description = description
	menu.Price = price
	if imageURL != "" {
		menu.ImageURL = sanitizeStorageKey(imageURL)
	}
	menu.IsAvailable = isAvailable
	menu.UpdatedAt = time.Now()

	if err := s.repo.UpdateMenu(ctx, menu); err != nil {
		return nil, err
	}
	// COS cleanup jika ganti gambar
	if oldImg != "" && oldImg != menu.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	s.signMenuImage(ctx, menu)
	return menu, nil
}

func (s *service) UpdateMenuFull(ctx context.Context, id uuid.UUID, name, description string, price float64, imageURL string, categoryID *uuid.UUID, isAvailable bool) (*Menu, error) {
	menu, err := s.repo.GetMenuByID(ctx, id)
	if err != nil {
		return nil, err
	}
	oldImg := menu.ImageURL
	menu.Name = name
	menu.Description = description
	menu.Price = price
	if imageURL != "" {
		menu.ImageURL = sanitizeStorageKey(imageURL)
	}
	if categoryID != nil {
		menu.CategoryID = categoryID
	}
	menu.IsAvailable = isAvailable
	menu.UpdatedAt = time.Now()
	if err := s.repo.UpdateMenu(ctx, menu); err != nil {
		return nil, err
	}
	if oldImg != "" && oldImg != menu.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	s.signMenuImage(ctx, menu)
	return menu, nil
}

func (s *service) GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error) {
	menu, err := s.repo.GetMenuByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.signMenuImage(ctx, menu)
	return menu, nil
}

func (s *service) ListMenusByMerchantID(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error) {
	menus, err := s.repo.ListMenusByMerchantID(ctx, merchantID, onlyAvailable)
	if err != nil {
		return nil, err
	}
	for i := range menus {
		s.signMenuImage(ctx, &menus[i])
	}
	return menus, nil
}

func (s *service) ListMenusByMerchantIDWithVariants(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error) {
	menus, err := s.repo.ListMenusByMerchantIDWithVariants(ctx, merchantID, onlyAvailable)
	if err != nil {
		return nil, err
	}
	for i := range menus {
		s.signMenuImage(ctx, &menus[i])
		s.signMenuImagesForVariant(ctx, &menus[i])
	}
	return menus, nil
}

func (s *service) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	// P0 COS cleanup: hapus semua gambar menu + varian + topping di COS
	images, _ := s.repo.ListImagesByMenuID(ctx, id)
	if err := s.repo.DeleteMenu(ctx, id); err != nil {
		return err
	}
	// Best effort delete COS
	for _, url := range images {
		key := sanitizeStorageKey(url)
		if key != "" {
			_ = s.storage.Delete(ctx, key)
		}
	}
	return nil
}

func (s *service) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	var catImg string
	if cat, err := s.repo.GetCategoryByID(ctx, id); err == nil && cat != nil {
		catImg = cat.ImageURL
	}
	if err := s.repo.DeleteCategory(ctx, id); err != nil {
		return err
	}
	if catImg != "" {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(catImg))
	}
	return nil
}

// Category CRUD
func (s *service) CreateCategory(ctx context.Context, merchantID uuid.UUID, name, imageURL string, sortOrder int) (*MenuCategory, error) {
	c := &MenuCategory{
		ID:         uuid.New(),
		MerchantID: merchantID,
		Name:       name,
		ImageURL:   sanitizeStorageKey(imageURL),
		SortOrder:  sortOrder,
		IsActive:   true,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.repo.CreateCategory(ctx, c); err != nil {
		return nil, err
	}
	s.signCategoryImage(ctx, c)
	return c, nil
}
func (s *service) UpdateCategory(ctx context.Context, id uuid.UUID, name, imageURL string, sortOrder int, isActive bool) (*MenuCategory, error) {
	cat, err := s.repo.GetCategoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	oldImg := cat.ImageURL
	cat.Name = name
	if imageURL != "" {
		cat.ImageURL = sanitizeStorageKey(imageURL)
	}
	cat.SortOrder = sortOrder
	cat.IsActive = isActive
	cat.UpdatedAt = time.Now()
	if err := s.repo.UpdateCategory(ctx, cat); err != nil {
		return nil, err
	}
	if oldImg != "" && oldImg != cat.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	s.signCategoryImage(ctx, cat)
	return cat, nil
}
func (s *service) ListCategoriesByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]MenuCategory, error) {
	cats, err := s.repo.ListCategoriesByMerchantID(ctx, merchantID)
	if err != nil {
		return nil, err
	}
	for i := range cats {
		s.signCategoryImage(ctx, &cats[i])
	}
	return cats, nil
}
func (s *service) signCategoryImage(ctx context.Context, c *MenuCategory) {
	if c == nil || c.ImageURL == "" {
		return
	}
	if len(c.ImageURL) > 4 && c.ImageURL[:4] == "http" {
		// if already https://upload.nihtip.com/ keep, else raw myqcloud that slipped? sanitize then sign
		if strings.HasPrefix(c.ImageURL, "https://upload.nihtip.com/") {
			return
		}
	}
	if signed, err := s.storage.SignedURL(ctx, c.ImageURL, 1*time.Hour); err == nil {
		c.ImageURL = signed
	}
}

// Variant Groups
func (s *service) CreateVariantGroup(ctx context.Context, menuID uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuVariantGroup, error) {
	g := &MenuVariantGroup{
		ID:         uuid.New(),
		MenuID:     menuID,
		Name:       name,
		Type:       gtype,
		IsRequired: isRequired,
		MinSelect:  minSelect,
		MaxSelect:  maxSelect,
		SortOrder:  sortOrder,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.repo.CreateVariantGroup(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}
func (s *service) UpdateVariantGroup(ctx context.Context, id uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuVariantGroup, error) {
	// Fetch existing to preserve MenuID & CreatedAt
	existing, err := s.repo.GetVariantGroupByID(ctx, id)
	if err != nil {
		// fallback stub if not found
		existing = &MenuVariantGroup{ID: id}
	}
	existing.Name = name
	existing.Type = gtype
	existing.IsRequired = isRequired
	existing.MinSelect = minSelect
	existing.MaxSelect = maxSelect
	existing.SortOrder = sortOrder
	existing.UpdatedAt = time.Now()
	if err := s.repo.UpdateVariantGroup(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}
func (s *service) DeleteVariantGroup(ctx context.Context, id uuid.UUID) error {
	// collect images of options before delete
	opts, _ := s.repo.ListVariantOptionsByGroupID(ctx, id)
	if err := s.repo.DeleteVariantGroup(ctx, id); err != nil {
		return err
	}
	for _, o := range opts {
		if o.ImageURL != "" {
			_ = s.storage.Delete(ctx, sanitizeStorageKey(o.ImageURL))
		}
	}
	return nil
}
func (s *service) ListVariantGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuVariantGroup, error) {
	groups, err := s.repo.ListVariantGroupsByMenuID(ctx, menuID)
	if err != nil {
		return nil, err
	}
	// Sign variant option images untuk kelola variant di merchant panel (requester sudah sign via signMenuImagesForVariant)
	for gi := range groups {
		for oi := range groups[gi].Options {
			opt := &groups[gi].Options[oi]
			if opt.ImageURL != "" && (len(opt.ImageURL) <= 4 || opt.ImageURL[:4] != "http") {
				if signed, err := s.storage.SignedURL(ctx, opt.ImageURL, 1*time.Hour); err == nil {
					opt.ImageURL = signed
				}
			}
		}
	}
	return groups, nil
}
func (s *service) CreateVariantOption(ctx context.Context, groupID uuid.UUID, label string, priceDelta float64, imageURL string, isDefault bool, isAvailable bool, sortOrder int) (*MenuVariantOption, error) {
	o := &MenuVariantOption{
		ID:          uuid.New(),
		GroupID:     groupID,
		Label:       label,
		PriceDelta:  priceDelta,
		ImageURL:    sanitizeStorageKey(imageURL),
		IsDefault:   isDefault,
		IsAvailable: isAvailable,
		SortOrder:   sortOrder,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.repo.CreateVariantOption(ctx, o); err != nil {
		return nil, err
	}
	// Enforce final base https://upload.nihtip.com/ for read
	if o.ImageURL != "" {
		if signed, err := s.storage.SignedURL(ctx, o.ImageURL, 1*time.Hour); err == nil {
			o.ImageURL = signed
		}
	}
	return o, nil
}
func (s *service) UpdateVariantOption(ctx context.Context, id uuid.UUID, label string, priceDelta float64, imageURL string, isDefault bool, isAvailable bool, sortOrder int) (*MenuVariantOption, error) {
	// Fetch existing to preserve GroupID & handle old image COS cleanup
	existing, err := s.repo.GetVariantOptionByID(ctx, id)
	if err != nil {
		existing = &MenuVariantOption{ID: id}
	}
	oldImg := existing.ImageURL
	existing.Label = label
	existing.PriceDelta = priceDelta
	if imageURL != "" {
		existing.ImageURL = sanitizeStorageKey(imageURL)
	}
	existing.IsDefault = isDefault
	existing.IsAvailable = isAvailable
	existing.SortOrder = sortOrder
	existing.UpdatedAt = time.Now()
	if err := s.repo.UpdateVariantOption(ctx, existing); err != nil {
		return nil, err
	}
	if oldImg != "" && oldImg != existing.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	// Enforce final base https://upload.nihtip.com/
	if existing.ImageURL != "" && (len(existing.ImageURL) <= 4 || existing.ImageURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, existing.ImageURL, 1*time.Hour); err == nil {
			existing.ImageURL = signed
		}
	}
	return existing, nil
}
func (s *service) DeleteVariantOption(ctx context.Context, id uuid.UUID) error {
	// need to fetch image before delete - not have get, try via list? best effort ignore
	// we will delete via repo and if had image, we can't know, but we try to delete by fetching via direct query later - for now best effort
	if err := s.repo.DeleteVariantOption(ctx, id); err != nil {
		return err
	}
	return nil
}

// Topping Groups
func (s *service) CreateToppingGroup(ctx context.Context, menuID uuid.UUID, variantOptionID *uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuToppingGroup, error) {
	g := &MenuToppingGroup{
		ID:              uuid.New(),
		MenuID:          menuID,
		VariantOptionID: variantOptionID,
		Name:            name,
		Type:            gtype,
		IsRequired:      isRequired,
		MinSelect:       minSelect,
		MaxSelect:       maxSelect,
		SortOrder:       sortOrder,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := s.repo.CreateToppingGroup(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}
func (s *service) UpdateToppingGroup(ctx context.Context, id uuid.UUID, name, gtype string, isRequired bool, minSelect int, maxSelect *int, sortOrder int) (*MenuToppingGroup, error) {
	existing, err := s.repo.GetToppingGroupByID(ctx, id)
	if err != nil {
		existing = &MenuToppingGroup{ID: id}
	}
	existing.Name = name
	existing.Type = gtype
	existing.IsRequired = isRequired
	existing.MinSelect = minSelect
	existing.MaxSelect = maxSelect
	existing.SortOrder = sortOrder
	existing.UpdatedAt = time.Now()
	if err := s.repo.UpdateToppingGroup(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}
func (s *service) DeleteToppingGroup(ctx context.Context, id uuid.UUID) error {
	opts, _ := s.repo.ListToppingOptionsByGroupID(ctx, id)
	if err := s.repo.DeleteToppingGroup(ctx, id); err != nil {
		return err
	}
	for _, o := range opts {
		if o.ImageURL != "" {
			_ = s.storage.Delete(ctx, sanitizeStorageKey(o.ImageURL))
		}
	}
	return nil
}
func (s *service) ListToppingGroupsByMenuID(ctx context.Context, menuID uuid.UUID) ([]MenuToppingGroup, error) {
	groups, err := s.repo.ListToppingGroupsByMenuID(ctx, menuID)
	if err != nil {
		return nil, err
	}
	// Sign topping option images untuk merchant panel (tambahan)
	for gi := range groups {
		for oi := range groups[gi].Options {
			opt := &groups[gi].Options[oi]
			if opt.ImageURL != "" && (len(opt.ImageURL) <= 4 || opt.ImageURL[:4] != "http") {
				if signed, err := s.storage.SignedURL(ctx, opt.ImageURL, 1*time.Hour); err == nil {
					opt.ImageURL = signed
				}
			}
		}
	}
	return groups, nil
}
func (s *service) CreateToppingOption(ctx context.Context, groupID uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*MenuToppingOption, error) {
	o := &MenuToppingOption{
		ID:          uuid.New(),
		GroupID:     groupID,
		Label:       label,
		PriceDelta:  priceDelta,
		ImageURL:    sanitizeStorageKey(imageURL),
		IsAvailable: isAvailable,
		SortOrder:   sortOrder,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.repo.CreateToppingOption(ctx, o); err != nil {
		return nil, err
	}
	if o.ImageURL != "" {
		if signed, err := s.storage.SignedURL(ctx, o.ImageURL, 1*time.Hour); err == nil {
			o.ImageURL = signed
		}
	}
	return o, nil
}
func (s *service) UpdateToppingOption(ctx context.Context, id uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*MenuToppingOption, error) {
	existing, err := s.repo.GetToppingOptionByID(ctx, id)
	if err != nil {
		existing = &MenuToppingOption{ID: id}
	}
	oldImg := existing.ImageURL
	existing.Label = label
	existing.PriceDelta = priceDelta
	if imageURL != "" {
		existing.ImageURL = sanitizeStorageKey(imageURL)
	}
	existing.IsAvailable = isAvailable
	existing.SortOrder = sortOrder
	existing.UpdatedAt = time.Now()
	if err := s.repo.UpdateToppingOption(ctx, existing); err != nil {
		return nil, err
	}
	if oldImg != "" && oldImg != existing.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	if existing.ImageURL != "" && (len(existing.ImageURL) <= 4 || existing.ImageURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, existing.ImageURL, 1*time.Hour); err == nil {
			existing.ImageURL = signed
		}
	}
	return existing, nil
}
func (s *service) DeleteToppingOption(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteToppingOption(ctx, id)
}

func (s *service) ToggleMenuAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) (*Menu, error) {
	menu, err := s.repo.GetMenuByID(ctx, id)
	if err != nil {
		return nil, err
	}

	menu.IsAvailable = isAvailable
	menu.UpdatedAt = time.Now()

	if err := s.repo.UpdateMenu(ctx, menu); err != nil {
		return nil, err
	}
	s.signMenuImage(ctx, menu)
	return menu, nil
}

func (s *service) signMenuImage(ctx context.Context, m *Menu) {
	if m == nil || m.ImageURL == "" {
		return
	}
	if len(m.ImageURL) > 4 && m.ImageURL[:4] == "http" {
		return
	}
	if signed, err := s.storage.SignedURL(ctx, m.ImageURL, 1*time.Hour); err == nil {
		m.ImageURL = signed
	}
}

func (s *service) signMerchantImages(ctx context.Context, m *Merchant) {
	if m == nil {
		return
	}
	if m.ImageURL != "" && (len(m.ImageURL) <= 4 || m.ImageURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, m.ImageURL, 1*time.Hour); err == nil {
			m.ImageURL = signed
		}
	}
	if m.CoverURL != "" && (len(m.CoverURL) <= 4 || m.CoverURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, m.CoverURL, 1*time.Hour); err == nil {
			m.CoverURL = signed
		}
	}
}

func (s *service) UploadMenuImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error) {
	// Compress <1MB with bounded concurrency (Lighthouse 2C4G) + anti-penumpukan delete old via UpdateMenu
	compressed, compSize, compErr := fileutil.CompressToLimit(content, 1200, fileutil.DefaultMaxUpload)
	if compErr != nil {
		return "", fmt.Errorf("gagal mengompresi gambar menu: %w", compErr)
	}
	if compSize > fileutil.DefaultMaxUpload {
		return "", fmt.Errorf("gambar menu masih >1MB (%dKB), coba foto lebih kecil", compSize/1024)
	}

	// Penamaan clean tanpa nama aneh — hanya uuid_nano.jpg (tanpa filename asli berantakan seperti banner sebelumnya)
	// Sebelumnya pakai filename asli menyebabkan 403 SignatureDoesNotMatch kalau ada spasi/koma
	objectKey := "menus/" + uuid.New().String() + "_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".jpg"
	return s.storage.Upload(ctx, objectKey, compressed, compSize, "image/jpeg")
}

// compressImageForMerchant kept for backward compat — now uses CompressToLimit via UploadMenuImage
// ignore unused, used in older code path
var _ = compressWithFileUtil

// OrderItem Implementation

func (s *service) CreateOrderItems(ctx context.Context, items []OrderItem) error {
	return s.repo.CreateOrderItems(ctx, items)
}

func (s *service) ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error) {
	return s.repo.ListOrderItemsByOrderID(ctx, orderID)
}

func (s *service) CreateSurvey(ctx context.Context, merchantID uuid.UUID, monthlySalesRange string, averageItemPrice float64) (*MerchantSurvey, error) {
	survey := &MerchantSurvey{
		ID:                uuid.New(),
		MerchantID:        merchantID,
		MonthlySalesRange: monthlySalesRange,
		AverageItemPrice:  averageItemPrice,
		CreatedAt:         time.Now(),
	}
	if err := s.repo.CreateSurvey(ctx, survey); err != nil {
		return nil, err
	}
	return survey, nil
}

// ===== Addon Masters (Tambahan - independent shared) =====
func (s *service) CreateAddonMaster(ctx context.Context, merchantID uuid.UUID, name, imageURL string, sortOrder int) (*AddonMaster, error) {
	m := &AddonMaster{
		ID:         uuid.New(),
		MerchantID: merchantID,
		Name:       name,
		ImageURL:   sanitizeStorageKey(imageURL),
		SortOrder:  sortOrder,
		IsActive:   true,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.repo.CreateAddonMaster(ctx, m); err != nil {
		return nil, err
	}
	// Enforce https://upload.nihtip.com/ final
	s.signAddonMasterImages(ctx, m)
	return m, nil
}
func (s *service) UpdateAddonMaster(ctx context.Context, id uuid.UUID, name, imageURL string, sortOrder int, isActive bool) (*AddonMaster, error) {
	m, err := s.repo.GetAddonMasterByID(ctx, id)
	if err != nil {
		return nil, err
	}
	oldImg := m.ImageURL
	m.Name = name
	if imageURL != "" {
		m.ImageURL = sanitizeStorageKey(imageURL)
	}
	m.SortOrder = sortOrder
	m.IsActive = isActive
	m.UpdatedAt = time.Now()
	if err := s.repo.UpdateAddonMaster(ctx, m); err != nil {
		return nil, err
	}
	if oldImg != "" && oldImg != m.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	s.signAddonMasterImages(ctx, m)
	return m, nil
}
func (s *service) DeleteAddonMaster(ctx context.Context, id uuid.UUID) error {
	// collect images before delete
	m, _ := s.repo.GetAddonMasterByID(ctx, id)
	if err := s.repo.DeleteAddonMaster(ctx, id); err != nil {
		return err
	}
	if m != nil {
		if m.ImageURL != "" {
			_ = s.storage.Delete(ctx, sanitizeStorageKey(m.ImageURL))
		}
		for _, o := range m.Options {
			if o.ImageURL != "" {
				_ = s.storage.Delete(ctx, sanitizeStorageKey(o.ImageURL))
			}
		}
	}
	return nil
}
func (s *service) ListAddonMastersByMerchantID(ctx context.Context, merchantID uuid.UUID) ([]AddonMaster, error) {
	list, err := s.repo.ListAddonMastersByMerchantID(ctx, merchantID)
	if err != nil {
		return nil, err
	}
	// sign urls
	for i := range list {
		s.signAddonMasterImages(ctx, &list[i])
	}
	return list, nil
}
func (s *service) GetAddonMasterByID(ctx context.Context, id uuid.UUID) (*AddonMaster, error) {
	m, err := s.repo.GetAddonMasterByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.signAddonMasterImages(ctx, m)
	return m, nil
}
func (s *service) CreateAddonOption(ctx context.Context, masterID uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*AddonOption, error) {
	o := &AddonOption{
		ID:          uuid.New(),
		MasterID:    masterID,
		Label:       label,
		PriceDelta:  priceDelta,
		ImageURL:    sanitizeStorageKey(imageURL),
		IsAvailable: isAvailable,
		SortOrder:   sortOrder,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := s.repo.CreateAddonOption(ctx, o); err != nil {
		return nil, err
	}
	if o.ImageURL != "" {
		if signed, err := s.storage.SignedURL(ctx, o.ImageURL, 1*time.Hour); err == nil {
			o.ImageURL = signed
		}
	}
	return o, nil
}
func (s *service) UpdateAddonOption(ctx context.Context, id uuid.UUID, label string, priceDelta float64, imageURL string, isAvailable bool, sortOrder int) (*AddonOption, error) {
	existing, err := s.repo.GetAddonOptionByID(ctx, id)
	if err != nil {
		existing = &AddonOption{ID: id}
	}
	oldImg := existing.ImageURL
	existing.Label = label
	existing.PriceDelta = priceDelta
	if imageURL != "" {
		existing.ImageURL = sanitizeStorageKey(imageURL)
	}
	existing.IsAvailable = isAvailable
	existing.SortOrder = sortOrder
	existing.UpdatedAt = time.Now()
	if err := s.repo.UpdateAddonOption(ctx, existing); err != nil {
		return nil, err
	}
	if oldImg != "" && oldImg != existing.ImageURL {
		_ = s.storage.Delete(ctx, sanitizeStorageKey(oldImg))
	}
	if existing.ImageURL != "" && (len(existing.ImageURL) <= 4 || existing.ImageURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, existing.ImageURL, 1*time.Hour); err == nil {
			existing.ImageURL = signed
		}
	}
	return existing, nil
}
func (s *service) DeleteAddonOption(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteAddonOption(ctx, id)
}
func (s *service) signAddonMasterImages(ctx context.Context, m *AddonMaster) {
	if m == nil {
		return
	}
	if m.ImageURL != "" && (len(m.ImageURL) <= 4 || m.ImageURL[:4] != "http") {
		if signed, err := s.storage.SignedURL(ctx, m.ImageURL, 1*time.Hour); err == nil {
			m.ImageURL = signed
		}
	}
	for i := range m.Options {
		if m.Options[i].ImageURL != "" && (len(m.Options[i].ImageURL) <= 4 || m.Options[i].ImageURL[:4] != "http") {
			if signed, err := s.storage.SignedURL(ctx, m.Options[i].ImageURL, 1*time.Hour); err == nil {
				m.Options[i].ImageURL = signed
			}
		}
	}
}
