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

	// 1. Try finding by email
	staff, err = s.repo.GetStaffByEmail(ctx, strings.ToLower(trimmedIdentifier))
	if err != nil || staff == nil {
		// 2. If restaurant ID provided or identifier looks like an Employee ID (e.g. EMP-...)
		if restaurantID != nil && *restaurantID != uuid.Nil {
			staff, err = s.repo.GetStaffByEmployeeID(ctx, *restaurantID, strings.ToUpper(trimmedIdentifier))
		}
	}

	if staff == nil {
		return nil, ErrInvalidCredentials
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
