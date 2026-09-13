package user_test

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	userMocks "github.com/codecoffy/nitip-core/internal/domain/user/mocks"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/uptrace/bun"
)

func TestMain(m *testing.M) {
	orig := log.Writer()
	log.SetOutput(io.Discard)
	code := m.Run()
	log.SetOutput(orig)
	os.Exit(code)
}

func mustHash(pw string) string {
	h, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h)
}

func newTestUser(overrides ...func(*user.User)) *user.User {
	now := time.Now()
	u := &user.User{
		ID:             uuid.New(),
		Name:           "Budi",
		Email:          "budi@test.local",
		WhatsappNumber: "6281234567890",
		Password:       mustHash("password123"),
		Role:           user.RoleRequester,
		IsSuspended:    false,
		TokenVersion:   1,
		DeviceId:       strPtr("dev-1"),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	for _, fn := range overrides {
		fn(u)
	}
	return u
}

func strPtr(s string) *string { return &s }

func TestAuthUser(t *testing.T) {
	t.Run("NormalizeWhatsapp", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			cases := []struct {
				name string
				in   string
				want string
			}{
				{"format 08 menjadi 62", "081234567890", "6281234567890"},
				{"format 8 menjadi 62", "81234567890", "6281234567890"},
				{"format 62 tetap canonical", "6281234567890", "6281234567890"},
				{"format +62 menjadi canonical", "+6281234567890", "6281234567890"},
				{"spasi didukung", "08 1234 567890", "6281234567890"},
				{"hubung didukung", "08-1234-567890", "6281234567890"},
				{"kurung didukung", "(0812) 3456-7890", "6281234567890"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					got, err := user.ValidateAndSanitizeWhatsapp(tc.in)
					require.NoError(t, err)
					assert.Equal(t, tc.want, got)
					assert.Equal(t, tc.want, user.SanitizeWhatsappNumber(tc.in))
				})
			}
		})
		t.Run("negative", func(t *testing.T) {
			cases := []struct {
				name string
				in   string
			}{
				{"nomor mengandung huruf ditolak", "0812abc7890"},
				{"prefix wa ditolak", "wa081234567890"},
				{"simbol ilegal # ditolak", "0812#3456"},
				{"double plus ditolak", "++6281234567890"},
				{"plus 0 ditolak", "+081234567890"},
				{"nomor terlalu pendek", "62812"},
				{"nomor terlalu panjang", "628123456789012345"},
				{"input kosong", ""},
				{"hanya spasi", "   "},
				{"huruf saja", "abc"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					_, err := user.ValidateAndSanitizeWhatsapp(tc.in)
					assert.Error(t, err, "input %q should be rejected", tc.in)
				})
			}
			t.Run("0812abc tidak boleh lolos via strip", func(t *testing.T) {
				raw := "0812abc7890"
				sanitized := user.SanitizeWhatsappNumber(raw)
				assert.False(t, user.IsValidWhatsappCanonical(sanitized), "sanitized=%q must be invalid", sanitized)
			})
		})
	})
	t.Run("POST /users/register", func(t *testing.T) {
if config.App == nil {
		config.App = &config.Config{BypassGeofence: true}
	}
	origBypass := config.App.BypassGeofence
	config.App.BypassGeofence = true
	t.Cleanup(func() { config.App.BypassGeofence = origBypass })

	validLat := 0.741049
	validLng := 124.312988

	newReq := func(wa, role string, lat, lng *float64) user.CreateUserRequest {
		return user.CreateUserRequest{
			Name:           "Budi",
			Email:          "budi@test.local",
			Password:       "password123",
			Role:           role,
			WhatsappNumber: wa,
			DeviceId:       "dev-1",
			Latitude:       lat,
			Longitude:      lng,
		}
	}

	t.Run("positive", func(t *testing.T) {
		t.Run("requester valid berhasil dibuat", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("081234567890", "", &validLat, &validLng)
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(nil, errors.New("not found")).Times(1)
			repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *user.User) error {
				assert.Equal(t, "6281234567890", u.WhatsappNumber)
				assert.Equal(t, user.RoleRequester, u.Role)
				return nil
			}).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			got, err := svc.Create(context.Background(), req)
			require.NoError(t, err)
			assert.Equal(t, "6281234567890", got.WhatsappNumber)
			assert.Equal(t, user.RoleRequester, got.Role)
		})
		t.Run("nomor 08 disimpan sebagai 62", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("081234567890", "", &validLat, &validLng)
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(nil, errors.New("not found")).Times(1)
			repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *user.User) error {
				assert.Equal(t, "6281234567890", u.WhatsappNumber)
				return nil
			}).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			got, err := svc.Create(context.Background(), req)
			require.NoError(t, err)
			assert.Equal(t, "6281234567890", got.WhatsappNumber)
		})
		t.Run("field role kosong menjadi requester", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("081234567890", "", &validLat, &validLng)
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(nil, errors.New("not found")).Times(1)
			repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *user.User) error {
				assert.Equal(t, user.RoleRequester, u.Role)
				return nil
			}).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			got, err := svc.Create(context.Background(), req)
			require.NoError(t, err)
			assert.Equal(t, user.RoleRequester, got.Role)
		})
		t.Run("client mengirim role runner tetap disimpan sebagai requester", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("081234567890", user.RoleRunner, &validLat, &validLng)
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(nil, errors.New("not found")).Times(1)
			repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *user.User) error {
				assert.Equal(t, user.RoleRequester, u.Role, "backend must ignore client role")
				return nil
			}).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			got, err := svc.Create(context.Background(), req)
			require.NoError(t, err)
			assert.Equal(t, user.RoleRequester, got.Role)
		})
	})

	t.Run("negative", func(t *testing.T) {
		t.Run("whatsapp duplicate dengan format berbeda ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			existing := newTestUser()
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(existing, nil).Times(1)
			req := newReq("081234567890", "", &validLat, &validLng)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "sudah digunakan")
		})
		t.Run("nomor mengandung huruf ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("0812abc7890", "", &validLat, &validLng)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "tidak valid")
		})
		t.Run("simbol ilegal ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("0812#3456", "", &validLat, &validLng)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
		})
		t.Run("lokasi di luar wilayah tidak membuat user", func(t *testing.T) {
			if config.App == nil {
				config.App = &config.Config{}
			}
			config.App.BypassGeofence = false
			t.Cleanup(func() { config.App.BypassGeofence = true })
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			lat, lng := -6.2, 106.8
			req := newReq("081234567890", "", &lat, &lng)
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(nil, errors.New("not found")).Times(1)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "di luar wilayah")
		})
		t.Run("input invalid tidak memanggil Repository.Create", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("not-a-number", "", &validLat, &validLng)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
		})
		t.Run("repository error tidak menghasilkan partial state", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("081234567890", "", &validLat, &validLng)
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(nil, errors.New("not found")).Times(1)
			repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("db down")).Times(1)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "db down")
		})
		t.Run("nomor terlalu pendek ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			req := newReq("62812", "", &validLat, &validLng)
			_, err := svc.Create(context.Background(), req)
			assert.Error(t, err)
		})
	})
	})
	t.Run("POST /auth/login", func(t *testing.T) {
pw := "password123"
	hash := mustHash(pw)

	baseUser := func() *user.User {
		return &user.User{
			ID:             uuid.New(),
			Name:           "Budi",
			Email:          "budi@test.local",
			WhatsappNumber: "6281234567890",
			Password:       hash,
			Role:           user.RoleRequester,
			IsSuspended:    false,
			TokenVersion:   5,
			DeviceId:       strPtr("old-dev"),
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
	}

	t.Run("positive", func(t *testing.T) {
		t.Run("login dengan email berhasil", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			repo.EXPECT().FindByEmail(gomock.Any(), "budi@test.local").Return(u, nil).Times(1)
			repo.EXPECT().ClearDeviceSessions(gomock.Any(), "dev-1", u.ID).Return(nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, got *user.User) error {
				assert.Equal(t, 6, got.TokenVersion)
				return nil
			}).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.NotEmpty(t, res.Token)
			assert.NotEmpty(t, res.RefreshToken)
		})
		t.Run("login dengan nomor 08 berhasil", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(u, nil).Times(1)
			repo.EXPECT().ClearDeviceSessions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "081234567890", Password: pw, DeviceId: "dev-1"}, "web")
			require.NoError(t, err)
			assert.NotEmpty(t, res.Token)
		})
		t.Run("login dengan format +62 berhasil", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(u, nil).Times(1)
			repo.EXPECT().ClearDeviceSessions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "+6281234567890", Password: pw, DeviceId: "dev-1"}, "web")
			require.NoError(t, err)
			assert.NotEmpty(t, res.Token)
		})
		t.Run("variasi format login menemukan user yang sama", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(u, nil).Times(1)
			repo.EXPECT().ClearDeviceSessions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "08 1234 567890", Password: pw, DeviceId: "dev-1"}, "web")
			require.NoError(t, err)
			assert.NotEmpty(t, res.Token)
		})
	})

	t.Run("negative", func(t *testing.T) {
		t.Run("password salah tidak membuat session", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: "wrong", DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
		t.Run("user tidak ditemukan", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(nil, errors.New("not found")).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "x@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
		t.Run("nomor invalid tidak membuat session", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "0812abc7890", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
		t.Run("akun suspended ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			u.IsSuspended = true
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "ditangguhkan")
			assert.Nil(t, res)
		})
		t.Run("akun soft-deleted ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			now := time.Now()
			u.DeletedAt = &now
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "tidak tersedia")
			assert.Nil(t, res)
		})
		t.Run("soft-deleted via whatsapp juga ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			now := time.Now()
			u.DeletedAt = &now
			repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "081234567890", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
		t.Run("role tidak sesuai platform ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			u.Role = user.RoleMerchant
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "portal merchant")
			assert.Nil(t, res)
		})
		t.Run("admin via mobile ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			u.Role = user.RoleAdmin
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "mobile")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
		t.Run("repository error tidak membuat token", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down")).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
		t.Run("update session failure tidak menghasilkan token", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := baseUser()
			repo.EXPECT().FindByEmail(gomock.Any(), gomock.Any()).Return(u, nil).Times(1)
			repo.EXPECT().ClearDeviceSessions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("update failed")).Times(1)
			res, err := svc.Login(context.Background(), user.LoginRequest{Email: "budi@test.local", Password: pw, DeviceId: "dev-1"}, "web")
			assert.Error(t, err)
			assert.Nil(t, res)
		})
	})
	})
	t.Run("POST /auth/refresh", func(t *testing.T) {
t.Run("positive", func(t *testing.T) {
		t.Run("refresh valid menghasilkan token baru", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) {
				u.TokenVersion = 5
				u.DeviceId = strPtr("dev-1")
			})
			refreshToken, err := jwt.GenerateRefreshToken(u.ID, "dev-1", 5)
			require.NoError(t, err)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, got *user.User) error {
				assert.Equal(t, 6, got.TokenVersion)
				return nil
			}).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			res, err := svc.Refresh(context.Background(), refreshToken)
			require.NoError(t, err)
			assert.NotEmpty(t, res.Token)
			assert.NotEmpty(t, res.RefreshToken)
			assert.NotEqual(t, refreshToken, res.RefreshToken)
			assert.Equal(t, 6, u.TokenVersion)
		})
		t.Run("token version berubah dan device sama diterima", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) {
				u.TokenVersion = 10
				u.DeviceId = strPtr("dev-xyz")
			})
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-xyz", 10)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			res, err := svc.Refresh(context.Background(), refreshToken)
			require.NoError(t, err)
			assert.NotEmpty(t, res.Token)
		})
	})
	t.Run("negative", func(t *testing.T) {
		t.Run("refresh invalid ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			_, err := svc.Refresh(context.Background(), "not-a-jwt")
			assert.Error(t, err)
		})
		t.Run("refresh kedaluwarsa ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) {
				u.TokenVersion = 5
				u.DeviceId = strPtr("dev-1")
			})
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 5)
			u.TokenVersion = 6
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "kedaluwarsa")
		})
		t.Run("token version lama ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) { u.TokenVersion = 10; u.DeviceId = strPtr("dev-1") })
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 9)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err)
		})
		t.Run("device berbeda ditolak", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) { u.TokenVersion = 5; u.DeviceId = strPtr("dev-1") })
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-2", 5)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "perangkat")
		})
		t.Run("suspended tidak dapat refresh", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) {
				u.TokenVersion = 5
				u.DeviceId = strPtr("dev-1")
				u.IsSuspended = true
			})
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 5)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "ditangguhkan")
		})
		t.Run("deleted tidak dapat refresh", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			now := time.Now()
			u := newTestUser(func(u *user.User) {
				u.TokenVersion = 5
				u.DeviceId = strPtr("dev-1")
				u.DeletedAt = &now
			})
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 5)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "tidak tersedia")
		})
		t.Run("tidak ada token baru saat gagal", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) { u.TokenVersion = 5; u.DeviceId = strPtr("dev-1") })
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 5)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db down")).Times(1)
			res, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err)
			assert.Nil(t, res)
		})
	})
	})
	t.Run("POST /auth/logout", func(t *testing.T) {
// Logout is implemented in handler (DB token_version+1, redis del).
	// We test the invalidation indirectly via Refresh after logout: old refresh must fail.
	t.Run("positive", func(t *testing.T) {
		t.Run("logout membatalkan session via version bump", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) { u.TokenVersion = 3; u.DeviceId = strPtr("dev-1") })
			// Simulate logout by bumping version (as handler does)
			u.TokenVersion = 4
			refreshToken, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 3) // old version
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), refreshToken)
			assert.Error(t, err, "old refresh must be rejected after version bump")
		})
	})
	t.Run("negative", func(t *testing.T) {
		t.Run("refresh token lama ditolak setelah version bump", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svc := user.NewService(repo, nil, nil, nil)
			u := newTestUser(func(u *user.User) { u.TokenVersion = 10; u.DeviceId = strPtr("dev-1") })
			oldRefresh, _ := jwt.GenerateRefreshToken(u.ID, "dev-1", 9)
			repo.EXPECT().FindByID(gomock.Any(), u.ID).Return(u, nil).Times(1)
			_, err := svc.Refresh(context.Background(), oldRefresh)
			assert.Error(t, err)
		})
		t.Run("user A tidak mengubah session user B", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := userMocks.NewMockRepository(ctrl)
			svcA := user.NewService(repo, nil, nil, nil)
			svcB := user.NewService(repo, nil, nil, nil)
			uA := newTestUser(func(u *user.User) { u.TokenVersion = 5; u.DeviceId = strPtr("dev-A") })
			uB := newTestUser(func(u *user.User) { u.TokenVersion = 5; u.DeviceId = strPtr("dev-B") })
			refreshA, _ := jwt.GenerateRefreshToken(uA.ID, "dev-A", 5)
			repo.EXPECT().FindByID(gomock.Any(), uA.ID).Return(uA, nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			resA, err := svcA.Refresh(context.Background(), refreshA)
			require.NoError(t, err)
			assert.NotNil(t, resA)
			// B's token still valid independently
			refreshB, _ := jwt.GenerateRefreshToken(uB.ID, "dev-B", 5)
			repo.EXPECT().FindByID(gomock.Any(), uB.ID).Return(uB, nil).Times(1)
			repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			resB, err := svcB.Refresh(context.Background(), refreshB)
			require.NoError(t, err)
			assert.NotNil(t, resB)
		})
	})
	})
}

// Ensure imports used
var _ = bun.IDB(nil)
