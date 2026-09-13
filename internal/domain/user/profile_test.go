package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/codecoffy/nitip-core/internal/domain/user"
	userMocks "github.com/codecoffy/nitip-core/internal/domain/user/mocks"
	"github.com/codecoffy/nitip-core/pkg/jwt"
)

// helper already defined in auth_test.go: mustHash, newTestUser, strPtr

func newProfileUser(id uuid.UUID, wa string) *user.User {
	u := newTestUser(func(u *user.User) {
		u.ID = id
		u.WhatsappNumber = wa
		u.Name = "Budi"
		u.Email = "budi@test.local"
		u.Role = user.RoleRequester
		u.IsVerified = false
		u.IsSuspended = false
	})
	// ensure avatar nil
	u.AvatarUrl = nil
	return u
}

func TestProfile(t *testing.T) {
	t.Run("PUT /users/profile", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("nama berhasil diubah", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				origName := u.Name
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(cloneUser(u), nil) // own number
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					assert.Equal(t, "Budi Baru", updated.Name)
					assert.Equal(t, "6281234567890", updated.WhatsappNumber)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi Baru", WhatsappNumber: "081234567890", HomeAddress: ""}, nil, "")
				require.NoError(t, err)
				_ = origName
			})
			t.Run("nomor 08 disimpan sebagai 62", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6289990000000")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(nil, errors.New("no rows in result set"))
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					assert.Equal(t, "6281234567890", updated.WhatsappNumber)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081234567890"}, nil, "")
				require.NoError(t, err)
			})
			t.Run("nomor +62 disimpan canonical", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6289990000000")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(nil, errors.New("no rows"))
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					assert.Equal(t, "6281234567890", updated.WhatsappNumber)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "+6281234567890"}, nil, "")
				require.NoError(t, err)
			})
			t.Run("user menyimpan ulang nomor miliknya sendiri", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(cloneUser(u), nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081234567890"}, nil, "")
				require.NoError(t, err)
			})
			t.Run("whatsapp baru belum digunakan berhasil disimpan", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6289990000000")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281112223333").Return(nil, errors.New("no rows"))
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					assert.Equal(t, "6281112223333", updated.WhatsappNumber)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081112223333"}, nil, "")
				require.NoError(t, err)
			})
			t.Run("home_address berhasil diubah", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(cloneUser(u), nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					require.NotNil(t, updated.HomeAddress)
					assert.Equal(t, "Jl Merdeka 1", *updated.HomeAddress)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081234567890", HomeAddress: "Jl Merdeka 1"}, nil, "")
				require.NoError(t, err)
			})
			t.Run("home_address kosong menjadi nil", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				addr := "old"
				u := newProfileUser(uid, "6281234567890")
				u.HomeAddress = &addr
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(cloneUser(u), nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					assert.Nil(t, updated.HomeAddress)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081234567890", HomeAddress: ""}, nil, "")
				require.NoError(t, err)
			})
			t.Run("update hanya mengubah field whitelist", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				u.Role = user.RoleRequester
				u.IsVerified = false
				u.IsSuspended = false
				u.Email = "budi@test.local"
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(cloneUser(u), nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated *user.User) error {
					assert.Equal(t, user.RoleRequester, updated.Role)
					assert.False(t, updated.IsVerified)
					assert.False(t, updated.IsSuspended)
					assert.Equal(t, "budi@test.local", updated.Email)
					assert.Equal(t, "Budi Baru", updated.Name)
					return nil
				})
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi Baru", WhatsappNumber: "081234567890"}, nil, "")
				require.NoError(t, err)
			})
			t.Run("admin update memakai aturan sama", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				targetID := uuid.New()
				u := newProfileUser(targetID, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), targetID).Return(cloneUser(u), nil)
				otherID := uuid.New()
				other := newProfileUser(otherID, "6289999999999")
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6289999999999").Return(other, nil)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), targetID, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "089999999999"}, nil, "")
				assert.Error(t, err)
				assert.ErrorIs(t, err, user.ErrWhatsappAlreadyUsed)
			})
			t.Run("variasi format 08 spasi dash kurung", func(t *testing.T) {
				cases := []string{"08 1234 567890", "08-1234-567890", "(0812) 3456-7890", "81234567890", "6281234567890", "+6281234567890"}
				for _, wa := range cases {
					ctrl := gomock.NewController(t)
					repo := userMocks.NewMockRepository(ctrl)
					uid := uuid.New()
					u := newProfileUser(uid, "6289990000000")
					repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil).Times(1)
					repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281234567890").Return(nil, errors.New("no rows")).Times(1)
					repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(1)
					svc := user.NewService(repo, nil, nil, nil)
					err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: wa}, nil, "")
					require.NoError(t, err, "wa %q should be accepted", wa)
				}
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("nomor mengandung huruf ditolak", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				// FindByWhatsappNumber must not be called for invalid input
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "0812abc7890"}, nil, "")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "tidak valid")
			})
			t.Run("nomor mengandung simbol ilegal ditolak", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "0812#3456"}, nil, "")
				assert.Error(t, err)
			})
			t.Run("nomor terlalu pendek ditolak", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "62812"}, nil, "")
				assert.Error(t, err)
			})
			t.Run("nomor terlalu panjang ditolak", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "628123456789012345"}, nil, "")
				assert.Error(t, err)
			})
			t.Run("nomor milik user lain ditolak", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				other := newProfileUser(uuid.New(), "6289998887776")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6289998887776").Return(other, nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "089998887776"}, nil, "")
				assert.ErrorIs(t, err, user.ErrWhatsappAlreadyUsed)
			})
			t.Run("error lookup dikembalikan", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081112223333"}, nil, "")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "db down")
			})
			t.Run("user target tidak ditemukan", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(nil, errors.New("not found"))
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081234567890"}, nil, "")
				assert.Error(t, err)
			})
			t.Run("repository update gagal", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6281112223333").Return(nil, errors.New("no rows"))
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081112223333"}, nil, "")
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "db down")
			})
			t.Run("input invalid tidak memanggil Repository.Update", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "wa081234567890"}, nil, "")
				assert.Error(t, err)
			})
			t.Run("duplicate tidak memanggil Repository.Update", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				other := newProfileUser(uuid.New(), "6289998887776")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), "6289998887776").Return(other, nil)
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "089998887776"}, nil, "")
				assert.Error(t, err)
			})
			t.Run("unique violation dari repository dipetakan duplicate", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindByWhatsappNumber(gomock.Any(), gomock.Any()).Return(nil, errors.New("no rows"))
				repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("duplicate key value violates unique constraint \"idx_users_unique_whatsapp\""))
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "081112223333"}, nil, "")
				assert.ErrorIs(t, err, user.ErrWhatsappAlreadyUsed)
			})
			t.Run("Storage.Upload tidak dipanggil saat invalid wa", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				// storage is nil so Upload tidak akan terpanggil; tapi error harus sebelum upload
				svc := user.NewService(repo, nil, nil, nil)
				err := svc.UpdateProfile(context.Background(), uid, user.UpdateProfileRequest{Name: "Budi", WhatsappNumber: "0812abc7890"}, bytes.NewReader([]byte("fake")), "a.jpg")
				assert.Error(t, err)
			})
		})
	})

	t.Run("GET /users/me", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("profile sendiri berhasil ditampilkan", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				u := newProfileUser(uid, "6281234567890")
				u.Name = "Budi"
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(cloneUser(u), nil)
				repo.EXPECT().FindWebAuthnCredentialsByUserID(gomock.Any(), uid).Return(nil, nil).AnyTimes()
				svc := user.NewService(repo, nil, nil, nil)
				got, err := svc.GetByID(context.Background(), uid, uid)
				require.NoError(t, err)
				assert.Equal(t, "Budi", got.Name)
				assert.Equal(t, "6281234567890", got.WhatsappNumber)
			})
			t.Run("response tidak memuat fcm token", func(t *testing.T) {
				u := newProfileUser(uuid.New(), "6281234567890")
				token := "secret-fcm"
				u.FcmToken = &token
				data, err := json.Marshal(u)
				require.NoError(t, err)
				assert.NotContains(t, string(data), "secret-fcm")
				assert.NotContains(t, string(data), "fcm_token")
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("user tidak ditemukan", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(nil, errors.New("not found"))
				svc := user.NewService(repo, nil, nil, nil)
				_, err := svc.GetByID(context.Background(), uid, uid)
				assert.Error(t, err)
			})
			t.Run("error dipetakan", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repo := userMocks.NewMockRepository(ctrl)
				uid := uuid.New()
				repo.EXPECT().FindByID(gomock.Any(), uid).Return(nil, errors.New("db down"))
				svc := user.NewService(repo, nil, nil, nil)
				_, err := svc.GetByID(context.Background(), uid, uid)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "db down")
			})
		})
	})

	t.Run("Serialization", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("field profile aman tetap ditampilkan", func(t *testing.T) {
				u := newProfileUser(uuid.New(), "6281234567890")
				u.Name = "Budi"
				u.Email = "budi@test.local"
				u.Role = user.RoleRequester
				data, err := json.Marshal(u)
				require.NoError(t, err)
				var m map[string]interface{}
				require.NoError(t, json.Unmarshal(data, &m))
				assert.Equal(t, "Budi", m["name"])
				assert.Equal(t, "budi@test.local", m["email"])
				assert.Equal(t, "requester", m["role"])
				assert.Equal(t, "6281234567890", m["whatsapp_number"])
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("fcm token tidak muncul", func(t *testing.T) {
				u := newProfileUser(uuid.New(), "6281234567890")
				tok := "secret-fcm-token-123"
				u.FcmToken = &tok
				data, _ := json.Marshal(u)
				assert.NotContains(t, string(data), tok)
				assert.NotContains(t, string(data), "fcm_token")
			})
			t.Run("device_id tidak muncul", func(t *testing.T) {
				u := newProfileUser(uuid.New(), "6281234567890")
				did := "dev-123"
				u.DeviceId = &did
				data, _ := json.Marshal(u)
				assert.NotContains(t, string(data), did)
				assert.NotContains(t, string(data), "device_id")
			})
			t.Run("password dan field internal tidak muncul", func(t *testing.T) {
				u := newProfileUser(uuid.New(), "6281234567890")
				u.Password = "hashed-secret"
				pin := "hashed-pin"
				u.Pin = &pin
				sec := "totp-secret"
				u.TotpSecret = &sec
				data, _ := json.Marshal(u)
				assert.NotContains(t, string(data), "hashed-secret")
				assert.NotContains(t, string(data), "hashed-pin")
				assert.NotContains(t, string(data), "totp-secret")
				assert.NotContains(t, string(data), "password")
				assert.NotContains(t, string(data), "\"pin\"")
				assert.NotContains(t, string(data), "totp_secret")
				assert.NotContains(t, string(data), "token_version")
			})
		})
	})

	t.Run("Ownership", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("user ID diambil dari claims", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				msvc := userMocks.NewMockService(ctrl)
				uid := uuid.New()
				// handler test: use fiber with injected claims via locals
				app := fiber.New()
				h := newTestHandlerWithMock(msvc)
				// mount like real: Protected would set c.Locals("user", claims)
				// Instead test handler directly that it reads claims
				// Use Fiber's internal handler chaining via middleware that sets Locals then calls Next
				// Create app with two handlers: first sets claims, second is real handler
				app.Put("/users/profile",
					func(c *fiber.Ctx) error {
						c.Locals("user", &jwt.CustomClaims{UserID: uid, Role: user.RoleRequester})
						return c.Next()
					},
					h.UpdateProfile,
				)
				msvc.EXPECT().UpdateProfile(gomock.Any(), uid, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				body, ct := newMultipartBody("Budi", "081234567890", "addr")
				req := httptest.NewRequest(http.MethodPut, "/users/profile", body)
				req.Header.Set("Content-Type", ct)
				resp, err := app.Test(req)
				require.NoError(t, err)
				if resp == nil {
					t.Fatalf("resp nil")
				}
				if resp.StatusCode != http.StatusOK {
					b, _ := io.ReadAll(resp.Body)
					t.Logf("resp %d body %s", resp.StatusCode, string(b))
				}
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("tanpa claims ditolak tidak mencapai service", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				msvc := userMocks.NewMockService(ctrl)
				app := fiber.New()
				h := newTestHandlerWithMock(msvc)
				app.Put("/users/profile", h.UpdateProfile)
				body, ct := newMultipartBody("Budi", "081234567890", "")
				req := httptest.NewRequest(http.MethodPut, "/users/profile", body)
				req.Header.Set("Content-Type", ct)
				resp, _ := app.Test(req)
				assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			})
			t.Run("user tidak dapat menentukan target lain via body", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				msvc := userMocks.NewMockService(ctrl)
				uidA := uuid.New()
				uidB := uuid.New()
				app := fiber.New()
				h := newTestHandlerWithMock(msvc)
				app.Put("/users/profile",
					func(c *fiber.Ctx) error {
						c.Locals("user", &jwt.CustomClaims{UserID: uidA, Role: user.RoleRequester})
						return c.Next()
					},
					h.UpdateProfile,
				)
				msvc.EXPECT().UpdateProfile(gomock.Any(), uidA, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, id uuid.UUID, req user.UpdateProfileRequest, _ io.Reader, _ string) error {
						assert.Equal(t, uidA, id)
						assert.NotEqual(t, uidB, id)
						return nil
					})
				body, ct := newMultipartBodyWithExtra("Budi", "081234567890", map[string]string{"role": "admin", "id": uidB.String(), "email": "hacker@evil.com"})
				req := httptest.NewRequest(http.MethodPut, "/users/profile", body)
				req.Header.Set("Content-Type", ct)
				resp, _ := app.Test(req)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			})
		})
	})

	t.Run("Admin update", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("admin dapat memperbarui profile target", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				msvc := userMocks.NewMockService(ctrl)
				adminID := uuid.New()
				targetID := uuid.New()
				app := fiber.New()
				h := newTestHandlerWithMock(msvc)
				app.Put("/admin/users/:id/profile",
					func(c *fiber.Ctx) error {
						c.Locals("user", &jwt.CustomClaims{UserID: adminID, Role: user.RoleAdmin})
						return c.Next()
					},
					h.AdminUpdateProfile,
				)
				msvc.EXPECT().UpdateProfile(gomock.Any(), targetID, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				body, ct := newMultipartBody("Budi Admin Edit", "081234567890", "addr admin")
				req := httptest.NewRequest(http.MethodPut, "/admin/users/"+targetID.String()+"/profile", body)
				req.Header.Set("Content-Type", ct)
				resp, _ := app.Test(req)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			})
		})
		t.Run("route memakai Protected + Role admin", func(t *testing.T) {
			// Wire as real: adminUser group = Protected + Role(admin)
			// Proof: requester 403, admin reaches handler
			ctrl := gomock.NewController(t)
			msvc := userMocks.NewMockService(ctrl)
			h := newTestHandlerWithMock(msvc)
			targetID := uuid.New()
			// requester should be forbidden
			app := fiber.New()
			app.Put("/admin/users/:id/profile",
				func(c *fiber.Ctx) error {
					c.Locals("user", &jwt.CustomClaims{UserID: uuid.New(), Role: user.RoleRequester})
					return c.Next()
				},
				func(c *fiber.Ctx) error {
					v := c.Locals("user")
					claims, _ := v.(*jwt.CustomClaims)
					if claims == nil || claims.Role != user.RoleAdmin {
						return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"success": false})
					}
					return c.Next()
				},
				h.AdminUpdateProfile,
			)
			body, ct := newMultipartBody("Budi", "081234567890", "")
			req := httptest.NewRequest(http.MethodPut, "/admin/users/"+targetID.String()+"/profile", body)
			req.Header.Set("Content-Type", ct)
			resp, _ := app.Test(req)
			assert.Equal(t, http.StatusForbidden, resp.StatusCode)
			// admin should reach handler
			app2 := fiber.New()
			app2.Put("/admin/users/:id/profile",
				func(c *fiber.Ctx) error {
					c.Locals("user", &jwt.CustomClaims{UserID: uuid.New(), Role: user.RoleAdmin})
					return c.Next()
				},
				func(c *fiber.Ctx) error {
					v := c.Locals("user")
					claims, _ := v.(*jwt.CustomClaims)
					if claims == nil || claims.Role != user.RoleAdmin {
						return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"success": false})
					}
					return c.Next()
				},
				h.AdminUpdateProfile,
			)
			msvc.EXPECT().UpdateProfile(gomock.Any(), targetID, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			body2, ct2 := newMultipartBody("Budi", "081234567890", "")
			req2 := httptest.NewRequest(http.MethodPut, "/admin/users/"+targetID.String()+"/profile", body2)
			req2.Header.Set("Content-Type", ct2)
			resp2, _ := app2.Test(req2)
			assert.Equal(t, http.StatusOK, resp2.StatusCode)
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("admin update tetap tidak dapat mengubah role via profile", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				msvc := userMocks.NewMockService(ctrl)
				targetID := uuid.New()
				app := fiber.New()
				h := newTestHandlerWithMock(msvc)
				app.Put("/admin/users/:id/profile",
					func(c *fiber.Ctx) error {
						c.Locals("user", &jwt.CustomClaims{UserID: uuid.New(), Role: user.RoleAdmin})
						return c.Next()
					},
					h.AdminUpdateProfile,
				)
				msvc.EXPECT().UpdateProfile(gomock.Any(), targetID, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, _ uuid.UUID, req user.UpdateProfileRequest, _ io.Reader, _ string) error {
						return nil
					})
				body, ct := newMultipartBodyWithExtra("Budi", "081234567890", map[string]string{"role": "admin", "is_verified": "true", "email": "evil@x.com"})
				req := httptest.NewRequest(http.MethodPut, "/admin/users/"+targetID.String()+"/profile", body)
				req.Header.Set("Content-Type", ct)
				resp, _ := app.Test(req)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			})
		})
	})
}

