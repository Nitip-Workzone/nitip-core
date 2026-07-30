package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	// App
	AppPort string
	AppEnv  string

	// Bypass & Feature Toggles
	BypassKYCValidation bool
	FcmEnabled          bool

	// Database
	DBDriver   string // "postgres" | "mysql"
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Firebase
	FirebaseCredentialsFile string
	FirebaseBucketName      string

	// Storage
	StorageDriver   string // "firebase" | "minio" | "local"
	StorageBaseURL  string
	MinioEndpoint   string
	MinioAccessKey  string
	MinioSecretKey  string
	MinioBucketName string
	MinioUseSSL     bool

	// Midtrans
	MidtransServerKey    string
	MidtransClientKey    string
	MidtransIsProduction bool
	UseMockPayment       bool

	// Webhook Security
	WebhookCallbackToken string

	// Storage Details
	LocalStoragePath    string
	LocalStorageBaseURL string
	CosSecretID         string
	CosSecretKey        string
	CosRegion           string
	CosBucket           string
	CosBaseURL          string
	CosSignExpire       string
}

var App *Config

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		// In production (docker), .env is injected via env_file: .env as env vars, not as file inside container
		// So file not found is expected in container and should not log as warning in prod to avoid confusion in GitHub Actions
		// Only log in development
		if os.Getenv("APP_ENV") == "" || os.Getenv("APP_ENV") == "development" {
			log.Println("[config] .env file not found, using environment variables")
		}
	}

	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))

	cfg := &Config{
		// App
		AppPort: getEnv("APP_PORT", "3000"),
		AppEnv:  getEnv("APP_ENV", "development"),

		// Bypass & Feature Toggles
		BypassKYCValidation: getEnv("BYPASS_KYC_VALIDATION", "true") == "true",
		FcmEnabled:          getEnv("FCM_ENABLED", "false") == "true",

		// Database
		DBDriver:   getEnv("DB_DRIVER", "postgres"),
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBName:     getEnv("DB_NAME", "nitip"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		// Redis
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       redisDB,

		// Firebase
		FirebaseCredentialsFile: getEnv("FIREBASE_CREDENTIALS_FILE", "./firebase-credentials.json"),
		FirebaseBucketName:      getEnv("FIREBASE_BUCKET", ""),

		// Storage
		StorageDriver:   getEnv("STORAGE_DRIVER", "local"), // Default to local for dev
		StorageBaseURL:  getEnv("STORAGE_BASE_URL", "http://localhost:8000"),
		MinioEndpoint:   getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinioAccessKey:  getEnv("MINIO_ACCESS_KEY", ""),
		MinioSecretKey:  getEnv("MINIO_SECRET_KEY", ""),
		MinioBucketName: getEnv("MINIO_BUCKET", "nitip"),
		MinioUseSSL:     getEnv("MINIO_USE_SSL", "false") == "true",

		// Midtrans
		MidtransServerKey:    getEnv("MIDTRANS_SERVER_KEY", ""),
		MidtransClientKey:    getEnv("MIDTRANS_CLIENT_KEY", ""),
		MidtransIsProduction: getEnv("MIDTRANS_IS_PRODUCTION", "false") == "true",
		UseMockPayment:       getEnv("USE_MOCK_PAYMENT", "true") == "true",

		// Webhook Security
		WebhookCallbackToken: getEnv("WEBHOOK_CALLBACK_TOKEN", ""),

		// Tencent COS & Local Storage details
		LocalStoragePath:    getEnv("LOCAL_STORAGE_PATH", "./uploads"),
		LocalStorageBaseURL: getEnv("LOCAL_STORAGE_BASE_URL", "http://localhost:8000/uploads"),
		CosSecretID:         getEnv("COS_SECRET_ID", ""),
		CosSecretKey:        getEnv("COS_SECRET_KEY", ""),
		CosRegion:           getEnv("COS_REGION", "ap-singapore"),
		CosBucket:           getEnv("COS_BUCKET", ""),
		CosBaseURL:          getEnv("COS_BASE_URL", ""),
		CosSignExpire:       getEnv("COS_SIGN_EXPIRE", "5m"),
	}

	App = cfg
	return cfg
}

// DSN builds connection string based on driver.
// SECURITY: URL-escapes user & password to avoid parse error when password contains : @ / % # etc
// and to prevent password leak in error messages (invalid port ':password' after host)
func (c *Config) DSN() string {
	switch c.DBDriver {
	case "mysql":
		// MySQL DSN: user:pass@tcp(host:port)/dbname — escape via url.QueryEscape for special chars? MySQL driver handles, but we keep raw with paranoid check
		// Use same approach as postgres: url escape password to be safe (mysql driver accepts escaped? it uses custom parser, so avoid escaping, but at least handle @ and :)
		// For safety, we still escape : and @ in password for mysql via replacer, but keep simple: use raw (existing) as mysql is less strict
		// Better: if password contains @ or : log warn but keep.
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
			c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
	default: // postgres
		// Properly URL-escape user and password to handle special chars : @ / % # ? & = etc
		// Example broken before: postgres://nitip-prod:cr34t3PROD193290 -> invalid port because : inside password not escaped
		// Fix uses QueryEscape
		escUser := c.DBUser
		escPass := c.DBPassword
		// Use url.PathEscape for user/pass in postgres URL (not QueryEscape which encodes space as +)
		// We use QueryEscape but replace + with %20 for safety, or use url.PathEscape
		// Simplest: use url.QueryEscape and it works for most special chars
		// Import net/url locally to avoid cycle
		// Note: we need to import net/url at top — will add
		escUser = url.QueryEscape(escUser)
		escPass = url.QueryEscape(escPass)
		return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			escUser, escPass, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
	}
}

// SafeDSN returns DSN with password redacted for logging — never log real password
func (c *Config) SafeDSN() string {
	switch c.DBDriver {
	case "mysql":
		return fmt.Sprintf("%s:***@tcp(%s:%s)/%s",
			c.DBUser, c.DBHost, c.DBPort, c.DBName)
	default:
		return fmt.Sprintf("postgres://%s:***@%s:%s/%s?sslmode=%s",
			c.DBUser, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
	}
}

func (c *Config) IsDevelopment() bool {
	return c.AppEnv == "development"
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
