package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/audit"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

func (s *service) geoDistance(lat1, lon1, lat2, lon2 float64) float64 {
	// Haversine km, aligned with pkg/geolocation (avoid import cycle)
	const earth = 6371.0
	dLat := (lat2 - lat1) * (math.Pi / 180.0)
	dLon := (lon2 - lon1) * (math.Pi / 180.0)
	lat1r := lat1 * (math.Pi / 180.0)
	lat2r := lat2 * (math.Pi / 180.0)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Sin(dLon/2)*math.Sin(dLon/2)*math.Cos(lat1r)*math.Cos(lat2r)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earth * c
}

type CreateUserRequest struct {
	Name           string `json:"name"            validate:"required,min=2,max=100"`
	Email          string `json:"email"           validate:"required,email"`
	Password       string `json:"password"        validate:"required,min=8,max=72"`
	Role           string `json:"role"            validate:"omitempty,oneof=requester runner"`
	DeviceId       string `json:"device_id"       validate:"required"`
	WhatsappNumber string `json:"whatsapp_number" validate:"required,min=9,max=15,numeric"`
}

type AdminCreateUserRequest struct {
	Name           string `json:"name"            validate:"required,min=2,max=100"`
	Email          string `json:"email"           validate:"required,email"`
	Password       string `json:"password"        validate:"required,min=8,max=72"`
	Role           string `json:"role"            validate:"required,oneof=requester runner admin merchant cs"`
	WhatsappNumber string `json:"whatsapp_number" validate:"required,min=9,max=15,numeric"`
	IsVerified     bool   `json:"is_verified"`
	AdminPassword  string `json:"admin_password"  validate:"required"`
}

type LoginRequest struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required"`
	DeviceId string `json:"device_id" validate:"required"`
	TotpCode string `json:"totp_code" validate:"omitempty,len=6,numeric"`
}

type LoginResponse struct {
	RequireTotp  bool   `json:"require_totp,omitempty"`
	Token        string `json:"token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	User         *User  `json:"user,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

type UpdateHomeRequest struct {
	Lat     float64 `json:"lat"     validate:"required,latitude"`
	Lng     float64 `json:"lng"     validate:"required,longitude"`
	Address string  `json:"address" validate:"required,max=500"`
}

type UpdateProfileRequest struct {
	Name           string `json:"name"            form:"name"            validate:"required,min=2,max=100"`
	WhatsappNumber string `json:"whatsapp_number" form:"whatsapp_number" validate:"required,min=9,max=20"`
	HomeAddress    string `json:"home_address"    form:"home_address"    validate:"omitempty,max=500"`
}

type SetupPinRequest struct {
	Pin string `json:"pin" validate:"required,len=6,numeric"`
}

type VerifyPinRequest struct {
	Pin string `json:"pin" validate:"required,len=6,numeric"`
}

type ChangePinRequest struct {
	OldPin string `json:"old_pin" validate:"required,len=6,numeric"`
	NewPin string `json:"new_pin" validate:"required,len=6,numeric"`
}

type UpdateAcceptingOrdersRequest struct {
	IsAcceptingOrders bool `json:"is_accepting_orders"`
}

type Service interface {
	GetAll(ctx context.Context) ([]User, error)
	GetAllWithFilters(ctx context.Context, role string, isVerified, isSuspended *bool) ([]User, error)
	GetByID(ctx context.Context, id uuid.UUID, requestorID uuid.UUID) (*User, error)
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]User, error)
	Create(ctx context.Context, req CreateUserRequest) (*User, error)
	AdminCreate(ctx context.Context, req AdminCreateUserRequest) (*User, error)
	Login(ctx context.Context, req LoginRequest, platform string) (*LoginResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*LoginResponse, error)
	Delete(ctx context.Context, id uuid.UUID) error

	// PIN Management
	SetupPin(ctx context.Context, id uuid.UUID, req SetupPinRequest) error
	VerifyPin(ctx context.Context, id uuid.UUID, pin string) error
	ChangePin(ctx context.Context, id uuid.UUID, req ChangePinRequest) error
	UnlockPin(ctx context.Context, id uuid.UUID) error

	// TOTP Management
	SetupTOTP(ctx context.Context, id uuid.UUID) (string, string, error)
	VerifyAndEnableTOTP(ctx context.Context, id uuid.UUID, code string) error
	DisableTOTP(ctx context.Context, id uuid.UUID, code string) error
	AdminDisableTOTP(ctx context.Context, id uuid.UUID, adminID uuid.UUID) error

	// Admin specific
	UpdateVerification(ctx context.Context, id, actorID uuid.UUID, isVerified bool) error
	UpdateTrustScore(ctx context.Context, id, actorID uuid.UUID, score int) error
	UpdateSuspendStatus(ctx context.Context, id, actorID uuid.UUID, isSuspended bool, reason string) error
	UpdateLocation(ctx context.Context, id uuid.UUID, lat, lng float64) error
	UpdateHeartbeat(ctx context.Context, id uuid.UUID, req HeartbeatRequest) error
	UpdateHome(ctx context.Context, id uuid.UUID, req UpdateHomeRequest) error
	UpdateProfile(ctx context.Context, id uuid.UUID, req UpdateProfileRequest, avatarFile io.Reader, avatarFilename string) error
	UpdateAcceptingOrders(ctx context.Context, id uuid.UUID, isAccepting bool) error
	GetRedis() *cache.Redis

	// Bank Account Management
	GetBankAccount(ctx context.Context, userID uuid.UUID) (*UserBankAccount, error)
	AdminRegisterBankAccount(ctx context.Context, userID uuid.UUID, bankName, accountNo, accountName, adminPassword, totpCode string, adminID uuid.UUID) error
}

