package domain

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Vertical represents the clinical domain a clinic operates in.
// Adding a new vertical is additive — existing code is unchanged.
type Vertical string

const (
	VerticalVeterinary    Vertical = "veterinary"
	VerticalDental        Vertical = "dental"
	VerticalGeneralClinic Vertical = "general_clinic"
	VerticalAgedCare      Vertical = "aged_care"
)

// ClinicStatus represents the subscription lifecycle state of a clinic.
type ClinicStatus string

const (
	ClinicStatusTrial       ClinicStatus = "trial"
	ClinicStatusActive      ClinicStatus = "active"
	ClinicStatusPastDue     ClinicStatus = "past_due"
	ClinicStatusGracePeriod ClinicStatus = "grace_period"
	ClinicStatusCancelled   ClinicStatus = "cancelled"
	ClinicStatusSuspended   ClinicStatus = "suspended"
)

// StaffRole represents a staff member's role within their clinic.
type StaffRole string

const (
	StaffRoleSuperAdmin   StaffRole = "super_admin"
	StaffRoleAdmin        StaffRole = "admin"
	StaffRoleVet          StaffRole = "vet"
	StaffRoleVetNurse     StaffRole = "vet_nurse"
	StaffRoleReceptionist StaffRole = "receptionist"
)

// NoteTier determines how a staff member is counted for billing purposes.
type NoteTier string

const (
	// NoteTierStandard counts toward the clinic's billing tier and gets full note quota.
	NoteTierStandard NoteTier = "standard"
	// NoteTierNurse does not count toward billing tier and gets 50% note quota.
	NoteTierNurse NoteTier = "nurse"
	// NoteTierNone does not get a personal note quota (admin/reception staff).
	NoteTierNone NoteTier = "none"
)

// StaffStatus represents the lifecycle state of a staff account.
type StaffStatus string

const (
	StaffStatusInvited     StaffStatus = "invited"
	StaffStatusActive      StaffStatus = "active"
	StaffStatusDeactivated StaffStatus = "deactivated"
)

// SubjectStatus represents the lifecycle state of a subject (animal, patient, resident).
type SubjectStatus string

const (
	SubjectStatusActive      SubjectStatus = "active"
	SubjectStatusDeceased    SubjectStatus = "deceased"
	SubjectStatusTransferred SubjectStatus = "transferred"
	SubjectStatusArchived    SubjectStatus = "archived"
)

// VetSpecies represents the species of an animal in a veterinary clinic.
type VetSpecies string

const (
	VetSpeciesDog     VetSpecies = "dog"
	VetSpeciesCat     VetSpecies = "cat"
	VetSpeciesBird    VetSpecies = "bird"
	VetSpeciesRabbit  VetSpecies = "rabbit"
	VetSpeciesReptile VetSpecies = "reptile"
	VetSpeciesOther   VetSpecies = "other"
)

// VetSex represents the biological sex of a veterinary subject.
type VetSex string

const (
	VetSexMale    VetSex = "male"
	VetSexFemale  VetSex = "female"
	VetSexUnknown VetSex = "unknown"
)

// DentalSex represents the biological sex of a dental subject (human).
type DentalSex string

const (
	DentalSexMale    DentalSex = "male"
	DentalSexFemale  DentalSex = "female"
	DentalSexOther   DentalSex = "other"
	DentalSexUnknown DentalSex = "unknown"
)

// GeneralSex represents the biological sex of a general_clinic subject (human).
type GeneralSex string

const (
	GeneralSexMale    GeneralSex = "male"
	GeneralSexFemale  GeneralSex = "female"
	GeneralSexOther   GeneralSex = "other"
	GeneralSexUnknown GeneralSex = "unknown"
)

// AgedCareSex represents the biological sex of an aged-care resident.
type AgedCareSex string

const (
	AgedCareSexMale    AgedCareSex = "male"
	AgedCareSexFemale  AgedCareSex = "female"
	AgedCareSexOther   AgedCareSex = "other"
	AgedCareSexUnknown AgedCareSex = "unknown"
)

// AgedCareCognitiveStatus classifies a resident's cognitive function.
// Values map to InterRAI domains used in NZ/AU aged-care assessments.
type AgedCareCognitiveStatus string

const (
	CognitiveIndependent         AgedCareCognitiveStatus = "independent"
	CognitiveMildImpairment      AgedCareCognitiveStatus = "mild_impairment"
	CognitiveModerateImpairment  AgedCareCognitiveStatus = "moderate_impairment"
	CognitiveSevereImpairment    AgedCareCognitiveStatus = "severe_impairment"
	CognitiveStatusUnknown       AgedCareCognitiveStatus = "unknown"
)

// AgedCareMobilityStatus classifies a resident's mobility level.
type AgedCareMobilityStatus string

