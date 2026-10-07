package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App        AppConfig
	Database   DatabaseConfig
	Auth       AuthConfig
	Razorpay   RazorpayConfig
	Storage    StorageConfig
	AWS        AWSConfig
	Worker     WorkerConfig
	Restaurant RestaurantDefaultConfig
	AI         AIConfig
}

type AWSConfig struct {
	Region           string
	AccessKeyID      string
	SecretAccessKey  string
	S3BucketName     string
	S3Endpoint       string // Optional for LocalStack/MinIO
	CloudFrontDomain string // Optional CDN domain
	SNSSenderID      string // Sender ID for transactional SMS (e.g. TABLEOS)
	PinpointAppID    string // Optional AWS Pinpoint Application ID
}

type AppConfig struct {
	Env     string // "development", "staging", "production"
	Port    string
	BaseURL string
}

type DatabaseConfig struct {
	URL                 string
	MaxOpenConns        int
	MaxIdleConns        int
	AllowMemoryFallback bool
}

type AuthConfig struct {
	JWTSecret          string
	TokenTTLHours      int
	GuardTokenHours    int
	SuperAdminEmail    string
	SuperAdminPassword string
}

type RazorpayConfig struct {
	KeyID         string
	KeySecret     string
	WebhookSecret string
}

type StorageConfig struct {
	Type           string // "memory", "local", "s3", "minio"
	UploadDir      string
	MaxUploadBytes int64 // e.g. 5242880 (5MB)
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	UseSSL         bool
}

type AIConfig struct {
	Provider           string
	GeminiAPIKey       string
	OpenAIAPIKey       string
	AWSBedrockRegion   string
	AWSNovaModel       string
	EmbeddingModel     string
	EmbeddingDimension int
	LLMModel           string
}

type WorkerConfig struct {
	OutboxPollInterval       time.Duration
	InactivitySweepInterval  time.Duration
	FailedPaymentSweepWindow time.Duration
}

type RestaurantDefaultConfig struct {
	HighValueThresholdMinor int64 // 500000 = 5000 INR
	RapidOrderJumpFactor    int   // 3x
	ExitPassOTPTTLMinutes   int   // 120 mins
	FirstOrderOTPTTLMinutes int   // 15 mins
}

