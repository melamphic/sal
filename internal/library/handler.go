package library

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires library HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new library Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Input types ───────────────────────────────────────────────────────────────

type templateIDInput struct {
	TemplateID string `path:"template_id" doc:"The Salvia template ID (e.g. 'consultation_note')."`
}

type listLibraryInput struct {
	Kind string `query:"kind" enum:"form,policy" doc:"Filter by kind (form or policy). Omit for all."`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

type libraryListHTTPResponse struct {
	Body *LibraryListResponse
}

type libraryDetailHTTPResponse struct {
	Body *LibraryTemplateDetail
}

type libraryImportHTTPResponse struct {
	Body *LibraryImportResponse
}

// listLibraryForms handles GET /api/v1/library/forms.
func (h *Handler) listLibraryForms(ctx context.Context, _ *struct{}) (*libraryListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListForms(ctx, clinicID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &libraryListHTTPResponse{Body: resp}, nil
}

// listLibraryPolicies handles GET /api/v1/library/policies.
func (h *Handler) listLibraryPolicies(ctx context.Context, _ *struct{}) (*libraryListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.ListPolicies(ctx, clinicID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &libraryListHTTPResponse{Body: resp}, nil
}

// getLibraryForm handles GET /api/v1/library/forms/:template_id.
func (h *Handler) getLibraryForm(ctx context.Context, input *templateIDInput) (*libraryDetailHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.GetForm(ctx, input.TemplateID, clinicID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &libraryDetailHTTPResponse{Body: resp}, nil
}

// getLibraryPolicy handles GET /api/v1/library/policies/:template_id.
func (h *Handler) getLibraryPolicy(ctx context.Context, input *templateIDInput) (*libraryDetailHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	resp, err := h.svc.GetPolicy(ctx, input.TemplateID, clinicID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &libraryDetailHTTPResponse{Body: resp}, nil
}

// importLibraryForm handles POST /api/v1/library/forms/:template_id/import.
func (h *Handler) importLibraryForm(ctx context.Context, input *templateIDInput) (*libraryImportHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	resp, err := h.svc.ImportForm(ctx, input.TemplateID, clinicID, staffID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &libraryImportHTTPResponse{Body: resp}, nil
}

// importLibraryPolicy handles POST /api/v1/library/policies/:template_id/import.
func (h *Handler) importLibraryPolicy(ctx context.Context, input *templateIDInput) (*libraryImportHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	resp, err := h.svc.ImportPolicy(ctx, input.TemplateID, clinicID, staffID)
	if err != nil {
		return nil, mapErr(err)
	}
	return &libraryImportHTTPResponse{Body: resp}, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("template not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("forbidden")
	}
	return err
}

// Suppress uuid import lint — it's used via mw helpers that return uuid.UUID.
var _ = uuid.Nil
