// Package app wires all dependencies together and builds the HTTP server.
// This is the only place in the codebase where concrete types are instantiated
// and injected. No business logic lives here.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/melamphic/sal/internal/aigen"
	"github.com/melamphic/sal/internal/audio"
	"github.com/melamphic/sal/internal/auth"
	"github.com/melamphic/sal/internal/drugs"
	drugscatalog "github.com/melamphic/sal/internal/drugs/catalog"
	"github.com/melamphic/sal/internal/incidents"
	"github.com/melamphic/sal/internal/billing"
	"github.com/melamphic/sal/internal/clinic"
	"github.com/melamphic/sal/internal/domain"
	"github.com/melamphic/sal/internal/extraction"
	"github.com/melamphic/sal/internal/forms"
	"github.com/melamphic/sal/internal/marketplace"
	"github.com/melamphic/sal/internal/notecap"
	"github.com/melamphic/sal/internal/notes"
	"github.com/melamphic/sal/internal/notifications"
	"github.com/melamphic/sal/internal/patient"
	"github.com/melamphic/sal/internal/platform/confidence"
	"github.com/melamphic/sal/internal/platform/config"
	"github.com/melamphic/sal/internal/platform/crypto"
	"github.com/melamphic/sal/internal/platform/logger"
	"github.com/melamphic/sal/internal/platform/mailer"
	mw "github.com/melamphic/sal/internal/platform/middleware"
	"github.com/melamphic/sal/internal/platform/storage"
	"github.com/melamphic/sal/internal/policy"
	"github.com/melamphic/sal/internal/reports"
	"github.com/melamphic/sal/internal/staff"
	"github.com/melamphic/sal/internal/tiering"
	"github.com/melamphic/sal/internal/timeline"
	"github.com/melamphic/sal/internal/verticals"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"golang.org/x/time/rate"
)

