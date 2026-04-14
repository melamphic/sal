package notes

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the repo interface used in unit tests.
type fakeRepo struct {
	mu     sync.RWMutex
	notes  map[uuid.UUID]*NoteRecord
	fields map[uuid.UUID][]*NoteFieldRecord // keyed by note ID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		notes:  make(map[uuid.UUID]*NoteRecord),
		fields: make(map[uuid.UUID][]*NoteFieldRecord),
	}
}

func (f *fakeRepo) CreateNote(_ context.Context, p CreateNoteParams) (*NoteRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := &NoteRecord{
		ID:            p.ID,
		ClinicID:      p.ClinicID,
		RecordingID:   p.RecordingID,
		FormVersionID: p.FormVersionID,
		SubjectID:     p.SubjectID,
		CreatedBy:     p.CreatedBy,
		Status:        p.Status,
		CreatedAt:     domain.TimeNow(),
		UpdatedAt:     domain.TimeNow(),
	}
	f.notes[n.ID] = n
	return cloneNote(n), nil
}

func (f *fakeRepo) GetNoteByID(_ context.Context, id, clinicID uuid.UUID) (*NoteRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	n, ok := f.notes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	// uuid.Nil = internal worker use, skip clinic check.
	if clinicID != uuid.Nil && n.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	return cloneNote(n), nil
}

func (f *fakeRepo) ListNotes(_ context.Context, clinicID uuid.UUID, p ListNotesParams) ([]*NoteRecord, int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []*NoteRecord
	for _, n := range f.notes {
		if n.ClinicID != clinicID {
			continue
		}
		if !p.IncludeArchived && n.ArchivedAt != nil {
			continue
		}
		if p.RecordingID != nil {
			if n.RecordingID == nil || *n.RecordingID != *p.RecordingID {
				continue
			}
		}
		if p.SubjectID != nil {
			if n.SubjectID == nil || *n.SubjectID != *p.SubjectID {
				continue
			}
		}
		if p.Status != nil && n.Status != *p.Status {
			continue
		}
		out = append(out, cloneNote(n))
	}
	total := len(out)
	if p.Offset >= total {
		return []*NoteRecord{}, total, nil
	}
	out = out[p.Offset:]
	if p.Limit > 0 && len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, total, nil
}

func (f *fakeRepo) UpdateNoteStatus(_ context.Context, id uuid.UUID, status domain.NoteStatus, errMsg *string) (*NoteRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	n.Status = status
	n.ErrorMessage = errMsg
	n.UpdatedAt = domain.TimeNow()
	return cloneNote(n), nil
}

func (f *fakeRepo) SubmitNote(_ context.Context, p SubmitNoteParams) (*NoteRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[p.ID]
	if !ok || n.ClinicID != p.ClinicID {
		return nil, domain.ErrNotFound
	}
	if n.Status != domain.NoteStatusDraft {
		return nil, domain.ErrConflict
	}
	n.Status = domain.NoteStatusSubmitted
	n.ReviewedBy = &p.ReviewedBy
	n.ReviewedAt = &p.ReviewedAt
	n.SubmittedBy = &p.SubmittedBy
	t := p.SubmittedAt
	n.SubmittedAt = &t
	n.UpdatedAt = domain.TimeNow()
	return cloneNote(n), nil
}

func (f *fakeRepo) ArchiveNote(_ context.Context, p ArchiveNoteParams) (*NoteRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[p.ID]
	if !ok || n.ClinicID != p.ClinicID {
		return nil, domain.ErrNotFound
	}
	if n.ArchivedAt != nil {
		return nil, domain.ErrNotFound // archived_at IS NULL condition failed
	}
	t := p.ArchivedAt
	n.ArchivedAt = &t
	n.UpdatedAt = domain.TimeNow()
	return cloneNote(n), nil
}

func (f *fakeRepo) CountNotesByRecording(_ context.Context, recordingID uuid.UUID) (int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	count := 0
	for _, n := range f.notes {
		if n.RecordingID != nil && *n.RecordingID == recordingID {
			count++
		}
	}
	return count, nil
}

func (f *fakeRepo) UpsertNoteFields(_ context.Context, noteID uuid.UUID, fields []UpsertFieldParams) ([]*NoteFieldRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing := make(map[uuid.UUID]*NoteFieldRecord)
	for _, nf := range f.fields[noteID] {
		existing[nf.FieldID] = nf
	}
	for _, p := range fields {
		if rec, ok := existing[p.FieldID]; ok {
			rec.Value = p.Value
			rec.Confidence = p.Confidence
			rec.SourceQuote = p.SourceQuote
			rec.TransformationType = p.TransformationType
			rec.UpdatedAt = domain.TimeNow()
		} else {
			rec := &NoteFieldRecord{
				ID:                 p.ID,
				NoteID:             noteID,
				FieldID:            p.FieldID,
				Value:              p.Value,
				Confidence:         p.Confidence,
				SourceQuote:        p.SourceQuote,
				TransformationType: p.TransformationType,
				CreatedAt:          domain.TimeNow(),
				UpdatedAt:          domain.TimeNow(),
			}
			f.fields[noteID] = append(f.fields[noteID], rec)
			existing[p.FieldID] = rec
		}
	}
	return f.fields[noteID], nil
}

func (f *fakeRepo) GetNoteFields(_ context.Context, noteID uuid.UUID) ([]*NoteFieldRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*NoteFieldRecord, len(f.fields[noteID]))
	for i, nf := range f.fields[noteID] {
		cp := *nf
		out[i] = &cp
	}
	return out, nil
}

func (f *fakeRepo) UpdatePolicyAlignment(_ context.Context, id uuid.UUID, pct float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok {
		return domain.ErrNotFound
	}
	n.PolicyAlignmentPct = &pct
	return nil
}

func (f *fakeRepo) UpdateNoteField(_ context.Context, p UpdateNoteFieldParams) (*NoteFieldRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, nf := range f.fields[p.NoteID] {
		if nf.FieldID == p.FieldID {
			nf.Value = p.Value
			nf.OverriddenBy = &p.OverriddenBy
			nf.OverriddenAt = &p.OverriddenAt
			nf.UpdatedAt = domain.TimeNow()
			cp := *nf
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func cloneNote(n *NoteRecord) *NoteRecord {
	cp := *n
	return &cp
}