type service struct {
	repo     Repository
	redis    *cache.Redis
	auditSvc audit.Service
	storage  storage.Storage
}

func NewService(repo Repository, redis *cache.Redis, auditSvc audit.Service, storage storage.Storage) Service {
	return &service{repo: repo, redis: redis, auditSvc: auditSvc, storage: storage}
}

func (s *service) GetAll(ctx context.Context) ([]User, error) {
	users, err := s.repo.FindAll(ctx)
	if err != nil {
		return nil, err
	}
	for i := range users {
		s.signAvatar(ctx, &users[i])
	}
	return users, nil
}

func (s *service) GetAllWithFilters(ctx context.Context, role string, isVerified, isSuspended *bool) ([]User, error) {
	users, err := s.repo.FindAllWithFilters(ctx, role, isVerified, isSuspended)
	if err != nil {
		return nil, err
	}
	for i := range users {
		s.signAvatar(ctx, &users[i])
	}
	return users, nil
}

func (s *service) GetByID(ctx context.Context, id uuid.UUID, requestorID uuid.UUID) (*User, error) {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Mask data if the requestor is not the owner and not an admin
	// Note: We don't have role here, so we check if ID matches or if requestor is empty (admin might skip this)
	// Actually, let's just check ID match for now.
	if requestorID != uuid.Nil && requestorID != id {
		u.MaskSensitiveData()
	}

	u.ComputeHasPin()
	s.signAvatar(ctx, u)
	return u, nil
}

func (s *service) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]User, error) {
	users, err := s.repo.FindByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range users {
		s.signAvatar(ctx, &users[i])
	}
	return users, nil
}

func (s *service) Create(ctx context.Context, req CreateUserRequest) (*User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.New("gagal mengenkripsi kata sandi")
	}

	role := RoleRequester
	if req.Role != "" {
		role = req.Role
	}

	now := time.Now()
	user := &User{
		ID:             uuid.New(),
		Name:           req.Name,
		Email:          req.Email,
		WhatsappNumber: req.WhatsappNumber,
		Password:       string(hashedPassword),
		Role:           role,
		DeviceId:       &req.DeviceId,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}
	user.ComputeHasPin()
	return user, nil
}

func (s *service) AdminCreate(ctx context.Context, req AdminCreateUserRequest) (*User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.New("gagal mengenkripsi kata sandi")
	}

	now := time.Now()
	user := &User{
		ID:             uuid.New(),
		Name:           req.Name,
		Email:          req.Email,
		WhatsappNumber: req.WhatsappNumber,
		Password:       string(hashedPassword),
		Role:           req.Role,
		IsVerified:     req.IsVerified,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if req.IsVerified {
		user.VerifiedAt = &now
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}
	user.ComputeHasPin()
	return user, nil
}

