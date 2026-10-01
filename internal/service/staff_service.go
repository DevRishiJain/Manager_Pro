package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/audit"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username/employee ID or password")
	ErrStaffInactive      = errors.New("staff account is inactive")
	ErrStaffEmailExists   = errors.New("staff member with this email already exists")
	ErrInvalidRole        = errors.New("invalid staff role")
)

type StaffService struct {
	repo      storage.Repository
	jwtSecret []byte
}

func NewStaffService(repo storage.Repository, jwtSecret []byte) *StaffService {
	return &StaffService{
		repo:      repo,
		jwtSecret: jwtSecret,
	}
}

// GenerateEmployeeID creates a clean, predictable employee ID per restaurant and role.
// Format: EMP-<ROLE_CODE>-<3_DIGIT_SEQ>, e.g. EMP-WTR-001, EMP-MGR-002
func (s *StaffService) GenerateEmployeeID(ctx context.Context, restaurantID uuid.UUID, role restaurant.Role) (string, error) {
	staffList, err := s.repo.ListStaff(ctx, restaurantID)
	if err != nil {
		return "", err
	}

	prefix := "STF"
	switch role {
	case restaurant.RoleWaiter:
		prefix = "WTR"
	case restaurant.RoleManager:
		prefix = "MGR"
	case restaurant.RoleCashier:
		prefix = "CSH"
	case restaurant.RoleKitchen:
		prefix = "KIT"
	case restaurant.RoleRestaurantAdmin:
		prefix = "ADM"
	case restaurant.RoleRestaurantOwner:
		prefix = "OWN"
	case restaurant.RoleGuard:
		prefix = "GRD"
	}

	count := 0
	for _, st := range staffList {
		if st.Role == role {
			count++
		}
	}

	return fmt.Sprintf("EMP-%s-%03d", prefix, count+1), nil
}

type CreateStaffInput struct {
	RestaurantID uuid.UUID       `json:"restaurant_id"`
	Name         string          `json:"name"`
	Phone        string          `json:"phone"`
	Email        string          `json:"email"`
	Password     string          `json:"password"`
	Role         restaurant.Role `json:"role"`
}

func (s *StaffService) CreateStaff(ctx context.Context, input CreateStaffInput, creatorStaffID uuid.UUID) (*restaurant.StaffUser, error) {
	if strings.TrimSpace(input.Email) == "" {
		return nil, errors.New("email is required")
	}
	if strings.TrimSpace(input.Password) == "" {
		return nil, errors.New("password is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("name is required")
	}

	// Check if email already registered
	existing, _ := s.repo.GetStaffByEmail(ctx, strings.ToLower(strings.TrimSpace(input.Email)))
	if existing != nil {
		return nil, ErrStaffEmailExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	empID, err := s.GenerateEmployeeID(ctx, input.RestaurantID, input.Role)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	staff := &restaurant.StaffUser{
		ID:           uuid.New(),
		RestaurantID: input.RestaurantID,
		EmployeeID:   empID,
		Name:         strings.TrimSpace(input.Name),
		Phone:        strings.TrimSpace(input.Phone),
		Email:        strings.ToLower(strings.TrimSpace(input.Email)),
		PasswordHash: string(hash),
		Role:         input.Role,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.CreateStaff(ctx, staff); err != nil {
		return nil, err
	}

	// Append audit log for staff creation
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeStaff,
		ActorID:      creatorStaffID.String(),
		RestaurantID: staff.RestaurantID,
		Action:       "STAFF_CREATED",
		CreatedAt:    now,
	})

	_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
		ID:           uuid.New(),
		StaffID:      creatorStaffID,
		RestaurantID: staff.RestaurantID,
		ActionType:   "STAFF_CREATED",
		Reason:       fmt.Sprintf("Created %s (%s, %s)", staff.Name, staff.Role, staff.EmployeeID),
		CreatedAt:    now,
	})

	return staff, nil
}

type StaffAuthResult struct {
	Token string                `json:"token"`
	Staff *restaurant.StaffUser `json:"staff"`
}

