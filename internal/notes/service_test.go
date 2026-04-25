package notes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// fakeEnqueuer satisfies jobEnqueuer without a real River client.
type fakeEnqueuer struct{ inserted int }

func (f *fakeEnqueuer) Insert(_ context.Context, _ river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	f.inserted++
	return &rivertype.JobInsertResult{}, nil
}

func newTestService() *Service {
	return NewService(newFakeRepo(), &fakeEnqueuer{}, nil, nil)
}

var (
	clinicID  = uuid.New()
	staffID   = uuid.New()
	recID     = uuid.New()
	formVerID = uuid.New()
)

// ── CreateNote ────────────────────────────────────────────────────────────────

func TestService_CreateNote_AI_OK(t *testing.T) {
	t.Parallel()
	svc := newTestService()

	rid := recID
	resp, err := svc.CreateNote(context.Background(), CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusExtracting {
		t.Errorf("expected status extracting, got %s", resp.Status)
	}
	if resp.RecordingID == nil || *resp.RecordingID != rid.String() {
		t.Errorf("recording_id mismatch")
	}
}

func TestService_CreateNote_Manual_OK(t *testing.T) {
	t.Parallel()
	enq := &fakeEnqueuer{}
	svc := NewService(newFakeRepo(), enq, nil, nil)

	resp, err := svc.CreateNote(context.Background(), CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
		// No RecordingID — manual note.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusDraft {
		t.Errorf("expected status draft for manual note, got %s", resp.Status)
	}
	if resp.RecordingID != nil {
		t.Errorf("expected nil recording_id for manual note")
	}
	if enq.inserted != 0 {
		t.Errorf("expected no jobs enqueued for manual note, got %d", enq.inserted)
	}
}

func TestService_CreateNote_EnqueuesJob(t *testing.T) {
	t.Parallel()
	enq := &fakeEnqueuer{}
	svc := NewService(newFakeRepo(), enq, nil, nil)

	rid := recID
	_, err := svc.CreateNote(context.Background(), CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enq.inserted != 1 {
		t.Errorf("expected 1 job inserted, got %d", enq.inserted)
	}
}

func TestService_CreateNote_MaxCapEnforced(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	for i := 0; i < maxNotesPerRecording; i++ {
		fvid := uuid.New()
		_, err := svc.CreateNote(ctx, CreateNoteInput{
			ClinicID:      clinicID,
			StaffID:       staffID,
			RecordingID:   &rid,
			FormVersionID: fvid,
		})
		if err != nil {
			t.Fatalf("note %d: unexpected error: %v", i+1, err)
		}
	}

	fvid := uuid.New()
	_, err := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: fvid,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected conflict error, got %v", err)
	}
}

func TestService_CreateNote_CapNotAppliedToManualNotes(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	// Fill cap with AI notes linked to a recording.
	rid := recID
	for i := 0; i < maxNotesPerRecording; i++ {
		fvid := uuid.New()
		_, err := svc.CreateNote(ctx, CreateNoteInput{
			ClinicID:      clinicID,
			StaffID:       staffID,
			RecordingID:   &rid,
			FormVersionID: fvid,
		})
		if err != nil {
			t.Fatalf("note %d: %v", i+1, err)
		}
	}

	// Manual note with no recording should still succeed.
	_, err := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  uuid.New(),
		SkipExtraction: true,
	})
	if err != nil {
		t.Errorf("expected manual note to bypass cap, got %v", err)
	}
}

// ── GetNote ───────────────────────────────────────────────────────────────────

