package forms

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Regression tests for bugs fixed in the 2026-05-13/14 review pass.
// Each test pins behaviour that was previously broken; failures here mean
// someone reverted or undermined a security / data-integrity fix.

// ── Cross-tenant group_id IDOR ───────────────────────────────────────────────
//
// CreateForm + UpdateDraft must verify that any caller-supplied group_id
// belongs to the caller's clinic. Without the check, a clinic could parent
// its forms under another clinic's folder (broken tenant isolation).

func TestRegression_CreateForm_RejectsCrossTenantGroupID(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	foreignClinicID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000999")
	foreignGroup, err := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: foreignClinicID,
		StaffID:  testStaffID,
		Name:     "Foreign Folder",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	foreignGroupID := uuid.MustParse(foreignGroup.ID)

	_, err = svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID, // caller's clinic — different from foreign
		StaffID:  testStaffID,
		Name:     "Sneaky form",
		GroupID:  &foreignGroupID,
	})
	if !isNotFound(err) {
		t.Fatalf("expected ErrNotFound on cross-tenant group, got %v", err)
	}
}

func TestRegression_UpdateDraft_RejectsCrossTenantGroupID(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// Seed a form in caller's clinic.
	created, err := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	formID := uuid.MustParse(created.ID)

	// Foreign-clinic group.
	foreignClinicID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000998")
	foreignGroup, _ := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: foreignClinicID, StaffID: testStaffID, Name: "Foreign",
	})
	foreignGroupID := uuid.MustParse(foreignGroup.ID)

	_, err = svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
		GroupID:  &foreignGroupID,
	})
	if !isNotFound(err) {
		t.Fatalf("expected ErrNotFound on cross-tenant group, got %v", err)
	}
}

// ── Group deletion (reparenting) ────────────────────────────────────────────
//
// DeleteGroup must reparent every form inside the folder to NULL group_id
// and delete the folder row atomically. Forms must NOT be deleted.

func TestRegression_DeleteGroup_ReparentsContainedForms(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	group, _ := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Folder",
	})
	groupID := uuid.MustParse(group.ID)

	// Two forms inside the folder.
	for _, name := range []string{"Form A", "Form B"} {
		if _, err := svc.CreateForm(ctx, CreateFormInput{
			ClinicID: testClinicID,
			StaffID:  testStaffID,
			Name:     name,
			GroupID:  &groupID,
		}); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}

	resp, err := svc.DeleteGroup(ctx, DeleteGroupInput{
		GroupID:  groupID,
		ClinicID: testClinicID,
	})
	if err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if resp.ReparentedForms != 2 {
		t.Errorf("ReparentedForms: got %d, want 2", resp.ReparentedForms)
	}

	// Folder gone.
	groups, _ := svc.ListGroups(ctx, testClinicID)
	if len(groups.Items) != 0 {
		t.Errorf("expected folder removed, got %d groups", len(groups.Items))
	}

	// Forms survive, both unparented.
	list, _ := svc.ListForms(ctx, testClinicID, ListFormsInput{Limit: 50})
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 forms surviving, got %d", len(list.Items))
	}
	for _, f := range list.Items {
		if f.GroupID != nil {
			t.Errorf("form %q still parented to %q after folder delete", f.Name, *f.GroupID)
		}
	}
}

func TestRegression_DeleteGroup_NotFoundForUnknownGroup(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	_, err := svc.DeleteGroup(ctx, DeleteGroupInput{
		GroupID:  uuid.New(),
		ClinicID: testClinicID,
	})
	if !isNotFound(err) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRegression_DeleteGroup_RejectsCrossTenantGroup(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	// Group lives in clinic A.
	otherClinicID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000777")
	g, _ := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: otherClinicID, StaffID: testStaffID, Name: "Theirs",
	})
	groupID := uuid.MustParse(g.ID)

	// Caller from clinic B tries to delete it.
	_, err := svc.DeleteGroup(ctx, DeleteGroupInput{
		GroupID:  groupID,
		ClinicID: testClinicID,
	})
	if !isNotFound(err) {
		t.Fatalf("expected ErrNotFound (tenant scoping), got %v", err)
	}
}

// ── Duplicate group names ────────────────────────────────────────────────────

func TestRegression_CreateGroup_DuplicateNameReturnsConflict(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	if _, err := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Post-op",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "POST-OP",
	})
	if !isConflict(err) {
		t.Fatalf("expected ErrConflict on case-insensitive duplicate, got %v", err)
	}
	if !strings.Contains(err.Error(), "Post-op") {
		t.Errorf("error message should name the existing folder, got %q", err.Error())
	}
}

func TestRegression_UpdateGroup_DuplicateNameReturnsConflict(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	a, _ := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Intake",
	})
	if _, err := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Discharge",
	}); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	// Rename A → "Discharge" (case-insensitive collision).
	_, err := svc.UpdateGroup(ctx, UpdateGroupInput{
		GroupID:  uuid.MustParse(a.ID),
		ClinicID: testClinicID,
		Name:     "discharge",
	})
	if !isConflict(err) {
		t.Fatalf("expected ErrConflict on rename collision, got %v", err)
	}
}

func TestRegression_UpdateGroup_RenameToOwnNameIsAllowed(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	g, _ := svc.CreateGroup(ctx, CreateGroupInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Intake",
	})

	// Re-saving the same name should succeed — collision guard must exclude
	// the renaming group itself.
	if _, err := svc.UpdateGroup(ctx, UpdateGroupInput{
		GroupID:  uuid.MustParse(g.ID),
		ClinicID: testClinicID,
		Name:     "Intake",
	}); err != nil {
		t.Fatalf("renaming to own current name should succeed, got %v", err)
	}
}

