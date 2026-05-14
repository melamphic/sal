package patient

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	mw "github.com/melamphic/sal/internal/platform/middleware"
)

// clinicLookup is the minimal surface the patient handler needs from the
// clinic service: resolving the clinic's configured vertical so the create
// subject endpoint can dispatch to the right extension table without the
// frontend having to tell it what vertical it's running in.
type clinicLookup interface {
	GetVertical(ctx context.Context, clinicID uuid.UUID) (domain.Vertical, error)
}

// Handler wires patient and contact HTTP endpoints to the Service.
type Handler struct {
	svc    *Service
	clinic clinicLookup
}

// NewHandler creates a new patient Handler.
func NewHandler(svc *Service, clinic clinicLookup) *Handler {
	return &Handler{svc: svc, clinic: clinic}
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

type dentalDetailsInput struct {
	DateOfBirth           *string           `json:"date_of_birth,omitempty" format:"date"                      doc:"Date of birth in YYYY-MM-DD format."`
	Sex                   *domain.DentalSex `json:"sex,omitempty"           enum:"male,female,other,unknown"   doc:"Biological sex."`
	MedicalAlerts         *string           `json:"medical_alerts,omitempty"          maxLength:"2000"         doc:"Medical alerts (latex allergy, MRSA, etc.). Encrypted at rest."`
	Medications           *string           `json:"medications,omitempty"             maxLength:"2000"         doc:"Current medications. Encrypted at rest."`
	Allergies             *string           `json:"allergies,omitempty"               maxLength:"2000"         doc:"Known allergies. Encrypted at rest."`
	ChronicConditions     *string           `json:"chronic_conditions,omitempty"      maxLength:"2000"         doc:"Chronic conditions. Encrypted at rest."`
	AdmissionWarnings     *string           `json:"admission_warnings,omitempty"      maxLength:"500"          doc:"Safety warnings at intake."`
	InsuranceProviderName *string           `json:"insurance_provider_name,omitempty" maxLength:"200"          doc:"Insurance provider display name."`
	InsurancePolicyNumber *string           `json:"insurance_policy_number,omitempty" maxLength:"100"          doc:"Insurance policy number. Encrypted at rest."`
	ReferringDentistName  *string           `json:"referring_dentist_name,omitempty"  maxLength:"200"          doc:"Referring dentist name."`
	PrimaryDentistName    *string           `json:"primary_dentist_name,omitempty"    maxLength:"200"          doc:"Primary dentist name."`
}

type generalDetailsInput struct {
	DateOfBirth           *string            `json:"date_of_birth,omitempty" format:"date"                    doc:"Date of birth in YYYY-MM-DD format."`
	Sex                   *domain.GeneralSex `json:"sex,omitempty"           enum:"male,female,other,unknown" doc:"Biological sex."`
	MedicalAlerts         *string            `json:"medical_alerts,omitempty"          maxLength:"2000"       doc:"Medical alerts. Encrypted at rest."`
	Medications           *string            `json:"medications,omitempty"             maxLength:"2000"       doc:"Current medications. Encrypted at rest."`
	Allergies             *string            `json:"allergies,omitempty"               maxLength:"2000"       doc:"Known allergies. Encrypted at rest."`
	ChronicConditions     *string            `json:"chronic_conditions,omitempty"      maxLength:"2000"       doc:"Chronic conditions. Encrypted at rest."`
	AdmissionWarnings     *string            `json:"admission_warnings,omitempty"      maxLength:"500"        doc:"Safety warnings at intake."`
	InsuranceProviderName *string            `json:"insurance_provider_name,omitempty" maxLength:"200"        doc:"Insurance provider display name."`
	InsurancePolicyNumber *string            `json:"insurance_policy_number,omitempty" maxLength:"100"        doc:"Insurance policy number. Encrypted at rest."`
	ReferringProviderName *string            `json:"referring_provider_name,omitempty" maxLength:"200"        doc:"Referring provider name."`
	PrimaryProviderName   *string            `json:"primary_provider_name,omitempty"   maxLength:"200"        doc:"Primary provider name."`
}

type agedCareDetailsInput struct {
	DateOfBirth          *string                          `json:"date_of_birth,omitempty" format:"date"                                                          doc:"Date of birth in YYYY-MM-DD format."`
	Sex                  *domain.AgedCareSex              `json:"sex,omitempty"           enum:"male,female,other,unknown"                                       doc:"Biological sex."`
	Room                 *string                          `json:"room,omitempty"             maxLength:"50"                                                      doc:"Room or bed identifier within the facility."`
	NHINumber            *string                          `json:"nhi_number,omitempty"       maxLength:"20"                                                      doc:"NZ National Health Index number. Encrypted at rest."`
	MedicareNumber       *string                          `json:"medicare_number,omitempty"  maxLength:"20"                                                      doc:"AU Medicare number. Encrypted at rest."`
	Ethnicity            *string                          `json:"ethnicity,omitempty"        maxLength:"100"                                                     doc:"Self-reported ethnicity."`
	PreferredLanguage    *string                          `json:"preferred_language,omitempty" maxLength:"50"                                                    doc:"Preferred spoken language."`
	MedicalAlerts        *string                          `json:"medical_alerts,omitempty"     maxLength:"2000"                                                  doc:"Medical alerts (DNR, falls risk, etc.). Encrypted at rest."`
	Medications          *string                          `json:"medications,omitempty"        maxLength:"2000"                                                  doc:"Current medications. Encrypted at rest."`
	Allergies            *string                          `json:"allergies,omitempty"          maxLength:"2000"                                                  doc:"Known allergies. Encrypted at rest."`
	ChronicConditions    *string                          `json:"chronic_conditions,omitempty" maxLength:"2000"                                                  doc:"Chronic conditions. Encrypted at rest."`
	CognitiveStatus      *domain.AgedCareCognitiveStatus  `json:"cognitive_status,omitempty"   enum:"independent,mild_impairment,moderate_impairment,severe_impairment,unknown" doc:"Cognitive impairment level."`
	MobilityStatus       *domain.AgedCareMobilityStatus   `json:"mobility_status,omitempty"    enum:"independent,supervised,assisted,immobile,unknown"                          doc:"Mobility level."`
	ContinenceStatus     *domain.AgedCareContinenceStatus `json:"continence_status,omitempty"  enum:"continent,urinary_incontinence,faecal_incontinence,double_incontinence,catheterised,unknown" doc:"Continence status."`
	DietNotes            *string                          `json:"diet_notes,omitempty"         maxLength:"2000"                                                  doc:"Dietary restrictions and texture modifications. Encrypted at rest."`
	AdvanceDirectiveFlag bool                             `json:"advance_directive_flag"                                                                         doc:"Whether an advance directive is on file."`
	FundingLevel         *domain.AgedCareFundingLevel     `json:"funding_level,omitempty"      enum:"home_care_1,home_care_2,home_care_3,home_care_4,residential_low,residential_high,unfunded,unknown" doc:"InterRAI (NZ) or Home Care Package (AU) tier."`
	AdmissionDate        *string                          `json:"admission_date,omitempty"     format:"date"                                                     doc:"Admission date in YYYY-MM-DD format."`
	PrimaryGPName        *string                          `json:"primary_gp_name,omitempty"    maxLength:"200"                                                   doc:"Primary GP name."`
}

type createSubjectInput struct {
	Body struct {
		DisplayName     string                `json:"display_name" minLength:"1" maxLength:"200" doc:"Human-readable label shown in the UI. E.g. 'Buddy' or 'Alice Smith'."`
		PhotoURL        *string               `json:"photo_url,omitempty" maxLength:"500"        doc:"DEPRECATED — pass photo_key from POST /patients/upload-photo instead. Kept for legacy external URL fields."`
		PhotoKey        *string               `json:"photo_key,omitempty" maxLength:"500"        doc:"Durable object-storage key returned by POST /patients/upload-photo. The server signs a fresh download URL on every read so it never expires."`
		ContactID       *string               `json:"contact_id,omitempty"                       doc:"UUID of an existing contact to link. Can be omitted and linked later."`
		VetDetails      *vetDetailsInput      `json:"vet_details,omitempty"                      doc:"Veterinary-specific details. Required for veterinary vertical."`
		DentalDetails   *dentalDetailsInput   `json:"dental_details,omitempty"                   doc:"Dental-specific details. Required for dental vertical."`
		GeneralDetails  *generalDetailsInput  `json:"general_details,omitempty"                  doc:"General-clinic-specific details. Required for general_clinic vertical."`
		AgedCareDetails *agedCareDetailsInput `json:"aged_care_details,omitempty"                doc:"Aged-care-specific details. Required for aged_care vertical."`
	}
}

type updateVetDetailsBody struct {
	Breed                 *string        `json:"breed,omitempty"         maxLength:"100"             doc:"Updated breed."`
	Sex                   *domain.VetSex `json:"sex,omitempty"           enum:"male,female,unknown"  doc:"Updated sex."`
	Desexed               *bool          `json:"desexed,omitempty"                                   doc:"Updated desexed status."`
	DateOfBirth           *string        `json:"date_of_birth,omitempty" format:"date"               doc:"Updated date of birth (YYYY-MM-DD)."`
	Color                 *string        `json:"color,omitempty"         maxLength:"100"             doc:"Updated color."`
	Microchip             *string        `json:"microchip,omitempty"     maxLength:"50"              doc:"Updated microchip ID."`
	WeightKg              *float64       `json:"weight_kg,omitempty"                                 doc:"Updated weight in kg."`
	Allergies             *string        `json:"allergies,omitempty"              maxLength:"2000"   doc:"Updated allergies. Encrypted at rest."`
	ChronicConditions     *string        `json:"chronic_conditions,omitempty"     maxLength:"2000"   doc:"Updated chronic conditions. Encrypted at rest."`
	AdmissionWarnings     *string        `json:"admission_warnings,omitempty"     maxLength:"500"    doc:"Updated admission warnings."`
	InsuranceProviderName *string        `json:"insurance_provider_name,omitempty" maxLength:"200"   doc:"Updated insurance provider name."`
	InsurancePolicyNumber *string        `json:"insurance_policy_number,omitempty" maxLength:"100"   doc:"Updated policy number. Encrypted at rest."`
	ReferringVetName      *string        `json:"referring_vet_name,omitempty"     maxLength:"200"    doc:"Updated referring vet name."`
}

type updateDentalDetailsBody struct {
	DateOfBirth           *string           `json:"date_of_birth,omitempty" format:"date"                    doc:"Updated date of birth (YYYY-MM-DD)."`
	Sex                   *domain.DentalSex `json:"sex,omitempty"           enum:"male,female,other,unknown" doc:"Updated sex."`
	MedicalAlerts         *string           `json:"medical_alerts,omitempty"          maxLength:"2000"       doc:"Updated medical alerts. Encrypted at rest."`
	Medications           *string           `json:"medications,omitempty"             maxLength:"2000"       doc:"Updated medications. Encrypted at rest."`
	Allergies             *string           `json:"allergies,omitempty"               maxLength:"2000"       doc:"Updated allergies. Encrypted at rest."`
	ChronicConditions     *string           `json:"chronic_conditions,omitempty"      maxLength:"2000"       doc:"Updated chronic conditions. Encrypted at rest."`
	AdmissionWarnings     *string           `json:"admission_warnings,omitempty"      maxLength:"500"        doc:"Updated admission warnings."`
	InsuranceProviderName *string           `json:"insurance_provider_name,omitempty" maxLength:"200"        doc:"Updated insurance provider name."`
	InsurancePolicyNumber *string           `json:"insurance_policy_number,omitempty" maxLength:"100"        doc:"Updated policy number. Encrypted at rest."`
	ReferringDentistName  *string           `json:"referring_dentist_name,omitempty"  maxLength:"200"        doc:"Updated referring dentist name."`
	PrimaryDentistName    *string           `json:"primary_dentist_name,omitempty"    maxLength:"200"        doc:"Updated primary dentist name."`
}

type updateGeneralDetailsBody struct {
	DateOfBirth           *string            `json:"date_of_birth,omitempty" format:"date"                    doc:"Updated date of birth (YYYY-MM-DD)."`
	Sex                   *domain.GeneralSex `json:"sex,omitempty"           enum:"male,female,other,unknown" doc:"Updated sex."`
	MedicalAlerts         *string            `json:"medical_alerts,omitempty"          maxLength:"2000"       doc:"Updated medical alerts. Encrypted at rest."`
	Medications           *string            `json:"medications,omitempty"             maxLength:"2000"       doc:"Updated medications. Encrypted at rest."`
	Allergies             *string            `json:"allergies,omitempty"               maxLength:"2000"       doc:"Updated allergies. Encrypted at rest."`
	ChronicConditions     *string            `json:"chronic_conditions,omitempty"      maxLength:"2000"       doc:"Updated chronic conditions. Encrypted at rest."`
	AdmissionWarnings     *string            `json:"admission_warnings,omitempty"      maxLength:"500"        doc:"Updated admission warnings."`
	InsuranceProviderName *string            `json:"insurance_provider_name,omitempty" maxLength:"200"        doc:"Updated insurance provider name."`
	InsurancePolicyNumber *string            `json:"insurance_policy_number,omitempty" maxLength:"100"        doc:"Updated policy number. Encrypted at rest."`
	ReferringProviderName *string            `json:"referring_provider_name,omitempty" maxLength:"200"        doc:"Updated referring provider name."`
	PrimaryProviderName   *string            `json:"primary_provider_name,omitempty"   maxLength:"200"        doc:"Updated primary provider name."`
}

type updateAgedCareDetailsBody struct {
	DateOfBirth          *string                          `json:"date_of_birth,omitempty" format:"date"                                                             doc:"Updated date of birth (YYYY-MM-DD)."`
	Sex                  *domain.AgedCareSex              `json:"sex,omitempty"           enum:"male,female,other,unknown"                                          doc:"Updated sex."`
	Room                 *string                          `json:"room,omitempty"             maxLength:"50"                                                         doc:"Updated room/bed."`
	NHINumber            *string                          `json:"nhi_number,omitempty"       maxLength:"20"                                                         doc:"Updated NHI number. Encrypted at rest."`
	MedicareNumber       *string                          `json:"medicare_number,omitempty"  maxLength:"20"                                                         doc:"Updated Medicare number. Encrypted at rest."`
	Ethnicity            *string                          `json:"ethnicity,omitempty"        maxLength:"100"                                                        doc:"Updated ethnicity."`
	PreferredLanguage    *string                          `json:"preferred_language,omitempty" maxLength:"50"                                                       doc:"Updated preferred language."`
	MedicalAlerts        *string                          `json:"medical_alerts,omitempty"     maxLength:"2000"                                                     doc:"Updated medical alerts. Encrypted at rest."`
	Medications          *string                          `json:"medications,omitempty"        maxLength:"2000"                                                     doc:"Updated medications. Encrypted at rest."`
	Allergies            *string                          `json:"allergies,omitempty"          maxLength:"2000"                                                     doc:"Updated allergies. Encrypted at rest."`
	ChronicConditions    *string                          `json:"chronic_conditions,omitempty" maxLength:"2000"                                                     doc:"Updated chronic conditions. Encrypted at rest."`
	CognitiveStatus      *domain.AgedCareCognitiveStatus  `json:"cognitive_status,omitempty"   enum:"independent,mild_impairment,moderate_impairment,severe_impairment,unknown" doc:"Updated cognitive status."`
	MobilityStatus       *domain.AgedCareMobilityStatus   `json:"mobility_status,omitempty"    enum:"independent,supervised,assisted,immobile,unknown"                          doc:"Updated mobility status."`
	ContinenceStatus     *domain.AgedCareContinenceStatus `json:"continence_status,omitempty"  enum:"continent,urinary_incontinence,faecal_incontinence,double_incontinence,catheterised,unknown" doc:"Updated continence status."`
	DietNotes            *string                          `json:"diet_notes,omitempty"         maxLength:"2000"                                                     doc:"Updated diet notes. Encrypted at rest."`
	AdvanceDirectiveFlag *bool                            `json:"advance_directive_flag,omitempty"                                                                  doc:"Updated advance directive flag."`
	FundingLevel         *domain.AgedCareFundingLevel     `json:"funding_level,omitempty"      enum:"home_care_1,home_care_2,home_care_3,home_care_4,residential_low,residential_high,unfunded,unknown" doc:"Updated funding level."`
	AdmissionDate        *string                          `json:"admission_date,omitempty"     format:"date"                                                        doc:"Updated admission date (YYYY-MM-DD)."`
	PrimaryGPName        *string                          `json:"primary_gp_name,omitempty"    maxLength:"200"                                                      doc:"Updated primary GP name."`
}

type updateSubjectInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
	Body      struct {
		DisplayName     *string                    `json:"display_name,omitempty" minLength:"1" maxLength:"200"               doc:"Updated display name."`
		Status          *domain.SubjectStatus      `json:"status,omitempty"       enum:"active,deceased,transferred,archived" doc:"Updated lifecycle status."`
		PhotoURL        *string                    `json:"photo_url,omitempty"    maxLength:"500"                             doc:"DEPRECATED — use photo_key. Kept for legacy external URL fields."`
		PhotoKey        *string                    `json:"photo_key,omitempty"    maxLength:"500"                             doc:"Updated avatar storage key. Send empty string to clear."`
		VetDetails      *updateVetDetailsBody      `json:"vet_details,omitempty"                                              doc:"Veterinary details to update. Only provided fields are changed."`
		DentalDetails   *updateDentalDetailsBody   `json:"dental_details,omitempty"                                           doc:"Dental details to update. Only provided fields are changed."`
		GeneralDetails  *updateGeneralDetailsBody  `json:"general_details,omitempty"                                          doc:"General-clinic details to update. Only provided fields are changed."`
		AgedCareDetails *updateAgedCareDetailsBody `json:"aged_care_details,omitempty"                                        doc:"Aged-care details to update. Only provided fields are changed."`
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
	Q         string               `query:"q"          maxLength:"100"                             doc:"Case-insensitive substring match on display_name."`
}

type subjectResponse struct {
	Body *SubjectResponse
}

type subjectListResponse struct {
	Body *SubjectListResponse
}

type addSubjectContactInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
	Body      struct {
		ContactID string                    `json:"contact_id" doc:"UUID of an existing contact to link."`
		Role      domain.SubjectContactRole `json:"role" enum:"primary_owner,co_owner,emergency_contact,guardian,next_of_kin,power_of_attorney,referring_provider,other" doc:"Relationship of the contact to the subject."`
		Note      *string                   `json:"note,omitempty" maxLength:"200" doc:"Optional free-text clarifier (e.g. 'daughter', 'work phone only')."`
	}
}