// App holds the running HTTP server and all wired dependencies.
type App struct {
	Server      *http.Server
	DB          *pgxpool.Pool
	Log         *slog.Logger
	RiverClient *river.Client[pgx.Tx]
	Broker      *notifications.Broker
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
	// Auth and Staff have circular dependencies (auth creates staff on invite accept,
	// staff creates invite tokens via auth). Lazy adapters break the cycle.
	staffCreator := &staffCreatorAdapter{}
	authRepo := auth.NewRepository(db)
	authSvc := auth.NewService(authRepo, cipher, m, jwtSecret, auth.ServiceConfig{
		JWTAccessTTL:  cfg.JWTAccessTTL,
		JWTRefreshTTL: cfg.JWTRefreshTTL,
		MagicLinkTTL:  cfg.MagicLinkTTL,
		AppURL:        cfg.AppURL,
	}, staffCreator)
	// Per-email throttle (3 burst, 1 every 2 minutes) defends against
	// distributed botnets flooding a single victim's inbox; the per-IP
	// middleware below covers the orthogonal flooding-many-emails-from-one-IP
	// case. Both must be in place for full coverage.
	authSvc.EnableMagicLinkEmailLimit()
	// 10 requests per minute per IP on public auth endpoints.
	rlStore := mw.NewRateLimiterStore(10.0/60.0, 10)
	authHandler := auth.NewHandler(authSvc, rlStore)

	// ── Storage (S3-compatible) ────────────────────────────────────────────────
	store, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: storage: %w", err)
	}
	clinicLogos := &clinicLogoAdapter{store: store}

	clinicRepo := clinic.NewRepository(db)
	// adminBootstrapAdapter is set after authSvc and staffSvc are wired below.
	clinicBootstrap := &adminBootstrapAdapter{}
	clinicSvc := clinic.NewService(clinicRepo, cipher, clinicBootstrap, clinicLogos, clinicLogos)
	clinicHandler := clinic.NewHandler(clinicSvc)

	inviteAdapter := &inviteCreatorAdapter{auth: authSvc, cipher: cipher}
	clinicNameAdapter := &clinicNameProviderAdapter{clinic: clinicSvc}
	staffRepo := staff.NewRepository(db)
	staffSvc := staff.NewService(staffRepo, cipher, m, cfg.AppURL, inviteAdapter, clinicNameAdapter)
	staffHandler := staff.NewHandler(staffSvc)

	// Now both authSvc and staffSvc exist — set up lazy adapters.
	clinicBootstrap.auth = authSvc
	clinicBootstrap.staff = staffSvc
	staffCreator.staff = staffSvc

	// /mel handoff wiring — only enabled when the shared JWT secret is set.
	if cfg.MelHandoffJWTSecret != "" {
		authSvc.SetMelHandoff(
			[]byte(cfg.MelHandoffJWTSecret),
			&melHandoffAdapter{clinic: clinicSvc, staff: staffSvc},
		)
	}

	// ── Billing (Stripe webhook + portal) ────────────────────────────────
	// Gated on STRIPE_WEBHOOK_SECRET — without it neither route is mounted.
	// Portal additionally requires STRIPE_API_KEY; when it's missing the
	// portal endpoint 400s with a clear message.
	var billingHandler *billing.Handler
	if cfg.StripeWebhookSecret != "" {
		priceMap, err := cfg.ParseStripePriceMap()
		if err != nil {
			return nil, fmt.Errorf("app.Build: %w", err)
		}
		billingRepo := billing.NewRepository(db)
		planLookup := newStaticPlanLookup(priceMap)
		billingSvc := billing.NewService(
			billingRepo,
			&billingClinicAdapter{clinic: clinicSvc},
			planLookup,
			[]byte(cfg.StripeWebhookSecret),
		)
		if cfg.StripeAPIKey != "" {
			billingSvc.EnablePortal(
				billing.NewStripePortalClient(cfg.StripeAPIKey),
				cfg.AppURL+"/settings/billing",
			)
			checkoutClient := billing.NewStripeCheckoutClient(cfg.StripeAPIKey)
			customerClient := billing.NewStripeCustomerClient(cfg.StripeAPIKey)
			billingSvc.EnableCheckout(
				checkoutClient,
				customerClient,
				cfg.AppURL+"/settings/billing?checkout=success",
				cfg.AppURL+"/settings/billing?checkout=cancelled",
			)
			// Signup-checkout (mel card-up-front) needs all three primitives
			// — Stripe customer + checkout session + plan-code → price-id —
			// PLUS the mel handoff JWT secret so it can mint the post-checkout
			// success URL. Wire it only when both gates are open.
			if cfg.MelHandoffJWTSecret != "" {
				authSvc.EnableSignupCheckout(
					&signupCheckoutAdapter{
						customers: customerClient,
						checkout:  checkoutClient,
						plans:     planLookup,
					},
					strings.TrimRight(cfg.MelBaseURL, "/")+"/signup?canceled=1",
				)
			}
			// ── Tier auto-derivation (pricing-model-v3 §6) ────────────────────
			// Wired only when Stripe is fully configured — without an API
			// key we can't issue subscription-item swaps. Hooks into
			// staff.Service so every invite/create/deactivate that touches
			// a standard seat triggers a Reconcile.
			tieringSvc := tiering.NewService(
				&tieringClinicAdapter{clinic: clinicSvc},
				&tieringStaffAdapter{staff: staffSvc},
				billing.NewStripeSubscriptionClient(cfg.StripeAPIKey),
				planLookup,
				log,
			)
			staffSvc.SetTierReconciler(tieringSvc)
		}
		billingHandler = billing.NewHandler(billingSvc, log)
	}

	patientRepo := patient.NewRepository(db)
	patientSvc := patient.NewService(patientRepo, cipher)
	patientHandler := patient.NewHandler(patientSvc, clinicSvc)

	verticalAdapter := &clinicVerticalProviderAdapter{clinic: clinicSvc}
	verticalsSvc := verticals.NewService(verticalAdapter)
	verticalsHandler := verticals.NewHandler(verticalsSvc)
	verticalStrings := &verticalStringAdapter{clinic: clinicSvc}

	// ── Transcription provider ─────────────────────────────────────────────────
	transcriber, err := newTranscriberFromConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: transcriber: %w", err)
	}

	// ── River (job queue) ─────────────────────────────────────────────────────
	// All workers must be registered before river.NewClient is called.
	workers := river.NewWorkers()
	audioRepo := audio.NewRepository(db)
	river.AddWorker(workers, audio.NewTranscribeAudioWorker(audioRepo, store, transcriber))

	// ── Forms repo (needed by extract worker adapter) ──────────────────────────
	formsRepo := forms.NewRepository(db)

	// ── AI extraction ──────────────────────────────────────────────────────────
	extractor, err := extraction.NewFromConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: extraction: %w", err)
	}
	aligner, err := extraction.NewPolicyAlignerFromConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: policy aligner: %w", err)
	}
	formChecker, err := extraction.NewFormCheckerFromConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: form checker: %w", err)
	}

	// ── Timeline repo + event adapter ─────────────────────────────────────────
	timelineRepo := timeline.NewRepository(db)
	eventAdapter := &timelineEventAdapter{repo: timelineRepo, log: log}

	// ── Reports repo + worker (registered before river.NewClient) ─────────────
	reportsRepo := reports.NewRepository(db)
	river.AddWorker(workers, reports.NewGenerateReportWorker(reportsRepo, store))
	// Compliance PDF worker — uses the lazy data adapter because its
	// dependencies (drugs / clinic / staff services) are constructed below,
	// after river.NewClient. The lazy wrapper resolves at job-run time.
	complianceData := &lazyComplianceData{}
	river.AddWorker(workers, reports.NewGenerateCompliancePDFWorker(reportsRepo, store, complianceData))

	// ── Notes workers (registered before river.NewClient) ─────────────────────
	// lazyEnqueuer is set after river.NewClient so workers can enqueue downstream jobs.
	notesRepo := notes.NewRepository(db)
	policyRepo := policy.NewRepository(db)
	lazy := &lazyEnqueuer{}
	river.AddWorker(workers, notes.NewExtractNoteWorker(
		notesRepo,
		&formsFieldAdapter{repo: formsRepo},
		&audioTranscriptAdapter{repo: audioRepo},
		extractor,
		verticalStrings,
		eventAdapter,
		lazy,
	))
	river.AddWorker(workers, notes.NewComputePolicyAlignmentWorker(
		notesRepo,
		&formsFieldAdapter{repo: formsRepo},
		&policyClauseProviderAdapter{forms: formsRepo, policy: policyRepo},
		aligner,
		verticalStrings,
	))
	pdfRenderer := notes.NewPDFRenderer(
		notesRepo,
		&formMetaAdapter{repo: formsRepo},
		&formsFieldAdapter{repo: formsRepo},
		&clinicStyleAdapter{clinic: clinicSvc},
		&staffNameAdapter{staff: staffSvc},
		&docThemeAdapter{repo: formsRepo},
		&systemHeaderAdapter{repo: formsRepo},
		&subjectRenderAdapter{patient: patientSvc},
		store,
		eventAdapter,
	)
	river.AddWorker(workers, notes.NewGenerateNotePDFWorker(pdfRenderer))

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("app.Build: river client: %w", err)
	}
	lazy.client = riverClient

	// ── Audio module ──────────────────────────────────────────────────────────
	audioSvc := audio.NewService(audioRepo, store, riverClient)
	audioHandler := audio.NewHandler(audioSvc)

	// ── Forms module ──────────────────────────────────────────────────────────
	docThemeLogos := &docThemeLogoAdapter{store: store}
	formsSvc := forms.NewService(
		formsRepo,
		&formPolicyClauseFetcherAdapter{forms: formsRepo, policy: policyRepo},
		formChecker,
		docThemeLogos,
		docThemeLogos,
		&formsStaffNameAdapter{staff: staffSvc},
		&formsPolicyOwnershipAdapter{policy: policyRepo},
	)
	formsSvc.SetVerticalProvider(verticalStrings)
	formsHandler := forms.NewHandler(formsSvc)

	// ── Notes module ──────────────────────────────────────────────────────────
	notesSvc := notes.NewService(notesRepo, riverClient, eventAdapter, &formsFieldAdapter{repo: formsRepo})
	notesSvc.SetVerticalProvider(verticalStrings)
	// Wire per-clause policy checker if available (Gemini only for now).
	detailedChecker, err := extraction.NewPolicyDetailedCheckerFromConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("app.Build: policy detailed checker: %w", err)
	}
	if detailedChecker != nil {
		notesSvc.SetPolicyChecker(detailedChecker, &policyClauseProviderAdapter{forms: formsRepo, policy: policyRepo})
	}
	// ── Note-cap metering (pricing-model-v3 §7) ──────────────────────────────
	// Gates note creation against the per-period (or trial) note cap,
	// fires 80% / 110% emails, and blocks at 150%. Wired here so the
	// notes service can call into it for every CreateNote.
	noteCapSvc := notecap.NewService(
		&notecapClinicAdapter{clinic: clinicSvc},
		&notecapNotesAdapter{notes: notesRepo},
		m,
		cfg.OpsAlertEmail,
		log,
	)
	notesSvc.SetNoteCapEnforcer(noteCapSvc)
	notesSvc.SetPDFRenderer(pdfRenderer)
	notesHandler := notes.NewHandler(notesSvc, store)

	// ── Timeline module ───────────────────────────────────────────────────────
	timelineSvc := timeline.NewService(timelineRepo)
	timelineHandler := timeline.NewHandler(timelineSvc)

	// ── Notifications (SSE broker) ────────────────────────────────────────────
	broker := notifications.NewBroker(db, log)
	notificationsHandler := notifications.NewHandler(broker)

	// ── Policy module ─────────────────────────────────────────────────────────
	policySvc := policy.NewService(policyRepo, &policyFormLinkerAdapter{repo: formsRepo})
	policyHandler := policy.NewHandler(policySvc)

	// ── Drugs module ──────────────────────────────────────────────────────────
	// System catalog ships as embedded JSON files (one per vertical × country).
	// On startup we parse + validate every file; a malformed catalog fails
	// boot, by design — silently shipping a broken drug list is the wrong
	// failure mode for a compliance feature.
	drugCatalog, err := drugscatalog.NewLoader()
	if err != nil {
		return nil, fmt.Errorf("app.Build: drugs catalog: %w", err)
	}
	log.Info("drugs: catalog loaded", "combos", len(drugCatalog.Manifest()))
	drugsRepo := drugs.NewRepository(db)
	drugsSvc := drugs.NewService(
		drugsRepo,
		drugCatalog,
		&drugsClinicLookupAdapter{clinicSvc: clinicSvc},
		&drugsStaffPermAdapter{staffSvc: staffSvc},
		&drugsAccessLogAdapter{patientRepo: patientRepo},
	)
	drugsHandler := drugs.NewHandler(drugsSvc)

	// ── Incidents module ─────────────────────────────────────────────────────
	// Vertical-agnostic. SIRS/CQC classifier auto-stamps regulator deadlines
	// for aged-care AU/UK; other (vertical, country) combos record without
	// auto-classification. Reuses the drugs adapters for clinic lookup +
	// subject-access logging — same shape, same dependencies.
	incidentsRepo := incidents.NewRepository(db)
	// drugsClinicLookupAdapter and drugsAccessLogAdapter satisfy
	// incidents.ClinicLookup / SubjectAccessLogger structurally — same
	// signatures across both modules, so a single adapter pair serves both.
	incidentsSvc := incidents.NewService(
		incidentsRepo,
		&drugsClinicLookupAdapter{clinicSvc: clinicSvc},
		&drugsAccessLogAdapter{patientRepo: patientRepo},
	)
	incidentsHandler := incidents.NewHandler(incidentsSvc)

	// ── AI generation (forms + policies) ─────────────────────────────────────
	// Provider is best-effort: missing API keys disable the feature without
	// failing startup. The corresponding handlers detect a nil provider and
	// skip route registration so the OpenAPI surface only advertises what
	// will actually answer.
	aigenClinicLookup := &aigenClinicLookupAdapter{clinicSvc: clinicSvc}
	var (
		formAIGenHandler   *forms.AIGenHandler
		policyAIGenHandler *policy.AIGenHandler
	)
	aigenProvider, aigenErr := aigen.NewProvider(aigen.FactoryConfig{
		Provider:     cfg.AIGenProvider,
		GeminiAPIKey: cfg.GeminiAPIKey,
		OpenAIAPIKey: cfg.OpenAIAPIKey,
		GeminiModel:  cfg.AIGenGeminiModel,
		OpenAIModel:  cfg.AIGenOpenAIModel,
	})
	switch {
	case aigenErr == nil:
		log.Info("aigen: provider configured", "provider", aigenProvider.Name(), "model", aigenProvider.Model())
		formGenSvc := aigen.NewFormGenService(aigenProvider, log)
		policyGenSvc := aigen.NewPolicyGenService(aigenProvider, log)
		// Per-IP rate limit on /generate. Generation is expensive and
		// latency-bound; a tight bucket (0.1 rps, burst 3) blocks runaway
		// scripts while leaving room for legitimate bursts (e.g., user
		// retries after a typo). Cleanup goroutine reaps idle entries.
		aigenRateLimit := mw.NewRateLimiterStore(rate.Every(10*time.Second), 3)
		formAIGenHandler = forms.NewAIGenHandler(formsSvc, formGenSvc, aigenClinicLookup, aigenRateLimit)
		policyAIGenHandler = policy.NewAIGenHandler(policySvc, policyGenSvc, aigenClinicLookup, aigenRateLimit)
	case errors.Is(aigenErr, aigen.ErrProviderNotConfigured):
		log.Info("aigen: no provider configured — /generate routes disabled")
	default:
		return nil, fmt.Errorf("app.Build: aigen provider: %w", aigenErr)
	}

	// ── Reports module ────────────────────────────────────────────────────────
	// Build the real compliance data adapter now that drugs / clinic / staff
	// are all constructed, and resolve the lazy wrapper used by the worker.
	complianceData.inner = &complianceDataAdapter{
		clinicSvc: clinicSvc,
		staffSvc:  staffSvc,
		drugsSvc:  drugsSvc,
	}
	reportsSvc := reports.NewService(reportsRepo, riverClient, complianceData)
	reportsHandler := reports.NewHandler(reportsSvc, store)

	// ── Marketplace module ───────────────────────────────────────────────────
	marketplaceRepo := marketplace.NewRepository(db)
	stripeClient := marketplace.NewStripeSDKClient(cfg.StripeAPIKey, cfg.StripeWebhookSecret)
	marketplaceSvc := marketplace.NewService(
		marketplaceRepo,
		&marketplaceSnapshotAdapter{formsRepo: formsRepo},
		&marketplacePolicySnapshotAdapter{policyRepo: policyRepo},
		&marketplaceImporterAdapter{formsSvc: formsSvc},
		&marketplacePolicyImporterAdapter{policySvc: policySvc},
		&marketplacePolicyNamerAdapter{policyRepo: policyRepo},
		&marketplaceClinicInfoAdapter{clinicSvc: clinicSvc},
		stripeClient,
		marketplace.ServiceConfig{
			PlatformFeeRegularPct: cfg.MarketplacePlatformFeePct,
			PolicyAttribution:     cfg.MarketplacePolicyAttribution,
		},
	)
	marketplaceHandler := marketplace.NewHandler(marketplaceSvc)

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(mw.RequestLogger(log))
	r.Use(chimw.Recoverer)
	r.Use(chimw.RequestSize(8 * 1024 * 1024)) // 8 MB — audio uploads bypass via S3 presigned URLs
	// Grace-period write-block: if the clinic's Stripe subscription has
	// gone unpaid past the dunning window, every write returns 402 until
	// they pay. Reads + auth/billing/health prefixes pass through so the
	// clinic can sign in and recover.
	r.Use(mw.BlockWritesOnGracePeriod(&clinicStatusAdapter{clinic: clinicSvc}, jwtSecret, log))
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
	verticalsHandler.Mount(r, api, jwtSecret)
	audioHandler.Mount(r, api, jwtSecret)
	formsHandler.Mount(r, api, jwtSecret)
	if formAIGenHandler != nil {
		formAIGenHandler.Mount(r, api, jwtSecret)
	}
	notesHandler.Mount(r, api, jwtSecret)
	timelineHandler.Mount(r, api, jwtSecret)
	notificationsHandler.Mount(r, jwtSecret)
	policyHandler.Mount(r, api, jwtSecret)
	if policyAIGenHandler != nil {
		policyAIGenHandler.Mount(r, api, jwtSecret)
	}
	drugsHandler.Mount(r, api, jwtSecret)
	incidentsHandler.Mount(r, api, jwtSecret)
	reportsHandler.Mount(r, api, jwtSecret)
	marketplaceHandler.Mount(r, api, jwtSecret)
	if billingHandler != nil {
		billingHandler.Mount(r, api, jwtSecret)
	}

	// Health check — no auth, no logging overhead.
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := db.Ping(req.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"db_unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	return &App{
		Server: &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Port),
			Handler:      r,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		DB:          db,
		Log:         log,
		RiverClient: riverClient,
		Broker:      broker,
	}, nil
}

