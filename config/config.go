package config

import (
	"fmt"
	"log"
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
	MinioEndpoint   string
	MinioAccessKey  string
	MinioSecretKey  string
	MinioBucketName string
	MinioUseSSL     bool
}

var App *Config

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("[config] .env file not found, using environment variables")
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
		MinioEndpoint:   getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinioAccessKey:  getEnv("MINIO_ACCESS_KEY", ""),
		MinioSecretKey:  getEnv("MINIO_SECRET_KEY", ""),
		MinioBucketName: getEnv("MINIO_BUCKET", "nitip"),
		MinioUseSSL:     getEnv("MINIO_USE_SSL", "false") == "true",
	}

	App = cfg
	return cfg
}

// DSN builds connection string based on driver.
func (c *Config) DSN() string {
	switch c.DBDriver {
	case "mysql":
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
			c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
	default: // postgres
		return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
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
