package staff

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the staff repo interface.
type fakeRepo struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]*StaffRecord
	byEmail map[string]*StaffRecord // keyed by emailHash+clinicID
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byID:    make(map[uuid.UUID]*StaffRecord),
		byEmail: make(map[string]*StaffRecord),
	}
}

func emailClinicKey(emailHash string, clinicID uuid.UUID) string {
	return emailHash + ":" + clinicID.String()
}

func (f *fakeRepo) Create(_ context.Context, p CreateParams) (*StaffRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	s := &StaffRecord{
		ID:        p.ID,
		ClinicID:  p.ClinicID,
		Email:     p.Email,
		EmailHash: p.EmailHash,
		FullName:  p.FullName,
		Role:      p.Role,
		NoteTier:  p.NoteTier,
		Perms:     p.Perms,
		Status:    p.Status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.byID[s.ID] = s
	f.byEmail[emailClinicKey(s.EmailHash, s.ClinicID)] = s
	return s, nil
}

func (f *fakeRepo) GetByID(_ context.Context, staffID, clinicID uuid.UUID) (*StaffRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[staffID]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeRepo) ExistsByEmailHash(_ context.Context, emailHash string, clinicID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byEmail[emailClinicKey(emailHash, clinicID)]
	return ok && s.ArchivedAt == nil, nil
}

func (f *fakeRepo) GetByEmailHash(_ context.Context, emailHash string, clinicID uuid.UUID) (*StaffRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byEmail[emailClinicKey(emailHash, clinicID)]
	if !ok || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeRepo) List(_ context.Context, clinicID uuid.UUID, p ListParams) ([]*StaffRecord, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var all []*StaffRecord
	for _, s := range f.byID {
		if s.ClinicID == clinicID && s.ArchivedAt == nil {
			all = append(all, s)
		}
	}
	total := len(all)

	start := p.Offset
	if start > total {
		start = total
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (f *fakeRepo) UpdatePermissions(_ context.Context, staffID, clinicID uuid.UUID, p UpdatePermsParams) (*StaffRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[staffID]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	s.Perms = p.Perms
	s.UpdatedAt = time.Now().UTC()
	return s, nil
}

func (f *fakeRepo) Deactivate(_ context.Context, staffID, clinicID uuid.UUID) (*StaffRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[staffID]
	if !ok || s.ClinicID != clinicID || s.ArchivedAt != nil {
		return nil, domain.ErrNotFound
	}
	s.Status = domain.StaffStatusDeactivated
	s.UpdatedAt = time.Now().UTC()
	return s, nil
}
