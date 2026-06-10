package jwt

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var defaultSecret = "nitip-dev-secret-do-not-use-in-prod" // Only used in non-production environments

// CustomClaims holds custom data inside JWT
type CustomClaims struct {
	UserID       uuid.UUID `json:"user_id"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	IsVerified   bool      `json:"is_verified"`
	DeviceId     string    `json:"device_id"`
	TokenVersion int       `json:"tv"`
	jwt.RegisteredClaims
}

func getJWTSecret() (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		if os.Getenv("APP_ENV") == "production" {
			return "", errors.New("JWT_SECRET must be set in production")
		}
		secret = defaultSecret
	}
	return secret, nil
}

// GenerateToken creates a new JWT string for the user (Access Token)
func GenerateToken(userID uuid.UUID, email, role string, isVerified bool, deviceID string, tokenVersion int) (string, error) {
	return generate(userID, email, role, isVerified, deviceID, tokenVersion, 24*time.Hour)
}

// GenerateRefreshToken creates a new long-lived refresh token
func GenerateRefreshToken(userID uuid.UUID, deviceID string, tokenVersion int) (string, error) {
	return generate(userID, "", "", false, deviceID, tokenVersion, 7*24*time.Hour)
}

func generate(userID uuid.UUID, email, role string, isVerified bool, deviceID string, tokenVersion int, expiry time.Duration) (string, error) {
	secret, err := getJWTSecret()
	if err != nil {
		return "", err
	}

	claims := CustomClaims{
		UserID:       userID,
		Email:        email,
		Role:         role,
		IsVerified:   isVerified,
		DeviceId:     deviceID,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "nitip",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken validates and extracts claims from a token string
func ParseToken(tokenStr string) (*CustomClaims, error) {
	secret, err := getJWTSecret()
	if err != nil {
		return nil, err
	}

	token, err := jwt.ParseWithClaims(tokenStr, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}
