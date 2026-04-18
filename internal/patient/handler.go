package patient

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// Handler wires patient and contact HTTP endpoints to the Service.
type Handler struct {
	svc *Service
}

// NewHandler creates a new patient Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ── Shared input types ────────────────────────────────────────────────────────

type subjectIDInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
}

type contactIDInput struct {
	ContactID string `path:"contact_id" doc:"The contact's UUID."`
}

type paginationInput struct {
	Limit  int `query:"limit"  minimum:"1" maximum:"100" default:"20" doc:"Number of results per page."`
	Offset int `query:"offset" minimum:"0" default:"0"  doc:"Number of results to skip."`
}

// ── Contact request / response types ─────────────────────────────────────────

type createContactInput struct {
	Body struct {
		FullName string  `json:"full_name" minLength:"1" maxLength:"200" doc:"Full legal name of the contact."`
		Phone    *string `json:"phone,omitempty"   maxLength:"30"  doc:"Contact phone number."`
		Email    *string `json:"email,omitempty"   format:"email"  doc:"Contact email address."`
		Address  *string `json:"address,omitempty" maxLength:"500" doc:"Contact postal address."`
	}
}

type updateContactInput struct {
	ContactID string `path:"contact_id" doc:"The contact's UUID."`
	Body      struct {
		FullName *string `json:"full_name,omitempty" minLength:"1" maxLength:"200" doc:"Full name to update."`
		Phone    *string `json:"phone,omitempty"    maxLength:"30"               doc:"Phone number to update."`
		Email    *string `json:"email,omitempty"    format:"email"               doc:"Email address to update."`
		Address  *string `json:"address,omitempty"  maxLength:"500"              doc:"Postal address to update."`
	}
}

type listContactsInput struct {
	paginationInput
}

type contactResponse struct {
	Body *ContactResponse
}

type contactListResponse struct {
	Body *ContactListResponse
}

type contactWithSubjectsResponse struct {
	Body *ContactWithSubjectsResponse
}

// ── Subject request / response types ─────────────────────────────────────────

type vetDetailsInput struct {
	Species               domain.VetSpecies `json:"species" enum:"dog,cat,bird,rabbit,reptile,other" doc:"Animal species."`
	Breed                 *string           `json:"breed,omitempty"         maxLength:"100" doc:"Breed of the animal."`
	Sex                   *domain.VetSex    `json:"sex,omitempty"           enum:"male,female,unknown" doc:"Biological sex."`
	Desexed               *bool             `json:"desexed,omitempty"                       doc:"Whether the animal has been desexed."`
	DateOfBirth           *string           `json:"date_of_birth,omitempty" format:"date"   doc:"Date of birth in YYYY-MM-DD format."`
	Color                 *string           `json:"color,omitempty"         maxLength:"100" doc:"Coat color or markings."`
	Microchip             *string           `json:"microchip,omitempty"     maxLength:"50"  doc:"Microchip identifier — not PII, stored unencrypted."`
	WeightKg              *float64          `json:"weight_kg,omitempty"                    doc:"Weight in kilograms."`
	Allergies             *string           `json:"allergies,omitempty"              maxLength:"2000" doc:"Known allergies and reactions. Encrypted at rest."`
	ChronicConditions     *string           `json:"chronic_conditions,omitempty"     maxLength:"2000" doc:"Chronic medical conditions. Encrypted at rest."`
	AdmissionWarnings     *string           `json:"admission_warnings,omitempty"     maxLength:"500"  doc:"Safety warnings at intake (e.g. aggressive, bite history)."`
	InsuranceProviderName *string           `json:"insurance_provider_name,omitempty" maxLength:"200" doc:"Pet insurance provider display name."`
	InsurancePolicyNumber *string           `json:"insurance_policy_number,omitempty" maxLength:"100" doc:"Insurance policy number. Encrypted at rest."`
	ReferringVetName      *string           `json:"referring_vet_name,omitempty"     maxLength:"200"  doc:"Name of referring veterinarian, if any."`
}

