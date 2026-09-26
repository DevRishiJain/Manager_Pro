package restaurant

import (
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "ACTIVE"
	StatusSuspended Status = "SUSPENDED"
	StatusPending   Status = "PENDING_ONBOARDING"
)

type ExitVerificationMode string

const (
	ExitVerificationModeNoGuard    ExitVerificationMode = "NO_EXIT_VERIFICATION"
	ExitVerificationModeGuardCheck ExitVerificationMode = "EXIT_GUARD_ENABLED"
)

type SharedSessionPolicy string

const (
	SharedSessionPolicySharedTable  SharedSessionPolicy = "SHARED_TABLE_SESSION"
	SharedSessionPolicySingleDevice SharedSessionPolicy = "SINGLE_DEVICE_SESSION"
)

type VenueType string

const (
	VenueTypeFineDine VenueType = "FINE_DINE"
	VenueTypeCafe     VenueType = "CAFE"
	VenueTypeHotel    VenueType = "HOTEL"
	VenueTypeDriveIn  VenueType = "DRIVE_IN"
)

type Role string

const (
	RoleSuperAdmin      Role = "SUPER_ADMIN"
	RoleRestaurantOwner Role = "RESTAURANT_OWNER"
	RoleRestaurantAdmin Role = "RESTAURANT_ADMIN"
	RoleManager         Role = "MANAGER"
	RoleCashier         Role = "CASHIER"
	RoleWaiter          Role = "WAITER"
	RoleKitchen         Role = "KITCHEN"
	RoleGuard           Role = "GUARD"
)

func (r Role) CanConfirmPayment() bool {
	return r == RoleCashier || r == RoleManager || r == RoleRestaurantAdmin || r == RoleRestaurantOwner
}

func (r Role) CanForceClose() bool {
	return r == RoleManager || r == RoleRestaurantAdmin || r == RoleRestaurantOwner
}

func (r Role) CanIssueRefund() bool {
	return r == RoleManager || r == RoleRestaurantAdmin || r == RoleRestaurantOwner
}

func (r Role) CanEditMenu() bool {
	return r == RoleRestaurantAdmin || r == RoleRestaurantOwner
}

type Restaurant struct {
	ID                    uuid.UUID   `json:"id"`
	Name                  string      `json:"name"`
	VenueType             VenueType   `json:"venue_type,omitempty"`
	GSTIN                 string      `json:"gstin"`
	CommissionRateBps     int64       `json:"commission_rate_bps"` // Default 100 = 1.00%
	SettlementBankDetails string      `json:"settlement_bank_details"`
	Status                Status      `json:"status"`
	Timezone              string      `json:"timezone"` // e.g. "Asia/Kolkata"
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type Table struct {
	ID           uuid.UUID `json:"id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	TableNumber  string    `json:"table_number"`
	TableToken   string    `json:"table_token"` // Opaque random string encoded in QR
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type StaffUser struct {
	ID           uuid.UUID `json:"id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	EmployeeID   string    `json:"employee_id"`
	Name         string    `json:"name"`
	Phone        string    `json:"phone"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type GuardUser struct {
	ID           uuid.UUID `json:"id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	Name         string    `json:"name"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type MenuCategory struct {
	ID           uuid.UUID `json:"id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	Name         string    `json:"name"`
	DisplayOrder int       `json:"display_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type MenuItem struct {
	ID           uuid.UUID   `json:"id"`
	RestaurantID uuid.UUID   `json:"restaurant_id"`
	CategoryID   uuid.UUID   `json:"category_id"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Price        money.Money `json:"price"`
	IsAvailable  bool        `json:"is_available"`
	HSNSACCode   string      `json:"hsn_sac_code"`  // e.g. "996331" for restaurant dining GST
	CGSTRateBps  int64       `json:"cgst_rate_bps"` // e.g. 250 for 2.5%
	SGSTRateBps  int64       `json:"sgst_rate_bps"` // e.g. 250 for 2.5%
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

type MenuItemVariant struct {
	ID            uuid.UUID   `json:"id"`
	MenuItemID    uuid.UUID   `json:"menu_item_id"`
	Name          string      `json:"name"`
	PriceOverride money.Money `json:"price_override"`
	IsAvailable   bool        `json:"is_available"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type RestaurantSettings struct {
	RestaurantID              uuid.UUID            `json:"restaurant_id"`
	ExitVerificationMode      ExitVerificationMode `json:"exit_verification_mode"`
	SharedSessionPolicy       SharedSessionPolicy  `json:"shared_session_policy"`
	HighValueThresholdMinor   int64                `json:"high_value_threshold_minor"`
	RapidOrderJumpFactor      int                  `json:"rapid_order_jump_factor"`
	ExternalEvidenceRequired  bool                 `json:"external_evidence_required"`
	POSEvidenceRequired       bool                 `json:"pos_evidence_required"`
	FirstOrderOTPTTLMinutes   int                  `json:"first_order_otp_ttl_minutes"`
	ExitPassOTPTTLMinutes     int                  `json:"exit_pass_otp_ttl_minutes"`
	UpdatedAt                 time.Time            `json:"updated_at"`
}

func DefaultSettings(restaurantID uuid.UUID) RestaurantSettings {
	return RestaurantSettings{
		RestaurantID:             restaurantID,
		ExitVerificationMode:     ExitVerificationModeNoGuard,
		SharedSessionPolicy:      SharedSessionPolicySharedTable,
		HighValueThresholdMinor:  500000, // 5000.00 INR
		RapidOrderJumpFactor:     3,      // 3x jump from initial order
		ExternalEvidenceRequired: true,
		POSEvidenceRequired:      false,
		FirstOrderOTPTTLMinutes:  15,
		ExitPassOTPTTLMinutes:    120,
		UpdatedAt:                time.Now(),
	}
}

type OnboardingStep int

const (
	StepProfileSetup OnboardingStep = 1
	StepTableSetup   OnboardingStep = 2
	StepMenuSetup    OnboardingStep = 3
	StepStaffSetup   OnboardingStep = 4
	StepPaymentSetup OnboardingStep = 5
	StepPolicySetup  OnboardingStep = 6
	StepTestOrder    OnboardingStep = 7
	StepGoLive       OnboardingStep = 8
)

type RestaurantOnboarding struct {
	RestaurantID            uuid.UUID        `json:"restaurant_id"`
	CurrentStep             OnboardingStep   `json:"current_step"`
	StepsCompleted          []OnboardingStep `json:"steps_completed"`
	StartedAt               time.Time        `json:"started_at"`
	CompletedAt             *time.Time       `json:"completed_at,omitempty"`
	AssignedPlatformContact *string          `json:"assigned_platform_contact,omitempty"`
	UpdatedAt               time.Time        `json:"updated_at"`
}