// ── Tag deduplication ───────────────────────────────────────────────────────

func TestRegression_CreateForm_NormalisesTags(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	resp, err := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Tag test",
		Tags:     []string{"urgent", "  urgent  ", "URGENT", "", "  ", "Important"},
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	// Expect: ["urgent", "Important"] — case-insensitive dedup, first-seen
	// casing preserved, empties dropped.
	if want := []string{"urgent", "Important"}; !stringsEqual(resp.Tags, want) {
		t.Errorf("tags: got %v, want %v", resp.Tags, want)
	}
}

func TestRegression_UpdateDraft_NormalisesTags(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID, StaffID: testStaffID, Name: "Form",
	})
	formID := uuid.MustParse(created.ID)

	resp, err := svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Form",
		Tags:     []string{"A", "a", "  b  ", "B", "C"},
	})
	if err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if want := []string{"A", "b", "C"}; !stringsEqual(resp.Tags, want) {
		t.Errorf("tags: got %v, want %v", resp.Tags, want)
	}
}

// ── humanConflictMessage helper ─────────────────────────────────────────────

func TestRegression_HumanConflictMessage_StripsServicePrefix(t *testing.T) {
	cases := []struct {
		name    string
		in      error
		want    string
	}{
		{
			name: "duplicate folder",
			in:   errors.New(`forms.service.CreateGroup: a folder named "Post-op" already exists: conflict`),
			want: `a folder named "Post-op" already exists`,
		},
		{
			name: "form retired",
			in:   errors.New("forms.service.UpdateDraft: form is retired: conflict"),
			want: "form is retired",
		},
		{
			name: "publish race exhausted",
			in:   errors.New("forms.service.PublishForm: could not assign version number: conflict"),
			want: "could not assign version number",
		},
		{
			name: "no service prefix",
			in:   errors.New("something bad: conflict"),
			want: "something bad",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := humanConflictMessage(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// stringsEqual compares two []string in order.
func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── Salvia fork-flag flip ───────────────────────────────────────────────────
//
// SalviaTemplateState shipped wired up at install ("default") but no code
// ever flipped it to "forked" on first mutation. The downstream consequence
// was that overlayTemplateFields kept painting YAML fields over clinic-
// authored content, and equivalent count/preview surfaces lied. Fix flips
// the flag at the end of every content-mutating service call
// (UpdateDraft, PublishForm) so that subsequent reads bypass the overlay.
//
// If a future commit removes the flip, the YAML overlay reappears and
// these tests fail.

func TestRegression_UpdateDraft_FlipsSalviaStateToForked(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()

	tID := "salvia.intake_assessment"
	tState := "default"
	created, err := svc.CreateForm(ctx, CreateFormInput{
		ClinicID:            testClinicID,
		StaffID:             testStaffID,
		Name:                "Intake Assessment",
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tState,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if created.SalviaTemplateState == nil || *created.SalviaTemplateState != "default" {
		t.Fatalf("seeded form must start in 'default' state, got %v", created.SalviaTemplateState)
	}

	formID := uuid.MustParse(created.ID)
	if _, err := svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Intake Assessment",
	}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	got, err := svc.GetForm(ctx, formID, testClinicID)
	if err != nil {
		t.Fatalf("GetForm: %v", err)
	}
	if got.SalviaTemplateState == nil || *got.SalviaTemplateState != "forked" {
		t.Errorf("expected state flipped to 'forked' after UpdateDraft, got %v", got.SalviaTemplateState)
	}
}

func TestRegression_UpdateDraft_NonSalviaFormUnaffected(t *testing.T) {
	// Plain clinic-authored form has SalviaTemplateID == nil; MarkFormForked
	// must short-circuit so the call doesn't blow up or set a phantom flag.
	svc := newTestService()
	ctx := context.Background()

	created, err := svc.CreateForm(ctx, CreateFormInput{
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Plain form",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	formID := uuid.MustParse(created.ID)

	if _, err := svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Plain form",
	}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	got, _ := svc.GetForm(ctx, formID, testClinicID)
	if got.SalviaTemplateState != nil {
		t.Errorf("non-Salvia form must keep SalviaTemplateState nil, got %v", *got.SalviaTemplateState)
	}
}

func TestRegression_UpdateDraft_AlreadyForkedFormStaysForked(t *testing.T) {
	// Idempotency: the flip helper must be a no-op for rows already in
	// "forked" state (no error, no state regression).
	svc := newTestService()
	ctx := context.Background()

	tID := "salvia.intake_assessment"
	tState := "forked"
	created, err := svc.CreateForm(ctx, CreateFormInput{
		ClinicID:            testClinicID,
		StaffID:             testStaffID,
		Name:                "Already forked",
		SalviaTemplateID:    &tID,
		SalviaTemplateState: &tState,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	formID := uuid.MustParse(created.ID)

	if _, err := svc.UpdateDraft(ctx, UpdateDraftInput{
		FormID:   formID,
		ClinicID: testClinicID,
		StaffID:  testStaffID,
		Name:     "Already forked",
	}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	got, _ := svc.GetForm(ctx, formID, testClinicID)
	if got.SalviaTemplateState == nil || *got.SalviaTemplateState != "forked" {
		t.Errorf("forked form must stay 'forked' after UpdateDraft, got %v", got.SalviaTemplateState)
	}
}