const (
	MobilityIndependent    AgedCareMobilityStatus = "independent"
	MobilitySupervised     AgedCareMobilityStatus = "supervised"
	MobilityAssisted       AgedCareMobilityStatus = "assisted"
	MobilityImmobile       AgedCareMobilityStatus = "immobile"
	MobilityStatusUnknown  AgedCareMobilityStatus = "unknown"
)

// AgedCareContinenceStatus classifies a resident's continence.
type AgedCareContinenceStatus string

const (
	ContinenceContinent             AgedCareContinenceStatus = "continent"
	ContinenceUrinaryIncontinence   AgedCareContinenceStatus = "urinary_incontinence"
	ContinenceFaecalIncontinence    AgedCareContinenceStatus = "faecal_incontinence"
	ContinenceDoubleIncontinence    AgedCareContinenceStatus = "double_incontinence"
	ContinenceCatheterised          AgedCareContinenceStatus = "catheterised"
	ContinenceStatusUnknown         AgedCareContinenceStatus = "unknown"
)

// AgedCareFundingLevel encodes the funding/assessment tier for a resident.
// Covers NZ InterRAI and AU Home Care Package levels plus residential.
type AgedCareFundingLevel string

const (
	FundingHomeCare1       AgedCareFundingLevel = "home_care_1"
	FundingHomeCare2       AgedCareFundingLevel = "home_care_2"
	FundingHomeCare3       AgedCareFundingLevel = "home_care_3"
	FundingHomeCare4       AgedCareFundingLevel = "home_care_4"
	FundingResidentialLow  AgedCareFundingLevel = "residential_low"
	FundingResidentialHigh AgedCareFundingLevel = "residential_high"
	FundingUnfunded        AgedCareFundingLevel = "unfunded"
	FundingLevelUnknown    AgedCareFundingLevel = "unknown"
)

// SubjectContactRole is the relationship between a subject and a linked
// contact. A subject can carry several contacts at once in different roles
// (e.g. primary_owner + emergency_contact), so this is a free vocabulary
// rather than a single choice. Enum values mirror the CHECK constraint on
// the subject_contacts table.
type SubjectContactRole string

const (
	SubjectContactRolePrimaryOwner      SubjectContactRole = "primary_owner"
	SubjectContactRoleCoOwner           SubjectContactRole = "co_owner"
	SubjectContactRoleEmergencyContact  SubjectContactRole = "emergency_contact"
	SubjectContactRoleGuardian          SubjectContactRole = "guardian"
	SubjectContactRoleNextOfKin         SubjectContactRole = "next_of_kin"
	SubjectContactRolePowerOfAttorney   SubjectContactRole = "power_of_attorney"
	SubjectContactRoleReferringProvider SubjectContactRole = "referring_provider"
	SubjectContactRoleOther             SubjectContactRole = "other"
)

// FormVersionStatus represents the lifecycle state of a form version.
type FormVersionStatus string

const (
	// FormVersionStatusDraft is the single mutable version being edited.
	// Only one draft per form is allowed (enforced by DB partial unique index).
	FormVersionStatusDraft FormVersionStatus = "draft"
	// FormVersionStatusPublished is a frozen, live version available for use.
	FormVersionStatusPublished FormVersionStatus = "published"
	// FormVersionStatusArchived marks the final version when a form is retired.
	FormVersionStatusArchived FormVersionStatus = "archived"
)

// ChangeType classifies the severity of a form version change for semver bumping.
type ChangeType string

const (
	// ChangeTypeMinor covers metadata-only changes: name, description, prompts, policies.
	ChangeTypeMinor ChangeType = "minor"
	// ChangeTypeMajor covers structural changes: fields added, removed, or retyped.
	ChangeTypeMajor ChangeType = "major"
)

// NoteStatus represents the lifecycle state of a clinical note.
type NoteStatus string

const (
	// NoteStatusExtracting means the River job is running AI extraction.
	NoteStatusExtracting NoteStatus = "extracting"
	// NoteStatusDraft means extraction is complete and the note is ready for review.
	NoteStatusDraft NoteStatus = "draft"
	// NoteStatusSubmitted means the reviewer confirmed and submitted the note.
	NoteStatusSubmitted NoteStatus = "submitted"
	// NoteStatusFailed means extraction failed after all retries.
	NoteStatusFailed NoteStatus = "failed"
	// NoteStatusOverriding means a previously-submitted note has been
	// unlocked for editing via the override flow. Field PATCH is allowed
	// while in this state; on re-submit the status returns to submitted
	// and override_count increments.
	NoteStatusOverriding NoteStatus = "overriding"
)

// TransformationType describes how an AI-extracted field value was derived.
type TransformationType string

const (
	// TransformationDirect means the value appears verbatim or near-verbatim in the transcript.
	TransformationDirect TransformationType = "direct"
	// TransformationInference means the value was derived or computed from surrounding context.
	TransformationInference TransformationType = "inference"
)

// RecordingStatus represents the lifecycle state of an audio recording.
type RecordingStatus string

