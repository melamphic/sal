package forms

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

var (
	testClinicID = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	testStaffID  = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
)

func newTestService() *Service {
	return NewService(newFakeRepo())
}

// ── CreateForm ────────────────────────────────────────────────────────────────

func TestService_CreateForm_CreatesFormAndDraft(t *testing.T) {
	svc := newTestService()

	resp, err := svc.CreateForm(context.Background(), CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Intake Assessment",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Intake Assessment" {
		t.Errorf("name: got %q, want %q", resp.Name, "Intake Assessment")
	}
	if resp.Draft == nil {
		t.Fatal("expected draft to be created")
	}
	if resp.Draft.Status != domain.FormVersionStatusDraft {
		t.Errorf("draft status: got %v", resp.Draft.Status)
	}
	if resp.LatestPublished != nil {
		t.Error("expected no published version on new form")
	}
}

func TestService_CreateForm_TagsDefaultToEmptySlice(t *testing.T) {
	svc := newTestService()

	resp, err := svc.CreateForm(context.Background(), CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "No Tags Form",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Tags == nil {
		t.Error("tags should not be nil")
	}
}

// ── GetForm ───────────────────────────────────────────────────────────────────

func TestService_GetForm_NotFound(t *testing.T) {
	svc := newTestService()

	_, err := svc.GetForm(context.Background(), uuid.New(), testClinicID)
	if !isNotFound(err) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_GetForm_ReturnsDraftAndPublished(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// Create form + draft.
	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Test Form",
	})
	formID := uuid.MustParse(created.ID)

	// Publish draft.
	_, _ = svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})

	// Add fields to new draft (auto-created by UpdateDraft).
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Test Form",
		Fields: []FieldInput{
			{Position: 1, Title: "Notes", Type: "long_text", Config: json.RawMessage(`{}`)},
		},
	})

	resp, err := svc.GetForm(ctx, formID, testClinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Draft == nil {
		t.Error("expected draft after update")
	}
	if resp.LatestPublished == nil {
		t.Error("expected latest published version")
	}
	major := 1
	if *resp.LatestPublished.VersionMajor != major {
		t.Errorf("published version major: got %d, want %d", *resp.LatestPublished.VersionMajor, major)
	}
}

// ── UpdateDraft ───────────────────────────────────────────────────────────────

func TestService_UpdateDraft_ReplacesFields(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "My Form",
	})
	formID := uuid.MustParse(created.ID)

	resp, err := svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "My Form Updated",
		Fields: []FieldInput{
			{Position: 1, Title: "Temp", Type: "text", Config: json.RawMessage(`{}`)},
			{Position: 2, Title: "Score", Type: "slider", Config: json.RawMessage(`{"min":0,"max":10}`)},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "My Form Updated" {
		t.Errorf("name: got %q", resp.Name)
	}
	if len(resp.Draft.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(resp.Draft.Fields))
	}
}

func TestService_UpdateDraft_ArchivedFormReturnsConflict(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Retiring Form",
	})
	formID := uuid.MustParse(created.ID)
	_, _ = svc.RetireForm(ctx, RetireFormInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
	})

	_, err := svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "My Form",
	})
	if !isConflict(err) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

// ── PublishForm ───────────────────────────────────────────────────────────────

func TestService_PublishForm_FirstPublishIsV1_0(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
	})
	formID := uuid.MustParse(created.ID)

	v, err := svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *v.VersionMajor != 1 || *v.VersionMinor != 0 {
		t.Errorf("expected 1.0, got %d.%d", *v.VersionMajor, *v.VersionMinor)
	}
}

func TestService_PublishForm_MinorBumpIncreasesMinor(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
	})
	formID := uuid.MustParse(created.ID)

	// First publish: 1.0
	_, _ = svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})

	// Auto-create draft for next publish.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
	})

	v, err := svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMinor,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *v.VersionMajor != 1 || *v.VersionMinor != 1 {
		t.Errorf("expected 1.1, got %d.%d", *v.VersionMajor, *v.VersionMinor)
	}
}

func TestService_PublishForm_MajorBumpResetMinor(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
	})
	formID := uuid.MustParse(created.ID)

	// v1.0
	_, _ = svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})
	// v1.1
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	_, _ = svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMinor})
	// v2.0
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	v, err := svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *v.VersionMajor != 2 || *v.VersionMinor != 0 {
		t.Errorf("expected 2.0, got %d.%d", *v.VersionMajor, *v.VersionMinor)
	}
}

func TestService_PublishForm_NoDraftReturnsNotFound(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
	})
	formID := uuid.MustParse(created.ID)

	// Publish once (consumes draft).
	_, _ = svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})

	// Second publish with no new draft → not found.
	_, err := svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})
	if !isNotFound(err) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── RollbackForm ──────────────────────────────────────────────────────────────