func (s *service) Login(ctx context.Context, req LoginRequest, platform string) (*LoginResponse, error) {
	isDev := os.Getenv("APP_ENV") != "production"
	if isDev {
		log.Printf("[DEBUG] Login attempt for email: %s, platform: %s", req.Email, platform)
	}
	user, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		if isDev {
			log.Printf("[DEBUG] Login failed: User not found for email %s: %v", req.Email, err)
		}
		return nil, errors.New("email atau kata sandi salah")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		if isDev {
			log.Printf("[DEBUG] Login failed: Password mismatch for email %s: %v", req.Email, err)
		}
		return nil, errors.New("email atau kata sandi salah")
	}

	// TOTP Check
	if user.TotpEnabled {
		if req.TotpCode == "" {
			return &LoginResponse{RequireTotp: true}, nil
		}
		if user.TotpSecret == nil || !totp.Validate(req.TotpCode, *user.TotpSecret) {
			return nil, errors.New("kode TOTP tidak valid")
		}
	}

	// Platform-based role validation
	switch platform {
	case "web-admin":
		if user.Role != RoleAdmin {
			return nil, errors.New("akses ditolak: hanya administrator yang dapat masuk ke sini")
		}
		user.FcmToken = nil
	case "web-merchant":
		if user.Role != RoleMerchant && user.Role != RoleAdmin {
			return nil, errors.New("akses ditolak: hanya akun merchant atau administrator yang dapat masuk ke portal merchant")
		}
		user.FcmToken = nil
	case "web":
		// Regular web (requester portal) — allow admin but block regular merchants
		if user.Role == RoleMerchant {
			return nil, errors.New("akses ditolak: akun merchant harus menggunakan portal merchant")
		}
		user.FcmToken = nil
	case "mobile":
		if user.Role == RoleAdmin {
			return nil, errors.New("akses ditolak: administrator harus menggunakan panel web")
		}
	}

	// Unique Device Session Handling:
	// If this device ID was previously used by another account, remove it and increment their token version to log them out
	if req.DeviceId != "" {
		_ = s.repo.ClearDeviceSessions(ctx, req.DeviceId, user.ID)
	}

	// Increment TokenVersion & Update device_id
	user.TokenVersion++
	user.DeviceId = &req.DeviceId
	user.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, user); err != nil {
		return nil, errors.New("gagal memperbarui sesi")
	}

	// Update Redis cache for fast session verification
	if s.redis != nil {
		key := fmt.Sprintf("user:session:v:%s", user.ID.String())
		_ = s.redis.Set(ctx, key, user.TokenVersion, 24*time.Hour)
	}

	token, err := jwt.GenerateToken(user.ID, user.Email, user.Role, user.IsVerified, req.DeviceId, user.TokenVersion)
	if err != nil {
		return nil, errors.New("gagal membuat token akses")
	}

	refreshToken, err := jwt.GenerateRefreshToken(user.ID, req.DeviceId, user.TokenVersion)
	if err != nil {
		return nil, errors.New("gagal membuat token penyegar")
	}

	user.ComputeHasPin()
	s.signAvatar(ctx, user)
	return &LoginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         user,
	}, nil
}