// ── Cross-module adapters ─────────────────────────────────────────────────────
// These bridge notes' provider interfaces to the concrete audio/forms/timeline repos.
// They live here because app.go is the only place allowed to wire cross-module deps.

// timelineEventAdapter implements notes.EventEmitter by writing to the timeline repo.
// Errors are logged but never propagated — event emission is best-effort.
type timelineEventAdapter struct {
	repo *timeline.Repository
	log  *slog.Logger
}

func (a *timelineEventAdapter) Emit(ctx context.Context, e notes.NoteEvent) {
	err := a.repo.InsertNoteEvent(ctx, timeline.InsertEventParams{
		ID:         domain.NewID(),
		NoteID:     e.NoteID,
		SubjectID:  e.SubjectID,
		ClinicID:   e.ClinicID,
		EventType:  string(e.EventType),
		FieldID:    e.FieldID,
		OldValue:   e.OldValue,
		NewValue:   e.NewValue,
		ActorID:    e.ActorID,
		ActorRole:  e.ActorRole,
		Reason:     e.Reason,
		OccurredAt: domain.TimeNow(),
	})
	if err != nil {
		a.log.Error("timeline: failed to emit note event",
			"error", err,
			"note_id", e.NoteID,
			"event_type", string(e.EventType),
		)
	}
}

type formsFieldAdapter struct{ repo *forms.Repository }

