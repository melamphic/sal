package audio

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the repo interface used in unit tests.
type fakeRepo struct {
	mu         sync.RWMutex
	recordings map[uuid.UUID]*RecordingRecord
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{recordings: make(map[uuid.UUID]*RecordingRecord)}
}

func (f *fakeRepo) CreateRecording(_ context.Context, p CreateRecordingParams) (*RecordingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := &RecordingRecord{
		ID:          p.ID,
		ClinicID:    p.ClinicID,
		StaffID:     p.StaffID,
		SubjectID:   p.SubjectID,
		Status:      domain.RecordingStatusPendingUpload,
		FileKey:     p.FileKey,
		ContentType: p.ContentType,
		CreatedAt:   domain.TimeNow(),
		UpdatedAt:   domain.TimeNow(),
	}
	f.recordings[rec.ID] = rec
	return cloneRec(rec), nil
}

func (f *fakeRepo) GetRecordingByID(_ context.Context, id, clinicID uuid.UUID) (*RecordingRecord, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	rec, ok := f.recordings[id]
	if !ok || rec.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	return cloneRec(rec), nil
}

func (f *fakeRepo) ListRecordings(_ context.Context, clinicID uuid.UUID, p ListRecordingsParams) ([]*RecordingRecord, int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var all []*RecordingRecord
	for _, rec := range f.recordings {
		if rec.ClinicID != clinicID {
			continue
		}
		if p.SubjectID != nil && (rec.SubjectID == nil || *rec.SubjectID != *p.SubjectID) {
			continue
		}
		if p.StaffID != nil && rec.StaffID != *p.StaffID {
			continue
		}
		if p.Status != nil && rec.Status != *p.Status {
			continue
		}
		all = append(all, cloneRec(rec))
	}
	total := len(all)
	start := p.Offset
	if start > total {
		return []*RecordingRecord{}, total, nil
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (f *fakeRepo) UpdateRecordingStatus(_ context.Context, id uuid.UUID, status domain.RecordingStatus, errorMsg *string) (*RecordingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.recordings[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	rec.Status = status
	rec.ErrorMessage = errorMsg
	rec.UpdatedAt = domain.TimeNow()
	return cloneRec(rec), nil
}

func (f *fakeRepo) UpdateRecordingTranscript(_ context.Context, id uuid.UUID, transcript string, durationSeconds *int) (*RecordingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.recordings[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	rec.Status = domain.RecordingStatusTranscribed
	rec.Transcript = &transcript
	rec.DurationSeconds = durationSeconds
	rec.ErrorMessage = nil
	rec.UpdatedAt = domain.TimeNow()
	return cloneRec(rec), nil
}

func (f *fakeRepo) LinkSubject(_ context.Context, id, clinicID, subjectID uuid.UUID) (*RecordingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.recordings[id]
	if !ok || rec.ClinicID != clinicID {
		return nil, domain.ErrNotFound
	}
	rec.SubjectID = &subjectID
	rec.UpdatedAt = domain.TimeNow()
	return cloneRec(rec), nil
}

func cloneRec(r *RecordingRecord) *RecordingRecord {
	cp := *r
	return &cp
}
