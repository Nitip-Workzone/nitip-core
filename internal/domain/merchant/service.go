package merchant

import (
	"context"
	"io"
	"time"

	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/google/uuid"
)

type Service interface {
	// Merchant
	CreateMerchant(ctx context.Context, ownerID uuid.UUID, name, description, address string, lat, lng float64, category string, autoConfirm bool, maxActiveOrders int) (*Merchant, error)
	UpdateMerchant(ctx context.Context, id uuid.UUID, name, description, address string, lat, lng float64, category string, maxActiveOrders int) (*Merchant, error)
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
	GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error)
	ListMenusByMerchantID(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error
	ToggleMenuAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) (*Menu, error)
	UploadMenuImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error)

	// OrderItem
	CreateOrderItems(ctx context.Context, items []OrderItem) error
	ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error)
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

func (s *service) GetMerchantByID(ctx context.Context, id uuid.UUID) (*Merchant, error) {
	return s.repo.GetMerchantByID(ctx, id)
}

func (s *service) GetMerchantByOwnerID(ctx context.Context, ownerID uuid.UUID) (*Merchant, error) {
	return s.repo.GetMerchantByOwnerID(ctx, ownerID)
}

func (s *service) ListNearbyMerchants(ctx context.Context, lat, lng float64, radiusKm float64) ([]Merchant, error) {
	return s.repo.ListNearbyMerchants(ctx, lat, lng, radiusKm)
}

func (s *service) ListAllMerchants(ctx context.Context) ([]Merchant, error) {
	return s.repo.ListAllMerchants(ctx)
}

func (s *service) DeleteMerchant(ctx context.Context, id uuid.UUID) error {
	m, err := s.repo.GetMerchantByID(ctx, id)
	if err != nil {
		return err
	}

	// Demote user role back to requester
	u, err := s.userRepo.FindByID(ctx, m.OwnerID)
	if err == nil {
		u.Role = user.RoleRequester
		_ = s.userRepo.Update(ctx, u)
	}

	return s.repo.DeleteMerchant(ctx, id)
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
		ImageURL:    imageURL,
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

	menu.Name = name
	menu.Description = description
	menu.Price = price
	menu.ImageURL = imageURL
	menu.IsAvailable = isAvailable
	menu.UpdatedAt = time.Now()

	if err := s.repo.UpdateMenu(ctx, menu); err != nil {
		return nil, err
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

func (s *service) DeleteMenu(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteMenu(ctx, id)
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

func (s *service) UploadMenuImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error) {
	objectKey := "menus/" + uuid.New().String() + "_" + filename
	return s.storage.Upload(ctx, objectKey, content, size, contentType)
}

// OrderItem Implementation

func (s *service) CreateOrderItems(ctx context.Context, items []OrderItem) error {
	return s.repo.CreateOrderItems(ctx, items)
}

func (s *service) ListOrderItemsByOrderID(ctx context.Context, orderID uuid.UUID) ([]OrderItem, error) {
	return s.repo.ListOrderItemsByOrderID(ctx, orderID)
}