func (a *formsFieldAdapter) GetFieldsByVersionID(ctx context.Context, versionID uuid.UUID) ([]notes.FormFieldMeta, error) {
	fields, err := a.repo.GetFieldsByVersionID(ctx, versionID)
	if err != nil {
		return nil, fmt.Errorf("app.formsFieldAdapter: %w", err)
	}
	out := make([]notes.FormFieldMeta, len(fields))
	for i, f := range fields {
		out[i] = notes.FormFieldMeta{
			ID:             f.ID,
			Title:          f.Title,
			Type:           f.Type,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  f.MinConfidence,
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

func (a *audioTranscriptAdapter) GetWordConfidences(ctx context.Context, recordingID uuid.UUID) ([]confidence.WordConfidence, error) {
	wc, err := a.repo.GetWordConfidences(ctx, recordingID)
	if err != nil {
		return nil, fmt.Errorf("app.audioTranscriptAdapter: %w", err)
	}
	return wc, nil
}

// adminBootstrapAdapter implements clinic.AdminBootstrapper.
// After clinic registration, it creates the first super admin and sends a magic link.
// auth and staff are set after their respective services are constructed.
type adminBootstrapAdapter struct {
	auth  *auth.Service
	staff *staff.Service
}

func (a *adminBootstrapAdapter) Bootstrap(ctx context.Context, clinicID uuid.UUID, email, name string) error {
	if _, err := a.staff.Create(ctx, staff.CreateStaffInput{
		ClinicID:    clinicID,
		Email:       email,
		FullName:    name,
		Role:        domain.StaffRoleSuperAdmin,
		NoteTier:    domain.NoteTierStandard,
		Permissions: domain.DefaultPermissions(domain.StaffRoleSuperAdmin),
	}); err != nil {
		return fmt.Errorf("app.adminBootstrapAdapter: create staff: %w", err)
	}
	if err := a.auth.SendMagicLink(ctx, email, nil); err != nil {
		return fmt.Errorf("app.adminBootstrapAdapter: send magic link: %w", err)
	}
	return nil
}

// billingClinicAdapter implements billing.ClinicUpdater by bridging to
// clinic.Service. Billing never imports the clinic package directly.
type billingClinicAdapter struct{ clinic *clinic.Service }

func (a *billingClinicAdapter) FindByStripeCustomerID(ctx context.Context, stripeCustomerID string) (uuid.UUID, error) {
	id, err := a.clinic.GetIDByStripeCustomer(ctx, stripeCustomerID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("app.billingClinicAdapter.FindByStripeCustomerID: %w", err)
	}
	return id, nil
}

func (a *billingClinicAdapter) GetStripeCustomerID(ctx context.Context, clinicID uuid.UUID) (*string, error) {
	id, err := a.clinic.GetStripeCustomerID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.billingClinicAdapter.GetStripeCustomerID: %w", err)
	}
	return id, nil
}

func (a *billingClinicAdapter) GetClinicProfile(ctx context.Context, clinicID uuid.UUID) (billing.ClinicProfile, error) {
	c, err := a.clinic.GetByID(ctx, clinicID)
	if err != nil {
		return billing.ClinicProfile{}, fmt.Errorf("app.billingClinicAdapter.GetClinicProfile: %w", err)
	}
	return billing.ClinicProfile{Email: c.Email, Name: c.Name}, nil
}

func (a *billingClinicAdapter) ApplySubscriptionState(ctx context.Context, clinicID uuid.UUID, s billing.SubscriptionState) error {
	if err := a.clinic.ApplyBillingState(ctx, clinicID, clinic.BillingState{
		Status:               s.Status,
		PlanCode:             s.PlanCode,
		StripeCustomerID:     s.StripeCustomerID,
		StripeSubscriptionID: s.StripeSubscriptionID,
		BillingPeriodStart:   s.BillingPeriodStart,
		BillingPeriodEnd:     s.BillingPeriodEnd,
	}); err != nil {
		return fmt.Errorf("app.billingClinicAdapter.ApplySubscriptionState: %w", err)
	}
	return nil
}

// staticPlanLookup implements billing.PlanLookup from a parsed env map.
// Holds the forward (price → plan) and reverse (plan → price) indexes —
// the reverse is computed once at startup so Checkout calls don't scan
// the map on every request.
type staticPlanLookup struct {
	byPrice map[string]domain.PlanCode
	byPlan  map[domain.PlanCode]string
}

func newStaticPlanLookup(byPrice map[string]domain.PlanCode) *staticPlanLookup {
	byPlan := make(map[domain.PlanCode]string, len(byPrice))
	for priceID, plan := range byPrice {
		// Last write wins on duplicate plan codes — config validation
		// already rejects unknown codes; duplicate codes (two prices
		// mapped to the same plan) are unusual but harmless: either
		// price will roundtrip the webhook back to the same plan.
		byPlan[plan] = priceID
	}
	return &staticPlanLookup{byPrice: byPrice, byPlan: byPlan}
}

func (m *staticPlanLookup) PlanCodeForStripePriceID(id string) (domain.PlanCode, bool) {
	pc, ok := m.byPrice[id]
	return pc, ok
}

func (m *staticPlanLookup) StripePriceIDForPlanCode(plan domain.PlanCode) (string, bool) {
	id, ok := m.byPlan[plan]
	return id, ok
}

// notecapClinicAdapter implements notecap.ClinicReader against
// clinic.Service. Lives in app.go because notecap must not import the
// clinic package directly per the cross-domain rule.
type notecapClinicAdapter struct{ clinic *clinic.Service }

func (a *notecapClinicAdapter) LoadForCap(ctx context.Context, clinicID uuid.UUID) (notecap.ClinicState, error) {
	st, err := a.clinic.LoadNoteCapState(ctx, clinicID)
	if err != nil {
		return notecap.ClinicState{}, fmt.Errorf("app.notecapClinicAdapter.LoadForCap: %w", err)
	}
	return notecap.ClinicState{
		ID:                 st.ID,
		Name:               st.Name,
		AdminEmail:         st.Email,
		Status:             st.Status,
		PlanCode:           st.PlanCode,
		BillingPeriodStart: st.BillingPeriodStart,
		CreatedAt:          st.CreatedAt,
		NoteCapWarnedAt:    st.NoteCapWarnedAt,
		NoteCapCSAlertedAt: st.NoteCapCSAlertedAt,
		NoteCapBlockedAt:   st.NoteCapBlockedAt,
	}, nil
}

func (a *notecapClinicAdapter) MarkNoteCapWarned(ctx context.Context, clinicID uuid.UUID) (bool, error) {
	claimed, err := a.clinic.MarkNoteCapWarned(ctx, clinicID)
	if err != nil {
		return false, fmt.Errorf("app.notecapClinicAdapter.MarkNoteCapWarned: %w", err)
	}
	return claimed, nil
}

func (a *notecapClinicAdapter) MarkNoteCapCSAlerted(ctx context.Context, clinicID uuid.UUID) (bool, error) {
	claimed, err := a.clinic.MarkNoteCapCSAlerted(ctx, clinicID)
	if err != nil {
		return false, fmt.Errorf("app.notecapClinicAdapter.MarkNoteCapCSAlerted: %w", err)
	}
	return claimed, nil
}

func (a *notecapClinicAdapter) MarkNoteCapBlocked(ctx context.Context, clinicID uuid.UUID) (bool, error) {
	claimed, err := a.clinic.MarkNoteCapBlocked(ctx, clinicID)
	if err != nil {
		return false, fmt.Errorf("app.notecapClinicAdapter.MarkNoteCapBlocked: %w", err)
	}
	return claimed, nil
}

// notecapNotesAdapter implements notecap.NoteCounter by bridging to the
// notes repository's per-period count. Repo here, not service: the
// service-level count would force unrelated event/policy work that's
// not needed for a hot-path COUNT(*).
type notecapNotesAdapter struct{ notes *notes.Repository }

func (a *notecapNotesAdapter) CountSinceForClinic(ctx context.Context, clinicID uuid.UUID, since time.Time) (int, error) {
	n, err := a.notes.CountSinceForClinic(ctx, clinicID, since)
	if err != nil {
		return 0, fmt.Errorf("app.notecapNotesAdapter.CountSinceForClinic: %w", err)
	}
	return n, nil
}

// tieringClinicAdapter implements tiering.ClinicReader against
// clinic.Service. Lives in app.go because tiering must not import the
// clinic package directly per the cross-domain rule.
type tieringClinicAdapter struct{ clinic *clinic.Service }

func (a *tieringClinicAdapter) LoadTierState(ctx context.Context, clinicID uuid.UUID) (tiering.ClinicState, error) {
	st, err := a.clinic.LoadTierState(ctx, clinicID)
	if err != nil {
		return tiering.ClinicState{}, fmt.Errorf("app.tieringClinicAdapter.LoadTierState: %w", err)
	}
	return tiering.ClinicState{
		Status:               st.Status,
		PlanCode:             st.PlanCode,
		StripeSubscriptionID: st.StripeSubscriptionID,
	}, nil
}

// clinicStatusAdapter implements mw.ClinicStatusReader against
// clinic.Service. Lives in app.go because the middleware package must
// not import the clinic package.
type clinicStatusAdapter struct{ clinic *clinic.Service }

func (a *clinicStatusAdapter) GetStatus(ctx context.Context, clinicID uuid.UUID) (domain.ClinicStatus, error) {
	st, err := a.clinic.GetStatus(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("app.clinicStatusAdapter.GetStatus: %w", err)
	}
	return st, nil
}

// tieringStaffAdapter implements tiering.StaffCounter by bridging to
// staff.Service's standard-seat count.
type tieringStaffAdapter struct{ staff *staff.Service }

func (a *tieringStaffAdapter) CountStandardActive(ctx context.Context, clinicID uuid.UUID) (int, error) {
	n, err := a.staff.CountStandardActive(ctx, clinicID)
	if err != nil {
		return 0, fmt.Errorf("app.tieringStaffAdapter.CountStandardActive: %w", err)
	}
	return n, nil
}

// signupCheckoutAdapter implements auth.SignupCheckoutClient by composing
// the billing primitives (Stripe customer creation, Checkout session
// creation, plan-code → price-id lookup). Lives in app.go because auth
// must not import billing directly per the cross-domain rule.
type signupCheckoutAdapter struct {
	customers billing.StripeCustomerCreator
	checkout  billing.CheckoutSessionCreator
	plans     billing.PlanLookup
}

func (a *signupCheckoutAdapter) CreateCustomer(email, clinicName string) (string, error) {
	// clinic_id is empty — no clinic row exists yet at this point in the
	// signup flow. The Stripe customer client elides the metadata key
	// when clinicID is "" so the dashboard isn't polluted.
	id, err := a.customers.Create(email, clinicName, "")
	if err != nil {
		return "", fmt.Errorf("app.signupCheckoutAdapter.CreateCustomer: %w", err)
	}
	return id, nil
}

func (a *signupCheckoutAdapter) CreateCheckoutSession(p auth.SignupCheckoutSessionInput) (string, error) {
	url, err := a.checkout.Create(billing.CheckoutParams{
		CustomerID: p.CustomerID,
		PriceID:    p.PriceID,
		SuccessURL: p.SuccessURL,
		CancelURL:  p.CancelURL,
		TrialDays:  p.TrialDays,
		// ClinicID intentionally zero — no clinic exists yet. The Stripe
		// CheckoutSession metadata gets the zero-uuid string, which is
		// fine: signup-checkout subscriptions resolve via cus_… on the
		// webhook, not via the metadata.
	})
	if err != nil {
		return "", fmt.Errorf("app.signupCheckoutAdapter.CreateCheckoutSession: %w", err)
	}
	return url, nil
}

func (a *signupCheckoutAdapter) PriceIDForPlanCode(planCode domain.PlanCode) (string, bool) {
	return a.plans.StripePriceIDForPlanCode(planCode)
}

// melHandoffAdapter implements auth.HandoffProvisioner by bridging to
// clinic.HandoffProvision + staff.EnsureOwner. Both calls are idempotent on
// email_hash so replaying the same email (with a fresh jti) returns the
// existing rows.
type melHandoffAdapter struct {
	clinic *clinic.Service
	staff  *staff.Service
}

func (a *melHandoffAdapter) ProvisionFromHandoff(ctx context.Context, in auth.HandoffProvisionInput) (uuid.UUID, uuid.UUID, error) {
	c, err := a.clinic.HandoffProvision(ctx, clinic.HandoffProvisionInput{
		Email:            in.Email,
		ClinicName:       in.ClinicName,
		Vertical:         in.Vertical,
		PlanCode:         in.PlanCode,
		StripeCustomerID: in.StripeCustomerID,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("app.melHandoffAdapter: provision clinic: %w", err)
	}
	clinicID, err := uuid.Parse(c.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("app.melHandoffAdapter: parse clinic id: %w", err)
	}

	s, err := a.staff.EnsureOwner(ctx, clinicID, in.Email, in.FullName)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("app.melHandoffAdapter: ensure owner: %w", err)
	}
	staffID, err := uuid.Parse(s.ID)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("app.melHandoffAdapter: parse staff id: %w", err)
	}
	return clinicID, staffID, nil
}

// clinicLogoAdapter implements clinic.LogoUploader and clinic.LogoSigner against
// the platform/storage S3 client. Logos are stored under logos/{clinic_id}/.
type clinicLogoAdapter struct {
	store *storage.Store
}

func (a *clinicLogoAdapter) UploadLogo(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (string, error) {
	ext := logoExtForContentType(contentType)
	key := fmt.Sprintf("logos/%s/%s%s", clinicID, domain.NewID(), ext)
	if err := a.store.Upload(ctx, key, contentType, body, size); err != nil {
		return "", fmt.Errorf("app.clinicLogoAdapter.UploadLogo: %w", err)
	}
	return key, nil
}

func (a *clinicLogoAdapter) SignLogoURL(ctx context.Context, key string) (string, error) {
	url, err := a.store.PresignDownload(ctx, key, time.Hour)
	if err != nil {
		return "", fmt.Errorf("app.clinicLogoAdapter.SignLogoURL: %w", err)
	}
	return url, nil
}

// docThemeLogoAdapter implements forms.StyleLogoUploader and forms.StyleLogoSigner
// against the platform/storage S3 client. Doc-theme logos are stored under
// form-style-logos/{clinic_id}/ so they stay distinct from the clinic-wide
// logo written by clinicLogoAdapter.
type docThemeLogoAdapter struct {
	store *storage.Store
}

func (a *docThemeLogoAdapter) UploadStyleLogo(ctx context.Context, clinicID uuid.UUID, contentType string, body io.Reader, size int64) (string, error) {
	ext := logoExtForContentType(contentType)
	key := fmt.Sprintf("form-style-logos/%s/%s%s", clinicID, domain.NewID(), ext)
	if err := a.store.Upload(ctx, key, contentType, body, size); err != nil {
		return "", fmt.Errorf("app.docThemeLogoAdapter.UploadStyleLogo: %w", err)
	}
	return key, nil
}

func (a *docThemeLogoAdapter) SignStyleLogoURL(ctx context.Context, key string) (string, error) {
	url, err := a.store.PresignDownload(ctx, key, time.Hour)
	if err != nil {
		return "", fmt.Errorf("app.docThemeLogoAdapter.SignStyleLogoURL: %w", err)
	}
	return url, nil
}

func logoExtForContentType(ct string) string {
	switch ct {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	return ""
}

// lazyEnqueuer wraps a *river.Client that is set after river.NewClient returns.
// Workers registered before the client is created use this to enqueue downstream jobs.
type lazyEnqueuer struct {
	client *river.Client[pgx.Tx]
}

func (e *lazyEnqueuer) Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if e.client == nil {
		return nil, fmt.Errorf("app.lazyEnqueuer: client not yet initialized")
	}
	res, err := e.client.Insert(ctx, args, opts)
	if err != nil {
		return nil, fmt.Errorf("app.lazyEnqueuer: %w", err)
	}
	return res, nil
}

// policyClauseProviderAdapter implements notes.PolicyClauseProvider.
// Given a form version ID it traverses form_policies → policy versions → clauses.
type policyClauseProviderAdapter struct {
	forms  *forms.Repository
	policy *policy.Repository
}

func (a *policyClauseProviderAdapter) GetClausesForNote(ctx context.Context, formVersionID uuid.UUID) ([]notes.PolicyClause, error) {
	version, err := a.forms.GetVersionByID(ctx, formVersionID)
	if err != nil {
		return nil, fmt.Errorf("app.policyClauseProviderAdapter: get version: %w", err)
	}

	policyIDs, err := a.forms.ListLinkedPolicies(ctx, version.FormID)
	if err != nil {
		return nil, fmt.Errorf("app.policyClauseProviderAdapter: list policies: %w", err)
	}
	if len(policyIDs) == 0 {
		return nil, nil
	}

	clauses, err := a.policy.GetLatestClausesForPolicies(ctx, policyIDs)
	if err != nil {
		return nil, fmt.Errorf("app.policyClauseProviderAdapter: get clauses: %w", err)
	}

	result := make([]notes.PolicyClause, 0, len(clauses))
	for _, c := range clauses {
		result = append(result, notes.PolicyClause{
			PolicyID: c.PolicyID.String(),
			BlockID:  c.BlockID,
			Title:    c.Title,
			Parity:   c.Parity,
		})
	}
	return result, nil
}

// formsPolicyOwnershipAdapter implements forms.PolicyOwnershipVerifier by
// round-tripping through the policy repository's clinic-scoped lookup. A
// mismatch surfaces as domain.ErrNotFound, which LinkPolicy then wraps into
// its own error chain — the caller sees a 404, never a 403, so cross-tenant
// IDs aren't distinguishable from non-existent ones.
type formsPolicyOwnershipAdapter struct {
	policy *policy.Repository
}

func (a *formsPolicyOwnershipAdapter) VerifyPolicyOwnership(ctx context.Context, policyID, clinicID uuid.UUID) error {
	if _, err := a.policy.GetPolicyByID(ctx, policyID, clinicID); err != nil {
		return fmt.Errorf("app.formsPolicyOwnershipAdapter: %w", err)
	}
	return nil
}

// formPolicyClauseFetcherAdapter implements forms.PolicyClauseFetcher.
// For a given form, it traverses form_policies → latest published policy version → clauses.
type formPolicyClauseFetcherAdapter struct {
	forms  *forms.Repository
	policy *policy.Repository
}

func (a *formPolicyClauseFetcherAdapter) GetClausesForForm(ctx context.Context, formID uuid.UUID) ([]forms.LinkedPolicyClauses, error) {
	policyIDs, err := a.forms.ListLinkedPolicies(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("app.formPolicyClauseFetcherAdapter: list policies: %w", err)
	}
	if len(policyIDs) == 0 {
		return nil, nil
	}

	clauses, err := a.policy.GetLatestClausesForPolicies(ctx, policyIDs)
	if err != nil {
		return nil, fmt.Errorf("app.formPolicyClauseFetcherAdapter: get clauses: %w", err)
	}

	byPolicy := make(map[uuid.UUID]*forms.LinkedPolicyClauses)
	order := make([]uuid.UUID, 0)
	for _, c := range clauses {
		g, ok := byPolicy[c.PolicyID]
		if !ok {
			g = &forms.LinkedPolicyClauses{
				PolicyID:        c.PolicyID,
				PolicyVersionID: c.PolicyVersionID,
			}
			byPolicy[c.PolicyID] = g
			order = append(order, c.PolicyID)
		}
		g.Clauses = append(g.Clauses, extraction.PolicyClause{
			BlockID: c.BlockID,
			Title:   c.Title,
			Parity:  c.Parity,
		})
	}

	result := make([]forms.LinkedPolicyClauses, 0, len(order))
	for _, id := range order {
		result = append(result, *byPolicy[id])
	}
	return result, nil
}

// staffCreatorAdapter implements auth.StaffCreator.
// When an invite is accepted, the auth module calls this to create the staff record.
// The staff field is set lazily after staff.Service is constructed.
type staffCreatorAdapter struct {
	staff *staff.Service
}

func (a *staffCreatorAdapter) CreateFromInvite(ctx context.Context, in auth.CreateStaffFromInviteInput) (uuid.UUID, error) {
	resp, err := a.staff.Create(ctx, staff.CreateStaffInput{
		ClinicID:    in.ClinicID,
		Email:       in.Email,
		FullName:    in.FullName,
		Role:        in.Role,
		NoteTier:    in.NoteTier,
		Permissions: in.Permissions,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("app.staffCreatorAdapter: %w", err)
	}

	id, err := uuid.Parse(resp.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("app.staffCreatorAdapter: parse id: %w", err)
	}
	return id, nil
}

// inviteCreatorAdapter implements staff.InviteCreator.
// When an admin invites a staff member, the staff module calls this to create the invite token.
type inviteCreatorAdapter struct {
	auth   *auth.Service
	cipher *crypto.Cipher
}

func (a *inviteCreatorAdapter) CreateInvite(ctx context.Context, params staff.CreateInviteTokenParams) (string, error) {
	emailHash := a.cipher.Hash(params.Email)
	token, err := a.auth.CreateInviteToken(ctx, params.ClinicID, params.Email, emailHash, params.Role, params.NoteTier, params.Permissions, params.InvitedByID)
	if err != nil {
		return "", fmt.Errorf("app.inviteCreatorAdapter: %w", err)
	}
	return token, nil
}

// clinicNameProviderAdapter implements staff.ClinicNameProvider.
// Resolves a clinic's display name for invitation emails.
type clinicNameProviderAdapter struct {
	clinic *clinic.Service
}

func (a *clinicNameProviderAdapter) GetClinicName(ctx context.Context, clinicID uuid.UUID) (string, error) {
	c, err := a.clinic.GetByID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("app.clinicNameProviderAdapter: %w", err)
	}
	return c.Name, nil
}

// clinicVerticalProviderAdapter implements verticals.ClinicVerticalProvider.
// Resolves a clinic's vertical so the verticals service can return the
// matching form schema.
type clinicVerticalProviderAdapter struct {
	clinic *clinic.Service
}

func (a *clinicVerticalProviderAdapter) GetClinicVertical(ctx context.Context, clinicID uuid.UUID) (domain.Vertical, error) {
	c, err := a.clinic.GetByID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("app.clinicVerticalProviderAdapter: %w", err)
	}
	return c.Vertical, nil
}

// drugsClinicLookupAdapter satisfies drugs.ClinicLookup. Returns the
// clinic's vertical + country from clinic.Service.GetByID.
type drugsClinicLookupAdapter struct {
	clinicSvc *clinic.Service
}

func (a *drugsClinicLookupAdapter) GetVerticalAndCountry(ctx context.Context, clinicID uuid.UUID) (string, string, error) {
	c, err := a.clinicSvc.GetByID(ctx, clinicID)
	if err != nil {
		return "", "", fmt.Errorf("app.drugsClinicLookupAdapter: %w", err)
	}
	return string(c.Vertical), c.Country, nil
}

// drugsStaffPermAdapter satisfies drugs.StaffPermLookup. v1 maps every
// "perm_*_drug*" name back onto the existing domain.Permissions struct
// fields — the JWT path doesn't yet ship the granular drug perms shipped
// in migration 00062.
type drugsStaffPermAdapter struct {
	staffSvc *staff.Service
}

func (a *drugsStaffPermAdapter) HasPermission(ctx context.Context, staffID, clinicID uuid.UUID, name string) (bool, error) {
	s, err := a.staffSvc.GetByID(ctx, staffID, clinicID)
	if err != nil {
		return false, fmt.Errorf("app.drugsStaffPermAdapter: %w", err)
	}
	switch name {
	case "perm_witness_controlled_drugs", "perm_dispense_controlled_drugs":
		return s.Permissions.Dispense, nil
	case "perm_manage_drug_shelf":
		return s.Permissions.ManagePatients, nil
	case "perm_reconcile_drugs":
		return s.Permissions.GenerateAuditExport, nil
	default:
		return false, nil
	}
}

// lazyComplianceData wraps a reports.ComplianceDataSource that's set after
// the river client is constructed. The compliance PDF worker is registered
// before drugs / clinic / staff services exist; this lazy wrapper lets the
// worker compile-time bind to the data source without forcing all those
// services to be created earlier.
type lazyComplianceData struct {
	inner reports.ComplianceDataSource
}

func (l *lazyComplianceData) GetClinic(ctx context.Context, clinicID uuid.UUID) (*reports.ClinicSnapshot, error) {
	if l.inner == nil {
		return nil, fmt.Errorf("app.lazyComplianceData: not yet wired")
	}
	c, err := l.inner.GetClinic(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.lazyComplianceData.GetClinic: %w", err)
	}
	return c, nil
}
func (l *lazyComplianceData) GetStaffName(ctx context.Context, clinicID, staffID uuid.UUID) (string, error) {
	if l.inner == nil {
		return "", fmt.Errorf("app.lazyComplianceData: not yet wired")
	}
	name, err := l.inner.GetStaffName(ctx, clinicID, staffID)
	if err != nil {
		return "", fmt.Errorf("app.lazyComplianceData.GetStaffName: %w", err)
	}
	return name, nil
}
func (l *lazyComplianceData) ListControlledDrugOps(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]reports.DrugOpView, error) {
	if l.inner == nil {
		return nil, fmt.Errorf("app.lazyComplianceData: not yet wired")
	}
	ops, err := l.inner.ListControlledDrugOps(ctx, clinicID, from, to)
	if err != nil {
		return nil, fmt.Errorf("app.lazyComplianceData.ListControlledDrugOps: %w", err)
	}
	return ops, nil
}
func (l *lazyComplianceData) ListReconciliationsInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]reports.DrugReconciliationView, error) {
	if l.inner == nil {
		return nil, fmt.Errorf("app.lazyComplianceData: not yet wired")
	}
	recs, err := l.inner.ListReconciliationsInPeriod(ctx, clinicID, from, to)
	if err != nil {
		return nil, fmt.Errorf("app.lazyComplianceData.ListReconciliationsInPeriod: %w", err)
	}
	return recs, nil
}
func (l *lazyComplianceData) CountNotesByStatus(ctx context.Context, clinicID uuid.UUID, from, to time.Time) (map[string]int, error) {
	if l.inner == nil {
		return nil, fmt.Errorf("app.lazyComplianceData: not yet wired")
	}
	counts, err := l.inner.CountNotesByStatus(ctx, clinicID, from, to)
	if err != nil {
		return nil, fmt.Errorf("app.lazyComplianceData.CountNotesByStatus: %w", err)
	}
	return counts, nil
}

