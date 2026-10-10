package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// EnsureSuperAdmin idempotently provisions the hidden platform restaurant and a
// SUPER_ADMIN staff account. It never overwrites an existing password.
func EnsureSuperAdmin(ctx context.Context, repo storage.Repository, email, password string) error {
	now := time.Now().UTC()

	// 1. Hidden platform restaurant (fixed ID, reserved slug)
	plat, err := repo.GetRestaurantByID(ctx, restaurant.PlatformRestaurantID)
	if err != nil || plat == nil {
		plat = &restaurant.Restaurant{
			ID:                 restaurant.PlatformRestaurantID,
			Name:               "TableOS Platform",
			Slug:               "tableos-platform",
			Theme:              "gold",
			Status:             restaurant.StatusActive,
			Timezone:           "Asia/Kolkata",
			SubscriptionPlan:   "PLATFORM",
			SubscriptionStatus: "ACTIVE",
			SubscriptionEndAt:  now.Add(100 * 365 * 24 * time.Hour),
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := repo.CreateRestaurant(ctx, plat); err != nil {
			return err
		}
	}

	settings := restaurant.DefaultSettings(restaurant.PlatformRestaurantID)
	_ = repo.UpdateSettings(ctx, &settings)

	// 2. SUPER_ADMIN staff — never overwrite an existing password
	existing, _ := repo.GetStaffByEmail(ctx, email)
	if existing != nil {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	empID := "EMP-SUP-001"
	if taken, _ := repo.GetStaffByEmployeeID(ctx, restaurant.PlatformRestaurantID, empID); taken != nil {
		empID = fmt.Sprintf("EMP-SUP-%03d", (time.Now().Unix()%900)+100)
		if taken2, _ := repo.GetStaffByEmployeeID(ctx, restaurant.PlatformRestaurantID, empID); taken2 != nil {
			empID = fmt.Sprintf("EMP-SUP-%s", strings.ToUpper(uuid.New().String()[:4]))
		}
	}

	admin := &restaurant.StaffUser{
		ID:           uuid.New(),
		RestaurantID: restaurant.PlatformRestaurantID,
		EmployeeID:   empID,
		Name:         "Platform Super Admin",
		Email:        email,
		PasswordHash: string(hash),
		Role:         restaurant.RoleSuperAdmin,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return repo.CreateStaff(ctx, admin)
}