func (s *service) Refresh(ctx context.Context, refreshToken string) (*LoginResponse, error) {
	claims, err := jwt.ParseToken(refreshToken)
	if err != nil {
		return nil, errors.New("token penyegar tidak valid")
	}

	user, err := s.repo.FindByID(ctx, claims.UserID)
	if err != nil {
		return nil, errors.New("user tidak ditemukan")
	}

	// Verify token version for rotation/revocation
	if user.TokenVersion != claims.TokenVersion {
		return nil, errors.New("token sudah kedaluwarsa atau tidak valid")
	}

	// Verify device id if needed (optional but recommended)
	if user.DeviceId == nil || *user.DeviceId != claims.DeviceId {
		return nil, errors.New("sesi tidak valid untuk perangkat ini")
	}

	// Refresh Token Rotation: Increment version to invalidate all previous tokens for this user
	user.TokenVersion++
	user.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, user); err != nil {
		return nil, errors.New("gagal memperbarui sesi")
	}

	// Update Redis cache for fast session verification
	if s.redis != nil {
		key := fmt.Sprintf("user:session:v:%s", user.ID.String())
		_ = s.redis.Set(ctx, key, user.TokenVersion, 24*time.Hour)
	}

	// Generate new tokens with NEW version
	accessToken, err := jwt.GenerateToken(user.ID, user.Email, user.Role, user.IsVerified, *user.DeviceId, user.TokenVersion)
	if err != nil {
		return nil, errors.New("gagal membuat token akses baru")
	}

	newRefreshToken, err := jwt.GenerateRefreshToken(user.ID, *user.DeviceId, user.TokenVersion)
	if err != nil {
		return nil, errors.New("gagal membuat token penyegar baru")
	}

	user.ComputeHasPin()
	s.signAvatar(ctx, user)
	return &LoginResponse{
		Token:        accessToken,
		RefreshToken: newRefreshToken,
		User:         user,
	}, nil
}

func (s *service) SetupPin(ctx context.Context, id uuid.UUID, req SetupPinRequest) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if u.Pin != nil && *u.Pin != "" {
		return errors.New("PIN sudah diatur, gunakan menu ubah PIN")
	}

	hashedPin, err := bcrypt.GenerateFromPassword([]byte(req.Pin), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("gagal memproses PIN")
	}

	pinStr := string(hashedPin)
	u.Pin = &pinStr
	u.UpdatedAt = time.Now()

	return s.repo.Update(ctx, u)
}

func (s *service) ChangePin(ctx context.Context, id uuid.UUID, req ChangePinRequest) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if u.Pin == nil || *u.Pin == "" {
		return errors.New("PIN belum diatur, gunakan menu atur PIN")
	}

	// Verify old PIN
	if err := bcrypt.CompareHashAndPassword([]byte(*u.Pin), []byte(req.OldPin)); err != nil {
		return errors.New("PIN lama salah")
	}

	if req.OldPin == req.NewPin {
		return errors.New("PIN baru tidak boleh sama dengan PIN lama")
	}

	hashedPin, err := bcrypt.GenerateFromPassword([]byte(req.NewPin), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("gagal memproses PIN baru")
	}

	pinStr := string(hashedPin)
	u.Pin = &pinStr
	u.UpdatedAt = time.Now()

	// Reset any lockout
	lockKey := fmt.Sprintf("pin_locked:%s", id.String())
	attemptKey := fmt.Sprintf("pin_attempts:%s", id.String())
	_ = s.redis.Del(ctx, lockKey, attemptKey)

	return s.repo.Update(ctx, u)
}

func (s *service) VerifyPin(ctx context.Context, id uuid.UUID, pin string) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if u.Pin == nil || *u.Pin == "" {
		return errors.New("PIN belum diatur")
	}

	// 1. Check Lockout Status
	lockKey := fmt.Sprintf("pin_locked:%s", id.String())
	isLocked, _ := s.redis.Exists(ctx, lockKey)
	if isLocked {
		return errors.New("pin terblokir selama 24 jam karena terlalu banyak percobaan, hubungi admin untuk bantuan")
	}

	// 2. Verify PIN
	if err := bcrypt.CompareHashAndPassword([]byte(*u.Pin), []byte(pin)); err != nil {
		// Increment attempts
		attemptKey := fmt.Sprintf("pin_attempts:%s", id.String())
		attemptsStr, _ := s.redis.Get(ctx, attemptKey)
		attempts, _ := strconv.Atoi(attemptsStr)
		attempts++

		if attempts >= 5 {
			// Lock for 24 hours
			_ = s.redis.Set(ctx, lockKey, "locked", 24*time.Hour)
			_ = s.redis.Del(ctx, attemptKey)
			return errors.New("terlalu banyak percobaan, PIN Anda diblokir selama 24 jam")
		}

		_ = s.redis.Set(ctx, attemptKey, strconv.Itoa(attempts), 1*time.Hour)
		return fmt.Errorf("PIN salah. Sisa percobaan: %d", 5-attempts)
	}

	// 3. Reset attempts on success
	_ = s.redis.Del(ctx, fmt.Sprintf("pin_attempts:%s", id.String()))

	return nil
}

