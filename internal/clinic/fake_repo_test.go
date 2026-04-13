package clinic

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

// fakeRepo is an in-memory implementation of the clinic repo interface.
type fakeRepo struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*Clinic
	byEmail  map[string]*Clinic // keyed by emailHash
	createFn func(CreateParams) (*Clinic, error)
}

func newFakeClinicRepo() *fakeRepo {
	return &fakeRepo{
		byID:    make(map[uuid.UUID]*Clinic),
		byEmail: make(map[string]*Clinic),
	}
}

func (f *fakeRepo) Create(_ context.Context, p CreateParams) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createFn != nil {
		return f.createFn(p)
	}
	c := &Clinic{
		ID:          p.ID,
		Name:        p.Name,
		Slug:        p.Slug,
		Email:       p.Email,
		EmailHash:   p.EmailHash,
		Phone:       p.Phone,
		Address:     p.Address,
		Vertical:    p.Vertical,
		Status:      domain.ClinicStatusTrial,
		TrialEndsAt: p.TrialEndsAt,
		DataRegion:  p.DataRegion,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	f.byID[c.ID] = c
	f.byEmail[c.EmailHash] = c
	return c, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) GetByEmailHash(_ context.Context, emailHash string) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byEmail[emailHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, p UpdateParams) (*Clinic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if p.Name != nil {
		c.Name = *p.Name
	}
	if p.Phone != nil {
		c.Phone = p.Phone
	}
	if p.Address != nil {
		c.Address = p.Address
	}
	c.UpdatedAt = time.Now().UTC()
	return c, nil
}
