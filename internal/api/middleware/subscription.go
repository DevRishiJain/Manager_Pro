package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

// SubscriptionGateMiddleware checks if a tenant's subscription is active for operational routes.
func SubscriptionGateMiddleware(repo storage.Repository, jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Never block auth, public, or subscription endpoints
			if strings.HasPrefix(path, "/api/v1/auth/") ||
				strings.HasPrefix(path, "/api/v1/staff/login") ||
				strings.HasSuffix(path, "/subscription") ||
				strings.HasSuffix(path, "/subscription/renew") ||
				strings.HasPrefix(path, "/api/v1/webhooks/") ||
				strings.HasPrefix(path, "/api/v1/public/") {
				next.ServeHTTP(w, r)
				return
			}

			var restID uuid.UUID
			if claims, ok := GetStaffClaimsFromContext(r.Context()); ok && claims != nil {
				if claims.IsPlatform {
					next.ServeHTTP(w, r)
					return
				}
				restID = claims.RestaurantID
			} else if sess, ok := GetSessionFromContext(r.Context()); ok && sess != nil {
				restID = sess.RestaurantID
			} else {
				// Parse JWT token from Authorization header if context isn't populated yet
				authHeader := r.Header.Get("Authorization")
				if strings.HasPrefix(authHeader, "Bearer ") {
					tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
					if claims, err := crypto.ParseStaffJWT(jwtSecret, tokenStr); err == nil && claims != nil {
						if claims.IsPlatform {
							next.ServeHTTP(w, r)
							return
						}
						restID = claims.RestaurantID
					}
				}
			}

			if restID != uuid.Nil {
				rest, err := repo.GetSubscription(r.Context(), restID)
				if err == nil && rest != nil {
					if !rest.IsSubscriptionActive() {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusPaymentRequired) // 402 Payment Required
						_ = json.NewEncoder(w).Encode(map[string]interface{}{
							"error":          "subscription_expired",
							"message":        "Restaurant subscription has expired. Please renew to restore full operational access.",
							"restaurant_id":  rest.ID,
							"days_remaining": rest.DaysRemaining(),
							"is_active":      false,
						})
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
