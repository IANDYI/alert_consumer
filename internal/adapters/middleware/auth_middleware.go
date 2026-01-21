package middleware

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// cacheEntry stores cached JWT claims keyed by JTI (JWT ID)
type cacheEntry struct {
	claims jwt.MapClaims
	exp    int64
}

// AuthMiddleware handles JWT validation and RBAC enforcement
// Validates tokens signed by Identity Service using mounted public key
// Uses JTI-based caching for performance optimization
type AuthMiddleware struct {
	publicKey *rsa.PublicKey
	// L1 cache: in-memory cache keyed by JTI (JWT ID) for fast lookups
	cache sync.Map
	// Background janitor for cache cleanup
	janitorStop chan bool
}

const CacheCleanupInterval = 10 * time.Minute

// NewAuthMiddleware creates a new JWT authentication middleware
// publicKey: RSA public key from Identity Service (mounted via ConfigMap)
func NewAuthMiddleware(publicKey *rsa.PublicKey) *AuthMiddleware {
	m := &AuthMiddleware{
		publicKey:   publicKey,
		janitorStop: make(chan bool),
	}

	// Start background janitor to sweep L1 cache periodically
	go m.startJanitor(CacheCleanupInterval)

	return m
}

// Context keys for storing user information
type contextKey string

const (
	UserIDKey     contextKey = "userID"
	RoleKey       contextKey = "role"
	TokenKey      contextKey = "token"
	UserEmailKey  contextKey = "userEmail"
	UserFirstName contextKey = "userFirstName"
	UserLastName  contextKey = "userLastName"
)

// GetClaimsFromCacheOrParse extracts claims from cache or parses token
// Uses JTI (JWT ID) for cache keying instead of full token string
// Returns claims, JTI, and error
// Public method for use in WebSocket handlers and other contexts
func (m *AuthMiddleware) GetClaimsFromCacheOrParse(tokenString string) (jwt.MapClaims, string, error) {
	// Peek at the JTI without verifying the signature yet (performance optimization)
	parser := new(jwt.Parser)
	unverifiedToken, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, "", err
	}

	claims, ok := unverifiedToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, "", errors.New("invalid token claims")
	}

	// Extract JTI (JWT ID) - use it as cache key
	jti, _ := claims["jti"].(string)
	if jti == "" {
		// Fallback: if no JTI, use a hash of the token (less efficient but works)
		// In production, tokens should always have JTI
		// Use a more unique key: first 32 chars + role + userID to avoid collisions
		role, _ := claims["role"].(string)
		userID, _ := claims["sub"].(string)
		jti = fmt.Sprintf("%s-%s-%s", tokenString[:min(20, len(tokenString))], role, userID[:min(8, len(userID))])
		log.Printf("Token missing JTI, using fallback key: %s (role: %s, userID: %s)", jti[:min(30, len(jti))], role, userID)
	}

	// Extract expiration for early validation
	var exp int64
	if expFloat, ok := claims["exp"].(float64); ok {
		exp = int64(expFloat)
	} else if expInt, ok := claims["exp"].(int64); ok {
		exp = expInt
	} else {
		return nil, "", errors.New("missing expiration claim")
	}

	// Immediate expiry check (fastest fail path)
	if time.Now().Unix() > exp {
		return nil, "", errors.New("token expired")
	}

	// L1 Cache Lookup (Keyed by JTI)
	if entry, ok := m.cache.Load(jti); ok {
		cached := entry.(cacheEntry)
		// Double-check expiration
		if time.Now().Unix() < cached.exp {
			// Log cache hit for debugging
			if cachedRole, ok := cached.claims["role"].(string); ok {
				log.Printf("Token cache hit - JTI: %s, Role: %s", jti[:min(20, len(jti))], cachedRole)
			}
			return cached.claims, jti, nil
		}
		// Expired, remove from cache
		m.cache.Delete(jti)
	}

	// Full RSA Validation (Cold path - only when cache miss)
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return m.publicKey, nil
	})

	if err != nil {
		return nil, "", err
	}

	if !token.Valid {
		return nil, "", jwt.ErrSignatureInvalid
	}

	// Extract claims from verified token (not unverified)
	verifiedClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, "", errors.New("invalid token claims")
	}

	// Store verified claims in cache for future requests
	m.cache.Store(jti, cacheEntry{claims: verifiedClaims, exp: exp})

	return verifiedClaims, jti, nil
}

// Authenticate validates JWT token and extracts claims
// Returns userID and role, or error if token is invalid
// Maintains backward compatibility with existing code
func (m *AuthMiddleware) Authenticate(tokenString string) (userID string, role string, err error) {
	claims, _, err := m.GetClaimsFromCacheOrParse(tokenString)
	if err != nil {
		return "", "", err
	}

	// Extract user ID (sub claim)
	userIDClaim, ok := claims["sub"].(string)
	if !ok || userIDClaim == "" {
		return "", "", errors.New("missing or invalid user ID claim")
	}

	// Extract role
	roleClaim, ok := claims["role"].(string)
	if !ok || roleClaim == "" {
		return "", "", errors.New("missing or invalid role claim")
	}

	return userIDClaim, roleClaim, nil
}

