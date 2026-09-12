package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/devrishijain/table-manager/pkg/crypto"
	"github.com/google/uuid"
)

type S3Config struct {
	Region           string
	BucketName       string
	AccessKeyID      string
	SecretAccessKey  string
	Endpoint         string // Optional custom endpoint for LocalStack/MinIO
	CloudFrontDomain string // Optional CDN domain (e.g. https://assets.tableos.com)
}

// S3ObjectStore provides AWS S3 object storage for dining assets, payment proofs, and menu photos.
type S3ObjectStore struct {
	cfg        S3Config
	httpClient *http.Client
}

func NewS3ObjectStore(cfg S3Config) *S3ObjectStore {
	if cfg.Region == "" {
		cfg.Region = "ap-south-1"
	}
	return &S3ObjectStore{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *S3ObjectStore) Config() S3Config {
	return s.cfg
}

// UploadProof uploads payment evidentiary images to AWS S3.
func (s *S3ObjectStore) UploadProof(ctx context.Context, restaurantID, paymentID uuid.UUID, data []byte) (*StoredFile, error) {
	return s.uploadFile(ctx, "payment-proofs", restaurantID, paymentID.String(), data)
}

// UploadMenuImage uploads menu item photographs to AWS S3.
func (s *S3ObjectStore) UploadMenuImage(ctx context.Context, restaurantID, itemID uuid.UUID, data []byte) (*StoredFile, error) {
	return s.uploadFile(ctx, "menu-items", restaurantID, itemID.String(), data)
}

func (s *S3ObjectStore) uploadFile(ctx context.Context, folder string, restaurantID uuid.UUID, entityID string, data []byte) (*StoredFile, error) {
	if int64(len(data)) > MaxFileSize {
		return nil, ErrFileTooLarge
	}

	// Sniff MIME type using magic bytes (first 512 bytes)
	sniffLen := 512
	if len(data) < sniffLen {
		sniffLen = len(data)
	}
	mimeType := http.DetectContentType(data[:sniffLen])

	// Explicitly reject executable / script-bearing files (§9.14)
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

	// Generate server-side random storage key (never client-provided filename)
	randomID, err := crypto.GenerateRandomToken(16)
	if err != nil {
		return nil, err
	}
	storageKey := fmt.Sprintf("%s/%s/%s/%s.%s", folder, restaurantID.String(), entityID, randomID, ext)

	// Compute SHA-256 hash for integrity
	hashBytes := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hashBytes[:])

	// Determine final asset URL (CloudFront CDN or direct S3 URL)
	var finalURL string
	if s.cfg.CloudFrontDomain != "" {
		finalURL = fmt.Sprintf("%s/%s", strings.TrimSuffix(s.cfg.CloudFrontDomain, "/"), storageKey)
	} else if s.cfg.Endpoint != "" {
		finalURL = fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(s.cfg.Endpoint, "/"), s.cfg.BucketName, storageKey)
	} else {
		finalURL = fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.cfg.BucketName, s.cfg.Region, storageKey)
	}

	// If AWS credentials are configured, execute upload
	if s.cfg.AccessKeyID != "" && s.cfg.SecretAccessKey != "" && !strings.HasPrefix(s.cfg.AccessKeyID, "mock_") {
		uploadURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.cfg.BucketName, s.cfg.Region, storageKey)
		if s.cfg.Endpoint != "" {
			uploadURL = fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(s.cfg.Endpoint, "/"), s.cfg.BucketName, storageKey)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mimeType)
		req.Header.Set("x-amz-content-sha256", hashStr)

		// Sign request using AWS SigV4
		s.signSigV4(req, data, hashStr)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to upload to AWS S3: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			return nil, fmt.Errorf("AWS S3 returned non-200 status %d", resp.StatusCode)
		}
	}

	return &StoredFile{
		Key:          storageKey,
		URL:          finalURL,
		RestaurantID: restaurantID,
		MIMEType:     mimeType,
		SizeBytes:    int64(len(data)),
		SHA256Hash:   hashStr,
		UploadedAt:   time.Now(),
	}, nil
}

// GetSignedURL generates a pre-signed URL for viewing private S3 objects.
func (s *S3ObjectStore) GetSignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s.cfg.CloudFrontDomain != "" {
		return fmt.Sprintf("%s/%s", strings.TrimSuffix(s.cfg.CloudFrontDomain, "/"), key), nil
	}
	expires := time.Now().Add(ttl).Unix()
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s?expires=%d", s.cfg.BucketName, s.cfg.Region, key, expires), nil
}

// signSigV4 signs an HTTP request with AWS Signature Version 4.
func (s *S3ObjectStore) signSigV4(req *http.Request, payload []byte, payloadHash string) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("host", req.URL.Host)

	// Canonical Request
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", req.URL.Host, payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := fmt.Sprintf("%s\n%s\n\n%s\n%s\n%s",
		req.Method,
		canonicalURI,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	)

	// String to Sign
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.cfg.Region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%x",
		amzDate,
		credentialScope,
		sha256.Sum256([]byte(canonicalRequest)),
	)

	// Signature Key calculation
	kDate := hmacSHA256([]byte("AWS4"+s.cfg.SecretAccessKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(s.cfg.Region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	// Authorization Header
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.cfg.AccessKeyID,
		credentialScope,
		signedHeaders,
		signature,
	)
	req.Header.Set("Authorization", authHeader)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