// complianceDataAdapter satisfies reports.ComplianceDataSource by wrapping
// clinic.Service + drugs.Service + staff.Service. Every cross-domain access
// goes through these public services — reports never queries another
// domain's tables directly.
type complianceDataAdapter struct {
	clinicSvc *clinic.Service
	staffSvc  *staff.Service
	drugsSvc  *drugs.Service
}

func (a *complianceDataAdapter) GetClinic(ctx context.Context, clinicID uuid.UUID) (*reports.ClinicSnapshot, error) {
	c, err := a.clinicSvc.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.complianceDataAdapter.GetClinic: %w", err)
	}
	legal := ""
	if c.LegalName != nil {
		legal = *c.LegalName
	}
	email := c.Email
	return &reports.ClinicSnapshot{
		Name:      c.Name,
		LegalName: legal,
		Vertical:  string(c.Vertical),
		Country:   c.Country,
		Address:   c.Address,
		Phone:     c.Phone,
		Email:     &email,
		License:   c.BusinessRegNo,
	}, nil
}

func (a *complianceDataAdapter) GetStaffName(ctx context.Context, clinicID, staffID uuid.UUID) (string, error) {
	s, err := a.staffSvc.GetByID(ctx, staffID, clinicID)
	if err != nil {
		// Don't fail the whole report on a single name miss; PDF degrades
		// to the UUID short form.
		return staffID.String()[:8], nil
	}
	return s.FullName, nil
}