func TestService_GetNote_WrongClinic(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	_, err := svc.GetNote(ctx, noteID, uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

// ── ListNotes ─────────────────────────────────────────────────────────────────

func TestService_ListNotes_ExcludesArchivedByDefault(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Archive the note.
	_, err := svc.ArchiveNote(ctx, noteID, clinicID, staffID, "")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Default list should not include archived notes.
	resp, err := svc.ListNotes(ctx, clinicID, ListNotesInput{Limit: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("expected 0 notes (archived excluded), got %d", resp.Total)
	}

	// With include_archived=true, it should appear.
	resp, err = svc.ListNotes(ctx, clinicID, ListNotesInput{Limit: 20, IncludeArchived: true})
	if err != nil {
		t.Fatalf("list with archived: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 note with include_archived, got %d", resp.Total)
	}
}

func TestService_ListNotes_FilterByRecording(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	otherRec := uuid.New()
	svc.CreateNote(ctx, CreateNoteInput{ClinicID: clinicID, StaffID: staffID, RecordingID: &rid, FormVersionID: formVerID})       //nolint:errcheck
	svc.CreateNote(ctx, CreateNoteInput{ClinicID: clinicID, StaffID: staffID, RecordingID: &otherRec, FormVersionID: uuid.New()}) //nolint:errcheck

	resp, err := svc.ListNotes(ctx, clinicID, ListNotesInput{
		Limit:       20,
		RecordingID: &rid,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 note, got %d", resp.Total)
	}
}

// ── UpdateField ───────────────────────────────────────────────────────────────

func TestService_UpdateField_RequiresDraft(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)
	fieldID := uuid.New()

	// Still in 'extracting' — UpdateField should fail.
	val := `"test"`
	_, err := svc.UpdateField(ctx, UpdateFieldInput{
		NoteID:   noteID,
		ClinicID: clinicID,
		StaffID:  staffID,
		FieldID:  fieldID,
		Value:    &val,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected conflict for non-draft note, got %v", err)
	}
}

func TestService_UpdateField_OK(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{}, nil, nil)
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Force note to draft and insert a field.
	repo.UpdateNoteStatus(ctx, noteID, domain.NoteStatusDraft, nil) //nolint:errcheck
	fieldID := uuid.New()
	repo.UpsertNoteFields(ctx, noteID, []UpsertFieldParams{ //nolint:errcheck
		{ID: uuid.New(), NoteID: noteID, FieldID: fieldID},
	})

	val := `"updated value"`
	resp, err := svc.UpdateField(ctx, UpdateFieldInput{
		NoteID:   noteID,
		ClinicID: clinicID,
		StaffID:  staffID,
		FieldID:  fieldID,
		Value:    &val,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Value == nil || *resp.Value != val {
		t.Errorf("expected value %q, got %v", val, resp.Value)
	}
	if resp.OverriddenBy == nil {
		t.Errorf("expected overridden_by to be set")
	}
}

// ── SubmitNote ────────────────────────────────────────────────────────────────

func TestService_SubmitNote_SetsReviewedBy(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{}, nil, nil)
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)
	repo.UpdateNoteStatus(ctx, noteID, domain.NoteStatusDraft, nil) //nolint:errcheck

	resp, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusSubmitted {
		t.Errorf("expected submitted, got %s", resp.Status)
	}
	if resp.ReviewedBy == nil || *resp.ReviewedBy != staffID.String() {
		t.Errorf("reviewed_by not set correctly, got %v", resp.ReviewedBy)
	}
	if resp.SubmittedBy == nil || *resp.SubmittedBy != staffID.String() {
		t.Errorf("submitted_by mismatch")
	}
}

func TestService_SubmitNote_NotDraft(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Note is still 'extracting' — cannot submit.
	_, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "", nil)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected conflict, got %v", err)
	}
}

// ── SubmitNote validation ─────────────────────────────────────────────────────

// fakeFormFieldProvider satisfies FormFieldProvider in tests.
type fakeFormFieldProvider struct {
	fields []FormFieldMeta
}

func (f *fakeFormFieldProvider) GetFieldsByVersionID(_ context.Context, _ uuid.UUID) ([]FormFieldMeta, error) {
	return f.fields, nil
}

func (f *fakeFormFieldProvider) GetFormPrompt(_ context.Context, _ uuid.UUID) (*string, error) {
	return nil, nil
}

func TestService_SubmitNote_ValidationBlocksMissingRequiredFields(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	requiredFieldID := uuid.New()
	optionalFieldID := uuid.New()
	fp := &fakeFormFieldProvider{
		fields: []FormFieldMeta{
			{ID: requiredFieldID, Title: "Chief Complaint", Required: true},
			{ID: optionalFieldID, Title: "Extra Notes", Required: false},
		},
	}
	svc := NewService(repo, &fakeEnqueuer{}, nil, fp)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Don't fill any fields — submit should fail.
	_, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestService_SubmitNote_ValidationPassesWhenFieldsFilled(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	requiredFieldID := uuid.New()
	fp := &fakeFormFieldProvider{
		fields: []FormFieldMeta{
			{ID: requiredFieldID, Title: "Chief Complaint", Required: true},
		},
	}
	svc := NewService(repo, &fakeEnqueuer{}, nil, fp)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Fill the required field.
	val := `"Persistent cough"`
	repo.UpsertNoteFields(ctx, noteID, []UpsertFieldParams{ //nolint:errcheck
		{ID: uuid.New(), NoteID: noteID, FieldID: requiredFieldID, Value: &val},
	})

	resp, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusSubmitted {
		t.Errorf("expected submitted, got %s", resp.Status)
	}
}

func TestService_SubmitNote_ValidationIgnoresOptionalFields(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	fp := &fakeFormFieldProvider{
		fields: []FormFieldMeta{
			{ID: uuid.New(), Title: "Extra Notes", Required: false},
		},
	}
	svc := NewService(repo, &fakeEnqueuer{}, nil, fp)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	// No fields filled — should still succeed because nothing is required.
	resp, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusSubmitted {
		t.Errorf("expected submitted, got %s", resp.Status)
	}
}

func TestService_SubmitNote_PolicyCheckBlocksHighParityViolation(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{}, nil, nil)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Simulate a stored policy check result with a high-parity violation.
	checkResult := `[{"block_id":"b1","status":"violated","reasoning":"not addressed","parity":"high"}]`
	repo.UpdatePolicyCheckResult(ctx, noteID, clinicID, checkResult) //nolint:errcheck

	_, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected validation error for high-parity violation, got %v", err)
	}
}

