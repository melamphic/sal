// Package config loads application configuration from environment variables.
// All configuration is required at startup — the application will not start
// with missing or invalid values.
package config

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/sethvargo/go-envconfig"
)

// Config holds all application configuration sourced from environment variables.
// Add new fields here as features are introduced — never read os.Getenv directly
// anywhere else in the codebase.
type Config struct {
	// Server
	Port int    `env:"PORT,default=8080"`
	Env  string `env:"ENV,default=development"`

	// Database
	DatabaseURL string `env:"DATABASE_URL,required"`

	// Security
	EncryptionKeyB64 string        `env:"ENCRYPTION_KEY,required"`
	JWTSecret        string        `env:"JWT_SECRET,required"`
	JWTAccessTTL     time.Duration `env:"JWT_ACCESS_TTL,default=15m"`
	JWTRefreshTTL    time.Duration `env:"JWT_REFRESH_TTL,default=720h"`
	MagicLinkTTL     time.Duration `env:"MAGIC_LINK_TTL,default=15m"`

	// Email (SMTP — Mailpit in dev, any SMTP relay in prod)
	SMTPHost     string `env:"SMTP_HOST,default=localhost"`
	SMTPPort     int    `env:"SMTP_PORT,default=1025"`
	SMTPUsername string `env:"SMTP_USERNAME"`
	SMTPPassword string `env:"SMTP_PASSWORD"`
	SMTPFrom     string `env:"SMTP_FROM,default=noreply@salvia.app"`
	SMTPFromName string `env:"SMTP_FROM_NAME,default=Salvia"`

	// Storage (S3-compatible — MinIO in dev)
	StorageEndpoint     string `env:"STORAGE_ENDPOINT,required"`
	StorageBucket       string `env:"STORAGE_BUCKET,default=salvia-audio"`
	StorageAccessKey    string `env:"STORAGE_ACCESS_KEY,required"`
	StorageSecretKey    string `env:"STORAGE_SECRET_KEY,required"`
	StorageRegion       string `env:"STORAGE_REGION,default=ap-southeast-2"`
	StorageUsePathStyle bool   `env:"STORAGE_USE_PATH_STYLE,default=true"`

	// Transcription provider — "deepgram" (production) or "gemini" (dev/staging, free tier).
	// deepgram: uses Deepgram Nova-3 Medical; requires DEEPGRAM_API_KEY.
	// gemini:   uses Gemini audio understanding; requires GEMINI_API_KEY; no word-level confidence.
	// Leave key empty for the configured provider to skip transcription entirely.
	TranscriptionProvider string `env:"TRANSCRIPTION_PROVIDER,default=deepgram"`
	DeepgramAPIKey        string `env:"DEEPGRAM_API_KEY"`

	// AI extraction — form field filling from transcripts.
	// GeminiAPIKey: Google AI Studio key (free tier — recommended for dev).
	// OpenAIAPIKey: OpenAI platform key (GPT-4o — recommended for prod).
	// ExtractionProvider: "gemini" (default) or "openai".
	// Leave both keys empty to skip extraction (pipeline stops after transcription).
	GeminiAPIKey       string `env:"GEMINI_API_KEY"`
	OpenAIAPIKey       string `env:"OPENAI_API_KEY"`
	ExtractionProvider string `env:"EXTRACTION_PROVIDER,default=gemini"`

	// Database pool tuning
	DBMaxConns int `env:"DB_MAX_CONNS,default=30"`
	DBMinConns int `env:"DB_MIN_CONNS,default=2"`

	// Frontend
	AppURL      string `env:"APP_URL,default=http://localhost:3000"`
	CORSOrigins string `env:"CORS_ORIGINS,default=http://localhost:3000"`

	// Marketplace — Phase 1.5 onwards.
	// Platform fee charged on paid listings from regular clinics. Authority
	// bodies (authority_type IN 'salvia','authority') always pay 0%.
	MarketplacePlatformFeePct    int    `env:"MARKETPLACE_PLATFORM_FEE_PCT,default=30"`
	MarketplacePolicyAttribution string `env:"MARKETPLACE_POLICY_ATTRIBUTION,default="`

	// Stripe Connect (required for paid marketplace listings; empty = disabled).
	StripeSecretKey     string `env:"STRIPE_SECRET_KEY"`
	StripeWebhookSecret string `env:"STRIPE_WEBHOOK_SECRET"`
}

// Load reads configuration from the environment and validates it.
func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := envconfig.Process(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("config.Load: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config.Load: invalid config: %w", err)
	}
	return &cfg, nil
}

// EncryptionKey decodes and returns the raw 32-byte AES-256 key.
func (c *Config) EncryptionKey() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(c.EncryptionKeyB64)
	if err != nil {
		return nil, fmt.Errorf("config: ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("config: ENCRYPTION_KEY must decode to exactly 32 bytes, got %d", len(key))
	}
	return key, nil
}

// AllowedOrigins returns CORS_ORIGINS as a slice of trimmed strings.
func (c *Config) AllowedOrigins() []string {
	parts := strings.Split(c.CORSOrigins, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// IsDevelopment returns true when running in the development environment.
func (c *Config) IsDevelopment() bool { return c.Env == "development" }

// IsProduction returns true when running in the production environment.
func (c *Config) IsProduction() bool { return c.Env == "production" }

func (c *Config) validate() error {
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	return nil
}