func TestService_RollbackForm_CopiesFieldsFromTarget(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)

	// Set fields and publish v1.0.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "Old Field", Type: "text", Config: json.RawMessage(`{}`)}},
	})
	v1, _ := svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})
	v1ID := uuid.MustParse(v1.ID)

	// Rollback to v1.
	draft, err := svc.RollbackForm(ctx, RollbackFormInput{
		FormID:          formID,
		ClinicID:        testClinicID,
		StaffID:         testStaffID,
		TargetVersionID: v1ID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(draft.Fields) != 1 || draft.Fields[0].Title != "Old Field" {
		t.Errorf("expected 1 field 'Old Field', got %v", draft.Fields)
	}
	if draft.RollbackOf == nil || *draft.RollbackOf != v1.ID {
		t.Errorf("rollback_of not set correctly")
	}
}

func TestService_RollbackForm_ExistingDraftBlocksRollback(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)

	// Publish v1.0.
	v1, _ := svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})
	v1ID := uuid.MustParse(v1.ID)

	// New draft auto-created.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})

	// Rollback blocked by existing draft.
	_, err := svc.RollbackForm(ctx, RollbackFormInput{
		FormID:          formID,
		ClinicID:        testClinicID,
		StaffID:         testStaffID,
		TargetVersionID: v1ID,
	})
	if !isConflict(err) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

// ── RetireForm ────────────────────────────────────────────────────────────────

func TestService_RetireForm_SetsArchivedAt(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)

	reason := "no longer needed"
	resp, err := svc.RetireForm(ctx, RetireFormInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Reason:   &reason,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ArchivedAt == nil {
		t.Error("expected archived_at to be set")
	}
	if resp.RetireReason == nil || *resp.RetireReason != reason {
		t.Errorf("retire_reason: got %v", resp.RetireReason)
	}
}

func TestService_RetireForm_AlreadyRetiredReturnsConflict(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)

	_, _ = svc.RetireForm(ctx, RetireFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID})
	_, err := svc.RetireForm(ctx, RetireFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID})
	if !isConflict(err) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

// ── Groups ────────────────────────────────────────────────────────────────────

func TestService_CreateGroup_And_ListGroups(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	desc := "All intake forms"
	g, err := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID:    testClinicID,
		StaffID:     testStaffID,
		Name:        "Intake",
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g.Name != "Intake" {
		t.Errorf("name: got %q", g.Name)
	}

	list, err := svc.ListGroups(ctx, testClinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected 1 group, got %d", len(list.Items))
	}
}

// ── Policies ──────────────────────────────────────────────────────────────────

func TestService_LinkUnlinkPolicy(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)
	policyID := uuid.New()

	if err := svc.LinkPolicy(ctx, formID, testClinicID, policyID, testStaffID); err != nil {
		t.Fatalf("link: %v", err)
	}

	ids, err := svc.ListLinkedPolicies(ctx, formID, testClinicID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ids) != 1 || ids[0] != policyID.String() {
		t.Errorf("expected policy, got %v", ids)
	}

	if err := svc.UnlinkPolicy(ctx, formID, testClinicID, policyID); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	ids, _ = svc.ListLinkedPolicies(ctx, formID, testClinicID)
	if len(ids) != 0 {
		t.Errorf("expected no policies after unlink, got %v", ids)
	}
}

// ── Style ─────────────────────────────────────────────────────────────────────

func TestService_UpdateStyle_IncrementsVersion(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	color := "#3B82F6"
	s1, err := svc.UpdateStyle(ctx, UpdateStyleInput{
		ClinicID:     testClinicID,
		StaffID:      testStaffID,
		PrimaryColor: &color,
	})
	if err != nil {
		t.Fatalf("update 1: %v", err)
	}
	if s1.Version != 1 {
		t.Errorf("expected version 1, got %d", s1.Version)
	}

	s2, err := svc.UpdateStyle(ctx, UpdateStyleInput{
		ClinicID:     testClinicID,
		StaffID:      testStaffID,
		PrimaryColor: &color,
	})
	if err != nil {
		t.Fatalf("update 2: %v", err)
	}
	if s2.Version != 2 {
		t.Errorf("expected version 2, got %d", s2.Version)
	}
}

func TestService_GetCurrentStyle_NoStyleReturnsNil(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	resp, err := svc.GetCurrentStyle(ctx, testClinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response, got %v", resp)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return containsErr(err, domain.ErrNotFound)
}

func isConflict(err error) bool {
	if err == nil {
		return false
	}
	return containsErr(err, domain.ErrConflict)
}

func containsErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