type createSubjectInput struct {
	Body struct {
		DisplayName string           `json:"display_name" minLength:"1" maxLength:"200" doc:"Human-readable label shown in the UI. E.g. 'Buddy' or 'Bella (Labrador)'."`
		ContactID   *string          `json:"contact_id,omitempty"                       doc:"UUID of an existing contact to link as owner. Can be omitted and linked later."`
		VetDetails  *vetDetailsInput `json:"vet_details,omitempty"                      doc:"Veterinary-specific details. Required for veterinary vertical."`
	}
}

type updateSubjectInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
	Body      struct {
		DisplayName *string               `json:"display_name,omitempty" minLength:"1" maxLength:"200"                        doc:"Updated display name."`
		Status      *domain.SubjectStatus `json:"status,omitempty"       enum:"active,deceased,transferred,archived"          doc:"Updated lifecycle status."`
		VetDetails  *struct {
			Breed                 *string        `json:"breed,omitempty"         maxLength:"100"             doc:"Updated breed."`
			Sex                   *domain.VetSex `json:"sex,omitempty"           enum:"male,female,unknown"  doc:"Updated sex."`
			Desexed               *bool          `json:"desexed,omitempty"                                  doc:"Updated desexed status."`
			DateOfBirth           *string        `json:"date_of_birth,omitempty" format:"date"              doc:"Updated date of birth (YYYY-MM-DD)."`
			Color                 *string        `json:"color,omitempty"         maxLength:"100"             doc:"Updated color."`
			Microchip             *string        `json:"microchip,omitempty"     maxLength:"50"              doc:"Updated microchip ID."`
			WeightKg              *float64       `json:"weight_kg,omitempty"                                doc:"Updated weight in kg."`
			Allergies             *string        `json:"allergies,omitempty"              maxLength:"2000"  doc:"Updated allergies. Encrypted at rest."`
			ChronicConditions     *string        `json:"chronic_conditions,omitempty"     maxLength:"2000"  doc:"Updated chronic conditions. Encrypted at rest."`
			AdmissionWarnings     *string        `json:"admission_warnings,omitempty"     maxLength:"500"   doc:"Updated admission warnings."`
			InsuranceProviderName *string        `json:"insurance_provider_name,omitempty" maxLength:"200"  doc:"Updated insurance provider name."`
			InsurancePolicyNumber *string        `json:"insurance_policy_number,omitempty" maxLength:"100"  doc:"Updated policy number. Encrypted at rest."`
			ReferringVetName      *string        `json:"referring_vet_name,omitempty"     maxLength:"200"   doc:"Updated referring vet name."`
		} `json:"vet_details,omitempty" doc:"Veterinary details to update. Only provided fields are changed."`
	}
}

type linkContactInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
	Body      struct {
		ContactID string `json:"contact_id" doc:"UUID of the contact to link as owner."`
	}
}

type listSubjectsInput struct {
	paginationInput
	Status    domain.SubjectStatus `query:"status"     enum:"active,deceased,transferred,archived" doc:"Filter by lifecycle status."`
	Species   domain.VetSpecies    `query:"species"    enum:"dog,cat,bird,rabbit,reptile,other"    doc:"Filter by species (vet vertical only)."`
	ContactID string               `query:"contact_id"                                             doc:"Filter subjects by contact UUID."`
}

type subjectResponse struct {
	Body *SubjectResponse
}

type subjectListResponse struct {
	Body *SubjectListResponse
}

type unmaskPIIInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
	Body      struct {
		Field   string  `json:"field" enum:"allergies,chronic_conditions,insurance_policy_number,medical_alerts,medications" doc:"Encrypted field to reveal."`
		Purpose *string `json:"purpose,omitempty" maxLength:"500" doc:"Free-text reason for revealing this field. Logged to the audit trail."`
	}
}

type unmaskPIIResponse struct {
	Body *UnmaskPIIResponse
}