// ListControlledDrugOps lists every drug operation in the period whose
// underlying catalog entry is controlled (Schedule != "" + IsControlled
// implied by witness rule). For v1 we filter inside the loop; future work
// can push the filter into the drugs service.
func (a *complianceDataAdapter) ListControlledDrugOps(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]reports.DrugOpView, error) {
	// Page through the ledger.
	out := []reports.DrugOpView{}
	const pageSize = 200
	offset := 0
	for {
		list, err := a.drugsSvc.ListOperations(ctx, clinicID, drugs.ListOperationsInput{
			Limit:  pageSize,
			Offset: offset,
			Since:  &from,
			Until:  &to,
		})
		if err != nil {
			return nil, fmt.Errorf("app.complianceDataAdapter.ListControlledDrugOps: %w", err)
		}
		for _, op := range list.Items {
			view, ok := a.translateOp(ctx, clinicID, op)
			if !ok {
				continue
			}
			out = append(out, view)
		}
		if len(list.Items) < pageSize {
			break
		}
		offset += pageSize
	}
	return out, nil
}

// translateOp converts a drugs.OperationResponse into a reports.DrugOpView
// and resolves shelf label + schedule from the catalog. Returns (_, false)
// when the underlying drug isn't controlled — the register PDF only shows
// controlled-drug ops.
func (a *complianceDataAdapter) translateOp(ctx context.Context, clinicID uuid.UUID, op *drugs.OperationResponse) (reports.DrugOpView, bool) {
	shelfID, err := uuid.Parse(op.ShelfID)
	if err != nil {
		return reports.DrugOpView{}, false
	}
	shelf, err := a.drugsSvc.GetShelfEntry(ctx, shelfID, clinicID)
	if err != nil {
		return reports.DrugOpView{}, false
	}
	if shelf.CatalogID == nil {
		// Override drugs aren't controlled in v1.
		return reports.DrugOpView{}, false
	}
	entry, err := a.drugsSvc.LookupCatalogEntry(ctx, clinicID, *shelf.CatalogID)
	if err != nil || entry == nil || !entry.IsControlled {
		return reports.DrugOpView{}, false
	}
	label := entry.Name
	if shelf.Strength != nil {
		label += " " + *shelf.Strength
	}
	schedule := entry.Schedule

	createdAt, _ := time.Parse(time.RFC3339, op.CreatedAt)

	administeredBy := op.AdministeredBy
	if id, err := uuid.Parse(op.AdministeredBy); err == nil {
		if name, err := a.GetStaffName(ctx, clinicID, id); err == nil {
			administeredBy = name
		}
	}

	var witnessName *string
	if op.WitnessedBy != nil {
		if id, err := uuid.Parse(*op.WitnessedBy); err == nil {
			if name, err := a.GetStaffName(ctx, clinicID, id); err == nil {
				witnessName = &name
			} else {
				witnessName = op.WitnessedBy
			}
		}
	}

	return reports.DrugOpView{
		ID:             op.ID,
		ShelfID:        op.ShelfID,
		ShelfLabel:     label,
		Operation:      op.Operation,
		Quantity:       op.Quantity,
		Unit:           op.Unit,
		BalanceAfter:   op.BalanceAfter,
		Dose:           op.Dose,
		Route:          op.Route,
		Reason:         op.ReasonIndication,
		Schedule:       schedule,
		BatchNumber:    shelf.BatchNumber,
		Location:       shelf.Location,
		SubjectID:      op.SubjectID,
		AdministeredBy: administeredBy,
		WitnessedBy:    witnessName,
		CreatedAt:      createdAt,
	}, true
}

func (a *complianceDataAdapter) ListReconciliationsInPeriod(ctx context.Context, clinicID uuid.UUID, from, to time.Time) ([]reports.DrugReconciliationView, error) {
	list, err := a.drugsSvc.ListReconciliations(ctx, clinicID, drugs.ListReconciliationsInput{
		Limit: 200,
		Since: &from,
		Until: &to,
	})
	if err != nil {
		return nil, fmt.Errorf("app.complianceDataAdapter.ListReconciliations: %w", err)
	}
	out := make([]reports.DrugReconciliationView, 0, len(list.Items))
	for _, r := range list.Items {
		shelfID, err := uuid.Parse(r.ShelfID)
		if err != nil {
			continue
		}
		shelf, err := a.drugsSvc.GetShelfEntry(ctx, shelfID, clinicID)
		if err != nil {
			continue
		}
		label := "(unknown drug)"
		if shelf.CatalogID != nil {
			if entry, err := a.drugsSvc.LookupCatalogEntry(ctx, clinicID, *shelf.CatalogID); err == nil && entry != nil {
				label = entry.Name
				if shelf.Strength != nil {
					label += " " + *shelf.Strength
				}
			}
		}
		periodStart, _ := time.Parse(time.RFC3339, r.PeriodStart)
		periodEnd, _ := time.Parse(time.RFC3339, r.PeriodEnd)
		createdAt, _ := time.Parse(time.RFC3339, r.CreatedAt)
		_ = createdAt

		primary := r.ReconciledByPrimary
		if id, err := uuid.Parse(r.ReconciledByPrimary); err == nil {
			if name, err := a.GetStaffName(ctx, clinicID, id); err == nil {
				primary = name
			}
		}
		var secondary *string
		if r.ReconciledBySecondary != nil {
			if id, err := uuid.Parse(*r.ReconciledBySecondary); err == nil {
				if name, err := a.GetStaffName(ctx, clinicID, id); err == nil {
					secondary = &name
				} else {
					secondary = r.ReconciledBySecondary
				}
			}
		}

		out = append(out, reports.DrugReconciliationView{
			ID:                r.ID,
			ShelfLabel:        label,
			PeriodStart:       periodStart,
			PeriodEnd:         periodEnd,
			PhysicalCount:     r.PhysicalCount,
			LedgerCount:       r.LedgerCount,
			Discrepancy:       r.Discrepancy,
			Status:            r.Status,
			PrimarySignedBy:   primary,
			SecondarySignedBy: secondary,
			Explanation:       r.DiscrepancyExplanation,
		})
	}
	return out, nil
}

// CountNotesByStatus is a v1 stub. The notes service doesn't yet expose a
// status-aggregation method; the audit pack PDF degrades to a "no notes
// recorded" message when the map is empty. TODO: wire to notes.Service.
func (a *complianceDataAdapter) CountNotesByStatus(ctx context.Context, clinicID uuid.UUID, from, to time.Time) (map[string]int, error) {
	return map[string]int{}, nil
}

// drugsAccessLogAdapter satisfies drugs.SubjectAccessLogger. Wraps the
// patient repository's CreateSubjectAccessLog so the drugs service can
// trace every drug-history view + every administer/dispense touch on
// PII without importing patient types directly.
type drugsAccessLogAdapter struct {
	patientRepo *patient.Repository
}

func (a *drugsAccessLogAdapter) LogAccess(ctx context.Context, clinicID, subjectID, staffID uuid.UUID, action, purpose string) error {
	var purposePtr *string
	if purpose != "" {
		purposePtr = &purpose
	}
	_, err := a.patientRepo.CreateSubjectAccessLog(ctx, patient.CreateSubjectAccessLogParams{
		ID:        domain.NewID(),
		SubjectID: subjectID,
		StaffID:   staffID,
		ClinicID:  clinicID,
		Action:    domain.SubjectAccessAction(action),
		Purpose:   purposePtr,
	})
	if err != nil {
		return fmt.Errorf("app.drugsAccessLogAdapter: %w", err)
	}
	return nil
}

// aigenClinicLookupAdapter satisfies forms.AIGenClinicLookup AND
// policy.AIGenClinicLookup by reading the clinic record via clinic.Service
// and projecting the fields aigen needs (vertical, country, plan tier).
type aigenClinicLookupAdapter struct {
	clinicSvc *clinic.Service
}

// GetForAIGen returns (vertical, country, tier) for the given clinic.
// Tier is derived from PlanCode; trial / unbilled clinics return "trial".
func (a *aigenClinicLookupAdapter) GetForAIGen(ctx context.Context, clinicID uuid.UUID) (string, string, string, error) {
	c, err := a.clinicSvc.GetByID(ctx, clinicID)
	if err != nil {
		return "", "", "", fmt.Errorf("app.aigenClinicLookupAdapter: %w", err)
	}
	tier := "trial"
	if c.PlanCode != nil {
		tier = string(*c.PlanCode)
	}
	return string(c.Vertical), c.Country, tier, nil
}

// verticalStringAdapter satisfies notes.VerticalProvider / forms.VerticalProvider,
// which expect a plain string rather than the typed domain.Vertical — the AI
// prompt helpers only need the string discriminator.
type verticalStringAdapter struct {
	clinic *clinic.Service
}

func (a *verticalStringAdapter) GetClinicVertical(ctx context.Context, clinicID uuid.UUID) (string, error) {
	c, err := a.clinic.GetByID(ctx, clinicID)
	if err != nil {
		return "", fmt.Errorf("app.verticalStringAdapter: %w", err)
	}
	return string(c.Vertical), nil
}

// policyFormLinkerAdapter implements policy.FormLinker.
// When a policy is retired, it soft-unlinks the policy from every form that
// references it, stamping the policy name and retire reason on each row so the
// form's compliance trail can surface synthetic "Policy X unlinked" entries.
type policyFormLinkerAdapter struct{ repo *forms.Repository }

func (a *policyFormLinkerAdapter) DetachPolicyFromForms(ctx context.Context, policyID uuid.UUID, policyName string, reason *string) error {
	if err := a.repo.UnlinkPolicyFromAllForms(ctx, forms.UnlinkPolicyFromAllFormsParams{
		PolicyID:           policyID,
		PolicyNameSnapshot: policyName,
		Reason:             reason,
	}); err != nil {
		return fmt.Errorf("app.policyFormLinkerAdapter: %w", err)
	}
	return nil
}

