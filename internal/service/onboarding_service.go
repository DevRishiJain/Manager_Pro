package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devrishijain/table-manager/internal/domain/money"
	"github.com/devrishijain/table-manager/internal/domain/restaurant"
	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/devrishijain/table-manager/internal/storage"
	"github.com/google/uuid"
)

var (
	ErrOnboardingIncomplete = errors.New("cannot go live before completing all prior onboarding steps")
	ErrManagerRequired      = errors.New("at least one active manager or owner account is required before go-live")
)

type TableQRExport struct {
	TableID     uuid.UUID `json:"table_id"`
	TableNumber string    `json:"table_number"`
	TableToken  string    `json:"table_token"`
	QRURL       string    `json:"qr_url"`
}

type OnboardingService struct {
	repo storage.Repository
}

func NewOnboardingService(repo storage.Repository) *OnboardingService {
	return &OnboardingService{repo: repo}
}

// StartOnboarding initiates the onboarding tracking pipeline for a new restaurant.
func (s *OnboardingService) StartOnboarding(ctx context.Context, r *restaurant.Restaurant, assignedContact *string) (*restaurant.RestaurantOnboarding, error) {
	if err := s.repo.CreateRestaurant(ctx, r); err != nil {
		return nil, err
	}

	// Initialize default settings
	settings := restaurant.DefaultSettings(r.ID)
	_ = s.repo.UpdateSettings(ctx, &settings)

	now := time.Now()
	onboarding := &restaurant.RestaurantOnboarding{
		RestaurantID:            r.ID,
		CurrentStep:             restaurant.StepProfileSetup,
		StepsCompleted:          []restaurant.OnboardingStep{},
		StartedAt:               now,
		AssignedPlatformContact: assignedContact,
		UpdatedAt:               now,
	}

	if err := s.repo.UpdateOnboarding(ctx, onboarding); err != nil {
		return nil, err
	}

	return onboarding, nil
}