// Load loads configuration from environment variables and optionally from a .env file.
func Load(envFiles ...string) (*Config, error) {
	// Look for .env in current directory or provided paths
	paths := []string{".env", "../.env", "../../.env"}
	if len(envFiles) > 0 {
		paths = envFiles
	}

	for _, p := range paths {
		if err := loadEnvFile(p); err == nil {
			break
		}
	}

	cfg := &Config{
		App: AppConfig{
			Env:     getEnv("APP_ENV", "development"),
			Port:    getEnv("PORT", "8080"),
			BaseURL: getEnv("APP_BASE_URL", "http://localhost:8080"),
		},
		Database: DatabaseConfig{
			URL:                 getEnv("DATABASE_URL", "postgres://localhost:5432/table_manager_test?sslmode=disable"),
			MaxOpenConns:        getEnvInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:        getEnvInt("DB_MAX_IDLE_CONNS", 10),
			AllowMemoryFallback: strings.EqualFold(getEnv("ALLOW_MEMORY_FALLBACK", "false"), "true"),
		},
		Auth: AuthConfig{
			JWTSecret:          getEnv("JWT_SECRET", "super-secure-dining-os-jwt-secret-key-32b"),
			TokenTTLHours:      getEnvInt("AUTH_TOKEN_TTL_HOURS", 24),
			GuardTokenHours:    getEnvInt("GUARD_TOKEN_TTL_HOURS", 12),
			SuperAdminEmail:    getEnv("SUPER_ADMIN_EMAIL", ""),
			SuperAdminPassword: getEnv("SUPER_ADMIN_PASSWORD", ""),
		},
		Razorpay: RazorpayConfig{
			KeyID:         getEnv("RAZORPAY_KEY_ID", "rzp_test_sample_key_id"),
			KeySecret:     getEnv("RAZORPAY_KEY_SECRET", "sample_razorpay_secret_key"),
			WebhookSecret: getEnv("RAZORPAY_WEBHOOK_SECRET", "sample_razorpay_webhook_secret"),
		},
		Storage: StorageConfig{
			Type:           getEnv("STORAGE_TYPE", "memory"),
			UploadDir:      getEnv("STORAGE_UPLOAD_DIR", "./uploads"),
			MaxUploadBytes: int64(getEnvInt("STORAGE_MAX_UPLOAD_BYTES", 5*1024*1024)), // 5MB
			Endpoint:       getEnv("STORAGE_ENDPOINT", getEnv("AWS_S3_ENDPOINT", "")),
			Region:         getEnv("STORAGE_REGION", getEnv("AWS_REGION", "us-east-1")),
			Bucket:         getEnv("STORAGE_BUCKET", getEnv("AWS_S3_BUCKET_NAME", "togetherly")),
			AccessKey:      getEnv("STORAGE_ACCESS_KEY", getEnv("AWS_ACCESS_KEY_ID", "")),
			SecretKey:      getEnv("STORAGE_SECRET_KEY", getEnv("AWS_SECRET_ACCESS_KEY", "")),
			UseSSL:         strings.EqualFold(getEnv("STORAGE_USE_SSL", "false"), "true"),
		},
		AWS: AWSConfig{
			Region:           getEnv("AWS_REGION", "ap-south-1"),
			AccessKeyID:      getEnv("AWS_ACCESS_KEY_ID", ""),
			SecretAccessKey:  getEnv("AWS_SECRET_ACCESS_KEY", ""),
			S3BucketName:     getEnv("AWS_S3_BUCKET_NAME", "tableos-dining-assets-prod"),
			S3Endpoint:       getEnv("AWS_S3_ENDPOINT", ""),
			CloudFrontDomain: getEnv("AWS_CLOUDFRONT_DOMAIN", ""),
			SNSSenderID:      getEnv("AWS_SNS_SENDER_ID", "TABLEOS"),
			PinpointAppID:    getEnv("AWS_PINPOINT_APP_ID", ""),
		},
		Worker: WorkerConfig{
			OutboxPollInterval:       time.Duration(getEnvInt("OUTBOX_POLL_INTERVAL_MS", 1000)) * time.Millisecond,
			InactivitySweepInterval:  time.Duration(getEnvInt("INACTIVITY_SWEEPER_INTERVAL_MS", 5000)) * time.Millisecond,
			FailedPaymentSweepWindow: time.Duration(getEnvInt("FAILED_PAYMENT_SWEEP_MINUTES", 15)) * time.Minute,
		},
		Restaurant: RestaurantDefaultConfig{
			HighValueThresholdMinor: int64(getEnvInt("DEFAULT_HIGH_VALUE_THRESHOLD_MINOR", 500000)),
			RapidOrderJumpFactor:    getEnvInt("DEFAULT_RAPID_ORDER_JUMP_FACTOR", 3),
			ExitPassOTPTTLMinutes:   getEnvInt("DEFAULT_EXIT_PASS_OTP_TTL_MINUTES", 120),
			FirstOrderOTPTTLMinutes: getEnvInt("DEFAULT_FIRST_ORDER_OTP_TTL_MINUTES", 15),
		},
		AI: AIConfig{
			Provider:           getEnv("AI_PROVIDER", "gemini"),
			GeminiAPIKey:       getEnv("GEMINI_API_KEY", ""),
			OpenAIAPIKey:       getEnv("OPENAI_API_KEY", ""),
			AWSBedrockRegion:   getEnv("AWS_BEDROCK_REGION", "us-east-1"),
			AWSNovaModel:       getEnv("AWS_NOVA_MODEL", "amazon.nova-lite-v1:0"),
			EmbeddingModel:     getEnv("EMBEDDING_MODEL", "text-embedding-3-small"),
			EmbeddingDimension: getEnvInt("EMBEDDING_DIMENSION", 1536),
			LLMModel:           getEnv("LLM_MODEL", "gemini-2.5-flash"),
		},
	}

	if len(cfg.Auth.JWTSecret) < 32 && cfg.App.Env == "production" {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters in production")
	}

	return cfg, nil
}

// loadEnvFile reads a key-value .env file and sets environment variables if not already set.
func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Strip optional outer quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}

		// Only set if not already set in OS environment (standard dotenv behavior)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return defaultVal
}