// Authenticate verifies credentials against staff_users (by email/username or by employee_id).
func (s *StaffService) Authenticate(ctx context.Context, identifier, password string, restaurantID *uuid.UUID) (*StaffAuthResult, error) {
	trimmedIdentifier := strings.TrimSpace(identifier)
	if trimmedIdentifier == "" || password == "" {
		return nil, ErrInvalidCredentials
	}

	var staff *restaurant.StaffUser
	var err error

	// 1. If restaurant ID provided, check restaurant-specific employee ID first
	if restaurantID != nil && *restaurantID != uuid.Nil {
		staff, err = s.repo.GetStaffByEmployeeID(ctx, *restaurantID, strings.ToUpper(trimmedIdentifier))
	}

	// 2. Try finding by email
	if staff == nil {
		staff, err = s.repo.GetStaffByEmail(ctx, strings.ToLower(trimmedIdentifier))
	}

	// 3. Try finding by staff name or email username within restaurant
	if staff == nil && restaurantID != nil && *restaurantID != uuid.Nil {
		staffList, _ := s.repo.ListStaff(ctx, *restaurantID)
		lowerID := strings.ToLower(trimmedIdentifier)

		for _, st := range staffList {
			cleanName := strings.ToLower(strings.TrimSpace(st.Name))
			// Remove parenthetical annotations like "(Floor Waiter)", "(Head Chef)"
			if idx := strings.Index(cleanName, "("); idx != -1 {
				cleanName = strings.TrimSpace(cleanName[:idx])
			}

			// Full name match (e.g. "Aman Verma")
			if cleanName == lowerID {
				cpy := st
				staff = &cpy
				break
			}

			// First name match (e.g. "Aman")
			fields := strings.Fields(cleanName)
			if len(fields) > 0 && fields[0] == lowerID {
				cpy := st
				staff = &cpy
				break
			}

			// Any single word in name match (e.g. "Rajesh" in "Chef Rajesh")
			for _, word := range fields {
				if word == lowerID {
					cpy := st
					staff = &cpy
					break
				}
			}
			if staff != nil {
				break
			}

			// Email prefix match (e.g. "aman.waiter" from "aman.waiter@...")
			emailPrefix := strings.ToLower(strings.Split(st.Email, "@")[0])
			if emailPrefix == lowerID || strings.HasPrefix(emailPrefix, lowerID) {
				cpy := st
				staff = &cpy
				break
			}
		}
	}

	// 4. Fallback: Check global employee ID across the system
	if staff == nil {
		staff, err = s.repo.GetStaffByEmployeeIDGlobal(ctx, strings.ToUpper(trimmedIdentifier))
	}

	if staff == nil {
		return nil, ErrInvalidCredentials
	}

	// Tenancy check: Ensure staff belongs to the specified restaurant (unless platform super admin)
	if restaurantID != nil && *restaurantID != uuid.Nil && staff.Role != restaurant.RoleSuperAdmin {
		if staff.RestaurantID != *restaurantID {
			return nil, ErrInvalidCredentials
		}
	}

	if !staff.IsActive {
		return nil, ErrStaffInactive
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	isPlatform := (staff.Role == restaurant.RoleSuperAdmin)
	token, err := crypto.GenerateFullStaffJWT(
		s.jwtSecret,
		staff.ID,
		staff.RestaurantID,
		staff.EmployeeID,
		staff.Name,
		string(staff.Role),
		isPlatform,
		24*time.Hour,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to issue staff token: %w", err)
	}

	// Append login audit
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeStaff,
		ActorID:      staff.ID.String(),
		RestaurantID: staff.RestaurantID,
		Action:       "STAFF_LOGIN",
		CreatedAt:    time.Now(),
	})

	return &StaffAuthResult{
		Token: token,
		Staff: staff,
	}, nil
}

// UpdateStaffPassword allows an authorized manager/admin to change an employee's password.
func (s *StaffService) UpdateStaffPassword(ctx context.Context, staffID uuid.UUID, newPassword string, requesterStaffID uuid.UUID) error {
	trimmed := strings.TrimSpace(newPassword)
	if trimmed == "" {
		return errors.New("new password cannot be empty")
	}
	if len(trimmed) < 6 {
		return errors.New("password must be at least 6 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(trimmed), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	staff, err := s.repo.GetStaffByID(ctx, staffID)
	if err != nil || staff == nil {
		return errors.New("staff member not found")
	}

	if err := s.repo.UpdateStaffPassword(ctx, staffID, string(hash)); err != nil {
		return err
	}

	now := time.Now()
	_ = s.repo.AppendAuditLog(ctx, &audit.AuditLog{
		ID:           uuid.New(),
		ActorType:    audit.ActorTypeStaff,
		ActorID:      requesterStaffID.String(),
		RestaurantID: staff.RestaurantID,
		Action:       "STAFF_PASSWORD_UPDATED",
		CreatedAt:    now,
	})

	_ = s.repo.AppendStaffAction(ctx, &audit.StaffAction{
		ID:           uuid.New(),
		StaffID:      requesterStaffID,
		RestaurantID: staff.RestaurantID,
		ActionType:   "STAFF_PASSWORD_RESET",
		Reason:       fmt.Sprintf("Reset password for %s (%s)", staff.Name, staff.EmployeeID),
		CreatedAt:    now,
	})

	return nil
}
