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
	return NewService(newFakeRepo(), nil, nil, nil, nil, nil, nil)
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

	// Add fields so publish has something to freeze.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Test Form",
		Fields: []FieldInput{
			{Position: 1, Title: "Notes", Type: "long_text", Config: json.RawMessage(`{}`)},
		},
	})

	// Publish draft.
	_, _ = svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})

	// Add fields to the freshly auto-created next draft.
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

	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "A", Type: "text", Config: json.RawMessage(`{}`)}},
	})

	v, err := svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *v.LatestPublished.VersionMajor != 1 || *v.LatestPublished.VersionMinor != 0 {
		t.Errorf("expected 1.0, got %d.%d", *v.LatestPublished.VersionMajor, *v.LatestPublished.VersionMinor)
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

	// First publish: 1.0 (draft must have a field).
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "A", Type: "text", Config: json.RawMessage(`{}`)}},
	})
	_, _ = svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})

	// Next draft must also have a field.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
		Fields:   []FieldInput{{Position: 1, Title: "A", Type: "text", Config: json.RawMessage(`{}`)}},
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
	if *v.LatestPublished.VersionMajor != 1 || *v.LatestPublished.VersionMinor != 1 {
		t.Errorf("expected 1.1, got %d.%d", *v.LatestPublished.VersionMajor, *v.LatestPublished.VersionMinor)
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

	withField := []FieldInput{{Position: 1, Title: "A", Type: "text", Config: json.RawMessage(`{}`)}}

	// v1.0
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form", Fields: withField})
	_, _ = svc.PublishForm(ctx, PublishFormInput{
		FormID:     formID,
		ClinicID:   testClinicID,
		StaffID:    testStaffID,
		ChangeType: domain.ChangeTypeMajor,
	})
	// v1.1
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form", Fields: withField})
	_, _ = svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMinor})
	// v2.0
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form", Fields: withField})
	v, err := svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *v.LatestPublished.VersionMajor != 2 || *v.LatestPublished.VersionMinor != 0 {
		t.Errorf("expected 2.0, got %d.%d", *v.LatestPublished.VersionMajor, *v.LatestPublished.VersionMinor)
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
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "A", Type: "text", Config: json.RawMessage(`{}`)}},
	})
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
	v1ID := uuid.MustParse(v1.LatestPublished.ID)

	// Change fields and publish v2.0 so rollback has somewhere to go back from.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "New Field", Type: "text", Config: json.RawMessage(`{}`)}},
	})
	_, _ = svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})

	// Rollback to v1.
	form, err := svc.RollbackForm(ctx, RollbackFormInput{
		FormID:          formID,
		ClinicID:        testClinicID,
		StaffID:         testStaffID,
		TargetVersionID: v1ID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form.LatestPublished == nil {
		t.Fatalf("expected rollback to produce a new published version")
	}
	if form.LatestPublished.Status != domain.FormVersionStatusPublished {
		t.Errorf("expected new version to be published, got %v", form.LatestPublished.Status)
	}
	if len(form.LatestPublished.Fields) != 1 || form.LatestPublished.Fields[0].Title != "Old Field" {
		t.Errorf("expected 1 field 'Old Field' copied from target, got %v", form.LatestPublished.Fields)
	}
	if form.LatestPublished.RollbackOf == nil || *form.LatestPublished.RollbackOf != v1.LatestPublished.ID {
		t.Errorf("rollback_of not set correctly")
	}
	// A rollback must not bump the major version — it's a corrective action.
	if form.LatestPublished.VersionMajor == nil || *form.LatestPublished.VersionMajor != 2 {
		t.Errorf("expected major=2, got %v", form.LatestPublished.VersionMajor)
	}
	if form.LatestPublished.VersionMinor == nil || *form.LatestPublished.VersionMinor != 1 {
		t.Errorf("expected minor=1, got %v", form.LatestPublished.VersionMinor)
	}
}

func TestService_RollbackForm_OverwritesDraftWithTargetFields(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)

	// Publish v1.0 with two fields.
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{
			{Position: 1, Title: "Published A", Type: "text", Config: json.RawMessage(`{}`)},
			{Position: 2, Title: "Published B", Type: "text", Config: json.RawMessage(`{}`)},
		},
	})
	v1, _ := svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})
	v1ID := uuid.MustParse(v1.LatestPublished.ID)

	// Write some post-publish scratch edits — e.g. the user started drafting
	// an unrelated change. Rolling back should restore the draft to match the
	// rolled-back target, because the user's mental model is "bring it back to
	// how it was", not "append a rollback beside my WIP".
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "Scratch Field", Type: "text", Config: json.RawMessage(`{}`)}},
	})

	form, err := svc.RollbackForm(ctx, RollbackFormInput{
		FormID:          formID,
		ClinicID:        testClinicID,
		StaffID:         testStaffID,
		TargetVersionID: v1ID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form.Draft == nil {
		t.Fatalf("expected a draft after rollback")
	}
	if len(form.Draft.Fields) != 2 {
		t.Fatalf("expected draft to mirror the target's 2 fields, got %d", len(form.Draft.Fields))
	}
	if form.Draft.Fields[0].Title != "Published A" || form.Draft.Fields[1].Title != "Published B" {
		t.Errorf("draft fields did not match target: %+v", form.Draft.Fields)
	}
	if form.LatestPublished == nil || form.LatestPublished.RollbackOf == nil {
		t.Fatalf("expected a new rollback version to be the latest published")
	}
	if *form.LatestPublished.RollbackOf != v1.LatestPublished.ID {
		t.Errorf("rollback_of not set correctly")
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

func TestService_ListVersions_IncludesRetireEntryAtTop(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)
	_, _ = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
		Fields: []FieldInput{{Position: 1, Title: "F", Type: "text", Config: json.RawMessage(`{}`)}},
	})
	_, _ = svc.PublishForm(ctx, PublishFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, ChangeType: domain.ChangeTypeMajor})

	// Without retirement, the trail is just the published versions.
	before, err := svc.ListVersions(ctx, formID, testClinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, v := range before.Items {
		if v.Kind == "retire" {
			t.Fatalf("unexpected retire entry before retiring: %+v", v)
		}
	}

	reason := "decommissioned"
	if _, err := svc.RetireForm(ctx, RetireFormInput{FormID: formID, ClinicID: testClinicID, StaffID: testStaffID, Reason: &reason}); err != nil {
		t.Fatalf("retire failed: %v", err)
	}

	after, err := svc.ListVersions(ctx, formID, testClinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(after.Items) == 0 || after.Items[0].Kind != "retire" {
		t.Fatalf("expected retire entry at head, got %+v", after.Items)
	}
	if after.Items[0].PublishedBy == nil {
		t.Errorf("expected retire entry to record publisher (retired_by)")
	}
	if after.Items[0].ChangeSummary == nil || *after.Items[0].ChangeSummary == "" {
		t.Errorf("expected retire entry to carry reason summary")
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

// fakePolicyVerifier implements PolicyOwnershipVerifier for tests.
// errForID lets a test simulate a specific policy being cross-tenant.
type fakePolicyVerifier struct {
	errForID uuid.UUID
	err      error
	calls    int
}

func (f *fakePolicyVerifier) VerifyPolicyOwnership(_ context.Context, policyID, _ uuid.UUID) error {
	f.calls++
	if policyID == f.errForID {
		return f.err
	}
	return nil
}

func TestService_LinkPolicy_RejectsCrossTenantPolicy(t *testing.T) {
	foreignPolicyID := uuid.New()
	verifier := &fakePolicyVerifier{errForID: foreignPolicyID, err: domain.ErrNotFound}
	svc := NewService(newFakeRepo(), nil, nil, nil, nil, nil, verifier)
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{ClinicID: testClinicID, StaffID: testStaffID, Name: "Form"})
	formID := uuid.MustParse(created.ID)

	err := svc.LinkPolicy(ctx, formID, testClinicID, foreignPolicyID, testStaffID)
	if !isNotFound(err) {
		t.Fatalf("expected ErrNotFound for cross-tenant policy, got %v", err)
	}
	if verifier.calls != 1 {
		t.Errorf("expected verifier to be called once, got %d", verifier.calls)
	}

	ids, _ := svc.ListLinkedPolicies(ctx, formID, testClinicID)
	if len(ids) != 0 {
		t.Errorf("rejected link should not have persisted, got %v", ids)
	}
}

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

// ── Style logo sniff ──────────────────────────────────────────────────────────

func TestSniffImageType(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}, "image/png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0}, "image/jpeg"},
		{"webp", append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0), "image/webp"},
		{"html", []byte("<html><body>h"), ""},
		{"svg", []byte("<svg xmlns=\""), ""},
		{"short", []byte{0x89, 0x50}, ""},
		{"empty", []byte{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sniffImageType(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCanonicalStyleLogoType(t *testing.T) {
	cases := map[string]string{
		"image/png":     "image/png",
		"image/jpeg":    "image/jpeg",
		"image/jpg":     "image/jpeg",
		"image/webp":    "image/webp",
		"image/svg+xml": "",
		"text/html":     "",
		"":              "",
	}
	for in, want := range cases {
		if got := canonicalStyleLogoType(in); got != want {
			t.Errorf("canonicalStyleLogoType(%q) = %q, want %q", in, got, want)
		}
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