const (
	// RecordingStatusPendingUpload means the recording row exists but the client
	// has not yet uploaded the audio file to storage.
	RecordingStatusPendingUpload RecordingStatus = "pending_upload"
	// RecordingStatusUploaded means the client confirmed the upload and the
	// transcription job has been enqueued.
	RecordingStatusUploaded RecordingStatus = "uploaded"
	// RecordingStatusTranscribing means the River job is actively calling Deepgram.
	RecordingStatusTranscribing RecordingStatus = "transcribing"
	// RecordingStatusTranscribed means the transcript is available.
	RecordingStatusTranscribed RecordingStatus = "transcribed"
	// RecordingStatusFailed means all transcription retries were exhausted.
	RecordingStatusFailed RecordingStatus = "failed"
)

// SubjectAccessAction records the kind of access event written to
// subject_access_log. `view` covers list/get; `unmask_pii` is the
// tap-to-reveal event that surfaces encrypted PII to the caller.
type SubjectAccessAction string

const (
	SubjectAccessActionView      SubjectAccessAction = "view"
	SubjectAccessActionCreate    SubjectAccessAction = "create"
	SubjectAccessActionUpdate    SubjectAccessAction = "update"
	SubjectAccessActionArchive   SubjectAccessAction = "archive"
	SubjectAccessActionUnmaskPII SubjectAccessAction = "unmask_pii"
)

// Permissions holds the full set of boolean capability flags for a staff member.
// These are embedded in the JWT and enforced by middleware on every request.
type Permissions struct {
	ManageStaff         bool `json:"manage_staff"`
	ManageForms         bool `json:"manage_forms"`
	ManagePolicies      bool `json:"manage_policies"`
	ManageBilling       bool `json:"manage_billing"`
	RollbackPolicies    bool `json:"rollback_policies"`
	RecordAudio         bool `json:"record_audio"`
	SubmitForms         bool `json:"submit_forms"`
	ViewAllPatients     bool `json:"view_all_patients"`
	ViewOwnPatients     bool `json:"view_own_patients"`
	Dispense            bool `json:"dispense"`
	GenerateAuditExport bool `json:"generate_audit_export"`
	ManagePatients      bool `json:"manage_patients"`
	MarketplaceManage   bool `json:"marketplace_manage"`
	MarketplaceDownload bool `json:"marketplace_download"`
}

// DefaultPermissions returns the minimum permissions for the given role.
// These are the defaults at invite time — admins may grant additional permissions.
func DefaultPermissions(role StaffRole) Permissions {
	switch role {
	case StaffRoleSuperAdmin:
		return Permissions{
			ManageStaff: true, ManageForms: true, ManagePolicies: true,
			ManageBilling: true, RollbackPolicies: true, RecordAudio: true,
			SubmitForms: true, ViewAllPatients: true, GenerateAuditExport: true,
			ManagePatients: true,
			MarketplaceManage: true, MarketplaceDownload: true,
		}
	case StaffRoleAdmin:
		return Permissions{
			ManageStaff: true, ManageForms: true, ManagePolicies: true,
			RecordAudio: true, SubmitForms: true, ViewAllPatients: true,
			GenerateAuditExport: true, ManagePatients: true,
			MarketplaceDownload: true,
		}
	case StaffRoleVet:
		return Permissions{
			RecordAudio: true, SubmitForms: true, ViewOwnPatients: true,
			ManagePatients: true,
			MarketplaceDownload: true,
		}
	case StaffRoleVetNurse:
		return Permissions{
			RecordAudio: true, SubmitForms: true, ViewOwnPatients: true,
			ManagePatients: true,
			MarketplaceDownload: true,
		}
	case StaffRoleReceptionist:
		return Permissions{
			ViewAllPatients: true, ManagePatients: true,
		}
	default:
		return Permissions{}
	}
}

// Page represents a cursor-paginated list result.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"total"`
}

// NewID generates a new UUID v4 for use as a primary key.
// We use v4 (random) for now — v7 (time-ordered) can be introduced via a
// library update when needed for index performance at large scale.
func NewID() uuid.UUID {
	return uuid.New()
}

// clock is the package-level time source. Protected by clockMu so that test
// code calling SetTimeNow from parallel goroutines does not race with
// production calls to TimeNow.
var (
	clockMu sync.RWMutex
	clockFn = func() time.Time { return time.Now().UTC() }
)

// TimeNow returns the current UTC time. Production code always calls this
// instead of time.Now() directly so tests can override it via SetTimeNow.
func TimeNow() time.Time {
	clockMu.RLock()
	defer clockMu.RUnlock()
	return clockFn()
}

// SetTimeNow replaces the clock function and returns a restore function.
// Tests should call the restore function via t.Cleanup:
//
//	t.Cleanup(domain.SetTimeNow(func() time.Time { return fixed }))
func SetTimeNow(fn func() time.Time) (restore func()) {
	clockMu.Lock()
	old := clockFn
	clockFn = fn
	clockMu.Unlock()
	return func() {
		clockMu.Lock()
		clockFn = old
		clockMu.Unlock()
	}
}
