package middleware_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/IANDYI/alert-consumer/internal/adapters/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return privateKey, &privateKey.PublicKey
}

func createTestToken(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privateKey)
	require.NoError(t, err)
	return tokenString
}

func TestNewAuthMiddleware(t *testing.T) {
	_, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	assert.NotNil(t, mw)
}

func TestAuthMiddleware_GetClaimsFromCacheOrParse_ValidToken(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	resultClaims, jti, err := mw.GetClaimsFromCacheOrParse(tokenString)
	require.NoError(t, err)
	assert.NotNil(t, resultClaims)
	assert.Equal(t, "test-jti-123", jti)
	assert.Equal(t, "user123", resultClaims["sub"])
	assert.Equal(t, "ADMIN", resultClaims["role"])
}

func TestAuthMiddleware_GetClaimsFromCacheOrParse_CacheHit(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	// First call - should parse and cache
	claims1, jti1, err1 := mw.GetClaimsFromCacheOrParse(tokenString)
	require.NoError(t, err1)

	// Second call - should hit cache
	claims2, jti2, err2 := mw.GetClaimsFromCacheOrParse(tokenString)
	require.NoError(t, err2)

	assert.Equal(t, jti1, jti2)
	assert.Equal(t, claims1["sub"], claims2["sub"])
	assert.Equal(t, claims1["role"], claims2["role"])
}

func TestAuthMiddleware_GetClaimsFromCacheOrParse_ExpiredToken(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "ADMIN",
		"exp":  time.Now().Add(-time.Hour).Unix(), // Expired
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	_, _, err := mw.GetClaimsFromCacheOrParse(tokenString)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestAuthMiddleware_GetClaimsFromCacheOrParse_InvalidToken(t *testing.T) {
	_, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	_, _, err := mw.GetClaimsFromCacheOrParse("invalid-token")
	assert.Error(t, err)
}

func TestAuthMiddleware_Authenticate(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	userID, role, err := mw.Authenticate(tokenString)
	require.NoError(t, err)
	assert.Equal(t, "user123", userID)
	assert.Equal(t, "ADMIN", role)
}

func TestAuthMiddleware_RequireAuth(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":        "user123",
		"role":       "ADMIN",
		"email":      "test@example.com",
		"first_name": "John",
		"last_name":  "Doe",
		"exp":        time.Now().Add(time.Hour).Unix(),
		"jti":        "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	handler := mw.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := middleware.GetUserID(r.Context())
		assert.True(t, ok)
		assert.Equal(t, "user123", userID)

		role, ok := middleware.GetRole(r.Context())
		assert.True(t, ok)
		assert.Equal(t, "ADMIN", role)

		email, ok := middleware.GetUserEmail(r.Context())
		assert.True(t, ok)
		assert.Equal(t, "test@example.com", email)

		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_RequireAuth_MissingHeader(t *testing.T) {
	_, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	handler := mw.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_RequireRole(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	handler := mw.RequireRole("ADMIN", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_RequireRole_WrongRole(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "USER",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	handler := mw.RequireRole("ADMIN", func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAuthMiddleware_RequireAnyRole(t *testing.T) {
	privateKey, publicKey := generateTestKeyPair(t)
	mw := middleware.NewAuthMiddleware(publicKey)
	defer mw.Stop()

	claims := jwt.MapClaims{
		"sub":  "user123",
		"role": "ADMIN",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"jti":  "test-jti-123",
	}
	tokenString := createTestToken(t, privateKey, claims)

	handler := mw.RequireAnyRole([]string{"ADMIN", "NURSE"}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()

	handler(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetUserID(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, "user123")
	userID, ok := middleware.GetUserID(ctx)
	assert.True(t, ok)
	assert.Equal(t, "user123", userID)

	ctx2 := context.Background()
	_, ok2 := middleware.GetUserID(ctx2)
	assert.False(t, ok2)
}

func TestGetRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.RoleKey, "ADMIN")
	role, ok := middleware.GetRole(ctx)
	assert.True(t, ok)
	assert.Equal(t, "ADMIN", role)

	ctx2 := context.Background()
	_, ok2 := middleware.GetRole(ctx2)
	assert.False(t, ok2)
}

func TestIsAdmin(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.RoleKey, "ADMIN")
	assert.True(t, middleware.IsAdmin(ctx))

	ctx2 := context.WithValue(context.Background(), middleware.RoleKey, "USER")
	assert.False(t, middleware.IsAdmin(ctx2))

	ctx3 := context.Background()
	assert.False(t, middleware.IsAdmin(ctx3))
}