// ── Marketplace adapters ──────────────────────────────────────────────────────

// marketplaceSnapshotAdapter implements marketplace.FormSnapshotter by reading
// directly from the forms repository.
type marketplaceSnapshotAdapter struct {
	formsRepo *forms.Repository
}

func (a *marketplaceSnapshotAdapter) SnapshotForm(ctx context.Context, formID, clinicID uuid.UUID) (*marketplace.FormSnapshot, error) {
	form, err := a.formsRepo.GetFormByID(ctx, formID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplaceSnapshotAdapter: %w", err)
	}
	version, err := a.formsRepo.GetLatestPublishedVersion(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplaceSnapshotAdapter: latest version: %w", err)
	}
	fields, err := a.formsRepo.GetFieldsByVersionID(ctx, version.ID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplaceSnapshotAdapter: fields: %w", err)
	}

	out := &marketplace.FormSnapshot{
		FormVersionID: version.ID,
		Name:          form.Name,
		Description:   form.Description,
		OverallPrompt: form.OverallPrompt,
		Tags:          form.Tags,
		Fields:        make([]marketplace.FormSnapshotField, len(fields)),
	}
	for i, f := range fields {
		// Copy the *float64 into a fresh variable so the slice carries
		// independent pointers rather than aliasing the repository scan target.
		var minConf *float64
		if f.MinConfidence != nil {
			v := *f.MinConfidence
			minConf = &v
		}
		out.Fields[i] = marketplace.FormSnapshotField{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         f.Config,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  minConf,
		}
	}
	return out, nil
}

func (a *marketplaceSnapshotAdapter) LinkedPolicyIDs(ctx context.Context, formID, clinicID uuid.UUID) ([]uuid.UUID, error) {
	// Ownership check first so cross-tenant probes fail.
	if _, err := a.formsRepo.GetFormByID(ctx, formID, clinicID); err != nil {
		return nil, fmt.Errorf("app.marketplaceSnapshotAdapter: %w", err)
	}
	ids, err := a.formsRepo.ListLinkedPolicies(ctx, formID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplaceSnapshotAdapter: %w", err)
	}
	return ids, nil
}

// marketplacePolicySnapshotAdapter implements marketplace.PolicySnapshotter
// by reading directly from the policy repository.
type marketplacePolicySnapshotAdapter struct {
	policyRepo *policy.Repository
}

func (a *marketplacePolicySnapshotAdapter) SnapshotPolicy(ctx context.Context, policyID, clinicID uuid.UUID) (*marketplace.PolicySnapshot, error) {
	p, err := a.policyRepo.GetPolicyByID(ctx, policyID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter: %w", err)
	}
	version, err := a.policyRepo.GetLatestPublishedVersion(ctx, policyID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter: version: %w", err)
	}
	clauses, err := a.policyRepo.ListClauses(ctx, version.ID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter: clauses: %w", err)
	}
	out := &marketplace.PolicySnapshot{
		PolicyID:    p.ID,
		Name:        p.Name,
		Description: p.Description,
		Content:     version.Content,
		Clauses:     make([]marketplace.PolicySnapshotClause, len(clauses)),
	}
	for i, c := range clauses {
		out.Clauses[i] = marketplace.PolicySnapshotClause{
			BlockID: c.BlockID,
			Title:   c.Title,
			Body:    c.Body,
			Parity:  c.Parity,
		}
	}
	return out, nil
}

// SnapshotPolicies fetches metadata, latest published versions, and clauses
// for many policies in three queries instead of 3*N. Order of input IDs is
// preserved in the output. Returns ErrNotFound if any policy is missing,
// belongs to a different tenant, or has no published version.
func (a *marketplacePolicySnapshotAdapter) SnapshotPolicies(ctx context.Context, policyIDs []uuid.UUID, clinicID uuid.UUID) ([]*marketplace.PolicySnapshot, error) {
	if len(policyIDs) == 0 {
		return nil, nil
	}

	policies, err := a.policyRepo.GetPoliciesByIDs(ctx, policyIDs, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter.SnapshotPolicies: policies: %w", err)
	}
	policyByID := make(map[uuid.UUID]*policy.PolicyRecord, len(policies))
	for _, p := range policies {
		policyByID[p.ID] = p
	}

	versions, err := a.policyRepo.GetLatestPublishedVersions(ctx, policyIDs)
	if err != nil {
		return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter.SnapshotPolicies: versions: %w", err)
	}

	clauses, err := a.policyRepo.GetLatestClausesForPolicies(ctx, policyIDs)
	if err != nil {
		return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter.SnapshotPolicies: clauses: %w", err)
	}
	clausesByPolicy := make(map[uuid.UUID][]*policy.ClauseWithPolicyID, len(policyIDs))
	for _, c := range clauses {
		clausesByPolicy[c.PolicyID] = append(clausesByPolicy[c.PolicyID], c)
	}

	out := make([]*marketplace.PolicySnapshot, 0, len(policyIDs))
	for _, pid := range policyIDs {
		p, ok := policyByID[pid]
		if !ok {
			return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter.SnapshotPolicies: policy %s: %w", pid, domain.ErrNotFound)
		}
		v, ok := versions[pid]
		if !ok {
			return nil, fmt.Errorf("app.marketplacePolicySnapshotAdapter.SnapshotPolicies: policy %s has no published version: %w", pid, domain.ErrNotFound)
		}
		pcs := clausesByPolicy[pid]
		snap := &marketplace.PolicySnapshot{
			PolicyID:    p.ID,
			Name:        p.Name,
			Description: p.Description,
			Content:     v.Content,
			Clauses:     make([]marketplace.PolicySnapshotClause, len(pcs)),
		}
		for i, c := range pcs {
			snap.Clauses[i] = marketplace.PolicySnapshotClause{
				BlockID: c.BlockID,
				Title:   c.Title,
				Body:    c.Body,
				Parity:  c.Parity,
			}
		}
		out = append(out, snap)
	}
	return out, nil
}

// marketplaceClinicInfoAdapter implements marketplace.ClinicInfoProvider.
type marketplaceClinicInfoAdapter struct {
	clinicSvc *clinic.Service
}

func (a *marketplaceClinicInfoAdapter) GetClinicInfo(ctx context.Context, clinicID uuid.UUID) (*marketplace.ClinicInfo, error) {
	c, err := a.clinicSvc.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.marketplaceClinicInfoAdapter: %w", err)
	}
	return &marketplace.ClinicInfo{
		Status:   string(c.Status),
		Vertical: string(c.Vertical),
	}, nil
}

// marketplacePolicyImporterAdapter implements marketplace.PolicyImporter by
// creating a tenant policy + published version + clauses via the policy service.
// Preserves block_ids verbatim so form extraction alignment keeps working.
type marketplacePolicyImporterAdapter struct {
	policySvc *policy.Service
}

