package approvals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler exposes the approvals queue + decide endpoints.
type Handler struct {
	svc *Service
}

// NewHandler builds a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ─── Response shapes ────────────────────────────────────────────────────────

// ApprovalResponse is the API-safe representation of an approval row.
//
//nolint:revive
type ApprovalResponse struct {
	ID             string  `json:"id"`
	ClinicID       string  `json:"clinic_id"`
	EntityKind     string  `json:"entity_kind"`
	EntityID       string  `json:"entity_id"`
	EntityOp       *string `json:"entity_op,omitempty"`
	Status         string  `json:"status"`
	SubmittedBy    string  `json:"submitted_by"`
	SubmittedAt    string  `json:"submitted_at"`
	SubmittedNote  *string `json:"submitted_note,omitempty"`
	DeadlineAt     string  `json:"deadline_at"`
	DecidedBy      *string `json:"decided_by,omitempty"`
	DecidedAt      *string `json:"decided_at,omitempty"`
	DecidedComment *string `json:"decided_comment,omitempty"`
	SubjectID      *string `json:"subject_id,omitempty"`
	NoteID         *string `json:"note_id,omitempty"`
}

// ApprovalListResponse pages over rows; small enough today to skip
// offset paging — clients ask for top N by deadline.
//
//nolint:revive
type ApprovalListResponse struct {
	Items []*ApprovalResponse `json:"items"`
}

type approvalHTTPResponse struct {
	Body *ApprovalResponse
}

type approvalListHTTPResponse struct {
	Body *ApprovalListResponse
}

// ─── List ───────────────────────────────────────────────────────────────────

type listInput struct {
	Kind      string `query:"kind" doc:"Optional filter — drug_op | consent | incident | pain_score. When empty returns every kind."`
	Submitter string `query:"submitter" enum:"others,mine" doc:"Whose submissions. Default 'others' = pending rows the caller can approve (excludes their own). 'mine' = pending rows the caller submitted, awaiting another staff to approve."`
	Limit     int    `query:"limit" doc:"Max rows. Default 50, max 200."`
}

func (h *Handler) listPending(ctx context.Context, input *listInput) (*approvalListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)

	var kind *domain.ApprovalEntityKind
	if input.Kind != "" {
		k := domain.ApprovalEntityKind(input.Kind)
		if !validEntityKind(k) {
			return nil, huma.Error400BadRequest("invalid kind")
		}
		kind = &k
	}

	onlyOwn := input.Submitter == "mine"
	out, err := h.svc.ListPending(ctx, clinicID, staffID, kind, input.Limit, onlyOwn)
	if err != nil {
		return nil, mapApprovalError(err)
	}

	resp := &ApprovalListResponse{
		Items: make([]*ApprovalResponse, len(out)),
	}
	for i, r := range out {
		resp.Items[i] = toResponse(r)
	}
	return &approvalListHTTPResponse{Body: resp}, nil
}

type countResponse struct {
	Body struct {
		Count int `json:"count"`
	}
}

type listSubjectPendingInput struct {
	SubjectID string `path:"subject_id" doc:"The subject UUID."`
	Limit     int    `query:"limit"     doc:"Max rows. Default 50, max 200."`
}

func (h *Handler) listSubjectPending(ctx context.Context, input *listSubjectPendingInput) (*approvalListHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	out, err := h.svc.ListPendingForSubject(ctx, clinicID, subjectID, input.Limit)
	if err != nil {
		return nil, mapApprovalError(err)
	}
	resp := &ApprovalListResponse{
		Items: make([]*ApprovalResponse, len(out)),
	}
	for i, r := range out {
		resp.Items[i] = toResponse(r)
	}
	return &approvalListHTTPResponse{Body: resp}, nil
}

func (h *Handler) countPending(ctx context.Context, _ *struct{}) (*countResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	n, err := h.svc.CountPendingForDecider(ctx, clinicID, staffID)
	if err != nil {
		return nil, mapApprovalError(err)
	}
	resp := &countResponse{}
	resp.Body.Count = n
	return resp, nil
}

// ─── Decide ─────────────────────────────────────────────────────────────────

type decideInput struct {
	ApprovalID string `path:"approval_id" doc:"The approval row's UUID."`
	Body       struct {
		Comment *string `json:"comment,omitempty" doc:"Required when challenging; optional when approving — surfaces in the patient timeline + on the regulator export."`
	}
}