func (s *service) UnlockPin(ctx context.Context, id uuid.UUID) error {
	lockKey := fmt.Sprintf("pin_locked:%s", id.String())
	attemptKey := fmt.Sprintf("pin_attempts:%s", id.String())
	return s.redis.Del(ctx, lockKey, attemptKey)
}

func (s *service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

func (s *service) UpdateVerification(ctx context.Context, id, actorID uuid.UUID, isVerified bool) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	old := u.IsVerified
	u.IsVerified = isVerified
	if isVerified {
		now := time.Now()
		u.VerifiedAt = &now
	} else {
		u.VerifiedAt = nil
	}
	u.UpdatedAt = time.Now()
	err = s.repo.Update(ctx, u)
	if err == nil {
		s.auditSvc.Log(ctx, &actorID, audit.ActionUserVerify, "user", id.String(), map[string]interface{}{"is_verified": old}, map[string]interface{}{"is_verified": isVerified}, "", "")
	}
	return err
}

func (s *service) UpdateTrustScore(ctx context.Context, id, actorID uuid.UUID, score int) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	old := u.TrustScore
	u.TrustScore = score
	u.UpdatedAt = time.Now()
	err = s.repo.Update(ctx, u)
	if err == nil {
		s.auditSvc.Log(ctx, &actorID, audit.ActionUserUpdateTrust, "user", id.String(), map[string]interface{}{"trust_score": old}, map[string]interface{}{"trust_score": score}, "", "")
	}
	return err
}

func (s *service) UpdateSuspendStatus(ctx context.Context, id, actorID uuid.UUID, isSuspended bool, reason string) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	oldStatus := u.IsSuspended
	u.IsSuspended = isSuspended
	if isSuspended {
		u.SuspendedReason = &reason
		// Security Hardening: Increment TokenVersion to revoke all active sessions immediately
		u.TokenVersion++
	} else {
		u.SuspendedReason = nil
	}
	u.UpdatedAt = time.Now()
	err = s.repo.Update(ctx, u)
	if err == nil {
		// Sync to Redis if possible to make revocation even faster
		if s.redis != nil {
			cacheKey := fmt.Sprintf("user:session:v:%s", id.String())
			_ = s.redis.Set(ctx, cacheKey, u.TokenVersion, 24*time.Hour)
		}

		action := audit.ActionUserSuspend
		if !isSuspended {
			action = audit.ActionUserUnsuspend
		}
		s.auditSvc.Log(ctx, &actorID, action, "user", id.String(), map[string]interface{}{"is_suspended": oldStatus}, map[string]interface{}{"is_suspended": isSuspended, "reason": reason}, "", "")
	}
	return err
}

func (s *service) UpdateLocation(ctx context.Context, id uuid.UUID, lat, lng float64) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	// 1. Update DB location only (efficient update, avoids locking full user row)
	if err := s.repo.UpdateLocation(ctx, id, lat, lng); err != nil {
		return err
	}

	// 2. Update Redis for Live Tracking (Intelligent detection & Spatial Search)
	if s.redis != nil {
		// Individual key for details
		key := "runner:track:" + id.String()
		val := fmt.Sprintf("%f,%f,%d", lat, lng, time.Now().Unix())
		_ = s.redis.Set(ctx, key, val, 10*time.Minute)

		// GEO set for spatial search (runners_live)
		if u.Role == RoleRunner && !u.IsSuspended {
			_ = s.redis.Client().GeoAdd(ctx, "runners_live", &redis.GeoLocation{
				Name:      id.String(),
				Longitude: lng,
				Latitude:  lat,
			})
		}
	}

	return nil
}