// ── Contact handlers ──────────────────────────────────────────────────────────

// createContact handles POST /api/v1/contacts.
func (h *Handler) createContact(ctx context.Context, input *createContactInput) (*contactResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	dto, err := h.svc.CreateContact(ctx, CreateContactInput{
		ClinicID: clinicID,
		FullName: input.Body.FullName,
		Phone:    input.Body.Phone,
		Email:    input.Body.Email,
		Address:  input.Body.Address,
	})
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &contactResponse{Body: dto}, nil
}

// getContact handles GET /api/v1/contacts/{contact_id}.
// Returns the contact with all of its subjects inline.
func (h *Handler) getContact(ctx context.Context, input *contactIDInput) (*contactWithSubjectsResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid contact_id")
	}

	dto, err := h.svc.GetContactWithSubjects(ctx, contactID, clinicID)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &contactWithSubjectsResponse{Body: dto}, nil
}

// listContacts handles GET /api/v1/contacts.
func (h *Handler) listContacts(ctx context.Context, input *listContactsInput) (*contactListResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	page, err := h.svc.ListContacts(ctx, clinicID, input.Limit, input.Offset)
	if err != nil {
		return nil, huma.Error500InternalServerError("internal server error")
	}
	return &contactListResponse{Body: page}, nil
}

// updateContact handles PATCH /api/v1/contacts/{contact_id}.
func (h *Handler) updateContact(ctx context.Context, input *updateContactInput) (*contactResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid contact_id")
	}

	dto, err := h.svc.UpdateContact(ctx, contactID, clinicID, UpdateContactInput{
		FullName: input.Body.FullName,
		Phone:    input.Body.Phone,
		Email:    input.Body.Email,
		Address:  input.Body.Address,
	})
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &contactResponse{Body: dto}, nil
}

// ── Subject handlers ──────────────────────────────────────────────────────────

// createSubject handles POST /api/v1/patients.
func (h *Handler) createSubject(ctx context.Context, input *createSubjectInput) (*subjectResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)
	vertical := domain.VerticalVeterinary // TODO: pull from clinic context in Phase 2 multi-vertical

	svcInput := CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    vertical,
		DisplayName: input.Body.DisplayName,
	}

	if input.Body.ContactID != nil {
		cid, err := uuid.Parse(*input.Body.ContactID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid contact_id")
		}
		svcInput.ContactID = &cid
	}

	if input.Body.VetDetails != nil {
		vd, err := parseVetDetailsInput(input.Body.VetDetails)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		svcInput.VetDetails = vd
	}

	dto, err := h.svc.CreateSubject(ctx, svcInput)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectResponse{Body: dto}, nil
}

// getSubject handles GET /api/v1/patients/{subject_id}.
func (h *Handler) getSubject(ctx context.Context, input *subjectIDInput) (*subjectResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)
	perms := mw.PermissionsFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	dto, err := h.svc.GetSubjectByID(ctx, subjectID, clinicID, callerID, perms.ViewAllPatients)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectResponse{Body: dto}, nil
}

// listSubjects handles GET /api/v1/patients.
func (h *Handler) listSubjects(ctx context.Context, input *listSubjectsInput) (*subjectListResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)
	perms := mw.PermissionsFromContext(ctx)

	svcInput := ListSubjectsInput{
		Limit:      input.Limit,
		Offset:     input.Offset,
		Status:     &input.Status,
		Species:    &input.Species,
		ViewAll:    perms.ViewAllPatients,
		OwnerScope: !perms.ViewAllPatients && perms.ViewOwnPatients,
		CallerID:   callerID,
	}

	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid contact_id")
		}
		svcInput.ContactID = &cid
	}

	page, err := h.svc.ListSubjects(ctx, clinicID, svcInput)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectListResponse{Body: page}, nil
}