func (a *marketplacePolicyImporterAdapter) ImportPolicy(ctx context.Context, in marketplace.PolicyImportInput) (uuid.UUID, error) {
	clauses := make([]policy.ClauseInput, len(in.Clauses))
	for i, c := range in.Clauses {
		clauses[i] = policy.ClauseInput{
			BlockID: c.BlockID,
			Title:   c.Title,
			Body:    c.Body,
			Parity:  c.Parity,
		}
	}
	id, err := a.policySvc.ImportFromMarketplace(ctx, policy.ImportFromMarketplaceInput{
		ClinicID:                   in.ClinicID,
		StaffID:                    in.StaffID,
		SourceMarketplaceVersionID: in.SourceMarketplaceVersionID,
		Name:                       in.Name,
		Description:                in.Description,
		Content:                    in.Content,
		Clauses:                    clauses,
		ChangeSummary:              in.ChangeSummary,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("app.marketplacePolicyImporterAdapter: %w", err)
	}
	return id, nil
}

// marketplaceImporterAdapter implements marketplace.FormImporter by
// materialising a package into a new tenant form via the forms service.
// Follows the forms invariant: create form (with draft) → replace fields →
// publish draft → v1.0.
type marketplaceImporterAdapter struct {
	formsSvc *forms.Service
}

// LinkFormToPolicy satisfies marketplace.FormImporter.
func (a *marketplaceImporterAdapter) LinkFormToPolicy(ctx context.Context, formID, clinicID, policyID, staffID uuid.UUID) error {
	if err := a.formsSvc.LinkPolicy(ctx, formID, clinicID, policyID, staffID); err != nil {
		return fmt.Errorf("app.marketplaceImporterAdapter: link: %w", err)
	}
	return nil
}

func (a *marketplaceImporterAdapter) ImportForm(ctx context.Context, in marketplace.FormImportInput) (uuid.UUID, error) {
	created, err := a.formsSvc.CreateForm(ctx, forms.CreateFormInput{
		ClinicID:      in.ClinicID,
		StaffID:       in.StaffID,
		Name:          in.Name,
		Description:   in.Description,
		OverallPrompt: in.OverallPrompt,
		Tags:          in.Tags,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("app.marketplaceImporterAdapter: create: %w", err)
	}
	formID, err := uuid.Parse(created.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("app.marketplaceImporterAdapter: parse id: %w", err)
	}

	fieldInputs := make([]forms.FieldInput, len(in.Fields))
	for i, f := range in.Fields {
		fieldInputs[i] = forms.FieldInput{
			Position:       f.Position,
			Title:          f.Title,
			Type:           f.Type,
			Config:         f.Config,
			AIPrompt:       f.AIPrompt,
			Required:       f.Required,
			Skippable:      f.Skippable,
			AllowInference: f.AllowInference,
			MinConfidence:  f.MinConfidence,
		}
	}

	if _, err := a.formsSvc.UpdateDraft(ctx, forms.UpdateDraftInput{
		FormID:        formID,
		ClinicID:      in.ClinicID,
		StaffID:       in.StaffID,
		Name:          in.Name,
		Description:   in.Description,
		OverallPrompt: in.OverallPrompt,
		Tags:          in.Tags,
		Fields:        fieldInputs,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("app.marketplaceImporterAdapter: update draft: %w", err)
	}

	changeSummary := in.ChangeSummary
	if _, err := a.formsSvc.PublishForm(ctx, forms.PublishFormInput{
		FormID:        formID,
		ClinicID:      in.ClinicID,
		StaffID:       in.StaffID,
		ChangeType:    domain.ChangeTypeMajor,
		ChangeSummary: &changeSummary,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("app.marketplaceImporterAdapter: publish: %w", err)
	}
	return formID, nil
}

// marketplacePolicyNamerAdapter implements marketplace.PolicyNamer by resolving
// policy IDs to their display names via the policy repository.
type marketplacePolicyNamerAdapter struct {
	policyRepo *policy.Repository
}

func (a *marketplacePolicyNamerAdapter) GetPolicyNames(ctx context.Context, clinicID uuid.UUID, policyIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(policyIDs))
	for _, id := range policyIDs {
		p, err := a.policyRepo.GetPolicyByID(ctx, id, clinicID)
		if err != nil {
			// Missing policies are skipped, not failed — policies can be retired.
			continue
		}
		out[id] = p.Name
	}
	return out, nil
}

// clinicStyleAdapter implements notes.ClinicStyleProvider. Returns the
// clinic-profile fields the PDF renderer uses for header/footer slot
// substitution. Brand color now lives on the doc-theme, not the clinic, so
// it is not returned here.
type clinicStyleAdapter struct {
	clinic *clinic.Service
}

func (a *clinicStyleAdapter) GetClinicStyle(ctx context.Context, clinicID uuid.UUID) (*notes.ClinicForRender, error) {
	c, err := a.clinic.GetByID(ctx, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.clinicStyleAdapter: %w", err)
	}
	email := c.Email
	return &notes.ClinicForRender{
		Name:    c.Name,
		Address: c.Address,
		Phone:   c.Phone,
		Email:   &email,
	}, nil
}

// docThemeAdapter implements notes.DocThemeProvider by reading the active
// clinic_form_style_versions row and decoding its rich JSONB config into a
// typed notes.DocTheme.
type docThemeAdapter struct {
	repo *forms.Repository
}

func (a *docThemeAdapter) GetActiveDocTheme(ctx context.Context, clinicID uuid.UUID) (*notes.DocTheme, error) {
	style, err := a.repo.GetCurrentStyle(ctx, clinicID)
	if err != nil {
		// No active style is normal for a fresh clinic — fall back to defaults.
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("app.docThemeAdapter: %w", err)
	}
	if style == nil || len(style.Config) == 0 {
		return nil, nil
	}
	theme, err := notes.DecodeDocTheme(style.Config)
	if err != nil {
		return nil, fmt.Errorf("app.docThemeAdapter: %w", err)
	}
	return theme, nil
}

// systemHeaderAdapter implements notes.SystemHeaderProvider by reading the
// per-form-version system_header_config JSONB through the forms repository.
type systemHeaderAdapter struct {
	repo *forms.Repository
}

func (a *systemHeaderAdapter) GetSystemHeader(ctx context.Context, formVersionID uuid.UUID) (*notes.SystemHeaderConfigForPDF, error) {
	v, err := a.repo.GetVersionByID(ctx, formVersionID)
	if err != nil {
		return nil, fmt.Errorf("app.systemHeaderAdapter: %w", err)
	}
	if len(v.SystemHeaderConfig) == 0 {
		return nil, nil
	}
	var raw struct {
		Enabled bool     `json:"enabled"`
		Fields  []string `json:"fields"`
	}
	if err := json.Unmarshal(v.SystemHeaderConfig, &raw); err != nil {
		return nil, fmt.Errorf("app.systemHeaderAdapter: %w", err)
	}
	return &notes.SystemHeaderConfigForPDF{Enabled: raw.Enabled, Fields: raw.Fields}, nil
}

// subjectRenderAdapter implements notes.SubjectProvider by mapping the
// vertical-specific subject details into the flat PDFSubject struct the
// renderer consumes. Bypasses access logging — the PDF job runs in system
// context, and the original submit action already produced an access log.
type subjectRenderAdapter struct {
	patient *patient.Service
}

func (a *subjectRenderAdapter) GetSubjectForRender(ctx context.Context, subjectID, clinicID uuid.UUID) (*notes.PDFSubject, error) {
	s, err := a.patient.GetSubjectForRender(ctx, subjectID, clinicID)
	if err != nil {
		return nil, fmt.Errorf("app.subjectRenderAdapter: %w", err)
	}
	out := &notes.PDFSubject{
		DisplayName: &s.DisplayName,
	}
	if s.VetDetails != nil {
		v := s.VetDetails
		species := string(v.Species)
		out.Species = &species
		out.Breed = v.Breed
		out.Microchip = v.Microchip
		out.WeightKg = v.WeightKg
		out.Desexed = v.Desexed
		out.Color = v.Color
		out.DOB = v.DateOfBirth
		out.Allergies = v.Allergies
		if v.Sex != nil {
			sx := string(*v.Sex)
			out.Sex = &sx
		}
	}
	if s.DentalDetails != nil {
		d := s.DentalDetails
		out.DOB = d.DateOfBirth
		out.MedicalAlerts = d.MedicalAlerts
		out.Medications = d.Medications
		out.Allergies = d.Allergies
		if d.Sex != nil {
			sx := string(*d.Sex)
			out.Sex = &sx
		}
	}
	if s.GeneralDetails != nil {
		g := s.GeneralDetails
		out.DOB = g.DateOfBirth
		out.MedicalAlerts = g.MedicalAlerts
		out.Medications = g.Medications
		out.Allergies = g.Allergies
		if g.Sex != nil {
			sx := string(*g.Sex)
			out.Sex = &sx
		}
	}
	if s.AgedCareDetails != nil {
		ac := s.AgedCareDetails
		out.DOB = ac.DateOfBirth
		out.Room = ac.Room
		out.NHINumber = ac.NHINumber
		out.MedicareNumber = ac.MedicareNumber
		out.PreferredLanguage = ac.PreferredLanguage
		out.MedicalAlerts = ac.MedicalAlerts
		out.Medications = ac.Medications
		out.Allergies = ac.Allergies
		out.AdmissionDate = ac.AdmissionDate
		if ac.FundingLevel != nil {
			fl := string(*ac.FundingLevel)
			out.FundingLevel = &fl
		}
		if ac.Sex != nil {
			sx := string(*ac.Sex)
			out.Sex = &sx
		}
	}
	// External ID (clinic's own patient identifier) — pulled by separate
	// service method until the SubjectResponse exposes it directly.
	return out, nil
}

// staffNameAdapter implements notes.StaffNameProvider.
type staffNameAdapter struct {
	staff *staff.Service
}

func (a *staffNameAdapter) GetStaffName(ctx context.Context, staffID, clinicID uuid.UUID) (string, error) {
	s, err := a.staff.GetByID(ctx, staffID, clinicID)
	if err != nil {
		return "", fmt.Errorf("app.staffNameAdapter: %w", err)
	}
	return s.FullName, nil
}

// formsStaffNameAdapter implements forms.StaffNameResolver. Separate from
// staffNameAdapter because the notes module uses a different method name.
type formsStaffNameAdapter struct {
	staff *staff.Service
}

func (a *formsStaffNameAdapter) ResolveStaffName(ctx context.Context, staffID, clinicID uuid.UUID) (string, error) {
	s, err := a.staff.GetByID(ctx, staffID, clinicID)
	if err != nil {
		return "", fmt.Errorf("app.formsStaffNameAdapter: %w", err)
	}
	return s.FullName, nil
}

// formMetaAdapter implements notes.FormMetaProvider.
// Returns the form name and a human-readable version string (e.g. "1.0").
type formMetaAdapter struct {
	repo *forms.Repository
}

func (a *formMetaAdapter) GetFormMeta(ctx context.Context, formVersionID, clinicID uuid.UUID) (string, string, error) {
	version, err := a.repo.GetVersionByID(ctx, formVersionID)
	if err != nil {
		return "", "", fmt.Errorf("app.formMetaAdapter: get version: %w", err)
	}
	form, err := a.repo.GetFormByID(ctx, version.FormID, clinicID)
	if err != nil {
		return "", "", fmt.Errorf("app.formMetaAdapter: get form: %w", err)
	}
	versionStr := "draft"
	if version.VersionMajor != nil && version.VersionMinor != nil {
		versionStr = fmt.Sprintf("%d.%d", *version.VersionMajor, *version.VersionMinor)
	}
	return form.Name, versionStr, nil
}

// newTranscriberFromConfig builds the correct Transcriber based on TRANSCRIPTION_PROVIDER.
// Returns nil (no error) when the provider's API key is not configured.
func newTranscriberFromConfig(ctx context.Context, cfg *config.Config) (audio.Transcriber, error) {
	switch cfg.TranscriptionProvider {
	case "deepgram":
		if cfg.DeepgramAPIKey == "" {
			return nil, nil
		}
		return audio.NewDeepgramTranscriber(cfg.DeepgramAPIKey), nil
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			return nil, nil
		}
		t, err := audio.NewGeminiTranscriber(ctx, cfg.GeminiAPIKey)
		if err != nil {
			return nil, fmt.Errorf("newTranscriberFromConfig: %w", err)
		}
		return t, nil
	default:
		return nil, fmt.Errorf("newTranscriberFromConfig: unknown TRANSCRIPTION_PROVIDER %q (use deepgram or gemini)", cfg.TranscriptionProvider)
	}
}

func connectDB(ctx context.Context, cfg *config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	// Import is in platform/db — use it directly here to keep app.go simple.
	// We inline the connect+migrate sequence so the startup order is explicit.
	from := "app.Build"

	log.InfoContext(ctx, "connecting to database")

	cfg2 := cfg // alias for closure
	_ = cfg2

	// Connect via platform/db.
	pool, err := connectPool(ctx, cfg.DatabaseURL, int32(cfg.DBMaxConns), int32(cfg.DBMinConns))
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