type removeSubjectContactInput struct {
	SubjectID string                    `path:"subject_id" doc:"The subject's UUID."`
	ContactID string                    `path:"contact_id" doc:"The contact's UUID."`
	Role      domain.SubjectContactRole `path:"role" doc:"The role binding to remove (e.g. 'emergency_contact')."`
}

type subjectContactResponse struct {
	Body *SubjectContactResponse
}

// SubjectContactListBody wraps the list response so huma can register a
// globally unique named schema.
type SubjectContactListBody struct {
	Items []*SubjectContactResponse `json:"items"`
}

type subjectContactListResponse struct {
	Body *SubjectContactListBody
}

type unmaskPIIInput struct {
	SubjectID string `path:"subject_id" doc:"The subject's UUID."`
	Body      struct {
		Field   string  `json:"field" enum:"allergies,chronic_conditions,insurance_policy_number,medical_alerts,medications,nhi_number,medicare_number,diet_notes" doc:"Encrypted field to reveal."`
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

// archiveContact handles DELETE /api/v1/contacts/{contact_id}.
// Soft-deletes the contact. Returns 409 Conflict if the contact still has
// active subjects — unlink or archive those first.
func (h *Handler) archiveContact(ctx context.Context, input *contactIDInput) (*contactResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid contact_id")
	}

	dto, err := h.svc.ArchiveContact(ctx, contactID, clinicID)
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

	vertical, err := h.clinic.GetVertical(ctx, clinicID)
	if err != nil {
		return nil, mapPatientError(err)
	}

	svcInput := CreateSubjectInput{
		ClinicID:    clinicID,
		CallerID:    callerID,
		Vertical:    vertical,
		DisplayName: input.Body.DisplayName,
		PhotoURL:    input.Body.PhotoURL,
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
	if input.Body.DentalDetails != nil {
		dd, err := parseDentalDetailsInput(input.Body.DentalDetails)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		svcInput.DentalDetails = dd
	}
	if input.Body.GeneralDetails != nil {
		gd, err := parseGeneralDetailsInput(input.Body.GeneralDetails)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		svcInput.GeneralDetails = gd
	}
	if input.Body.AgedCareDetails != nil {
		ad, err := parseAgedCareDetailsInput(input.Body.AgedCareDetails)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		svcInput.AgedCareDetails = ad
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
		ViewAll:    perms.ViewAllPatients,
		OwnerScope: !perms.ViewAllPatients && perms.ViewOwnPatients,
		CallerID:   callerID,
	}

	if input.Status != "" {
		s := input.Status
		svcInput.Status = &s
	}
	if input.Species != "" {
		sp := input.Species
		svcInput.Species = &sp
	}

	if input.ContactID != "" {
		cid, err := uuid.Parse(input.ContactID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid contact_id")
		}
		svcInput.ContactID = &cid
	}
	if input.Q != "" {
		q := input.Q
		svcInput.Search = &q
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
		PhotoURL:    input.Body.PhotoURL,
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

	if input.Body.DentalDetails != nil {
		d := input.Body.DentalDetails
		dd := &UpdateDentalDetailsInput{
			Sex:                   d.Sex,
			MedicalAlerts:         d.MedicalAlerts,
			Medications:           d.Medications,
			Allergies:             d.Allergies,
			ChronicConditions:     d.ChronicConditions,
			AdmissionWarnings:     d.AdmissionWarnings,
			InsuranceProviderName: d.InsuranceProviderName,
			InsurancePolicyNumber: d.InsurancePolicyNumber,
			ReferringDentistName:  d.ReferringDentistName,
			PrimaryDentistName:    d.PrimaryDentistName,
		}
		if d.DateOfBirth != nil {
			parsed, err := time.Parse("2006-01-02", *d.DateOfBirth)
			if err != nil {
				return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
			}
			dd.DateOfBirth = &parsed
		}
		svcInput.DentalDetails = dd
	}

	if input.Body.GeneralDetails != nil {
		g := input.Body.GeneralDetails
		gd := &UpdateGeneralDetailsInput{
			Sex:                   g.Sex,
			MedicalAlerts:         g.MedicalAlerts,
			Medications:           g.Medications,
			Allergies:             g.Allergies,
			ChronicConditions:     g.ChronicConditions,
			AdmissionWarnings:     g.AdmissionWarnings,
			InsuranceProviderName: g.InsuranceProviderName,
			InsurancePolicyNumber: g.InsurancePolicyNumber,
			ReferringProviderName: g.ReferringProviderName,
			PrimaryProviderName:   g.PrimaryProviderName,
		}
		if g.DateOfBirth != nil {
			parsed, err := time.Parse("2006-01-02", *g.DateOfBirth)
			if err != nil {
				return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
			}
			gd.DateOfBirth = &parsed
		}
		svcInput.GeneralDetails = gd
	}

	if input.Body.AgedCareDetails != nil {
		a := input.Body.AgedCareDetails
		ad := &UpdateAgedCareDetailsInput{
			Sex:                  a.Sex,
			Room:                 a.Room,
			NHINumber:            a.NHINumber,
			MedicareNumber:       a.MedicareNumber,
			Ethnicity:            a.Ethnicity,
			PreferredLanguage:    a.PreferredLanguage,
			MedicalAlerts:        a.MedicalAlerts,
			Medications:          a.Medications,
			Allergies:             a.Allergies,
			ChronicConditions:    a.ChronicConditions,
			CognitiveStatus:      a.CognitiveStatus,
			MobilityStatus:       a.MobilityStatus,
			ContinenceStatus:     a.ContinenceStatus,
			DietNotes:            a.DietNotes,
			AdvanceDirectiveFlag: a.AdvanceDirectiveFlag,
			FundingLevel:         a.FundingLevel,
			PrimaryGPName:        a.PrimaryGPName,
		}
		if a.DateOfBirth != nil {
			parsed, err := time.Parse("2006-01-02", *a.DateOfBirth)
			if err != nil {
				return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
			}
			ad.DateOfBirth = &parsed
		}
		if a.AdmissionDate != nil {
			parsed, err := time.Parse("2006-01-02", *a.AdmissionDate)
			if err != nil {
				return nil, huma.Error400BadRequest("admission_date must be YYYY-MM-DD")
			}
			ad.AdmissionDate = &parsed
		}
		svcInput.AgedCareDetails = ad
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

// addSubjectContact handles POST /api/v1/patients/{subject_id}/contacts.
// Links an existing contact to a subject in a given role.
func (h *Handler) addSubjectContact(ctx context.Context, input *addSubjectContactInput) (*subjectContactResponse, error) {
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

	dto, err := h.svc.AddSubjectContact(ctx, AddSubjectContactInput{
		SubjectID: subjectID,
		ClinicID:  clinicID,
		ContactID: contactID,
		Role:      input.Body.Role,
		Note:      input.Body.Note,
		CallerID:  callerID,
	})
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectContactResponse{Body: dto}, nil
}

// removeSubjectContact handles DELETE /api/v1/patients/{subject_id}/contacts/{contact_id}/{role}.
func (h *Handler) removeSubjectContact(ctx context.Context, input *removeSubjectContactInput) (*struct{}, error) {
	clinicID := mw.ClinicIDFromContext(ctx)
	callerID := mw.StaffIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}
	contactID, err := uuid.Parse(input.ContactID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid contact_id")
	}

	if err := h.svc.RemoveSubjectContact(ctx, subjectID, clinicID, contactID, callerID, input.Role); err != nil {
		return nil, mapPatientError(err)
	}
	return &struct{}{}, nil
}

// listSubjectContacts handles GET /api/v1/patients/{subject_id}/contacts.
func (h *Handler) listSubjectContacts(ctx context.Context, input *subjectIDInput) (*subjectContactListResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	subjectID, err := uuid.Parse(input.SubjectID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid subject_id")
	}

	links, err := h.svc.ListSubjectContacts(ctx, subjectID, clinicID)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &subjectContactListResponse{Body: &SubjectContactListBody{Items: links}}, nil
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

func parseDentalDetailsInput(d *dentalDetailsInput) (*DentalDetailsInput, error) {
	dd := &DentalDetailsInput{
		Sex:                   d.Sex,
		MedicalAlerts:         d.MedicalAlerts,
		Medications:           d.Medications,
		Allergies:             d.Allergies,
		ChronicConditions:     d.ChronicConditions,
		AdmissionWarnings:     d.AdmissionWarnings,
		InsuranceProviderName: d.InsuranceProviderName,
		InsurancePolicyNumber: d.InsurancePolicyNumber,
		ReferringDentistName:  d.ReferringDentistName,
		PrimaryDentistName:    d.PrimaryDentistName,
	}
	if d.DateOfBirth != nil {
		parsed, err := time.Parse("2006-01-02", *d.DateOfBirth)
		if err != nil {
			return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
		}
		dd.DateOfBirth = &parsed
	}
	return dd, nil
}

func parseGeneralDetailsInput(g *generalDetailsInput) (*GeneralDetailsInput, error) {
	gd := &GeneralDetailsInput{
		Sex:                   g.Sex,
		MedicalAlerts:         g.MedicalAlerts,
		Medications:           g.Medications,
		Allergies:             g.Allergies,
		ChronicConditions:     g.ChronicConditions,
		AdmissionWarnings:     g.AdmissionWarnings,
		InsuranceProviderName: g.InsuranceProviderName,
		InsurancePolicyNumber: g.InsurancePolicyNumber,
		ReferringProviderName: g.ReferringProviderName,
		PrimaryProviderName:   g.PrimaryProviderName,
	}
	if g.DateOfBirth != nil {
		parsed, err := time.Parse("2006-01-02", *g.DateOfBirth)
		if err != nil {
			return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
		}
		gd.DateOfBirth = &parsed
	}
	return gd, nil
}

func parseAgedCareDetailsInput(a *agedCareDetailsInput) (*AgedCareDetailsInput, error) {
	ad := &AgedCareDetailsInput{
		Sex:                  a.Sex,
		Room:                 a.Room,
		NHINumber:            a.NHINumber,
		MedicareNumber:       a.MedicareNumber,
		Ethnicity:            a.Ethnicity,
		PreferredLanguage:    a.PreferredLanguage,
		MedicalAlerts:        a.MedicalAlerts,
		Medications:          a.Medications,
		Allergies:            a.Allergies,
		ChronicConditions:    a.ChronicConditions,
		CognitiveStatus:      a.CognitiveStatus,
		MobilityStatus:       a.MobilityStatus,
		ContinenceStatus:     a.ContinenceStatus,
		DietNotes:            a.DietNotes,
		AdvanceDirectiveFlag: a.AdvanceDirectiveFlag,
		FundingLevel:         a.FundingLevel,
		PrimaryGPName:        a.PrimaryGPName,
	}
	if a.DateOfBirth != nil {
		parsed, err := time.Parse("2006-01-02", *a.DateOfBirth)
		if err != nil {
			return nil, huma.Error400BadRequest("date_of_birth must be YYYY-MM-DD")
		}
		ad.DateOfBirth = &parsed
	}
	if a.AdmissionDate != nil {
		parsed, err := time.Parse("2006-01-02", *a.AdmissionDate)
		if err != nil {
			return nil, huma.Error400BadRequest("admission_date must be YYYY-MM-DD")
		}
		ad.AdmissionDate = &parsed
	}
	return ad, nil
}

// ── Subject photo upload ──────────────────────────────────────────────────────
//
// Mirrors POST /api/v1/clinic/logo: multipart/form-data, single "file"
// field, 4 MiB cap, allowed image/* content-types only. Returns the
// persisted storage key plus a freshly-signed download URL the caller
// is expected to write into `subjects.photo_url` via Create / Update.
// The decoupled upload + persist step lets the mobile create sheet pick
// a photo before the subject row exists, then commit both in one POST.

const maxSubjectPhotoBytes int64 = 4 << 20

type uploadSubjectPhotoInput struct {
	RawBody multipart.Form
}

type uploadSubjectPhotoResponse struct {
	Body *UploadSubjectPhotoResponse
}

// UploadSubjectPhotoResponse is what the upload endpoint returns. Key
// is the durable storage path; URL is short-lived (1h) and re-mintable
// via the photo signer when needed.
type UploadSubjectPhotoResponse struct {
	PhotoKey string `json:"photo_key" doc:"Durable object-storage key for the photo."`
	PhotoURL string `json:"photo_url" doc:"Short-lived signed download URL — write into the subject's photo_url on the next create/update."`
}

func isAllowedSubjectPhotoType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/heic":
		return true
	}
	return false
}

// uploadSubjectPhoto handles POST /api/v1/patients/upload-photo.
func (h *Handler) uploadSubjectPhoto(ctx context.Context, input *uploadSubjectPhotoInput) (*uploadSubjectPhotoResponse, error) {
	clinicID := mw.ClinicIDFromContext(ctx)

	files := input.RawBody.File["file"]
	if len(files) == 0 {
		return nil, huma.Error400BadRequest("missing form field \"file\"")
	}
	hdr := files[0]
	if hdr.Size > maxSubjectPhotoBytes {
		return nil, huma.Error400BadRequest(fmt.Sprintf("photo too large (max %d bytes)", maxSubjectPhotoBytes))
	}

	contentType := hdr.Header.Get("Content-Type")
	if !isAllowedSubjectPhotoType(contentType) {
		return nil, huma.Error415UnsupportedMediaType("photo must be png, jpeg, webp or heic")
	}

	f, err := hdr.Open()
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read uploaded file")
	}
	defer func() { _ = f.Close() }()

	key, url, err := h.svc.UploadSubjectPhoto(ctx, clinicID, contentType, f, hdr.Size)
	if err != nil {
		return nil, mapPatientError(err)
	}
	return &uploadSubjectPhotoResponse{Body: &UploadSubjectPhotoResponse{
		PhotoKey: key,
		PhotoURL: url,
	}}, nil
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