func (h *Handler) approve(ctx context.Context, input *decideInput) (*approvalHTTPResponse, error) {
	return h.decide(ctx, input, domain.ApprovalStatusApproved)
}

func (h *Handler) challenge(ctx context.Context, input *decideInput) (*approvalHTTPResponse, error) {
	return h.decide(ctx, input, domain.ApprovalStatusChallenged)
}

func (h *Handler) decide(ctx context.Context, input *decideInput, newStatus domain.ApprovalStatus) (*approvalHTTPResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	staffID := mw.StaffIDFromContext(ctx)
	role := string(mw.RoleFromContext(ctx))

	approvalID, err := uuid.Parse(input.ApprovalID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid approval_id")
	}

	rec, err := h.svc.Decide(ctx, DecideInput{
		ApprovalID: approvalID,
		ClinicID:   clinicID,
		DeciderID:  staffID,
		StaffRole:  role,
		NewStatus:  newStatus,
		Comment:    input.Body.Comment,
	})
	if err != nil {
		return nil, mapApprovalError(err)
	}
	return &approvalHTTPResponse{Body: toResponse(rec)}, nil
}

// ─── Mapping ────────────────────────────────────────────────────────────────

func validEntityKind(k domain.ApprovalEntityKind) bool {
	switch k {
	case domain.ApprovalKindDrugOp,
		domain.ApprovalKindConsent,
		domain.ApprovalKindIncident,
		domain.ApprovalKindPainScore:
		return true
	}
	return false
}

func mapApprovalError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity(leafMessage(err))
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("approval not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict(leafMessage(err))
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden(leafMessage(err))
	default:
		slog.Error("approvals: unmapped error", "error", err.Error())
		return huma.Error500InternalServerError("internal server error")
	}
}

// leafMessage strips the wrapper prefixes added by fmt.Errorf so the
// HTTP body shows the user-actionable cause without the package path.
func leafMessage(err error) string {
	msg := err.Error()
	const sentinels = ": validation error"
	if idx := lastIndexAny(msg, sentinels); idx > 0 {
		msg = msg[:idx]
	}
	parts := splitOn(msg, ": ")
	for i := 0; i < len(parts)-1; i++ {
		if !looksLikeWrap(parts[i]) {
			return joinFrom(parts, i)
		}
	}
	return parts[len(parts)-1]
}

func lastIndexAny(s, sentinel string) int {
	for i := len(s) - len(sentinel); i >= 0; i-- {
		if s[i:i+len(sentinel)] == sentinel {
			return i
		}
	}
	return -1
}

func splitOn(s, sep string) []string {
	out := []string{}
	start := 0
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			out = append(out, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	out = append(out, s[start:])
	return out
}

func joinFrom(parts []string, from int) string {
	if from >= len(parts) {
		return ""
	}
	out := parts[from]
	for i := from + 1; i < len(parts); i++ {
		out += ": " + parts[i]
	}
	return out
}

func looksLikeWrap(s string) bool {
	// Tokens like "approvals.service.Decide" — at least one '.', no spaces.
	dotted := false
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			dotted = true
		}
		if s[i] == ' ' {
			return false
		}
	}
	return dotted
}

func toResponse(r *Record) *ApprovalResponse {
	resp := &ApprovalResponse{
		ID:            r.ID.String(),
		ClinicID:      r.ClinicID.String(),
		EntityKind:    string(r.EntityKind),
		EntityID:      r.EntityID.String(),
		EntityOp:      r.EntityOp,
		Status:        string(r.Status),
		SubmittedBy:   r.SubmittedBy.String(),
		SubmittedAt:   r.SubmittedAt.UTC().Format(rfc3339),
		SubmittedNote: r.SubmittedNote,
		DeadlineAt:    r.DeadlineAt.UTC().Format(rfc3339),
		DecidedComment: r.DecidedComment,
	}
	if r.DecidedBy != nil {
		s := r.DecidedBy.String()
		resp.DecidedBy = &s
	}
	if r.DecidedAt != nil {
		s := r.DecidedAt.UTC().Format(rfc3339)
		resp.DecidedAt = &s
	}
	if r.SubjectID != nil {
		s := r.SubjectID.String()
		resp.SubjectID = &s
	}
	if r.NoteID != nil {
		s := r.NoteID.String()
		resp.NoteID = &s
	}
	return resp
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

// ensureHTTP — placeholder so the linter doesn't complain about an
// unused import path in early scaffolding. Removed once routes.go
// references actual HTTP methods.
var _ = fmt.Errorf
var _ = http.MethodPost
