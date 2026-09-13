package middleware_test

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codecoffy/nitip-core/config"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/internal/testutil"
	"github.com/codecoffy/nitip-core/pkg/jwt"
)

func TestMain(m *testing.M) {
	orig := log.Writer()
	log.SetOutput(io.Discard)
	code := m.Run()
	log.SetOutput(orig)
	os.Exit(code)
}

func ensureConfig() {
	if config.App == nil {
		config.App = &config.Config{AppEnv: "test"}
	}
}

func TestAuthProtected(t *testing.T) {
	t.Run("Protected Gate", func(t *testing.T) {
		t.Run("positive", func(t *testing.T) {
			t.Run("user aktif dengan session valid diterima", func(t *testing.T) {
				db, mockSql := testutil.NewMockDB(t)
				uid := uuid.New()
				token, err := jwt.GenerateToken(uid, "budi@test.local", "requester", false, "dev-1", 5)
				require.NoError(t, err)
				rows := sqlmock.NewRows([]string{"token_version", "is_suspended", "deleted_at"}).
					AddRow(5, false, nil)
				mockSql.ExpectQuery(`(?i)SELECT.*token_version.*FROM.*users`).WillReturnRows(rows)
				ensureConfig()
				app := fiber.New()
				app.Get("/protected", middleware.Protected(db, nil), func(c *fiber.Ctx) error {
					return c.SendString("ok")
				})
				req := httptest.NewRequest("GET", "/protected", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				resp, err := app.Test(req, -1)
				require.NoError(t, err)
				assert.Equal(t, 200, resp.StatusCode)
			})
		})
		t.Run("negative", func(t *testing.T) {
			t.Run("suspended menghasilkan ACCOUNT_SUSPENDED", func(t *testing.T) {
				ensureConfig()
				db, mockSql := testutil.NewMockDB(t)
				uid := uuid.New()
				token, _ := jwt.GenerateToken(uid, "budi@test.local", "requester", false, "dev-1", 5)
				rows := sqlmock.NewRows([]string{"token_version", "is_suspended", "deleted_at"}).
					AddRow(5, true, nil)
				mockSql.ExpectQuery(`(?i)SELECT.*token_version.*FROM.*users`).WillReturnRows(rows)
				app := fiber.New()
				app.Get("/protected", middleware.Protected(db, nil), func(c *fiber.Ctx) error {
					return c.SendString("ok")
				})
				req := httptest.NewRequest("GET", "/protected", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				resp, _ := app.Test(req, -1)
				assert.Equal(t, 403, resp.StatusCode)
			})
			t.Run("deleted menghasilkan ACCOUNT_DELETED", func(t *testing.T) {
				ensureConfig()
				db, mockSql := testutil.NewMockDB(t)
				uid := uuid.New()
				token, _ := jwt.GenerateToken(uid, "budi@test.local", "requester", false, "dev-1", 5)
				deletedAt := time.Now().Format(time.RFC3339)
				rows := sqlmock.NewRows([]string{"token_version", "is_suspended", "deleted_at"}).
					AddRow(5, false, &deletedAt)
				mockSql.ExpectQuery(`(?i)SELECT.*token_version.*FROM.*users`).WillReturnRows(rows)
				app := fiber.New()
				app.Get("/protected", middleware.Protected(db, nil), func(c *fiber.Ctx) error {
					return c.SendString("ok")
				})
				req := httptest.NewRequest("GET", "/protected", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				resp, _ := app.Test(req, -1)
				assert.Equal(t, 401, resp.StatusCode)
			})
			t.Run("token version berbeda menghasilkan SESSION_EXPIRED", func(t *testing.T) {
				ensureConfig()
				db, mockSql := testutil.NewMockDB(t)
				uid := uuid.New()
				token, _ := jwt.GenerateToken(uid, "budi@test.local", "requester", false, "dev-1", 1)
				rows := sqlmock.NewRows([]string{"token_version", "is_suspended", "deleted_at"}).
					AddRow(5, false, nil)
				mockSql.ExpectQuery(`(?i)SELECT.*token_version.*FROM.*users`).WillReturnRows(rows)
				app := fiber.New()
				app.Get("/protected", middleware.Protected(db, nil), func(c *fiber.Ctx) error {
					return c.SendString("ok")
				})
				req := httptest.NewRequest("GET", "/protected", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				resp, _ := app.Test(req, -1)
				assert.Equal(t, 401, resp.StatusCode)
			})
			t.Run("tanpa token ditolak", func(t *testing.T) {
				ensureConfig()
				db, _ := testutil.NewMockDB(t)
				app := fiber.New()
				app.Get("/protected", middleware.Protected(db, nil), func(c *fiber.Ctx) error {
					return c.SendString("ok")
				})
				req := httptest.NewRequest("GET", "/protected", nil)
				resp, _ := app.Test(req, -1)
				assert.Equal(t, 401, resp.StatusCode)
			})
		})
	})
}
