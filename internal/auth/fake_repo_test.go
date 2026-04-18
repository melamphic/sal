package auth

// fakeRepo is a hand-rolled in-memory implementation of the repo interface.
// It is used exclusively in unit tests — production code never imports this file.
//
// It is defined in package auth (not auth_test) so it can access unexported types
// (staffRow, tokenRow, inviteRow) that are also unexported.

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/melamphic/sal/internal/domain"
)

type fakeRepo struct {
	mu     sync.Mutex
	staff  map[string]*staffRow // keyed by emailHash
	byID   map[uuid.UUID]*staffRow
	tokens map[string]*tokenRow // keyed by tokenHash
	last   struct {
		updatedLastActiveID uuid.UUID
		deletedRefreshID    uuid.UUID
		createdTokenHash    string
	}
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		staff:  make(map[string]*staffRow),
		byID:   make(map[uuid.UUID]*staffRow),
		tokens: make(map[string]*tokenRow),
	}
}

// seedStaff adds a staff row accessible by both emailHash and ID.
func (f *fakeRepo) seedStaff(s *staffRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.staff[s.EmailHash] = s
	f.byID[s.ID] = s
}

func (f *fakeRepo) FindStaffByEmailHash(_ context.Context, emailHash string) (*staffRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.staff[emailHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeRepo) CreateAuthToken(_ context.Context, staffID uuid.UUID, tokenHash, tokenType, _ string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[tokenHash] = &tokenRow{
		ID:        uuid.New(),
		StaffID:   staffID,
		TokenHash: tokenHash,
		TokenType: tokenType,
		ExpiresAt: expiresAt,
	}
	f.last.createdTokenHash = tokenHash
	return nil
}

func (f *fakeRepo) GetAndConsumeAuthToken(_ context.Context, tokenHash string) (*tokenRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[tokenHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if t.UsedAt != nil {
		return nil, domain.ErrTokenUsed
	}
	if time.Now().After(t.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}
	now := time.Now()
	t.UsedAt = &now
	return t, nil
}

func (f *fakeRepo) GetStaffByID(_ context.Context, staffID uuid.UUID) (*staffRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[staffID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeRepo) CreateInviteToken(_ context.Context, _ CreateInviteParams) error {
	return nil
}

func (f *fakeRepo) GetInviteByTokenHash(_ context.Context, _ string) (*inviteRow, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeRepo) MarkInviteAccepted(_ context.Context, _ string) error {
	return nil
}

func (f *fakeRepo) DeleteRefreshTokensForStaff(_ context.Context, staffID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last.deletedRefreshID = staffID
	for hash, t := range f.tokens {
		if t.StaffID == staffID && t.TokenType == "refresh" {
			delete(f.tokens, hash)
		}
	}
	return nil
}

func (f *fakeRepo) UpdateLastActive(_ context.Context, staffID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last.updatedLastActiveID = staffID
	return nil
}