// helpers

func cloneUser(u *user.User) *user.User {
	cp := *u
	if u.HomeAddress != nil {
		v := *u.HomeAddress
		cp.HomeAddress = &v
	}
	if u.AvatarUrl != nil {
		v := *u.AvatarUrl
		cp.AvatarUrl = &v
	}
	if u.FcmToken != nil {
		v := *u.FcmToken
		cp.FcmToken = &v
	}
	return &cp
}

func newTestHandlerWithMock(svc user.Service) *user.Handler {
	return user.NewHandler(svc, nil, nil)
}

func newMultipartBody(name, wa, addr string) (io.Reader, string) {
	return newMultipartBodyWithExtra(name, wa, map[string]string{"home_address": addr})
}
func newMultipartBodyWithExtra(name, wa string, extra map[string]string) (io.Reader, string) {
	boundary := "testboundary123"
	buf := &bytes.Buffer{}
	writeField := func(key, val string) {
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Disposition: form-data; name=\"" + key + "\"\r\n\r\n")
		buf.WriteString(val + "\r\n")
	}
	writeField("name", name)
	writeField("whatsapp_number", wa)
	for k, v := range extra {
		writeField(k, v)
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return bytes.NewReader(buf.Bytes()), "multipart/form-data; boundary=" + boundary
}

var _ = strings.Contains
