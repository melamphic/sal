package notes

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the repo interface used in unit tests.
type fakeRepo struct {
	mu             sync.RWMutex
	notes          map[uuid.UUID]*NoteRecord
	fields         map[uuid.UUID][]*NoteFieldRecord // keyed by note ID
	attachments    []*NoteAttachmentRecord
	policyChecks   []PolicyCheckRecord
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

func (f *fakeRepo) UpdateNoteStatus(_ context.Context, id, clinicID uuid.UUID, status domain.NoteStatus, errMsg *string) (*NoteRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok || n.ClinicID != clinicID {
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
	if n.Status != domain.NoteStatusDraft &&
		n.Status != domain.NoteStatusOverriding {
		return nil, domain.ErrConflict
	}
	wasOverriding := n.Status == domain.NoteStatusOverriding
	n.Status = domain.NoteStatusSubmitted
	n.ReviewedBy = &p.ReviewedBy
	n.ReviewedAt = &p.ReviewedAt
	n.SubmittedBy = &p.SubmittedBy
	t := p.SubmittedAt
	n.SubmittedAt = &t
	if p.OverrideReason != nil {
		reason := *p.OverrideReason
		by := p.SubmittedBy
		at := p.SubmittedAt
		n.OverrideReason = &reason
		n.OverrideBy = &by
		n.OverrideAt = &at
	}
	if wasOverriding {
		n.OverrideCount++
		n.PDFStorageKey = nil
	}
	n.UpdatedAt = domain.TimeNow()
	return cloneNote(n), nil
}

func (f *fakeRepo) OverrideUnlock(_ context.Context, p OverrideUnlockParams) (*NoteRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[p.ID]
	if !ok || n.ClinicID != p.ClinicID {
		return nil, domain.ErrNotFound
	}
	if n.Status != domain.NoteStatusSubmitted {
		return nil, domain.ErrConflict
	}
	n.Status = domain.NoteStatusOverriding
	at := p.UnlockedAt
	by := p.UnlockedBy
	reason := p.Reason
	n.OverrideUnlockedAt = &at
	n.OverrideUnlockedBy = &by
	n.OverrideUnlockedReason = &reason
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

func (f *fakeRepo) CountNotesByRecording(_ context.Context, clinicID, recordingID uuid.UUID) (int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	count := 0
	for _, n := range f.notes {
		if n.ClinicID == clinicID && n.RecordingID != nil && *n.RecordingID == recordingID {
			count++
		}
	}
	return count, nil
}

func (f *fakeRepo) ListExtractingNoteIDsByRecording(_ context.Context, recordingID uuid.UUID) ([]uuid.UUID, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []uuid.UUID
	for _, n := range f.notes {
		if n.RecordingID != nil && *n.RecordingID == recordingID &&
			n.Status == domain.NoteStatusExtracting && n.ArchivedAt == nil {
			out = append(out, n.ID)
		}
	}
	return out, nil
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
			rec.ASRConfidence = p.ASRConfidence
			rec.MinWordConfidence = p.MinWordConfidence
			rec.AlignmentScore = p.AlignmentScore
			rec.GroundingSource = p.GroundingSource
			rec.RequiresReview = p.RequiresReview
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
				ASRConfidence:      p.ASRConfidence,
				MinWordConfidence:  p.MinWordConfidence,
				AlignmentScore:     p.AlignmentScore,
				GroundingSource:    p.GroundingSource,
				RequiresReview:     p.RequiresReview,
				CreatedAt:          domain.TimeNow(),
				UpdatedAt:          domain.TimeNow(),
			}
			f.fields[noteID] = append(f.fields[noteID], rec)
			existing[p.FieldID] = rec
		}
	}
	return f.fields[noteID], nil
}

