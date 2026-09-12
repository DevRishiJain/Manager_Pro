package tests

import (
	"context"
	"testing"

	"github.com/devrishijain/table-manager/internal/adapter/storage"
	"github.com/google/uuid"
)

func TestSecureFileUploadValidation(t *testing.T) {
	store := storage.NewMemoryObjectStore()
	restID := uuid.New()
	payID := uuid.New()

	// 1. Valid JPEG file (magic bytes: FF D8 FF)
	validJPEG := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00}
	stored, err := store.UploadProof(context.Background(), restID, payID, validJPEG)
	if err != nil {
		t.Fatalf("expected valid JPEG to upload successfully: %v", err)
	}
	if stored.MIMEType != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", stored.MIMEType)
	}

	// 2. Malicious executable / script disguised as image (contains <script or not a real image)
	maliciousScript := []byte("<script>alert('xss')</script>")
	_, err = store.UploadProof(context.Background(), restID, payID, maliciousScript)
	if err == nil {
		t.Error("expected script-bearing upload to be rejected, but it succeeded")
	}

	// 3. Fake JPEG with binary shell script
	fakeJPEG := []byte("#!/bin/bash\nrm -rf /\n")
	_, err = store.UploadProof(context.Background(), restID, payID, fakeJPEG)
	if err == nil {
		t.Error("expected non-image binary to be rejected, but it succeeded")
	}

	// 4. Oversized file (> 5MB)
	oversized := make([]byte, 6*1024*1024)
	_, err = store.UploadProof(context.Background(), restID, payID, oversized)
	if err != storage.ErrFileTooLarge {
		t.Errorf("expected ErrFileTooLarge, got %v", err)
	}
}