// BatchProvisionTables creates N tables and generates permanent opaque QR tokens.
func (s *OnboardingService) BatchProvisionTables(ctx context.Context, restaurantID uuid.UUID, startNum, count int) ([]TableQRExport, error) {
	var exports []TableQRExport
	now := time.Now()

	for i := 0; i < count; i++ {
		tableNum := fmt.Sprintf("T%d", startNum+i)
		token, err := crypto.GenerateRandomToken(24)
		if err != nil {
			return nil, err
		}

		tbl := &restaurant.Table{
			ID:           uuid.New(),
			RestaurantID: restaurantID,
			TableNumber:  tableNum,
			TableToken:   token,
			IsActive:     true,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := s.repo.CreateTable(ctx, tbl); err != nil {
			return nil, err
		}

		exports = append(exports, TableQRExport{
			TableID:     tbl.ID,
			TableNumber: tableNum,
			TableToken:  token,
			QRURL:       fmt.Sprintf("https://dine.table-manager.internal/r/%s/t/%s", restaurantID, token),
		})
	}

	// Mark StepTableSetup completed
	s.markStepCompleted(ctx, restaurantID, restaurant.StepTableSetup)

	return exports, nil
}

// CloneMenuFromTemplate initializes the menu catalog from a predefined template (e.g. QSR or Cafe).
func (s *OnboardingService) CloneMenuFromTemplate(ctx context.Context, restaurantID uuid.UUID, templateType string) error {
	now := time.Now()
	catID := uuid.New()

	cat := &restaurant.MenuCategory{
		ID:           catID,
		RestaurantID: restaurantID,
		Name:         "Popular Specialties",
		DisplayOrder: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.CreateCategory(ctx, cat); err != nil {
		return err
	}

	sampleItems := []struct {
		Name  string
		Price int64
		Desc  string
	}{
		{"Signature Butter Paneer", 32000, "Rich creamy curry with tender paneer cubes"},
		{"Tandoori Roti Basket", 12000, "Assorted freshly baked breads"},
		{"Artisanal Cold Brew", 18000, "Steeped for 18 hours with hints of cocoa"},
	}

	for _, item := range sampleItems {
		mi := &restaurant.MenuItem{
			ID:           uuid.New(),
			RestaurantID: restaurantID,
			CategoryID:   catID,
			Name:         item.Name,
			Description:  item.Desc,
			Price:        money.New(item.Price),
			IsAvailable:  true,
			CGSTRateBps:  250, // 2.5%
			SGSTRateBps:  250, // 2.5%
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		_ = s.repo.CreateMenuItem(ctx, mi)
	}

	s.markStepCompleted(ctx, restaurantID, restaurant.StepMenuSetup)
	return nil
}

// CompleteGoLive validates that manager account exists and marks restaurant ACTIVE.
func (s *OnboardingService) CompleteGoLive(ctx context.Context, restaurantID uuid.UUID) error {
	staffList, err := s.repo.ListStaff(ctx, restaurantID)
	if err != nil {
		return err
	}

	hasManager := false
	for _, st := range staffList {
		if st.Role.CanForceClose() && st.IsActive {
			hasManager = true
			break
		}
	}
	if !hasManager {
		return ErrManagerRequired
	}

	rest, err := s.repo.GetRestaurantByID(ctx, restaurantID)
	if err != nil {
		return err
	}

	rest.Status = restaurant.StatusActive
	if err := s.repo.UpdateRestaurant(ctx, rest); err != nil {
		return err
	}

	now := time.Now()
	onboarding, err := s.repo.GetOnboarding(ctx, restaurantID)
	if err == nil && onboarding != nil {
		onboarding.CurrentStep = restaurant.StepGoLive
		onboarding.StepsCompleted = append(onboarding.StepsCompleted, restaurant.StepGoLive)
		onboarding.CompletedAt = &now
		_ = s.repo.UpdateOnboarding(ctx, onboarding)
	}

	return nil
}

func (s *OnboardingService) markStepCompleted(ctx context.Context, restaurantID uuid.UUID, step restaurant.OnboardingStep) {
	onboarding, err := s.repo.GetOnboarding(ctx, restaurantID)
	if err == nil && onboarding != nil {
		onboarding.StepsCompleted = append(onboarding.StepsCompleted, step)
		if step >= onboarding.CurrentStep {
			onboarding.CurrentStep = step + 1
		}
		_ = s.repo.UpdateOnboarding(ctx, onboarding)
	}
}

type StepDetail struct {
	StepNumber  int    `json:"step_number"`
	StepName    string `json:"step_name"`
	IsCompleted bool   `json:"is_completed"`
	StatusText  string `json:"status_text"`
}

type OnboardingProgressReport struct {
	RestaurantID       uuid.UUID    `json:"restaurant_id"`
	CurrentStep        int          `json:"current_step"`
	PercentageComplete int          `json:"percentage_complete"`
	Steps              []StepDetail `json:"steps"`
	GoLiveReady        bool         `json:"go_live_ready"`
}

// GetOnboardingProgress provides transparent step-by-step onboarding progress for restaurant and platform admin.
func (s *OnboardingService) GetOnboardingProgress(ctx context.Context, restaurantID uuid.UUID) (*OnboardingProgressReport, error) {
	onboard, err := s.repo.GetOnboarding(ctx, restaurantID)
	if err != nil {
		return nil, err
	}

	completedMap := make(map[restaurant.OnboardingStep]bool)
	for _, st := range onboard.StepsCompleted {
		completedMap[st] = true
	}

	stepNames := []struct {
		Step restaurant.OnboardingStep
		Name string
	}{
		{restaurant.StepProfileSetup, "Restaurant Profile Setup"},
		{restaurant.StepTableSetup, "Location & Table Provisioning"},
		{restaurant.StepMenuSetup, "Menu Catalog & GST Rates"},
		{restaurant.StepStaffSetup, "Staff Accounts & Role Assignment"},
		{restaurant.StepPaymentSetup, "Payment Methods & Gateway Connect"},
		{restaurant.StepPolicySetup, "Policies & Risk Thresholds"},
		{restaurant.StepTestOrder, "End-to-End Test Order Verification"},
		{restaurant.StepGoLive, "Final Go Live Activation"},
	}

	var details []StepDetail
	completedCount := 0

	for _, sn := range stepNames {
		done := completedMap[sn.Step]
		statusText := "Pending"
		if done {
			statusText = "Completed"
			completedCount++
		}
		details = append(details, StepDetail{
			StepNumber:  int(sn.Step),
			StepName:    sn.Name,
			IsCompleted: done,
			StatusText:  statusText,
		})
	}

	pct := (completedCount * 100) / len(stepNames)

	return &OnboardingProgressReport{
		RestaurantID:       restaurantID,
		CurrentStep:        int(onboard.CurrentStep),
		PercentageComplete: pct,
		Steps:              details,
		GoLiveReady:        completedCount >= 6, // ready for test order & go live
	}, nil
}