// updateSubject handles PATCH /api/v1/patients/{subject_id}.
func (h *Handler) updateSubject(ctx context.Context, input *updateSubjectInput) (*subjectResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	svcInput := UpdateSubjectInput{
		DisplayName: input.Body.DisplayName,
		Status:      input.Body.Status,
	}

	if input.Body.VetDetails != nil {
		v := input.Body.VetDetails
		vd := &UpdateVetDetailsInput{
			Breed:                 v.Breed,
			Sex:                   v.Sex,
			Desexed:               v.Desexed,
			Color:                 v.Color,
			Microchip:             v.Microchip,
			WeightKg:              v.WeightKg,
			Allergies:             v.Allergies,
			ChronicConditions:     v.ChronicConditions,
			AdmissionWarnings:     v.AdmissionWarnings,
			InsuranceProviderName: v.InsuranceProviderName,
			InsurancePolicyNumber: v.InsurancePolicyNumber,
			ReferringVetName:      v.ReferringVetName,
		}
		if v.DateOfBirth != nil {
			parsed, err := time.Parse("2006-01-02", *v.DateOfBirth)
			if err != nil {
				return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
			}
			vd.DateOfBirth = &parsed
		}
		svcInput.VetDetails = vd
	}

	dto, err := h.svc.UpdateSubject(ctx, subjectID, clinicID, callerID, svcInput)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectResponse{Body: dto}, nil
}

// archiveSubject handles DELETE /api/v1/patients/{subject_id}.
func (h *Handler) archiveSubject(ctx context.Context, input *subjectIDInput) (*subjectResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	dto, err := h.svc.ArchiveSubject(ctx, subjectID, clinicID, callerID)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectResponse{Body: dto}, nil
}

// linkContact handles POST /api/v1/patients/{subject_id}/contact.
func (h *Handler) linkContact(ctx context.Context, input *linkContactInput) (*subjectResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	contactID, err := uuid.Parse(input.Body.ContactID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid contact_id")
	}

	dto, err := h.svc.LinkContact(ctx, subjectID, clinicID, contactID, callerID)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectResponse{Body: dto}, nil
}

// unmaskPII handles POST /api/v1/patients/{subject_id}/reveal.
// Returns the plaintext of a single encrypted field and writes a
// subject_access_log entry with action='unmask_pii'.
func (h *Handler) unmaskPII(ctx context.Context, input *unmaskPIIInput) (*unmaskPIIResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	dto, err := h.svc.UnmaskPII(ctx, UnmaskPIIInput{
		SubjectID: subjectID,
		ClinicID:  clinicID,
		CallerID:  callerID,
		Field:     input.Body.Field,
		Purpose:   input.Body.Purpose,
	})
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &unmaskPIIResponse{Body: dto}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func parseVetDetailsInput(v *vetDetailsInput) (*VetDetailsInput, error) {
	vd := &VetDetailsInput{
		Species:               v.Species,
		Breed:                 v.Breed,
		Sex:                   v.Sex,
		Desexed:               v.Desexed,
		Color:                 v.Color,
		Microchip:             v.Microchip,
		WeightKg:              v.WeightKg,
		Allergies:             v.Allergies,
		ChronicConditions:     v.ChronicConditions,
		AdmissionWarnings:     v.AdmissionWarnings,
		InsuranceProviderName: v.InsuranceProviderName,
		InsurancePolicyNumber: v.InsurancePolicyNumber,
		ReferringVetName:      v.ReferringVetName,
	}
	if v.DateOfBirth != nil {
		parsed, err := time.Parse("2006-01-02", *v.DateOfBirth)
		if err != nil {
			return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
		}
		vd.DateOfBirth = &parsed
	}
	return vd, nil
}

func mapPatientError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound("resource not found")
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict("resource already exists")
	case errors.Is(err, domain.ErrForbidden):
		return huma.Error403Forbidden("insufficient permissions")
	case errors.Is(err, domain.ErrValidation):
		return huma.Error422UnprocessableEntity("validation error", nil)
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