func (s *service) UpdateHeartbeat(ctx context.Context, id uuid.UUID, req HeartbeatRequest) error {
	// Dedup by distance: use haversine for accuracy (cos(lat) corrected), threshold 20m aligned with mobile
	lastKey := fmt.Sprintf("runner:heartbeat:last:%s", id.String())
	if s.redis != nil {
		if lastVal, err := s.redis.Get(ctx, lastKey); err == nil && lastVal != "" {
			var lastLat, lastLng float64
			if n, _ := fmt.Sscanf(lastVal, "%f,%f", &lastLat, &lastLng); n == 2 {
				// Use same package geolocation for accurate distance
				distKm := s.geoDistance(lastLat, lastLng, req.Lat, req.Lng)
				if distKm*1000 < 20 { // 20m
					// Only refresh TTL, no DB write, but also refresh geo TTL via expire
					trackKey := "runner:track:" + id.String()
					_ = s.redis.Client().Expire(ctx, trackKey, 10*time.Minute)
					_ = s.redis.Set(ctx, lastKey, fmt.Sprintf("%f,%f", req.Lat, req.Lng), 10*time.Minute)
					// Refresh geo set TTL via separate key marker (since GEO doesn't support expire per member, we rely on Set)
					return nil
				}
			}
		}
	}

	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if u.Role != RoleRunner {
		return errors.New("hanya runner yang dapat mengirim heartbeat")
	}

	if !u.IsAcceptingOrders {
		return errors.New("heartbeat hanya saat status online")
	}

	if err := s.repo.UpdateLocation(ctx, id, req.Lat, req.Lng); err != nil {
		return err
	}

	if s.redis != nil {
		// Store last known for distance dedup (20m aligned with mobile)
		_ = s.redis.Set(ctx, lastKey, fmt.Sprintf("%f,%f", req.Lat, req.Lng), 10*time.Minute)

		// Live track key with TTL 10m (prod allkeys-lru safe: volatile key)
		trackKey := "runner:track:" + id.String()
		val := fmt.Sprintf("%f,%f,%d", req.Lat, req.Lng, time.Now().Unix())
		_ = s.redis.Set(ctx, trackKey, val, 10*time.Minute)

		// GEO set - prod allkeys-lru: GEO set has no per-member TTL, so we keep membership but also set a marker key with TTL for cleanup detection
		// The marker runner:alive:<id> = 1 TTL 10m helps identify stale GEO members if needed (background cleaner can check)
		_ = s.redis.Client().GeoAdd(ctx, "runners_live", &redis.GeoLocation{
			Name:      id.String(),
			Longitude: req.Lng,
			Latitude:  req.Lat,
		}).Err()
		aliveKey := fmt.Sprintf("runner:alive:%s", id.String())
		_ = s.redis.Set(ctx, aliveKey, "1", 10*time.Minute)

		// Optional: store trip context for observability
		if req.TripID != nil && *req.TripID != "" {
			hbMetaKey := fmt.Sprintf("runner:heartbeat:meta:%s", id.String())
			metaVal := fmt.Sprintf("trip=%s|orders=%d|fg=%v", *req.TripID, req.ActiveOrders, req.IsForeground)
			_ = s.redis.Set(ctx, hbMetaKey, metaVal, 10*time.Minute)
		}
	}

	return nil
}

func (s *service) UpdateHome(ctx context.Context, id uuid.UUID, req UpdateHomeRequest) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	u.HomeLat = &req.Lat
	u.HomeLng = &req.Lng
	u.HomeAddress = &req.Address
	u.UpdatedAt = time.Now()
	return s.repo.Update(ctx, u)
}

func (s *service) UpdateAcceptingOrders(ctx context.Context, id uuid.UUID, isAccepting bool) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if u.Role != RoleRunner {
		return errors.New("hanya runner yang dapat mengubah status penerimaan order")
	}

	u.IsAcceptingOrders = isAccepting
	u.UpdatedAt = time.Now()
	return s.repo.Update(ctx, u)
}

