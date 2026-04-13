// Package app wires all dependencies together and builds the HTTP server.
// This is the only place in the codebase where concrete types are instantiated
// and injected. No business logic lives here.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jackc/pgx/v5"
	"github.com/melamphic/sal/internal/audio"
	"github.com/melamphic/sal/internal/auth"
	"github.com/melamphic/sal/internal/clinic"
	"github.com/melamphic/sal/internal/patient"
	"github.com/melamphic/sal/internal/platform/config"
	"github.com/melamphic/sal/internal/platform/crypto"
	"github.com/melamphic/sal/internal/platform/logger"
	"github.com/melamphic/sal/internal/platform/mailer"
	mw "github.com/melamphic/sal/internal/platform/middleware"
	"github.com/melamphic/sal/internal/platform/storage"
	"github.com/melamphic/sal/internal/staff"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// App holds the running HTTP server and all wired dependencies.
type App struct {
	Server      *http.Server
	DB          *pgxpool.Pool
	Log         *slog.Logger
	RiverClient *river.Client[pgx.Tx]
}

// Build constructs the full application from config.
// Returns a ready-to-serve App with all dependencies wired.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	log := logger.New(cfg.Env)

	// ── Database ──────────────────────────────────────────────────────────────
	db, err := connectDB(ctx, cfg, log)
	if err != nil {
		return nil, err
	}

	// ── Encryption ────────────────────────────────────────────────────────────
	encKey, err := cfg.EncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("app.Build: %w", err)
	}
	cipher, err := crypto.New(encKey)
	if err != nil {
		return nil, fmt.Errorf("app.Build: %w", err)
	}

	// ── Email ─────────────────────────────────────────────────────────────────
	m := mailer.NewSMTP(mailer.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		FromName: cfg.SMTPFromName,
	})

	jwtSecret := []byte(cfg.JWTSecret)

	// ── Modules ───────────────────────────────────────────────────────────────
	authRepo := auth.NewRepository(db)
	authSvc := auth.NewService(authRepo, cipher, m, jwtSecret, auth.ServiceConfig{
		JWTAccessTTL:  cfg.JWTAccessTTL,
		JWTRefreshTTL: cfg.JWTRefreshTTL,
		MagicLinkTTL:  cfg.MagicLinkTTL,
		AppURL:        cfg.AppURL,
	})
	authHandler := auth.NewHandler(authSvc)

	clinicRepo := clinic.NewRepository(db)
	clinicSvc := clinic.NewService(clinicRepo, cipher)
	clinicHandler := clinic.NewHandler(clinicSvc)

	staffRepo := staff.NewRepository(db)
	staffSvc := staff.NewService(staffRepo, cipher, m, cfg.AppURL)
	staffHandler := staff.NewHandler(staffSvc)

	patientRepo := patient.NewRepository(db)
	patientSvc := patient.NewService(patientRepo, cipher)
	patientHandler := patient.NewHandler(patientSvc)

	// ── Storage (S3-compatible) ────────────────────────────────────────────────
	store, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: storage: %w", err)
	}

	// ── River (job queue) ─────────────────────────────────────────────────────
	// Workers are registered here; the client is used by services to enqueue jobs.
	workers := river.NewWorkers()
	audioRepo := audio.NewRepository(db)
	river.AddWorker(workers, audio.NewTranscribeAudioWorker(audioRepo, store, cfg.DeepgramAPIKey))

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("app.Build: river client: %w", err)
	}

	// ── Audio module ──────────────────────────────────────────────────────────
	audioSvc := audio.NewService(audioRepo, store, riverClient)
	audioHandler := audio.NewHandler(audioSvc)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(mw.RequestLogger(log))
	r.Use(chimw.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// ── Huma (OpenAPI 3.1 + Swagger UI) ───────────────────────────────────────
	api := humachi.New(r, huma.DefaultConfig("Salvia API", "1.0.0"))

	// Add bearer auth security scheme to the OpenAPI spec.
	api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
		},
	}

	// ── Mount routes ──────────────────────────────────────────────────────────
	authHandler.Mount(r, api, jwtSecret)
	clinicHandler.Mount(r, api, jwtSecret)
	staffHandler.Mount(r, api, jwtSecret)
	patientHandler.Mount(r, api, jwtSecret)
	audioHandler.Mount(r, api, jwtSecret)

	// Health check — no auth, no logging overhead.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	return &App{
		Server: &http.Server{
			Addr:    fmt.Sprintf(":%d", cfg.Port),
			Handler: r,
		},
		DB:          db,
		Log:         log,
		RiverClient: riverClient,
	}, nil
}

func connectDB(ctx context.Context, cfg *config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	// Import is in platform/db — use it directly here to keep app.go simple.
	// We inline the connect+migrate sequence so the startup order is explicit.
	from := "app.Build"

	log.InfoContext(ctx, "connecting to database")

	cfg2 := cfg // alias for closure
	_ = cfg2

	// Connect via platform/db.
	pool, err := connectPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("%s: connect db: %w", from, err)
	}

	log.InfoContext(ctx, "running migrations")
	if err := runMigrations(ctx, cfg.DatabaseURL, log); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: migrate: %w", from, err)
	}

	log.InfoContext(ctx, "database ready")
	return pool, nil
}
