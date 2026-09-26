package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/devrishijain/table-manager/internal/domain/session"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

type contextKey string

const (
	SessionContextKey contextKey = "dining_session"
	StaffContextKey   contextKey = "staff_claims"
	GuardContextKey   contextKey = "guard_claims"
)

// CustomerAuth validates opaque session token from X-Session-Token or Authorization Bearer.
func CustomerAuth(repo storage.Repository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("X-Session-Token")
			if token == "" {
				authHeader := r.Header.Get("Authorization")
				if strings.HasPrefix(authHeader, "Bearer ") {
					token = strings.TrimPrefix(authHeader, "Bearer ")
				}
			}

			if token == "" {
				http.Error(w, `{"error":"missing session token"}`, http.StatusUnauthorized)
				return
			}

			sess, err := repo.GetSessionByToken(r.Context(), token)
			if err != nil || sess == nil {
				http.Error(w, `{"error":"invalid or expired session token"}`, http.StatusUnauthorized)
				return
			}

			if sess.Status.IsTerminal() {
				// Allow read-only GET requests so diners can view completed session, settled bill, and gatepass clearance
				if r.Method == http.MethodGet {
					ctx := context.WithValue(r.Context(), SessionContextKey, sess)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				http.Error(w, `{"error":"session has closed"}`, http.StatusGone)
				return
			}

			ctx := context.WithValue(r.Context(), SessionContextKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// StaffAuth validates Staff JWT.
func StaffAuth(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"missing or invalid authorization header"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := crypto.ParseStaffJWT(jwtSecret, tokenStr)
			if err != nil || claims.StaffID == uuid.Nil || claims.Role == "" {
				http.Error(w, `{"error":"unauthorized staff token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), StaffContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// StaffAuthOptional parses Staff JWT if present in Authorization header, without rejecting unauthenticated requests.
func StaffAuthOptional(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
				claims, err := crypto.ParseStaffJWT(jwtSecret, tokenStr)
				if err == nil && claims != nil && claims.StaffID != uuid.Nil {
					ctx := context.WithValue(r.Context(), StaffContextKey, claims)
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole checks that staff has one of the required roles.
func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	roleMap := make(map[string]bool)
	for _, role := range allowedRoles {
		roleMap[role] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(StaffContextKey).(*crypto.StaffClaims)
			if !ok || claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			if !roleMap[claims.Role] && claims.Role != "SUPER_ADMIN" {
				http.Error(w, `{"error":"forbidden: insufficient role permissions"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GuardAuth strictly validates Guard JWT for exit verification.
func GuardAuth(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"missing or invalid guard token"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := crypto.ParseGuardJWT(jwtSecret, tokenStr)
			if err != nil || claims.GuardID == uuid.Nil {
				http.Error(w, `{"error":"unauthorized guard token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), GuardContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PlatformAdminAuth strictly enforces that only SUPER_ADMIN platform tokens can access /admin/* routes.
// Restaurant-scoped tokens fail closed (§7.11, §9.21).
func PlatformAdminAuth(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, `{"error":"missing platform admin authorization"}`, http.StatusUnauthorized)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			claims, err := crypto.ParseStaffJWT(jwtSecret, tokenStr)
			if err != nil || !claims.IsPlatform || claims.Role != "SUPER_ADMIN" {
				http.Error(w, `{"error":"forbidden: platform super-admin credentials required"}`, http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), StaffContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// IdempotencyMiddleware ensures that retrying with the same Idempotency-Key returns cached response,
// but modifying the request payload under the same key returns HTTP 409 Conflict (§9.17).
type idempotencyRecord struct {
	BodyHash   string
	StatusCode int
	Headers    http.Header
	Body       []byte
	InProgress bool
}

type IdempotencyManager struct {
	mu      sync.RWMutex
	records map[string]*idempotencyRecord
}

func NewIdempotencyManager() *IdempotencyManager {
	return &IdempotencyManager{records: make(map[string]*idempotencyRecord)}
}

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	headers    http.Header
}

func (r *responseRecorder) Header() http.Header {
	return r.ResponseWriter.Header()
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (m *IdempotencyManager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Read and clone body for hashing
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			hash := sha256.Sum256(bodyBytes)
			hashHex := hex.EncodeToString(hash[:])

			m.mu.Lock()
			rec, exists := m.records[key]
			if exists {
				if rec.BodyHash != hashHex {
					m.mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":"idempotency conflict: request payload differs for existing idempotency key","code":"IDEMPOTENCY_KEY_REUSED"}`))
					return
				}

				// If the initial request is still processing, signal concurrent in-flight
				if rec.InProgress {
					m.mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":"request with this idempotency key is currently in progress","code":"IDEMPOTENCY_KEY_IN_PROGRESS"}`))
					return
				}

				// Replay the exact original cached response!
				statusCode := rec.StatusCode
				cachedHeaders := rec.Headers.Clone()
				cachedBody := make([]byte, len(rec.Body))
				copy(cachedBody, rec.Body)
				m.mu.Unlock()

				for k, vals := range cachedHeaders {
					for _, v := range vals {
						w.Header().Add(k, v)
					}
				}
				w.Header().Set("X-Cache", "IDEMPOTENT-REPLAY")
				if statusCode != 0 {
					w.WriteHeader(statusCode)
				}
				_, _ = w.Write(cachedBody)
				return
			}

			// First time seeing this key: mark as in-progress
			rec = &idempotencyRecord{
				BodyHash:   hashHex,
				InProgress: true,
			}
			m.records[key] = rec
			m.mu.Unlock()

			// Intercept response to store status, headers, and body
			recWriter := &responseRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK, // default if WriteHeader not explicitly called
			}

			next.ServeHTTP(recWriter, r)

			m.mu.Lock()
			rec.StatusCode = recWriter.statusCode
			rec.Headers = recWriter.Header().Clone()
			rec.Body = recWriter.body.Bytes()
			rec.InProgress = false
			m.mu.Unlock()
		})
	}
}

const (
	CorrelationIDContextKey contextKey = "correlation_id"
	RequestIDContextKey     contextKey = "request_id"
)

// CorrelationMiddleware injects X-Request-ID and X-Correlation-ID headers and populates context.
func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		corrID := r.Header.Get("X-Correlation-ID")
		if corrID == "" {
			corrID = reqID
		}

		w.Header().Set("X-Request-ID", reqID)
		w.Header().Set("X-Correlation-ID", corrID)

		ctx := context.WithValue(r.Context(), RequestIDContextKey, reqID)
		ctx = context.WithValue(ctx, CorrelationIDContextKey, corrID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SecurityHeadersMiddleware applies production security headers (§9.12, §10).
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

func GetSessionFromContext(ctx context.Context) (*session.DiningSession, bool) {
	s, ok := ctx.Value(SessionContextKey).(*session.DiningSession)
	return s, ok
}

func GetStaffClaimsFromContext(ctx context.Context) (*crypto.StaffClaims, bool) {
	c, ok := ctx.Value(StaffContextKey).(*crypto.StaffClaims)
	return c, ok
}

func GetGuardClaimsFromContext(ctx context.Context) (*crypto.GuardClaims, bool) {
	g, ok := ctx.Value(GuardContextKey).(*crypto.GuardClaims)
	return g, ok
}

// TenantScopeValidator ensures that restaurant staff can only access resources belonging to their own restaurant_id.
// Cross-tenant probing returns HTTP 404 or 403 (§9.18).
func ValidateTenantScope(ctx context.Context, targetRestaurantID uuid.UUID) bool {
	claims, ok := GetStaffClaimsFromContext(ctx)
	if !ok || claims == nil {
		return false
	}
	if claims.IsPlatform {
		return true // Platform admin can view all
	}
	return claims.RestaurantID == targetRestaurantID
}