// RequireAuth is middleware that validates JWT token from Authorization header
// Adds userID and role to request context
func (m *AuthMiddleware) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			log.Printf("Missing Authorization header")
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		// Support both "Bearer token" and "Bearer token" formats
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			// Try splitting if TrimPrefix didn't work
			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				log.Printf("Invalid Authorization header format")
				http.Error(w, "invalid authorization header", http.StatusUnauthorized)
				return
			}
			tokenString = parts[1]
		}

		// Get claims from cache or parse
		claims, jti, err := m.GetClaimsFromCacheOrParse(tokenString)
		if err != nil {
			log.Printf("Token validation failed: %v", err)
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		// Extract user ID and role
		userID, ok := claims["sub"].(string)
		if !ok || userID == "" {
			log.Printf("Missing or invalid 'sub' claim")
			http.Error(w, "invalid token: missing user ID", http.StatusUnauthorized)
			return
		}

		userRole, ok := claims["role"].(string)
		if !ok || userRole == "" {
			log.Printf("Missing or invalid 'role' claim")
			http.Error(w, "invalid token: missing role", http.StatusUnauthorized)
			return
		}

		log.Printf("Token validated - UserID: %s, Role: %s, JTI: %s (processing time: %v)", userID, userRole, jti, time.Since(start))

		// Extract optional user details from claims
		email, _ := claims["email"].(string)
		firstName, _ := claims["first_name"].(string)
		lastName, _ := claims["last_name"].(string)

		// Add to context
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		ctx = context.WithValue(ctx, RoleKey, userRole)
		ctx = context.WithValue(ctx, TokenKey, tokenString)
		ctx = context.WithValue(ctx, UserEmailKey, email)
		ctx = context.WithValue(ctx, UserFirstName, firstName)
		ctx = context.WithValue(ctx, UserLastName, lastName)

		next(w, r.WithContext(ctx))
	}
}

// RequireRole enforces role-based access control
// Only allows access if user has the required role
// Maintains backward compatibility: accepts single string role
func (m *AuthMiddleware) RequireRole(requiredRole string, next http.HandlerFunc) http.HandlerFunc {
	return m.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		role, ok := GetRole(r.Context())
		if !ok {
			log.Printf("Missing role in context")
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if role != requiredRole {
			log.Printf("Role mismatch: required %s, got %s", requiredRole, role)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		next(w, r)
	})
}

// RequireAnyRole enforces role-based access control with multiple allowed roles
// Allows access if user has any of the required roles
func (m *AuthMiddleware) RequireAnyRole(allowedRoles []string, next http.HandlerFunc) http.HandlerFunc {
	return m.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		role, ok := GetRole(r.Context())
		if !ok {
			log.Printf("Missing role in context")
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		authorized := false
		for _, allowedRole := range allowedRoles {
			if role == allowedRole {
				authorized = true
				break
			}
		}

		if !authorized {
			log.Printf("Role mismatch: required one of %v, got %s", allowedRoles, role)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		next(w, r)
	})
}

// startJanitor periodically cleans up expired cache entries
func (m *AuthMiddleware) startJanitor(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now().Unix()
			deleted := 0
			m.cache.Range(func(key, value interface{}) bool {
				if entry, ok := value.(cacheEntry); ok && now >= entry.exp {
					m.cache.Delete(key)
					deleted++
				}
				return true
			})
			if deleted > 0 {
				log.Printf("L1 Cache Janitor: Purged %d expired entries", deleted)
			}
		case <-m.janitorStop:
			return
		}
	}
}

// Stop stops the background janitor (for graceful shutdown)
func (m *AuthMiddleware) Stop() {
	close(m.janitorStop)
}

// GetUserID extracts user ID from request context
func GetUserID(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(UserIDKey).(string)
	return userID, ok
}

// GetRole extracts role from request context
func GetRole(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(RoleKey).(string)
	return role, ok
}

// GetToken extracts token string from request context
func GetToken(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(TokenKey).(string)
	return token, ok
}

// IsAdmin checks if the user in context is an ADMIN
func IsAdmin(ctx context.Context) bool {
	role, ok := GetRole(ctx)
	return ok && role == "ADMIN"
}

// GetUserEmail extracts user email from request context
func GetUserEmail(ctx context.Context) (string, bool) {
	email, ok := ctx.Value(UserEmailKey).(string)
	return email, ok
}

// GetUserFirstName extracts user first name from request context
func GetUserFirstName(ctx context.Context) (string, bool) {
	firstName, ok := ctx.Value(UserFirstName).(string)
	return firstName, ok
}

// GetUserLastName extracts user last name from request context
func GetUserLastName(ctx context.Context) (string, bool) {
	lastName, ok := ctx.Value(UserLastName).(string)
	return lastName, ok
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

