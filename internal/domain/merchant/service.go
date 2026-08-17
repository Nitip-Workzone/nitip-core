package merchant

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/fileutil"
	"github.com/google/uuid"
)

func compressWithFileUtil(r io.Reader) (io.Reader, error) {
	return fileutil.CompressAndResizeImage(r, 1200, 75)
}

func isJpegFilename(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg")
}

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
	GetMenuByID(ctx context.Context, id uuid.UUID) (*Menu, error)
	ListMenusByMerchantID(ctx context.Context, merchantID uuid.UUID, onlyAvailable bool) ([]Menu, error)
	DeleteMenu(ctx context.Context, id uuid.UUID) error
	ToggleMenuAvailability(ctx context.Context, id uuid.UUID, isAvailable bool) (*Menu, error)
	UploadMenuImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error)

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

	menu.Name = name
	menu.Description = description
	menu.Price = price
	menu.ImageURL = sanitizeStorageKey(imageURL)
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
	// Compress before upload - fixes slow loading merchant banner/profile (8MB -> 300KB)
	// Same pattern as KYC & onboarding fix to prevent timeout on some devices
	uploadReader := content
	uploadSize := size
	if compressed, err := s.compressImageForMerchant(content); err == nil {
		if buf, ok := compressed.(interface{ Len() int }); ok {
			// *bytes.Buffer
			if b, ok2 := compressed.(interface{ Bytes() []byte }); ok2 {
				_ = b
			}
			uploadReader = compressed
			uploadSize = int64(buf.Len())
		}
	}
	objectKey := "menus/" + uuid.New().String() + "_" + filename
	// Ensure jpeg ext
	if !isJpegFilename(filename) {
		objectKey = objectKey + ".jpg"
	}
	return s.storage.Upload(ctx, objectKey, uploadReader, uploadSize, "image/jpeg")
}

func (s *service) compressImageForMerchant(r io.Reader) (io.Reader, error) {
	// Reuse hardened compress logic 1200px JPEG 75% - runs before storage upload, not inside DB Tx
	return compressWithFileUtil(r)
}

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