func TestService_SubmitNote_HighParityOverrideAllowsSubmit(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{}, nil, nil)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	checkResult := `[{"block_id":"b1","status":"violated","reasoning":"not addressed","parity":"high"}]`
	repo.UpdatePolicyCheckResult(ctx, noteID, clinicID, checkResult) //nolint:errcheck

	reason := "Elderly pet refused draw; informed owner verbally."
	resp, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", &reason)
	if err != nil {
		t.Fatalf("expected override to allow submit, got %v", err)
	}
	if resp.Status != domain.NoteStatusSubmitted {
		t.Errorf("expected submitted, got %s", resp.Status)
	}
	if resp.OverrideReason == nil || *resp.OverrideReason != reason {
		t.Errorf("expected override_reason persisted, got %v", resp.OverrideReason)
	}
	if resp.OverrideBy == nil || *resp.OverrideBy != staffID.String() {
		t.Errorf("expected override_by=staffID, got %v", resp.OverrideBy)
	}
	if resp.OverrideAt == nil {
		t.Errorf("expected override_at set")
	}
}

func TestService_SubmitNote_BlankOverrideReasonStillBlocks(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{}, nil, nil)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	checkResult := `[{"block_id":"b1","status":"violated","reasoning":"not addressed","parity":"high"}]`
	repo.UpdatePolicyCheckResult(ctx, noteID, clinicID, checkResult) //nolint:errcheck

	blank := "   "
	_, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", &blank)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected blank override to be rejected as missing, got %v", err)
	}
}

func TestService_SubmitNote_PolicyCheckAllowsLowParityViolation(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{}, nil, nil)
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:       clinicID,
		StaffID:        staffID,
		FormVersionID:  formVerID,
		SkipExtraction: true,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Low-parity violation should not block.
	checkResult := `[{"block_id":"b1","status":"violated","reasoning":"not addressed","parity":"low"}]`
	repo.UpdatePolicyCheckResult(ctx, noteID, clinicID, checkResult) //nolint:errcheck

	resp, err := svc.SubmitNote(ctx, noteID, clinicID, staffID, "vet", nil)
	if err != nil {
		t.Fatalf("unexpected error for low-parity violation: %v", err)
	}
	if resp.Status != domain.NoteStatusSubmitted {
		t.Errorf("expected submitted, got %s", resp.Status)
	}
}

// ── ArchiveNote ───────────────────────────────────────────────────────────────

func TestService_ArchiveNote_OK(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	resp, err := svc.ArchiveNote(ctx, noteID, clinicID, staffID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ArchivedAt == nil {
		t.Errorf("expected archived_at to be set")
	}
}

func TestService_ArchiveNote_AlreadyArchived(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	rid := recID
	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   &rid,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	svc.ArchiveNote(ctx, noteID, clinicID, staffID, "") //nolint:errcheck

	_, err := svc.ArchiveNote(ctx, noteID, clinicID, staffID, "")
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected conflict for double-archive, got %v", err)
	}
}