func (s *service) UpdateProfile(ctx context.Context, id uuid.UUID, req UpdateProfileRequest, avatarFile io.Reader, avatarFilename string) error {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	// Sanitize whatsapp: strip non-digit, 0->62
	sanitizedWa := sanitizeWhatsappNumber(req.WhatsappNumber)

	u.Name = req.Name
	u.WhatsappNumber = sanitizedWa
	// Allow clearing home_address: if empty string set to nil, else set pointer
	if req.HomeAddress == "" {
		u.HomeAddress = nil
	} else {
		u.HomeAddress = &req.HomeAddress
	}

	if avatarFile != nil && s.storage != nil {
		var buf bytes.Buffer
		size, err := io.Copy(&buf, avatarFile)
		if err != nil {
			return fmt.Errorf("failed to read avatar file: %w", err)
		}

		// Defense: size check also in service (in case called without handler check)
		if size > 5*1024*1024 {
			return errors.New("ukuran gambar avatar terlalu besar (maksimal 5MB)")
		}

		limit := 512
		if buf.Len() < limit {
			limit = buf.Len()
		}
		contentType := http.DetectContentType(buf.Bytes()[:limit])
		if contentType == "application/octet-stream" {
			contentType = "image/jpeg"
		}

		ext := ".jpg"
		if avatarFilename != "" {
			// Use original extension if image
			lower := strings.ToLower(avatarFilename)
			if strings.HasSuffix(lower, ".png") {
				ext = ".png"
			} else if strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".jpg") {
				ext = ".jpg"
			} else if strings.HasSuffix(lower, ".webp") {
				ext = ".webp"
			} else if strings.HasSuffix(lower, ".gif") {
				ext = ".gif"
			}
		}

		objectKey := fmt.Sprintf("avatars/%s%s", id.String(), ext)
		path, err := s.storage.Upload(ctx, objectKey, &buf, size, contentType)
		if err != nil {
			return fmt.Errorf("failed to upload avatar: %w", err)
		}
		u.AvatarUrl = &path
	}

	u.UpdatedAt = time.Now()
	return s.repo.Update(ctx, u)
}

func sanitizeWhatsappNumber(phone string) string {
	if phone == "" {
		return phone
	}
	// Keep digits only first
	sanitized := phone
	sanitized = strings.ReplaceAll(sanitized, "+", "")
	sanitized = strings.ReplaceAll(sanitized, " ", "")
	sanitized = strings.ReplaceAll(sanitized, "-", "")
	sanitized = strings.ReplaceAll(sanitized, "(", "")
	sanitized = strings.ReplaceAll(sanitized, ")", "")

	// If still contains non-digit, strip
	var digits strings.Builder
	for _, r := range sanitized {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	sanitized = digits.String()

	if strings.HasPrefix(sanitized, "0") {
		sanitized = "62" + sanitized[1:]
	}
	if strings.HasPrefix(sanitized, "8") {
		sanitized = "62" + sanitized
	}
	return sanitized
}

func (s *service) GetRedis() *cache.Redis {
	return s.redis
}

func (s *service) signAvatar(ctx context.Context, u *User) {
	if u == nil || u.AvatarUrl == nil || *u.AvatarUrl == "" {
		return
	}
	if signed, err := s.storage.SignedURL(ctx, *u.AvatarUrl, 1*time.Hour); err == nil {
		u.AvatarUrl = &signed
	}
}

func (s *service) GetBankAccount(ctx context.Context, userID uuid.UUID) (*UserBankAccount, error) {
	return s.repo.FindBankAccountByUserID(ctx, userID)
}

func (s *service) AdminRegisterBankAccount(ctx context.Context, userID uuid.UUID, bankName, accountNo, accountName, adminPassword, totpCode string, adminID uuid.UUID) error {
	admin, err := s.repo.FindByID(ctx, adminID)
	if err != nil {
		return errors.New("gagal menemukan akun admin")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(adminPassword)); err != nil {
		return errors.New("konfirmasi password admin salah")
	}

	if admin.TotpEnabled {
		if admin.TotpSecret == nil || *admin.TotpSecret == "" {
			return errors.New("TOTP terkonfigurasi tidak valid")
		}
		if !totp.Validate(totpCode, *admin.TotpSecret) {
			return errors.New("kode TOTP admin tidak valid")
		}
	} else {
		return errors.New("tindakan ini memerlukan autentikasi dua faktor (TOTP) diaktifkan pada akun admin Anda")
	}

	bankAccount := &UserBankAccount{
		ID:          uuid.New(),
		UserID:      userID,
		BankName:    bankName,
		AccountNo:   accountNo,
		AccountName: accountName,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.UpsertBankAccount(ctx, bankAccount); err != nil {
		return fmt.Errorf("gagal mendaftarkan rekening: %w", err)
	}

	s.auditSvc.Log(ctx, &adminID, "REGISTER_USER_BANK", "user_bank_accounts", userID.String(), nil, bankAccount, "", "")
	return nil
}
