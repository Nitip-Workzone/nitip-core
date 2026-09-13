package kyc_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/kyc"
	kycMocks "github.com/codecoffy/nitip-core/internal/domain/kyc/mocks"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/pkg/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"go.uber.org/mock/gomock"
)

func makeMultipart(fields map[string]string, files map[string][]byte) (*bytes.Buffer, string) {
	b := &bytes.Buffer{}
	w := multipart.NewWriter(b)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	for field, data := range files {
		fw, _ := w.CreateFormFile(field, field+".jpg")
		_, _ = fw.Write(data)
	}
	_ = w.Close()
	return b, w.FormDataContentType()
}

func validJpegBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.White)
		}
	}
	buf := &bytes.Buffer{}
	_ = jpeg.Encode(buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

func setupApp(svc kyc.Service) *fiber.App {
	app := fiber.New()
	h := kyc.NewHandler(svc, (*bun.DB)(nil), (*cache.Redis)(nil))
	app.Post("/kyc/submit", func(c *fiber.Ctx) error {
		uidStr := c.Get("X-User-ID")
		if uidStr != "" {
			uid, _ := uuid.Parse(uidStr)
			c.Locals("user", &jwt.CustomClaims{UserID: uid})
		}
		return h.Submit(c)
	})
	app.Post("/admin/kyc/:id/review", func(c *fiber.Ctx) error {
		uidStr := c.Get("X-User-ID")
		role := c.Get("X-Role")
		if uidStr != "" {
			uid, _ := uuid.Parse(uidStr)
			c.Locals("user", &jwt.CustomClaims{UserID: uid, Role: role})
		}
		if role != user.RoleAdmin {
			return c.Status(403).JSON(fiber.Map{"message": "forbidden"})
		}
		return h.Review(c)
	})
	app.Post("/admin/kyc/reset-retry", func(c *fiber.Ctx) error {
		uidStr := c.Get("X-User-ID")
		role := c.Get("X-Role")
		if uidStr != "" {
			uid, _ := uuid.Parse(uidStr)
			c.Locals("user", &jwt.CustomClaims{UserID: uid, Role: role})
		}
		if role != user.RoleAdmin {
			return c.Status(403).JSON(fiber.Map{"message": "forbidden"})
		}
		return h.ResetRetry(c)
	})
	return app
}

func TestKYC_Handler(t *testing.T) {
	t.Run("POST /kyc/submit", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("handler menerima multipart valid", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				svc := kycMocks.NewMockService(ctrl)
				app := setupApp(svc)
				uid := uuid.New()
				jpeg := validJpegBytes()
				svc.EXPECT().Submit(gomock.Any(), uid, gomock.Any()).Return(&kyc.KycSubmission{ID: uuid.New(), Status: kyc.StatusPending}, nil)
				body, ct := makeMultipart(map[string]string{"facebook_name": "Budi", "target_level": "separuh"}, map[string][]byte{"facebook_screenshot": jpeg, "selfie": jpeg})
				req := httptest.NewRequest(http.MethodPost, "/kyc/submit", body)
				req.Header.Set("Content-Type", ct)
				req.Header.Set("X-User-ID", uid.String())
				resp, err := app.Test(req)
				require.NoError(t, err)
				assert.Equal(t, 201, resp.StatusCode)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("non-admin tidak dapat review", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				svc := kycMocks.NewMockService(ctrl)
				app := setupApp(svc)
				kid := uuid.New()
				uid := uuid.New()
				body, _ := json.Marshal(map[string]interface{}{"approved": true, "note": "ok"})
				req := httptest.NewRequest(http.MethodPost, "/admin/kyc/"+kid.String()+"/review", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-User-ID", uid.String())
				req.Header.Set("X-Role", user.RoleRequester)
				resp, _ := app.Test(req)
				assert.Equal(t, 403, resp.StatusCode)
				svc.EXPECT().Review(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			})
			t.Run("rejection tanpa alasan ditolak handler", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				svc := kycMocks.NewMockService(ctrl)
				app := setupApp(svc)
				kid := uuid.New()
				uid := uuid.New()
				body, _ := json.Marshal(map[string]interface{}{"approved": false, "note": ""})
				req := httptest.NewRequest(http.MethodPost, "/admin/kyc/"+kid.String()+"/review", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-User-ID", uid.String())
				req.Header.Set("X-Role", user.RoleAdmin)
				resp, _ := app.Test(req)
				assert.Equal(t, 400, resp.StatusCode)
			})
			t.Run("KTP invalid format ditolak handler", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				svc := kycMocks.NewMockService(ctrl)
				app := setupApp(svc)
				uid := uuid.New()
				jpeg := validJpegBytes()
				txt := []byte("not an image")
				body, ct := makeMultipart(map[string]string{"facebook_name": "Budi", "target_level": "separuh", "id_card_number": "123"}, map[string][]byte{"facebook_screenshot": jpeg, "selfie": jpeg, "id_card": txt})
				req := httptest.NewRequest(http.MethodPost, "/kyc/submit", body)
				req.Header.Set("Content-Type", ct)
				req.Header.Set("X-User-ID", uid.String())
				resp, _ := app.Test(req)
				assert.Equal(t, 400, resp.StatusCode)
			})
			t.Run("non-admin tidak dapat reset", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				svc := kycMocks.NewMockService(ctrl)
				app := setupApp(svc)
				uid := uuid.New()
				body, _ := json.Marshal(map[string]interface{}{"user_id": uid.String(), "target_level": "separuh"})
				req := httptest.NewRequest(http.MethodPost, "/admin/kyc/reset-retry", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-User-ID", uid.String())
				req.Header.Set("X-Role", user.RoleRequester)
				resp, _ := app.Test(req)
				assert.Equal(t, 403, resp.StatusCode)
				svc.EXPECT().ResetRetry(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			})
			t.Run("upgrade penuh hanya KTP tanpa facebook/selfie diterima", func(t *testing.T) {
				ctrl := gomock.NewController(t)
				svc := kycMocks.NewMockService(ctrl)
				app := setupApp(svc)
				uid := uuid.New()
				jpeg := validJpegBytes()
				svc.EXPECT().Submit(gomock.Any(), uid, gomock.Any()).Return(&kyc.KycSubmission{ID: uuid.New(), TargetLevel: kyc.LevelPenuh}, nil)
				body, ct := makeMultipart(map[string]string{"target_level": "penuh", "id_card_number": "1234567890123456"}, map[string][]byte{"id_card": jpeg})
				req := httptest.NewRequest(http.MethodPost, "/kyc/submit", body)
				req.Header.Set("Content-Type", ct)
				req.Header.Set("X-User-ID", uid.String())
				resp, err := app.Test(req)
				require.NoError(t, err)
				assert.Equal(t, 201, resp.StatusCode)
			})
		})
	})
}
