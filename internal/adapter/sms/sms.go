package sms

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type SMSService interface {
	SendOTP(ctx context.Context, phone string, otp string, purpose string) error
}

type AWSSNSConfig struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SenderID        string // e.g. "TABLEOS"
}

// AWSSNSService sends transactional OTP messages via Amazon SNS / AWS End User Messaging.
type AWSSNSService struct {
	cfg        AWSSNSConfig
	httpClient *http.Client
	logger     *slog.Logger
}

func NewAWSSNSService(cfg AWSSNSConfig, logger *slog.Logger) *AWSSNSService {
	if cfg.Region == "" {
		cfg.Region = "ap-south-1"
	}
	if cfg.SenderID == "" {
		cfg.SenderID = "TABLEOS"
	}
	return &AWSSNSService{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}
}

func (s *AWSSNSService) SendOTP(ctx context.Context, phone string, otp string, purpose string) error {
	msg := fmt.Sprintf("Your %s code is %s. Valid for 10 minutes. Do not share this with anyone. - %s", purpose, otp, s.cfg.SenderID)

	s.logger.Info("Sending SMS via AWS SNS",
		"phone", phone,
		"purpose", purpose,
		"sender_id", s.cfg.SenderID,
	)

	// If mock credentials in dev, log and return success
	if s.cfg.AccessKeyID == "" || strings.HasPrefix(s.cfg.AccessKeyID, "mock_") {
		s.logger.Info("Dev/Mock mode: OTP dispatched", "phone", phone, "otp", otp)
		return nil
	}

	// Prepare standard AWS SNS Publish REST Request
	endpoint := fmt.Sprintf("https://sns.%s.amazonaws.com/", s.cfg.Region)
	form := url.Values{}
	form.Set("Action", "Publish")
	form.Set("PhoneNumber", phone)
	form.Set("Message", msg)
	form.Set("MessageAttributes.entry.1.Name", "AWS.SNS.SMS.SenderID")
	form.Set("MessageAttributes.entry.1.Value.DataType", "String")
	form.Set("MessageAttributes.entry.1.Value.StringValue", s.cfg.SenderID)
	form.Set("MessageAttributes.entry.2.Name", "AWS.SNS.SMS.SMSType")
	form.Set("MessageAttributes.entry.2.Value.DataType", "String")
	form.Set("MessageAttributes.entry.2.Value.StringValue", "Transactional")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to dispatch AWS SNS SMS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("AWS SNS SMS request failed with status: %d", resp.StatusCode)
	}

	return nil
}

// MockSMSService captures sent SMS in memory for test assertions.
type MockSMSService struct {
	mu       sync.RWMutex
	SentOTPs map[string][]string // phone -> list of OTPs
}

func NewMockSMSService() *MockSMSService {
	return &MockSMSService{
		SentOTPs: make(map[string][]string),
	}
}

func (m *MockSMSService) SendOTP(ctx context.Context, phone string, otp string, purpose string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SentOTPs[phone] = append(m.SentOTPs[phone], otp)
	return nil
}

func (m *MockSMSService) GetLastOTP(phone string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	otps := m.SentOTPs[phone]
	if len(otps) == 0 {
		return ""
	}
	return otps[len(otps)-1]
}