func (f *fakeRepo) SeedNoteFields(_ context.Context, noteID uuid.UUID, fieldIDs []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing := make(map[uuid.UUID]bool)
	for _, nf := range f.fields[noteID] {
		existing[nf.FieldID] = true
	}
	for _, fid := range fieldIDs {
		if existing[fid] {
			continue
		}
		f.fields[noteID] = append(f.fields[noteID], &NoteFieldRecord{
			ID:        domain.NewID(),
			NoteID:    noteID,
			FieldID:   fid,
			Value:     nil,
			CreatedAt: domain.TimeNow(),
			UpdatedAt: domain.TimeNow(),
		})
	}
	return nil
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

func (f *fakeRepo) UpdatePolicyAlignment(_ context.Context, id, clinicID uuid.UUID, pct float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok || n.ClinicID != clinicID {
		return domain.ErrNotFound
	}
	n.PolicyAlignmentPct = &pct
	return nil
}

func (f *fakeRepo) UpdatePolicyCheckResult(_ context.Context, id, clinicID uuid.UUID, resultJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok || n.ClinicID != clinicID {
		return domain.ErrNotFound
	}
	n.PolicyCheckResult = &resultJSON
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

func (f *fakeRepo) UpdatePDFKey(_ context.Context, id, clinicID uuid.UUID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok || n.ClinicID != clinicID {
		return domain.ErrNotFound
	}
	n.PDFStorageKey = &key
	return nil
}

func (f *fakeRepo) ClearPDFKey(_ context.Context, id, clinicID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok || n.ClinicID != clinicID {
		return domain.ErrNotFound
	}
	n.PDFStorageKey = nil
	return nil
}

func (f *fakeRepo) GetNoteFieldWithType(_ context.Context, _, _, _ uuid.UUID) (*NoteFieldWithType, error) {
	// System widget tests don't run through fakeRepo today; live tests
	// hit the real repository against postgres. Return ErrNotFound so
	// any accidental call surfaces clearly.
	return nil, domain.ErrNotFound
}

func (f *fakeRepo) ListSystemFieldStates(_ context.Context, _, _ uuid.UUID) ([]NoteFieldWithType, error) {
	return nil, nil
}

func (f *fakeRepo) WriteMaterialisedPointer(_ context.Context, _, _, _ uuid.UUID, _ string) error {
	return domain.ErrNotFound
}

func (f *fakeRepo) LookupFormNamesByNoteIDs(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (map[uuid.UUID]string, error) {
	return map[uuid.UUID]string{}, nil
}

func (f *fakeRepo) InsertAttachment(_ context.Context, p CreateAttachmentParams) (*NoteAttachmentRecord, error) {
	rec := &NoteAttachmentRecord{
		ID: p.ID, ClinicID: p.ClinicID, NoteID: p.NoteID, Kind: p.Kind,
		S3Key: p.S3Key, ContentType: p.ContentType, Bytes: p.Bytes,
		UploadedBy: p.UploadedBy, UploadedAt: domain.TimeNow(),
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachments = append(f.attachments, rec)
	return rec, nil
}

func (f *fakeRepo) ListAttachmentsByNote(_ context.Context, noteID, clinicID uuid.UUID) ([]*NoteAttachmentRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*NoteAttachmentRecord, 0)
	for _, a := range f.attachments {
		if a.NoteID == noteID && a.ClinicID == clinicID && a.ArchivedAt == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeRepo) GetAttachment(_ context.Context, id, clinicID uuid.UUID) (*NoteAttachmentRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, a := range f.attachments {
		if a.ID == id && a.ClinicID == clinicID && a.ArchivedAt == nil {
			return a, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeRepo) ArchiveAttachment(_ context.Context, id, clinicID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.attachments {
		if a.ID == id && a.ClinicID == clinicID && a.ArchivedAt == nil {
			now := domain.TimeNow()
			a.ArchivedAt = &now
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeRepo) InsertPolicyCheck(_ context.Context, noteID, clinicID uuid.UUID, resultJSON string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policyChecks = append(f.policyChecks, PolicyCheckRecord{
		ID:        domain.NewID(),
		NoteID:    noteID,
		ClinicID:  clinicID,
		Result:    resultJSON,
		CheckedAt: domain.TimeNow(),
	})
	return nil
}

func (f *fakeRepo) ListPolicyChecks(_ context.Context, noteID, clinicID uuid.UUID) ([]PolicyCheckRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []PolicyCheckRecord
	for i := len(f.policyChecks) - 1; i >= 0; i-- {
		r := f.policyChecks[i]
		if r.NoteID == noteID && r.ClinicID == clinicID {
			out = append(out, r)
		}
	}
	return out, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func cloneNote(n *NoteRecord) *NoteRecord {
	cp := *n
	return &cp
}
