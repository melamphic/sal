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
	return NewService(newFakeRepo(), &fakeEnqueuer{})
}

var (
	clinicID  = uuid.New()
	staffID   = uuid.New()
	recID     = uuid.New()
	formVerID = uuid.New()
)

// ── CreateNote ────────────────────────────────────────────────────────────────

func TestService_CreateNote_OK(t *testing.T) {
	t.Parallel()
	svc := newTestService()

	resp, err := svc.CreateNote(context.Background(), CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
		FormVersionID: formVerID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusExtracting {
		t.Errorf("expected status extracting, got %s", resp.Status)
	}
	if resp.ClinicID != clinicID.String() {
		t.Errorf("clinic_id mismatch")
	}
}

func TestService_CreateNote_EnqueuesJob(t *testing.T) {
	t.Parallel()
	enq := &fakeEnqueuer{}
	svc := NewService(newFakeRepo(), enq)

	_, err := svc.CreateNote(context.Background(), CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
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

	for i := 0; i < maxNotesPerRecording; i++ {
		_, err := svc.CreateNote(ctx, CreateNoteInput{
			ClinicID:      clinicID,
			StaffID:       staffID,
			RecordingID:   recID,
			FormVersionID: uuid.New(), // different form version each time
		})
		if err != nil {
			t.Fatalf("note %d: unexpected error: %v", i+1, err)
		}
	}

	_, err := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
		FormVersionID: uuid.New(),
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected conflict error, got %v", err)
	}
}

// ── GetNote ───────────────────────────────────────────────────────────────────

func TestService_GetNote_OK(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	resp, err := svc.GetNote(ctx, noteID, clinicID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != created.ID {
		t.Errorf("ID mismatch: got %s", resp.ID)
	}
}

func TestService_GetNote_WrongClinic(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	_, err := svc.GetNote(ctx, noteID, uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
}

// ── ListNotes ─────────────────────────────────────────────────────────────────

func TestService_ListNotes_FilterByRecording(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	otherRec := uuid.New()
	svc.CreateNote(ctx, CreateNoteInput{ClinicID: clinicID, StaffID: staffID, RecordingID: recID, FormVersionID: formVerID})     //nolint:errcheck
	svc.CreateNote(ctx, CreateNoteInput{ClinicID: clinicID, StaffID: staffID, RecordingID: otherRec, FormVersionID: uuid.New()}) //nolint:errcheck

	resp, err := svc.ListNotes(ctx, clinicID, ListNotesInput{
		Limit:       20,
		RecordingID: &recID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected 1 note, got %d", resp.Total)
	}
	if resp.Items[0].RecordingID != recID.String() {
		t.Errorf("recording_id mismatch")
	}
}

// ── UpdateField ───────────────────────────────────────────────────────────────

func TestService_UpdateField_RequiresDraft(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
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
	svc := NewService(repo, &fakeEnqueuer{})
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
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

func TestService_SubmitNote_OK(t *testing.T) {
	t.Parallel()
	restore := domain.SetTimeNow(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	t.Cleanup(restore)

	repo := newFakeRepo()
	svc := NewService(repo, &fakeEnqueuer{})
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)
	repo.UpdateNoteStatus(ctx, noteID, domain.NoteStatusDraft, nil) //nolint:errcheck

	resp, err := svc.SubmitNote(ctx, noteID, clinicID, staffID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != domain.NoteStatusSubmitted {
		t.Errorf("expected submitted, got %s", resp.Status)
	}
	if resp.SubmittedBy == nil || *resp.SubmittedBy != staffID.String() {
		t.Errorf("submitted_by mismatch")
	}
}

func TestService_SubmitNote_NotDraft(t *testing.T) {
	t.Parallel()
	svc := newTestService()
	ctx := context.Background()

	created, _ := svc.CreateNote(ctx, CreateNoteInput{
		ClinicID:      clinicID,
		StaffID:       staffID,
		RecordingID:   recID,
		FormVersionID: formVerID,
	})
	noteID, _ := uuid.Parse(created.ID)

	// Note is still 'extracting' — cannot submit.
	_, err := svc.SubmitNote(ctx, noteID, clinicID, staffID)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected conflict, got %v", err)
	}
}
