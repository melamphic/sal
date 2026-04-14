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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/melamphic/sal/internal/audio"
	"github.com/melamphic/sal/internal/auth"
	"github.com/melamphic/sal/internal/clinic"
	"github.com/melamphic/sal/internal/extraction"
	"github.com/melamphic/sal/internal/forms"
	"github.com/melamphic/sal/internal/notes"
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
		return nil, fmt.Errorf("app.Build: %w", err)
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
	// All workers must be registered before river.NewClient is called.
	workers := river.NewWorkers()
	audioRepo := audio.NewRepository(db)
	river.AddWorker(workers, audio.NewTranscribeAudioWorker(audioRepo, store, cfg.DeepgramAPIKey))

	// ── Forms repo (needed by extract worker adapter) ──────────────────────────
	formsRepo := forms.NewRepository(db)

	// ── AI extraction ──────────────────────────────────────────────────────────
	extractor, err := extraction.NewFromConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: extraction: %w", err)
	}

	// ── Notes worker (registered before river.NewClient) ──────────────────────
	notesRepo := notes.NewRepository(db)
	river.AddWorker(workers, notes.NewExtractNoteWorker(
		notesRepo,
		&formsFieldAdapter{repo: formsRepo},
		&audioTranscriptAdapter{repo: audioRepo},
		extractor,
	))

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

	// ── Forms module ──────────────────────────────────────────────────────────
	formsSvc := forms.NewService(formsRepo)
	formsHandler := forms.NewHandler(formsSvc)

	// ── Notes module ──────────────────────────────────────────────────────────
	notesSvc := notes.NewService(notesRepo, riverClient)
	notesHandler := notes.NewHandler(notesSvc)

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
	formsHandler.Mount(r, api, jwtSecret)
	notesHandler.Mount(r, api, jwtSecret)

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

// ── Cross-module adapters ─────────────────────────────────────────────────────
// These bridge notes' provider interfaces to the concrete audio/forms repos.
// They live here because app.go is the only place allowed to wire cross-module deps.

type formsFieldAdapter struct{ repo *forms.Repository }

func (a *formsFieldAdapter) GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]notes.FormFieldMeta, error) {
	fields, err := a.repo.GetFieldsByVersionID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("app.formsFieldAdapter: %w", err)
	}
	out := make([]notes.FormFieldMeta, len(fields))
	for i, f := range fields {
		out[i] = notes.FormFieldMeta{
			ID:        f.ID,
			Title:     f.Title,
			Type:      f.Type,
			AIPrompt:  f.AIPrompt,
			Required:  f.Required,
			Skippable: f.Skippable,
		}
	}
	return out, nil
}

func (a *formsFieldAdapter) GetFormPrompt(ctx context.Context, versionID uuid.UUID) (*string, error) {
	p, err := a.repo.GetFormPrompt(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("app.formsFieldAdapter: %w", err)
	}
	return p, nil
}

type audioTranscriptAdapter struct{ repo *audio.Repository }

func (a *audioTranscriptAdapter) GetTranscript(ctx context.Context, recordingID uuid.UUID) (*string, error) {
	t, err := a.repo.GetTranscript(ctx, recordingID)
	if err != nil {
		return nil, fmt.Errorf("app.audioTranscriptAdapter: %w", err)
	}
	return t, nil
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
