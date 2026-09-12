package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

const MaxFileSize = 5 * 1024 * 1024 // 5 MB max

var (
	ErrFileTooLarge    = errors.New("file exceeds maximum allowed size of 5MB")
	ErrUnsupportedMIME = errors.New("unsupported file type: only JPEG, PNG, and WebP are allowed")
	ErrExecutableOrSVG = errors.New("security violation: executable or script-bearing files are strictly prohibited")
)

type StoredFile struct {
	Key          string    `json:"key"`
	URL          string    `json:"url"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	PaymentID    uuid.UUID `json:"payment_id"`
	MIMEType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256Hash   string    `json:"sha256_hash"`
	UploadedAt   time.Time `json:"uploaded_at"`
}

type ObjectStore interface {
	UploadProof(ctx context.Context, restaurantID, paymentID uuid.UUID, data []byte) (*StoredFile, error)
	UploadMenuImage(ctx context.Context, restaurantID, itemID uuid.UUID, data []byte) (*StoredFile, error)
	GetSignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type MemoryObjectStore struct {
	mu    sync.RWMutex
	files map[string][]byte
	meta  map[string]*StoredFile
}

func NewMemoryObjectStore() *MemoryObjectStore {
	return &MemoryObjectStore{
		files: make(map[string][]byte),
		meta:  make(map[string]*StoredFile),
	}
}

func (s *MemoryObjectStore) UploadMenuImage(ctx context.Context, restaurantID, itemID uuid.UUID, data []byte) (*StoredFile, error) {
	return s.UploadProof(ctx, restaurantID, itemID, data)
}

func (s *MemoryObjectStore) UploadProof(ctx context.Context, restaurantID, paymentID uuid.UUID, data []byte) (*StoredFile, error) {

	if int64(len(data)) > MaxFileSize {
		return nil, ErrFileTooLarge
	}

	// Sniff MIME type using magic bytes (first 512 bytes)
	sniffLen := 512
	if len(data) < sniffLen {
		sniffLen = len(data)
	}
	mimeType := http.DetectContentType(data[:sniffLen])

	// Explicitly reject SVGs, executables, scripts
	if bytes.Contains(data, []byte("<script")) || bytes.Contains(data, []byte("<?xml")) || bytes.Contains(data, []byte("<svg")) {
		return nil, ErrExecutableOrSVG
	}

	var ext string
	switch mimeType {
	case "image/jpeg":
		ext = "jpg"
	case "image/png":
		ext = "png"
	case "image/webp":
		ext = "webp"
	default:
		return nil, fmt.Errorf("%w: detected %s", ErrUnsupportedMIME, mimeType)
	}

	// Generate server-side secure storage key (never client-provided filename)
	randomID, err := crypto.GenerateRandomToken(16)
	if err != nil {
		return nil, err
	}
	storageKey := fmt.Sprintf("payment-proofs/%s/%s/%s.%s", restaurantID.String(), paymentID.String(), randomID, ext)

	// Compute SHA-256 hash
	hashBytes := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hashBytes[:])

	stored := &StoredFile{
		Key:          storageKey,
		URL:          fmt.Sprintf("https://storage.table-manager.internal/%s", storageKey),
		RestaurantID: restaurantID,
		PaymentID:    paymentID,
		MIMEType:     mimeType,
		SizeBytes:    int64(len(data)),
		SHA256Hash:   hashStr,
		UploadedAt:   time.Now(),
	}

	s.mu.Lock()
	s.files[storageKey] = data
	s.meta[storageKey] = stored
	s.mu.Unlock()

	return stored, nil
}

func (s *MemoryObjectStore) GetSignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.files[key]
	if !ok {
		return "", errors.New("file not found in object storage")
	}
	// Return signed simulated URL with expiration query param
	expires := time.Now().Add(ttl).Unix()
	return fmt.Sprintf("https://storage.table-manager.internal/%s?expires=%d&sig=mock-secure-sig", key, expires), nil
}
